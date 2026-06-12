// normalizer consumes raw.github, normalizes via the GitHub connector, resolves
// the tenant from the installation id, dedups the delivery, and — in one tx —
// upserts the canonical work_item under RLS and stages the canonical event in the
// outbox. The outbox relay publishes it to Kafka. At-least-once: process fully,
// then commit the offset.
//
// Why the outbox (ADR-012): the delivery mark, the work_item write, and the event
// emit all commit atomically, so there is no window where the write lands but the
// emit is lost (which delivery-id dedup would otherwise make unrecoverable).
//
// Failure handling (M0.5):
//   - Permanent errors (undecodable / invalid payload) → dead-lettered to
//     raw.github.dlq, then the offset is committed so the bad message can't block
//     the partition.
//   - Transient errors (DB / Kafka unavailable) → exponential backoff, no commit,
//     so the message is redelivered until it succeeds.
//   - Redeliveries are deduped on X-GitHub-Delivery in the same tx as the write,
//     so a duplicate is a provable no-op (no re-emit). See db/migrations/0004.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dev-intel/platform/libs/go/config"
	"github.com/dev-intel/platform/libs/go/connector"
	ghconn "github.com/dev-intel/platform/libs/go/connector/github"
	"github.com/dev-intel/platform/libs/go/events"
	"github.com/dev-intel/platform/libs/go/kafka"
	"github.com/dev-intel/platform/libs/go/observability"
	"github.com/dev-intel/platform/libs/go/tenancy"
)

const (
	baseBackoff = 250 * time.Millisecond
	maxBackoff  = 30 * time.Second
)

// errDuplicate signals a redelivery already recorded in processed_delivery; the
// transaction rolls back (nothing to re-write) and we skip re-emitting.
var errDuplicate = errors.New("duplicate delivery")

// permanentError marks a message that will never succeed on retry (bad payload).
// Such messages are dead-lettered rather than retried forever.
type permanentError struct{ err error }

func (p permanentError) Error() string { return p.err.Error() }
func (p permanentError) Unwrap() error { return p.err }

func isPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

func main() {
	log := observability.Logger("normalizer")

	brokers := config.List("KAFKA_BROKERS", "localhost:9092")
	// Connect as the least-privilege app role (NOSUPERUSER/NOBYPASSRLS) so RLS
	// actually engages — connecting as the superuser owner bypasses it. See 0003.
	dsn := config.String("POSTGRES_DSN", "postgres://devintel_app:devintel_app@localhost:5432/devintel")
	group := config.String("CONSUMER_GROUP", "normalizer")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := tenancy.New(ctx, dsn)
	if err != nil {
		log.Error("db connect", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	reader := kafka.NewReader(brokers, group, events.TopicRawGitHub)
	defer reader.Close()
	dlq := kafka.NewWriter(brokers, events.TopicDeadLetter)
	defer dlq.Close()

	src := ghconn.New()
	n := &normalizer{log: log, src: src, store: store, dlq: dlq}
	log.Info("started", "brokers", brokers, "topic", events.TopicRawGitHub, "group", group)

	var attempt int
	for {
		msg, err := reader.Fetch(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				log.Info("shutting down")
				return
			}
			log.Error("fetch", "err", err)
			continue
		}

		err = n.process(ctx, msg)
		switch {
		case err == nil:
			attempt = 0
			commit(ctx, log, reader, msg)

		case isPermanent(err):
			// Park the poison message in the DLQ, then commit so it can't block
			// the partition. If the DLQ write itself fails, treat as transient.
			if dlqErr := n.deadLetter(ctx, msg, err); dlqErr != nil {
				attempt++
				log.Error("dead-letter failed; will retry", "err", dlqErr, "delivery", msg.Key, "attempt", attempt)
				if sleep(ctx, backoff(attempt)) != nil {
					return
				}
				continue
			}
			attempt = 0
			log.Warn("dead-lettered", "err", err, "delivery", msg.Key)
			commit(ctx, log, reader, msg)

		default:
			// Transient: don't commit → redelivered. Back off so a hard-down
			// dependency doesn't become a hot retry loop.
			attempt++
			log.Error("process failed; will retry", "err", err, "delivery", msg.Key, "attempt", attempt)
			if sleep(ctx, backoff(attempt)) != nil {
				return
			}
		}
	}
}

type normalizer struct {
	log   *slog.Logger
	src   connector.Source
	store *tenancy.Store
	dlq   *kafka.Writer
}

func (n *normalizer) process(ctx context.Context, msg kafka.Message) error {
	traceID := msg.Headers[events.HeaderTraceID]
	raw := connector.RawEvent{
		DeliveryID: msg.Headers[events.HeaderGitHubDeliv],
		EventType:  msg.Headers[events.HeaderGitHubEvent],
		Body:       msg.Value,
		TraceID:    traceID,
	}

	res, err := n.src.Normalize(raw)
	if err != nil {
		// Normalize is pure parsing/validation; any failure is a bad payload
		// that will never succeed on retry → permanent (dead-letter it).
		return permanentError{err}
	}
	if res.Skip || len(res.Items) == 0 {
		n.log.Info("skipped event", "event", raw.EventType, "trace_id", traceID)
		return nil
	}

	tenantID, err := n.store.ResolveTenant(ctx, res.InstallationID)
	if err != nil {
		// Unknown installation: not "bad", just not onboarded yet — the raw is
		// archived and replayable, so drop (commit) rather than DLQ-spam.
		if errors.Is(err, tenancy.ErrUnknownInstallation) {
			n.log.Warn("unknown installation; dropping", "installation", res.InstallationID, "trace_id", traceID)
			return nil
		}
		return err // transient (e.g. DB down)
	}

	// Dedup + write + emit atomically under the tenant's RLS scope. The delivery
	// mark, the work_item upsert, and the canonical event (into the outbox) all
	// commit together: a redelivery is a provable no-op, and there is no window
	// where the write commits but the emit is lost. The outbox relay publishes the
	// event to Kafka afterwards (ADR-012).
	err = n.store.WithTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		fresh, err := markDelivery(ctx, tx, tenantID, raw.DeliveryID, raw.EventType)
		if err != nil {
			return err
		}
		if !fresh {
			return errDuplicate // rolls back; original write already committed
		}
		for _, wi := range res.Items {
			if err := upsertWorkItem(ctx, tx, tenantID, wi); err != nil {
				return err
			}
			ev := events.New(tenantID, res.EventType, raw.DeliveryID, wi.CreatedAt, workItemPayload(wi))
			if err := enqueueOutbox(ctx, tx, tenantID, ev, traceID); err != nil {
				return err
			}
			n.log.Info("normalized", "type", ev.Type, "repo", wi.Repo, "number", wi.Number,
				"tenant_id", tenantID, "trace_id", traceID)
		}
		return nil
	})
	if errors.Is(err, errDuplicate) {
		n.log.Info("duplicate delivery; no-op", "delivery", raw.DeliveryID, "trace_id", traceID)
		return nil
	}
	if err != nil {
		return err // transient
	}
	return nil
}

// enqueueOutbox stages a canonical event for publication in the same tx as the
// write. The relay (services/outbox-relay) drains it to Kafka at-least-once.
func enqueueOutbox(ctx context.Context, tx pgx.Tx, tenantID string, ev events.CanonicalEvent, traceID string) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO outbox (tenant_id, event_id, topic, kafka_key, trace_id, payload)
VALUES ($1, $2, $3, $4, $5, $6)`,
		tenantID, ev.EventID, events.TopicCanonical, tenantID, traceID, payload)
	return err
}

// deadLetter republishes the original message to the DLQ topic, preserving its
// headers and stamping the failure reason.
func (n *normalizer) deadLetter(ctx context.Context, msg kafka.Message, cause error) error {
	headers := make(map[string]string, len(msg.Headers)+1)
	for k, v := range msg.Headers {
		headers[k] = v
	}
	headers[events.HeaderDLQReason] = cause.Error()
	return n.dlq.Write(ctx, msg.Key, msg.Value, headers)
}

// markDelivery records the delivery id; returns false if it was already present
// (a redelivery). ON CONFLICT DO NOTHING + RowsAffected gives an atomic test-set.
func markDelivery(ctx context.Context, tx pgx.Tx, tenantID, deliveryID, eventType string) (fresh bool, err error) {
	if deliveryID == "" {
		// No delivery id (shouldn't happen from the gateway); skip dedup, process.
		return true, nil
	}
	tag, err := tx.Exec(ctx, `
INSERT INTO processed_delivery (tenant_id, delivery_id, event_type)
VALUES ($1, $2, $3)
ON CONFLICT (tenant_id, delivery_id) DO NOTHING`,
		tenantID, deliveryID, eventType)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func upsertWorkItem(ctx context.Context, tx pgx.Tx, tenantID string, wi connector.WorkItem) error {
	_, err := tx.Exec(ctx, `
INSERT INTO work_item
  (tenant_id, type, repo, node_id, number, title, status, current_stage,
   author_login, changed_files, additions, deletions, created_at, merged_at, closed_at, updated_at)
VALUES
  ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15, now())
ON CONFLICT (tenant_id, repo, type, node_id) DO UPDATE SET
  title         = EXCLUDED.title,
  status        = EXCLUDED.status,
  current_stage = EXCLUDED.current_stage,
  changed_files = EXCLUDED.changed_files,
  additions     = EXCLUDED.additions,
  deletions     = EXCLUDED.deletions,
  merged_at     = EXCLUDED.merged_at,
  closed_at     = EXCLUDED.closed_at,
  updated_at    = now()`,
		tenantID, wi.Type, wi.Repo, wi.NodeID, wi.Number, wi.Title, wi.Status, wi.CurrentStage,
		wi.AuthorLogin, wi.ChangedFiles, wi.Additions, wi.Deletions, wi.CreatedAt, wi.MergedAt, wi.ClosedAt,
	)
	return err
}

func workItemPayload(wi connector.WorkItem) map[string]any {
	return map[string]any{
		"type":          wi.Type,
		"repo":          wi.Repo,
		"node_id":       wi.NodeID,
		"number":        wi.Number,
		"title":         wi.Title,
		"status":        wi.Status,
		"current_stage": wi.CurrentStage,
		"author_login":  wi.AuthorLogin,
		"changed_files": wi.ChangedFiles,
	}
}

func commit(ctx context.Context, log *slog.Logger, reader *kafka.Reader, msg kafka.Message) {
	if err := reader.Commit(ctx, msg); err != nil {
		log.Error("commit offset", "err", err, "delivery", msg.Key)
	}
}

// backoff returns an exponential delay capped at maxBackoff for the given attempt.
func backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := baseBackoff << (attempt - 1)
	if d > maxBackoff || d <= 0 {
		return maxBackoff
	}
	return d
}

// sleep waits for d or until ctx is cancelled (returns ctx.Err() in that case).
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

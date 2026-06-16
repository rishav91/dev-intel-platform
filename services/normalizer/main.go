// normalizer consumes enriched.github (raw.github after the connector-github
// enricher, P1.C), normalizes via the GitHub connector, resolves the tenant from
// the installation id, dedups the delivery, and — in one tx —
// upserts the canonical entities (work items, reviews, check runs) under RLS and
// stages a canonical event per entity in the outbox. The outbox relay publishes
// them to Kafka. At-least-once: process fully, then commit the offset.
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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
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

	// Consume the enriched stream (P1.C): connector-github sits between raw.github
	// and here, attaching GraphQL detail. In degraded mode it passes events through
	// unenriched, so this topic always carries the full stream regardless.
	reader := kafka.NewReader(brokers, group, events.TopicEnrichedGitHub)
	defer reader.Close()
	dlq := kafka.NewWriter(brokers, events.TopicDeadLetter)
	defer dlq.Close()

	src := ghconn.New()
	n := &normalizer{log: log, src: src, store: store, dlq: dlq}
	log.Info("started", "brokers", brokers, "topic", events.TopicEnrichedGitHub, "group", group)

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
	if res.Skip || res.Empty() {
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
	// mark, the entity upserts, and the canonical events (into the outbox) all
	// commit together: a redelivery is a provable no-op, and there is no window
	// where the write commits but the emit is lost. The outbox relay publishes the
	// events to Kafka afterwards (ADR-012).
	err = n.store.WithTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		fresh, err := markDelivery(ctx, tx, tenantID, raw.DeliveryID, raw.EventType)
		if err != nil {
			return err
		}
		if !fresh {
			return errDuplicate // rolls back; original write already committed
		}
		return n.persist(ctx, tx, tenantID, traceID, res)
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

// persist writes every entity a single webhook produced and stages a canonical
// event for each one — all inside the caller's tenant-scoped tx so the whole
// delivery commits atomically (ADR-012). Each emitted event carries the entity's
// own natural id as source_event_id (not the delivery id), so a push of N commits
// yields N independently-idempotent canonical events.
func (n *normalizer) persist(ctx context.Context, tx pgx.Tx, tenantID, traceID string, res connector.Result) error {
	// Upsert work items first and remember each one's id by node id, so reviews in
	// the same delivery can link to their PR. Items with an empty Event are
	// ensure-only link targets (e.g. the PR a review references): persisted if
	// absent, never clobbered, never emitted.
	idByNode := make(map[string]string, len(res.WorkItems))
	for _, wi := range res.WorkItems {
		var (
			id  string
			err error
		)
		if wi.Event == "" {
			id, err = ensureWorkItem(ctx, tx, tenantID, wi)
		} else {
			id, err = upsertWorkItem(ctx, tx, tenantID, wi)
		}
		if err != nil {
			return err
		}
		idByNode[wi.NodeID] = id
		if wi.Event == "" {
			continue
		}
		ev := events.New(tenantID, wi.Event, wi.NodeID, wi.OccurredAt, workItemPayload(wi))
		if err := enqueueOutbox(ctx, tx, tenantID, ev, traceID); err != nil {
			return err
		}
		n.log.Info("normalized", "type", ev.Type, "repo", wi.Repo, "number", wi.Number,
			"tenant_id", tenantID, "trace_id", traceID)
	}

	for _, rv := range res.Reviews {
		workItemID, ok := idByNode[rv.PRNodeID]
		if !ok {
			// The connector always emits the PR alongside its review, so a miss is a
			// normalization bug, not a transient fault → permanent (dead-letter).
			return permanentError{fmt.Errorf("review %s: parent PR %s not in delivery", rv.SourceID, rv.PRNodeID)}
		}
		if rv.ReviewerLogin == "" {
			n.log.Warn("review without reviewer login; skipping", "review", rv.SourceID, "trace_id", traceID)
			continue
		}
		reviewerID, err := resolveContributor(ctx, tx, tenantID, rv.ReviewerLogin)
		if err != nil {
			return err
		}
		if err := insertReview(ctx, tx, tenantID, workItemID, reviewerID, rv); err != nil {
			return err
		}
		ev := events.New(tenantID, events.ReviewSubmitted, rv.SourceID, rv.SubmittedAt, reviewPayload(rv))
		if err := enqueueOutbox(ctx, tx, tenantID, ev, traceID); err != nil {
			return err
		}
		n.log.Info("normalized", "type", events.ReviewSubmitted, "repo", rv.Repo,
			"tenant_id", tenantID, "trace_id", traceID)
	}

	for _, cr := range res.CheckRuns {
		if err := upsertCheckRun(ctx, tx, tenantID, cr); err != nil {
			return err
		}
		occurred := cr.CompletedAt
		if occurred == nil {
			occurred = cr.StartedAt
		}
		ev := events.New(tenantID, events.CheckCompleted, cr.SourceID, deref(occurred), checkRunPayload(cr))
		if err := enqueueOutbox(ctx, tx, tenantID, ev, traceID); err != nil {
			return err
		}
		n.log.Info("normalized", "type", events.CheckCompleted, "repo", cr.Repo, "name", cr.Name,
			"tenant_id", tenantID, "trace_id", traceID)
	}

	// Comments have no table (DATA-MODEL §2); they exist only as canonical events
	// for downstream consumers (review depth, blocker detection in the AI layer).
	for _, cm := range res.Comments {
		ev := events.New(tenantID, events.CommentAdded, cm.SourceID, cm.CreatedAt, commentPayload(cm))
		if err := enqueueOutbox(ctx, tx, tenantID, ev, traceID); err != nil {
			return err
		}
		n.log.Info("normalized", "type", events.CommentAdded, "repo", cm.Repo,
			"tenant_id", tenantID, "trace_id", traceID)
	}

	return nil
}

// upsertWorkItem inserts or refreshes a work item, returning its id. Used for
// the entity the webhook is actually about (PR/issue/commit), so it overwrites
// mutable fields with the latest values. Idempotent on (tenant, repo, type, node).
func upsertWorkItem(ctx context.Context, tx pgx.Tx, tenantID string, wi connector.WorkItem) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
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
  updated_at    = now()
RETURNING work_item_id`,
		tenantID, wi.Type, wi.Repo, wi.NodeID, wi.Number, wi.Title, wi.Status, wi.CurrentStage,
		wi.AuthorLogin, wi.ChangedFiles, wi.Additions, wi.Deletions, wi.CreatedAt, wi.MergedAt, wi.ClosedAt,
	).Scan(&id)
	return id, err
}

// ensureWorkItem inserts a work item only if absent and returns its id, never
// clobbering an existing (richer) row. Used for the PR a review links to when its
// own webhook may not have arrived yet. The no-op DO UPDATE makes RETURNING fire
// on conflict without changing any column.
func ensureWorkItem(ctx context.Context, tx pgx.Tx, tenantID string, wi connector.WorkItem) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
INSERT INTO work_item
  (tenant_id, type, repo, node_id, number, title, status, current_stage,
   author_login, changed_files, additions, deletions, created_at, merged_at, closed_at)
VALUES
  ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
ON CONFLICT (tenant_id, repo, type, node_id) DO UPDATE SET updated_at = work_item.updated_at
RETURNING work_item_id`,
		tenantID, wi.Type, wi.Repo, wi.NodeID, wi.Number, wi.Title, wi.Status, wi.CurrentStage,
		wi.AuthorLogin, wi.ChangedFiles, wi.Additions, wi.Deletions, wi.CreatedAt, wi.MergedAt, wi.ClosedAt,
	).Scan(&id)
	return id, err
}

// insertReview upserts a review row, idempotent on (tenant, source_id).
func insertReview(ctx context.Context, tx pgx.Tx, tenantID, workItemID, reviewerID string, rv connector.Review) error {
	_, err := tx.Exec(ctx, `
INSERT INTO review
  (tenant_id, work_item_id, reviewer_id, source_id, state, comment_count, submitted_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (tenant_id, source_id) DO UPDATE SET
  state         = EXCLUDED.state,
  comment_count = EXCLUDED.comment_count,
  submitted_at  = EXCLUDED.submitted_at`,
		tenantID, workItemID, reviewerID, rv.SourceID, rv.State, rv.CommentCount, rv.SubmittedAt)
	return err
}

// upsertCheckRun upserts a check_run row, idempotent on (tenant, source_id).
// work_item_id stays NULL here — it is correlated to a PR by head SHA in P1.E.
func upsertCheckRun(ctx context.Context, tx pgx.Tx, tenantID string, cr connector.CheckRun) error {
	_, err := tx.Exec(ctx, `
INSERT INTO check_run
  (tenant_id, source_id, head_sha, name, conclusion, started_at, completed_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (tenant_id, source_id) DO UPDATE SET
  conclusion   = EXCLUDED.conclusion,
  started_at   = EXCLUDED.started_at,
  completed_at = EXCLUDED.completed_at`,
		tenantID, cr.SourceID, cr.HeadSHA, cr.Name, nullIfEmpty(cr.Conclusion), cr.StartedAt, cr.CompletedAt)
	return err
}

// resolveContributor maps a GitHub login to a stable contributor id, creating
// the contributor + identity_link if unseen. This is the deterministic
// login-match subset of identity resolution; P1.F adds email/noreply merging on
// top. Idempotent via the unique (tenant, kind, hash) on identity_link.
func resolveContributor(ctx context.Context, tx pgx.Tx, tenantID, login string) (string, error) {
	hash := hashIdentifier(login)

	var id string
	err := tx.QueryRow(ctx, `
SELECT contributor_id FROM identity_link
WHERE tenant_id = $1 AND identifier_kind = 'github_login' AND identifier_value_hash = $2`,
		tenantID, hash).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}

	isBot := strings.HasSuffix(login, "[bot]")
	if err := tx.QueryRow(ctx, `
INSERT INTO contributor (tenant_id, display_name, is_bot, resolution_confidence)
VALUES ($1, $2, $3, 1.0) RETURNING contributor_id`,
		tenantID, login, isBot).Scan(&id); err != nil {
		return "", err
	}

	tag, err := tx.Exec(ctx, `
INSERT INTO identity_link (tenant_id, contributor_id, identifier_kind, identifier_value_hash, confidence)
VALUES ($1, $2, 'github_login', $3, 1.0)
ON CONFLICT (tenant_id, identifier_kind, identifier_value_hash) DO NOTHING`,
		tenantID, id, hash)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		// A concurrent delivery created the link first; the contributor we just
		// inserted is an unreferenced orphan (harmless, rare). Re-read the winner.
		if err := tx.QueryRow(ctx, `
SELECT contributor_id FROM identity_link
WHERE tenant_id = $1 AND identifier_kind = 'github_login' AND identifier_value_hash = $2`,
			tenantID, hash).Scan(&id); err != nil {
			return "", err
		}
	}
	return id, nil
}

// hashIdentifier hashes a raw identifier; identity_link stores hashes, never raw
// PII (METRICS-ETHICS.md). SHA-256 hex is stable across replays.
func hashIdentifier(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func workItemPayload(wi connector.WorkItem) map[string]any {
	p := map[string]any{
		"type":          wi.Type,
		"repo":          wi.Repo,
		"node_id":       wi.NodeID,
		"number":        wi.Number,
		"title":         wi.Title,
		"status":        wi.Status,
		"current_stage": wi.CurrentStage,
		"author_login":  wi.AuthorLogin,
		"changed_files": wi.ChangedFiles,
		"additions":     wi.Additions,
		"deletions":     wi.Deletions,
	}
	// Enriched detail (P1.C) rides in the canonical payload, not work_item columns:
	// commit_oids drive PR↔commit correlation (P1.E), files drive review-health
	// hotspots and Phase-3 semantic analysis. Omitted when the event wasn't enriched.
	if len(wi.CommitOIDs) > 0 {
		p["commit_oids"] = wi.CommitOIDs
	}
	if len(wi.Files) > 0 {
		p["files"] = wi.Files
	}
	return p
}

func reviewPayload(rv connector.Review) map[string]any {
	return map[string]any{
		"repo":           rv.Repo,
		"pr_node_id":     rv.PRNodeID,
		"source_id":      rv.SourceID,
		"reviewer_login": rv.ReviewerLogin,
		"state":          rv.State,
		"comment_count":  rv.CommentCount,
	}
}

func checkRunPayload(cr connector.CheckRun) map[string]any {
	return map[string]any{
		"repo":       cr.Repo,
		"source_id":  cr.SourceID,
		"head_sha":   cr.HeadSHA,
		"name":       cr.Name,
		"conclusion": cr.Conclusion,
	}
}

func commentPayload(cm connector.Comment) map[string]any {
	return map[string]any{
		"repo":          cm.Repo,
		"source_id":     cm.SourceID,
		"parent_type":   cm.ParentType,
		"parent_number": cm.ParentNumber,
		"author_login":  cm.AuthorLogin,
		"body":          cm.Body,
	}
}

func deref(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
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

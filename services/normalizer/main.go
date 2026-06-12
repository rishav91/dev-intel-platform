// normalizer consumes raw.github, normalizes via the GitHub connector, resolves
// the tenant from the installation id, upserts the canonical work_item under RLS,
// and emits a canonical event. At-least-once: process fully, then commit offset.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5"

	"github.com/dev-intel/platform/libs/go/connector"
	ghconn "github.com/dev-intel/platform/libs/go/connector/github"
	"github.com/dev-intel/platform/libs/go/events"
	"github.com/dev-intel/platform/libs/go/kafka"
	"github.com/dev-intel/platform/libs/go/observability"
	"github.com/dev-intel/platform/libs/go/tenancy"
)

func main() {
	log := observability.Logger("normalizer")

	brokers := splitEnv("KAFKA_BROKERS", "localhost:9092")
	// Connect as the least-privilege app role (NOSUPERUSER/NOBYPASSRLS) so RLS
	// actually engages — connecting as the superuser owner bypasses it. See 0003.
	dsn := getenv("POSTGRES_DSN", "postgres://devintel_app:devintel_app@localhost:5432/devintel")
	group := getenv("CONSUMER_GROUP", "normalizer")

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
	writer := kafka.NewWriter(brokers, events.TopicCanonical)
	defer writer.Close()

	src := ghconn.New()
	log.Info("started", "brokers", brokers, "topic", events.TopicRawGitHub)

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

		if err := process(ctx, log, src, store, writer, msg); err != nil {
			// Don't commit on failure → message is redelivered (at-least-once).
			log.Error("process failed; will retry", "err", err, "delivery", msg.Key)
			continue
		}
		if err := reader.Commit(ctx, msg); err != nil {
			log.Error("commit offset", "err", err, "delivery", msg.Key)
		}
	}
}

func process(ctx context.Context, log *slog.Logger, src connector.Source, store *tenancy.Store, writer *kafka.Writer, msg kafka.Message) error {
	traceID := msg.Headers[events.HeaderTraceID]
	raw := connector.RawEvent{
		DeliveryID: msg.Headers[events.HeaderGitHubDeliv],
		EventType:  msg.Headers[events.HeaderGitHubEvent],
		Body:       msg.Value,
		TraceID:    traceID,
	}

	res, err := src.Normalize(raw)
	if err != nil {
		return err
	}
	if res.Skip || len(res.Items) == 0 {
		log.Info("skipped event", "event", raw.EventType, "trace_id", traceID)
		return nil
	}

	tenantID, err := store.ResolveTenant(ctx, res.InstallationID)
	if err != nil {
		// Unknown installation is a permanent error for this message; log and skip
		// (committing) so it doesn't block the partition.
		if errors.Is(err, tenancy.ErrUnknownInstallation) {
			log.Warn("unknown installation; dropping", "installation", res.InstallationID, "trace_id", traceID)
			return nil
		}
		return err
	}

	// Persist all items under the tenant's RLS scope in one transaction.
	err = store.WithTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		for _, wi := range res.Items {
			if err := upsertWorkItem(ctx, tx, tenantID, wi); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Emit canonical events (after the write commits).
	for _, wi := range res.Items {
		ev := events.New(tenantID, res.EventType, raw.DeliveryID, wi.CreatedAt, workItemPayload(wi))
		b, _ := json.Marshal(ev)
		if err := writer.Write(ctx, tenantID, b, map[string]string{events.HeaderTraceID: traceID}); err != nil {
			return err
		}
		log.Info("normalized", "type", ev.Type, "repo", wi.Repo, "number", wi.Number,
			"tenant_id", tenantID, "trace_id", traceID)
	}
	return nil
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

// --- env helpers ---
//nolint:unused

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func splitEnv(k, def string) []string {
	parts := strings.Split(getenv(k, def), ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

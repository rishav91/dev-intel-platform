// outbox-relay publishes staged canonical events from the outbox table to Kafka
// (ADR-012). The normalizer enqueues events transactionally with the write; this
// relay drains them, so the canonical topic is fed exactly from committed rows —
// no dual-write window.
//
// Loop: in one tx, lock a batch of unpublished rows (FOR UPDATE SKIP LOCKED, in
// insertion order), publish each to Kafka, then stamp published_at and commit.
// At-least-once: if it crashes after publishing but before commit, the rows stay
// unpublished and are republished next pass — downstream is keyed/upserting, so a
// duplicate is absorbed. Connects as devintel_relay (NOSUPERUSER/NOBYPASSRLS) whose
// outbox policy lets it read/update every tenant's rows but nothing else.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dev-intel/platform/libs/go/config"
	"github.com/dev-intel/platform/libs/go/events"
	"github.com/dev-intel/platform/libs/go/kafka"
	"github.com/dev-intel/platform/libs/go/observability"
)

func main() {
	log := observability.Logger("outbox-relay")

	brokers := config.List("KAFKA_BROKERS", "localhost:9092")
	// Dedicated relay role: may drain the outbox across tenants, nothing else.
	dsn := config.String("RELAY_DSN", "postgres://devintel_relay:devintel_relay@localhost:5432/devintel")
	poll := config.Duration("RELAY_POLL_INTERVAL", 250*time.Millisecond)
	batchSize := config.Int("RELAY_BATCH_SIZE", 100)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Error("db connect", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Error("db ping", "err", err)
		os.Exit(1)
	}

	writer := kafka.NewTopicWriter(brokers)
	defer writer.Close()

	r := &relay{log: log, pool: pool, writer: writer, batchSize: batchSize}
	log.Info("started", "brokers", brokers, "poll", poll.String(), "batch", batchSize)

	for {
		n, err := r.drainBatch(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				log.Info("shutting down")
				return
			}
			log.Error("drain failed; will retry", "err", err)
		}
		// Only idle when the queue was empty; otherwise keep draining promptly.
		if n == 0 {
			if sleep(ctx, poll) != nil {
				log.Info("shutting down")
				return
			}
		}
	}
}

type relay struct {
	log       *slog.Logger
	pool      *pgxpool.Pool
	writer    *kafka.TopicWriter
	batchSize int
}

type outboxRow struct {
	id      int64
	tenant  string
	eventID string
	topic   string
	key     string
	traceID string
	payload []byte
}

// drainBatch publishes up to batchSize pending rows and returns how many it
// published. Rows are locked FOR UPDATE SKIP LOCKED so multiple relay instances
// don't double-publish; a single instance preserves per-tenant order.
func (r *relay) drainBatch(ctx context.Context) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	rows, err := tx.Query(ctx, `
SELECT outbox_id, tenant_id, event_id, topic, kafka_key, trace_id, payload
FROM outbox
WHERE published_at IS NULL
ORDER BY outbox_id
LIMIT $1
FOR UPDATE SKIP LOCKED`, r.batchSize)
	if err != nil {
		return 0, err
	}
	var batch []outboxRow
	for rows.Next() {
		var o outboxRow
		var trace *string
		if err := rows.Scan(&o.id, &o.tenant, &o.eventID, &o.topic, &o.key, &trace, &o.payload); err != nil {
			rows.Close()
			return 0, err
		}
		if trace != nil {
			o.traceID = *trace
		}
		batch = append(batch, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(batch) == 0 {
		return 0, nil
	}

	published := make([]int64, 0, len(batch))
	for _, o := range batch {
		headers := map[string]string{}
		if o.traceID != "" {
			headers[events.HeaderTraceID] = o.traceID
		}
		if err := r.writer.Write(ctx, o.topic, o.key, o.payload, headers); err != nil {
			// Stop at the first publish failure so we don't skip ahead and break
			// per-tenant order; rows already published this pass are committed,
			// the rest retry next pass.
			r.log.Error("publish failed; will retry remainder", "err", err, "event_id", o.eventID)
			break
		}
		published = append(published, o.id)
		r.log.Info("published", "topic", o.topic, "event_id", o.eventID, "tenant_id", o.tenant, "trace_id", o.traceID)
	}

	if len(published) > 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE outbox SET published_at = now() WHERE outbox_id = ANY($1)`, published); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(published), nil
}

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

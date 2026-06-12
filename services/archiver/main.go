// archiver consumes raw.github and writes every payload to the object store
// keyed raw/github/<tenant>/<date>/<delivery>.json. This is the replay safety
// net (ADR-010, FR-2.4): the canonical log + this raw archive together let any
// read model — or the whole graph — be dropped and rebuilt by replay.
//
// It runs as its own consumer group, independent of the normalizer, so archiving
// never blocks (or is blocked by) normalization. At-least-once: archive the
// payload, then commit the offset. PutIfAbsent makes redeliveries no-ops.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dev-intel/platform/libs/go/config"
	"github.com/dev-intel/platform/libs/go/events"
	"github.com/dev-intel/platform/libs/go/kafka"
	"github.com/dev-intel/platform/libs/go/objectstore"
	"github.com/dev-intel/platform/libs/go/observability"
	"github.com/dev-intel/platform/libs/go/tenancy"
)

const (
	maxBackoff  = 30 * time.Second
	baseBackoff = 250 * time.Millisecond
)

func main() {
	log := observability.Logger("archiver")

	brokers := config.List("KAFKA_BROKERS", "localhost:9092")
	dsn := config.String("POSTGRES_DSN", "postgres://devintel_app:devintel_app@localhost:5432/devintel")
	group := config.String("ARCHIVER_CONSUMER_GROUP", "archiver")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := tenancy.New(ctx, dsn)
	if err != nil {
		log.Error("db connect", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	obj, err := objectstore.New(ctx, objectstore.Config{
		Endpoint:  config.String("S3_ENDPOINT", "http://localhost:8333"),
		Region:    config.String("S3_REGION", "us-east-1"),
		AccessKey: config.String("S3_ACCESS_KEY", "devintel"),
		SecretKey: config.String("S3_SECRET_KEY", "devintel12345"),
		Bucket:    config.String("S3_RAW_BUCKET", "raw-github"),
	})
	if err != nil {
		log.Error("object store init", "err", err)
		os.Exit(1)
	}

	reader := kafka.NewReader(brokers, group, events.TopicRawGitHub)
	defer reader.Close()

	a := &archiver{log: log, store: store, obj: obj}
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

		if err := a.archive(ctx, msg); err != nil {
			// Archiving is the safety net — never drop. Transient failures (S3/DB
			// down) retry with backoff without committing the offset.
			attempt++
			if sleep(ctx, backoff(attempt)) != nil {
				return // ctx cancelled during backoff
			}
			a.log.Error("archive failed; will retry", "err", err, "delivery", msg.Key, "attempt", attempt)
			continue
		}
		attempt = 0
		if err := reader.Commit(ctx, msg); err != nil {
			log.Error("commit offset", "err", err, "delivery", msg.Key)
		}
	}
}

type archiver struct {
	log   *slog.Logger
	store *tenancy.Store
	obj   *objectstore.Store
}

// installationEnvelope is the only field the archiver parses from the body; it
// never normalizes — it just needs the installation id to derive the tenant key.
type installationEnvelope struct {
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

func (a *archiver) archive(ctx context.Context, msg kafka.Message) error {
	delivery := msg.Headers[events.HeaderGitHubDeliv]
	traceID := msg.Headers[events.HeaderTraceID]
	if delivery == "" {
		delivery = msg.Key
	}

	partition, err := a.tenantPartition(ctx, msg.Value)
	if err != nil {
		return err // transient (e.g. DB down) → retried by the caller
	}
	date := time.Now().UTC().Format("2006-01-02")
	key := fmt.Sprintf("raw/github/%s/%s/%s.json", partition, date, delivery)

	written, err := a.obj.PutIfAbsent(ctx, key, msg.Value, "application/json")
	if err != nil {
		return err
	}
	if written {
		a.log.Info("archived", "key", key, "delivery", delivery, "trace_id", traceID)
	} else {
		a.log.Info("already archived (redelivery)", "key", key, "delivery", delivery, "trace_id", traceID)
	}
	return nil
}

// tenantPartition derives the object-key partition. It resolves
// installation→tenant when possible; an *unknown* installation falls back to an
// _unresolved partition so nothing is ever dropped (unknown installs may be
// onboarded/backfilled later, and the raw stays replayable). A *transient* DB
// error is returned so the caller retries rather than mis-filing known traffic
// under _unresolved during an outage.
func (a *archiver) tenantPartition(ctx context.Context, body []byte) (string, error) {
	var env installationEnvelope
	if err := json.Unmarshal(body, &env); err != nil || env.Installation.ID == 0 {
		return "_unresolved/_no_installation", nil
	}
	tenantID, err := a.store.ResolveTenant(ctx, env.Installation.ID)
	switch {
	case err == nil:
		return tenantID, nil
	case errors.Is(err, tenancy.ErrUnknownInstallation):
		return fmt.Sprintf("_unresolved/%d", env.Installation.ID), nil
	default:
		return "", err
	}
}

func backoff(attempt int) time.Duration {
	d := baseBackoff << (attempt - 1)
	if d > maxBackoff || d <= 0 {
		return maxBackoff
	}
	return d
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

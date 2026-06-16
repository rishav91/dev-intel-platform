// connector-github is the GitHub enrichment stage (P1.C). It sits between
// raw.github and the normalizer: it consumes each raw webhook, and for the event
// types that carry too little (today: pull_request), it fetches the missing
// detail from the GitHub GraphQL API — authoritative size counts, the commit
// list, and per-file churn — and injects it into the payload as an `_enrichment`
// block before emitting to enriched.github. The normalizer reads that block.
//
// Why a separate service (not folded into the normalizer): enrichment is
// network-bound and rate-limited (P1.A budget); isolating it keeps the
// normalizer's DB hot path free of GitHub latency/throttling, and keeps all
// rate-budget logic in one place. Cost: one more deployable (REPO-LAYOUT).
//
// Degraded behavior (FR-2.8): enrichment is best-effort. With no credentials, an
// un-enrichable event type, a transient GraphQL error, or an exhausted budget,
// the original event is passed through unchanged (stamped with why) so the
// pipeline never stalls. Missing detail can be backfilled later (P1.G).
//
// At-least-once: enrich (or pass through), emit, then commit the offset. The
// delivery id is preserved as the Kafka key, so the normalizer's per-delivery
// dedup still makes a redelivery a no-op even if we emit twice.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	"github.com/dev-intel/platform/libs/go/config"
	"github.com/dev-intel/platform/libs/go/events"
	"github.com/dev-intel/platform/libs/go/githubapp"
	"github.com/dev-intel/platform/libs/go/kafka"
	"github.com/dev-intel/platform/libs/go/observability"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	baseBackoff = 250 * time.Millisecond
	maxBackoff  = 30 * time.Second
	// maxEnrichWait bounds how long we'll block a partition waiting for a rate
	// window to reset before giving up and passing the event through unenriched.
	maxEnrichWait = 30 * time.Second
)

// enrich-status header values.
const (
	statusEnriched    = "enriched"
	statusSkipped     = "skipped"      // event type doesn't need enrichment
	statusDisabled    = "disabled"     // no credentials configured
	statusFailed      = "failed"       // enrichment errored; passed through
	statusRateLimited = "rate_limited" // budget exhausted past the wait cap
)

func main() {
	log := observability.Logger("connector-github")

	brokers := config.List("KAFKA_BROKERS", "localhost:9092")
	group := config.String("CONNECTOR_CONSUMER_GROUP", "connector-github")
	fetchPatches := config.String("ENRICH_PATCHES", "") != "" // off by default (log bloat)
	patchMaxBytes := config.Int("ENRICH_PATCH_MAX_BYTES", 64<<10)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := observability.Init(ctx, "connector-github")
	if err != nil {
		log.Error("tracing init", "err", err)
		return
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTracing(sctx)
	}()

	// Enrichment needs App credentials. Absent (dev, or before an App is
	// registered) → run as a pure pass-through so the pipeline still flows.
	var client *githubapp.Client
	if _, c, err := githubapp.LoadFromEnv(); err != nil {
		log.Warn("enrichment disabled: no GitHub App credentials; passing events through", "err", err)
	} else {
		client = c
		log.Info("enrichment enabled", "fetch_patches", fetchPatches)
	}

	reader := kafka.NewReader(brokers, group, events.TopicRawGitHub)
	defer reader.Close()
	writer := kafka.NewWriter(brokers, events.TopicEnrichedGitHub)
	defer writer.Close()

	c := &connector{log: log, client: client, writer: writer, fetchPatches: fetchPatches, patchMaxBytes: patchMaxBytes}
	log.Info("started", "brokers", brokers, "in", events.TopicRawGitHub, "out", events.TopicEnrichedGitHub, "group", group)

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

		if err := c.process(ctx, msg); err != nil {
			// Emitting failed (Kafka down) → don't commit; retry with backoff.
			attempt++
			log.Error("process failed; will retry", "err", err, "delivery", msg.Key, "attempt", attempt)
			if sleep(ctx, backoff(attempt)) != nil {
				return
			}
			continue
		}
		attempt = 0
		if err := reader.Commit(ctx, msg); err != nil {
			log.Error("commit offset", "err", err, "delivery", msg.Key)
		}
	}
}

type connector struct {
	log           *slog.Logger
	client        *githubapp.Client // nil → pass-through
	writer        *kafka.Writer
	fetchPatches  bool
	patchMaxBytes int
}

// prEnvelope is the minimal slice of a pull_request webhook the enricher needs to
// address the GraphQL call.
type prEnvelope struct {
	Number      int `json:"number"`
	PullRequest struct {
		UpdatedAt time.Time `json:"updated_at"`
		CreatedAt time.Time `json:"created_at"`
	} `json:"pull_request"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

func (c *connector) process(ctx context.Context, msg kafka.Message) error {
	// Continue the trace started at the gateway: extract upstream context from the
	// inbound headers, then open this stage's span as a child of it.
	ctx = observability.Extract(ctx, msg.Headers)
	eventType := msg.Headers[events.HeaderGitHubEvent]
	ctx, span := observability.Tracer().Start(ctx, "connector.enrich",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attribute.String("github.event", eventType)))
	defer span.End()

	body, status, occurredAt := c.enrich(ctx, eventType, msg.Value)
	span.SetAttributes(attribute.String("enrich.status", status))

	headers := make(map[string]string, len(msg.Headers)+2)
	for k, v := range msg.Headers {
		headers[k] = v
	}
	headers[events.HeaderEnrichStatus] = status
	if !occurredAt.IsZero() {
		// Stamp source time so downstream ordering uses it, not arrival (P1.C).
		headers[events.HeaderOccurredAt] = occurredAt.UTC().Format(time.RFC3339)
	}
	observability.Inject(ctx, headers) // re-stamp traceparent for this span
	return c.writer.Write(ctx, msg.Key, body, headers)
}

// enrich returns the (possibly augmented) body, the enrich-status, and the source
// event time. It never returns an error: enrichment is best-effort and falls back
// to pass-through, so a GitHub problem can't stall ingestion.
func (c *connector) enrich(ctx context.Context, eventType string, body []byte) ([]byte, string, time.Time) {
	if eventType != "pull_request" {
		return body, statusSkipped, time.Time{} // only PRs need enrichment today
	}

	var env prEnvelope
	if err := json.Unmarshal(body, &env); err != nil || env.Installation.ID == 0 || env.Repository.FullName == "" {
		c.log.WarnContext(ctx, "cannot address enrichment; passing through")
		return body, statusFailed, time.Time{}
	}
	occurredAt := env.PullRequest.UpdatedAt
	if occurredAt.IsZero() {
		occurredAt = env.PullRequest.CreatedAt
	}

	if c.client == nil {
		return body, statusDisabled, occurredAt
	}

	enr, err := c.fetchEnrichment(ctx, env)
	if err != nil {
		var rl *githubapp.RateLimitedError
		if errors.As(err, &rl) {
			c.log.WarnContext(ctx, "rate limited; passing through unenriched", "wait", rl.RetryAfter,
				"repo", env.Repository.FullName, "number", env.Number)
			return body, statusRateLimited, occurredAt
		}
		c.log.WarnContext(ctx, "enrichment failed; passing through", "err", err,
			"repo", env.Repository.FullName, "number", env.Number)
		return body, statusFailed, occurredAt
	}

	merged, err := mergeEnrichment(body, enr)
	if err != nil {
		c.log.WarnContext(ctx, "merge failed; passing through", "err", err)
		return body, statusFailed, occurredAt
	}
	c.log.InfoContext(ctx, "enriched", "repo", env.Repository.FullName, "number", env.Number,
		"files", len(enr.Files), "commits", len(enr.CommitOIDs))
	return merged, statusEnriched, occurredAt
}

// fetchEnrichment runs the GraphQL enrichment, optionally waiting out a short
// rate-limit window, and (if enabled) overlays REST-fetched patches.
func (c *connector) fetchEnrichment(ctx context.Context, env prEnvelope) (githubapp.PullRequestEnrichment, error) {
	enr, err := c.client.EnrichPullRequest(ctx, env.Installation.ID, env.Repository.FullName, env.Number)
	var rl *githubapp.RateLimitedError
	if errors.As(err, &rl) && rl.RetryAfter <= maxEnrichWait {
		// Brief window — wait it out rather than lose enrichment for this PR.
		if sleep(ctx, rl.RetryAfter) != nil {
			return enr, err // ctx cancelled; surface the rate-limit
		}
		enr, err = c.client.EnrichPullRequest(ctx, env.Installation.ID, env.Repository.FullName, env.Number)
	}
	if err != nil {
		return enr, err
	}

	if c.fetchPatches {
		files, perr := c.client.FetchPullRequestPatches(ctx, env.Installation.ID, env.Repository.FullName, env.Number, c.patchMaxBytes)
		if perr != nil {
			// Patches are a bonus; keep the GraphQL enrichment if they fail.
			c.log.WarnContext(ctx, "patch fetch failed; keeping metadata-only enrichment", "err", perr,
				"repo", env.Repository.FullName, "number", env.Number)
		} else {
			enr.Files = files // REST file list carries churn + patch text
		}
	}
	return enr, nil
}

// mergeEnrichment injects the `_enrichment` block into the original payload,
// preserving every original field byte-for-byte (map of RawMessage → re-marshal).
func mergeEnrichment(body []byte, enr githubapp.PullRequestEnrichment) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	enrJSON, err := json.Marshal(enr)
	if err != nil {
		return nil, err
	}
	m["_enrichment"] = enrJSON
	return json.Marshal(m)
}

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

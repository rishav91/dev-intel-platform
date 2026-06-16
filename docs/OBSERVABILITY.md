# Observability — end-to-end tracing & log correlation

How to follow one request across every service, with a `trace_id`, from the
GitHub webhook to the published canonical event. Tier 2 of the locked stack:
**OpenTelemetry → Tempo + Loki + Grafana** (NFR-7.2).

## TL;DR

```bash
make up                       # brings up the stack incl. Tempo/Loki/Grafana
make migrate                  # apply migrations to an already-running DB (adds outbox.traceparent)
make run-gateway              # each in its own terminal; they tee logs to ./logs/
make run-connector
make run-normalizer
make run-relay
make run-archiver
make send-sample              # fire a webhook
open http://localhost:3000    # Grafana (anonymous admin) → Explore → Tempo
```

In Grafana → **Explore → Tempo → Search**, open the latest trace: you get a
waterfall `webhook.receive → connector.enrich → normalizer.process → relay.publish`
(with `archiver.archive` in parallel). Each span links to its log lines in Loki;
each log line links back to its trace. Filter logs directly with
`{job="devintel"} | json | trace_id="<id>"`.

## What you get

- **One trace per request** spanning all services and the Kafka + outbox hops.
- **`trace_id`/`span_id` on every log line** (structured JSON via slog), so logs
  and traces cross-link both directions in Grafana.
- **Degraded-safe**: if no OTLP collector is reachable, spans are still created
  (logs stay correlated) and exports just drop — tracing never blocks the hot path.

## Architecture

```
GitHub ─webhook─▶ webhook-gateway ──raw.github──▶ connector-github ──enriched.github──▶ normalizer
                  (starts trace)                  (enrich span)                         (process span)
                       │                                                                     │
                       └──raw.github──▶ archiver (parallel consumer, own span)               │ writes outbox row
                                                                                             │ (traceparent persisted)
                                                                              outbox-relay ◀─┘
                                                                              (publish span) ──canonical.events──▶
```

Trace context rides as the **W3C `traceparent` header** on each Kafka message
(`libs/go/events.HeaderTraceParent`). A human-readable `trace-id` header is set
alongside for convenience, but nothing parses it for propagation.

### The two non-obvious seams

1. **Kafka boundary.** A consumer can't share an in-process context with its
   producer. Each service `Extract`s the upstream context from the inbound
   message headers, starts its span as a child, and `Inject`s its own context
   into the outbound headers. That stitches separate processes into one trace.

2. **The outbox DB hop.** The normalizer processes a delivery in one trace and
   stages canonical events in the `outbox` table *in the same transaction*. The
   relay drains those rows later, in a **different process**. So the trace
   context is persisted on the row (`outbox.traceparent`, migration 0007); the
   relay re-extracts it and publishes each event as a child span. Without this
   the trace would break exactly at the normalizer→relay boundary. (Mirrors how
   the outbox already carried `trace_id` for logs — ADR-012.)

## Code map

| Where | What |
|-------|------|
| `libs/go/observability` | `Init` (tracer provider + OTLP/HTTP exporter + W3C propagator), `Tracer`, `Inject`/`Extract` (Kafka header carrier), `TraceID`, and the slog handler that stamps `trace_id`/`span_id`. |
| `libs/go/events` | `HeaderTraceParent` (propagation) + `HeaderTraceID` (human-readable). |
| service `main.go` ×5 | `observability.Init(ctx, "<service>")` at startup; `Extract`→`Start span`→`Inject` per message. |
| `services/normalizer` | `enqueueOutbox` injects `traceparent` into the row. |
| `services/outbox-relay` | `drainBatch` extracts `traceparent` from the row, opens `relay.publish` child span. |
| `db/migrations/0007_outbox_traceparent.sql` | `outbox.traceparent` column. |
| `deploy/observability/` | Tempo, Loki, Promtail, and Grafana datasource config. |

## Local stack

The Go services run on the **host** (`make run-*`), not in containers, so:

- **Traces**: services export OTLP/HTTP to `OTEL_EXPORTER_OTLP_ENDPOINT`
  (default `localhost:4318`) → Tempo's OTLP receiver. (No separate collector —
  Tempo receives OTLP directly to keep the dev stack lean.)
- **Logs**: each `run-*` target tees JSON logs to `./logs/<service>.log`;
  Promtail (in compose) tails that dir into Loki. `trace_id` stays a parsed JSON
  field (not a Loki label) to avoid cardinality blow-up.

| Service | Port | Purpose |
|---------|------|---------|
| Grafana | 3000 | UI (anonymous admin) |
| Tempo   | 3200 / 4318 / 4317 | query API / OTLP-HTTP / OTLP-gRPC |
| Loki    | 3100 | log store |

## Production notes (not built now)

- Swap the host log-tee + Promtail for the standard container-stdout → Promtail
  (or OTLP logs) path once services run in Kubernetes.
- Front Tempo/Loki with an **OpenTelemetry Collector** for batching, tail-based
  sampling, and PII scrubbing (Presidio) before storage.
- Sampling: dev uses `AlwaysSample`; production should use parent-based +
  ratio sampling. Tune in `observability.Init`.
- **Metrics (Tier 3)** — Prometheus + OTel metrics (consumer lag, enrich
  hit/miss, rate-budget) — is deferred; this doc covers traces + logs.

# Dev Intelligence Platform

A **multi-tenant developer-intelligence platform** built on **GitHub as the single source of truth**. It ingests GitHub activity (PRs, reviews, commits, issues, CI checks), builds an intra-GitHub entity graph with contributor identity resolution, and surfaces **delivery-flow bottlenecks, recurring blockers, code-review health, CI reliability, collaboration patterns, and change risk** — with a governed AI layer for proactive insights and natural-language Q&A.

> **Scope decision:** one source, gone deep — not four, gone shallow. The architecture stays source-agnostic (canonical event model + pluggable connector seam) so a second source (Slack is the natural next) can be added later, but only the GitHub connector is built now. See [ADR-001](docs/ADRs.md).

**Target scale:** 100k DAU across 5k tenants (a tenant = a customer org connecting their GitHub). **Posture:** open-source / cloud-portable, event-driven, CQRS, polyglot persistence.

---

## The signal-confidence principle

We **only ship metrics built on signals reliably present in essentially every active GitHub repo.** If a signal is thin or inconsistently populated, the dependent metric is excluded entirely rather than shipped shaky.

| Tier | Signals | Policy |
|------|---------|--------|
| **STRONG** (core) | PRs, reviews, review comments, commits, issues + labels + reopen, check runs / statuses (CI), branches, refs | Build core metrics on these. |
| **CAPABILITY-GATED** | Deployments / Environments, Releases | Activate the dependent metric only if the connector detects the signal for a tenant. |
| **EXCLUDED** (thin) | Projects v2, Milestones, Iterations | Do **not** build metrics on these. |

## Insight pillars

**Core (P0 MVP)** — all on STRONG signals:
1. **PR flow & bottlenecks** — cycle-time decomposition, review wait, idle time, stuck/stale PRs.
2. **Code review health** — review depth, rubber-stamping, PR size vs. outcome, self-merge risk, hotspot files.
3. **CI reliability** — pass rate, time-to-green, flaky-check detection.
4. **Recurring blockers** — stuck PRs, flaky CI, rework loops, reopened items, clustered failure themes (AI).

**Staged** — contributor & collaboration (P1), change/revert risk (P1, AI), AI-authorship impact (P2, experimental).

---

## Architecture

Canonical, source-agnostic events flow on Kafka; read models are rebuildable projections (CQRS). Every consumer is idempotent and tenant-scoped (Postgres RLS backstop).

```
GitHub ──webhook──▶ webhook-gateway ──raw.github──┬─▶ archiver ──▶ object store (replay safety net)
                    (HMAC verify, 202)            │
                                                  └─▶ connector-github ──enriched.github──▶ normalizer
                                                      (GraphQL enrichment)                  (normalize, dedup,
                                                                                             RLS write + outbox)
                                                                                                    │
                                                            canonical.events ◀── outbox-relay ◀─────┘
                                                                                  (transactional outbox)
```

A single request is traced end-to-end (OpenTelemetry) across every service **and** the Kafka/outbox hops; see [docs/OBSERVABILITY.md](docs/OBSERVABILITY.md).

### Stack (locked)

- **Languages:** Go (platform/core services), Python 3.12 + FastAPI (AI/ML).
- **Event backbone:** Apache Kafka (Redpanda locally). **Stream processing:** Apache Flink. **Workflows:** Temporal.
- **OLTP:** PostgreSQL 16 + Citus (sharded by `tenant_id`, RLS). **Analytics:** ClickHouse. **Cache:** Redis. **Search:** OpenSearch. **Vectors:** pgvector → Qdrant.
- **Object store:** S3-compatible (SeaweedFS locally). **AI gateway:** LiteLLM; serving vLLM + TEI.
- **AuthN:** Keycloak/OIDC. **AuthZ:** OPA + Postgres RLS. **Secrets:** Vault.
- **Observability:** OpenTelemetry → Prometheus + Grafana + Tempo + Loki.
- **API:** GraphQL BFF + REST (external), gRPC (internal). **Runtime:** Kubernetes + Helm. **IaC:** Terraform. **CI:** GitHub Actions.

---

## Repository layout

| Path | Contents |
|------|----------|
| [services/](services/) | Deployable Go services: `webhook-gateway`, `connector-github`, `normalizer`, `archiver`, `outbox-relay`. |
| [libs/go/](libs/go/) | Shared libs: `events`, `kafka`, `tenancy` (RLS), `connector` (+`github`), `githubapp`, `observability`, `objectstore`, `config`. |
| [cmd/](cmd/) | CLIs (e.g. `ghcheck` — GitHub App smoke tool). |
| [db/migrations/](db/migrations/) | SQL migrations (schema + RLS policies). |
| [deploy/](deploy/) | `docker-compose.dev.yml` + observability stack config. |
| [schemas/](schemas/) | Canonical event JSON schema (contract of record). |
| [tests/](tests/) | Red-team RLS isolation gate, etc. |
| [docs/](docs/) | Design docs (see map below). |

---

## Quickstart (local dev)

**Prerequisites:** Go 1.24+, Docker. No GitHub credentials required — the connector runs in pass-through mode without them.

```bash
# 1. Bring up the local stack (Redpanda, Postgres/Citus, Redis, SeaweedFS,
#    plus Tempo/Loki/Grafana for tracing).
make up
make migrate                  # apply migrations to the running DB

# 2. Run the pipeline services, each in its own terminal:
make run-gateway
make run-connector
make run-normalizer
make run-relay
make run-archiver

# 3. Fire a sample pull_request webhook through the spine:
make send-sample

# 4. Watch the trace end-to-end in Grafana → Explore → Tempo:
open http://localhost:3000
```

### Tests

```bash
make test             # unit tests
make test-isolation   # red-team RLS tenant-isolation gate (needs running Postgres)
```

### Working with a real GitHub App (optional)

Set `GITHUB_APP_ID` + `GITHUB_APP_PRIVATE_KEY_PATH` in `.env` (copy from [.env.example](.env.example)), then:

```bash
make ghcheck-list                       # list the App's installations
make ghcheck-repos INSTALL=<id>         # list repos an installation can access
make ghcheck INSTALL=<id> REPO=<o/n>    # mint token + detect capabilities
```

See the full target list with the comments in the [Makefile](Makefile).

---

## Status

Phase 1 (ingestion depth + correlation + identity) in progress. Phase-0 spine, full event coverage (P1.B), GraphQL enrichment (P1.C), and end-to-end tracing are in place; state-transition derivation, the entity graph, and identity resolution are next. Track progress in [docs/IMPLEMENTATION-PLAN.md](docs/IMPLEMENTATION-PLAN.md) and [docs/ROADMAP.md](docs/ROADMAP.md).

---

## Documentation

Start with [CLAUDE.md](CLAUDE.md) for orientation, then:

**Reading order:** [PRD](docs/PRD.md) → [ARCHITECTURE](docs/ARCHITECTURE.md) → [DATA-MODEL](docs/DATA-MODEL.md) → [AI-ARCHITECTURE](docs/AI-ARCHITECTURE.md) → [ADRs](docs/ADRs.md) → [ROADMAP](docs/ROADMAP.md) → [REPO-LAYOUT](docs/REPO-LAYOUT.md).

| Doc | Purpose |
|-----|---------|
| [PRD.md](docs/PRD.md) | Problem, personas, insight pillars, signal-confidence scope, success metrics. |
| [ARCHITECTURE.md](docs/ARCHITECTURE.md) | System design: ingestion, correlation + identity, CQRS, persistence, scale. |
| [AI-ARCHITECTURE.md](docs/AI-ARCHITECTURE.md) | AI subsystem: funnel, RAG, risk scoring, governance, telemetry. |
| [DATA-MODEL.md](docs/DATA-MODEL.md) | Canonical schema, GitHub event mapping, entity graph, identity resolution. |
| [STATE-MACHINE.md](docs/STATE-MACHINE.md) | Work-item stage FSM + transition table; spine of flow/bottleneck metrics. |
| [METRIC-SPEC.md](docs/METRIC-SPEC.md) | Exact metric formulas, exclusions, confidence flags, min sample thresholds. |
| [METRICS-ETHICS.md](docs/METRICS-ETHICS.md) | Contributor-analytics ethics, k-anonymity suppression, "won't build" list. |
| [GITHUB-APP.md](docs/GITHUB-APP.md) | Required App permissions/events, degraded behavior, capability gating. |
| [API-CONTRACTS.md](docs/API-CONTRACTS.md) | Webhook intake, GraphQL insight queries, AI/chat, internal gRPC. |
| [OBSERVABILITY.md](docs/OBSERVABILITY.md) | End-to-end tracing + log correlation across services and the Kafka/outbox hops. |
| [ADRs.md](docs/ADRs.md) | Decision records (single-source, signal-confidence, consistency & idempotency). |
| [ROADMAP.md](docs/ROADMAP.md) · [IMPLEMENTATION-PLAN.md](docs/IMPLEMENTATION-PLAN.md) | Phased build sequence + tracked task list. |
| [REPO-LAYOUT.md](docs/REPO-LAYOUT.md) · [FRONTEND.md](docs/FRONTEND.md) | Monorepo structure · web client plan (Phase 2). |
| [requirements/](docs/requirements/) | [System requirements](docs/requirements/system-requirements.md), [NFRs & capacity](docs/requirements/nfr-and-capacity.md), [AI-layer requirements](docs/requirements/ai-layer-requirements.md). |

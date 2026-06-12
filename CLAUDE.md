# CLAUDE.md — Dev Intelligence Platform (GitHub-only)

Orientation file for Claude Code. Read this first. It states the scope, the signal-confidence principle that governs what we build, the locked stack, and the doc map.

## What this project is

A **multi-tenant developer-intelligence platform** built on **GitHub as the single source**. It ingests GitHub activity (PRs, reviews, commits, issues, CI checks), builds an intra-GitHub entity graph with contributor identity resolution, and surfaces **delivery-flow bottlenecks, recurring blockers, code-review health, CI reliability, collaboration patterns, and change risk** — with a production-grade, governed AI layer for proactive insights and natural-language Q&A.

**Scope decision:** one source, gone deep — not four, gone shallow. The architecture stays **source-agnostic** (canonical event model + pluggable connector seam) so a second source (Slack is the natural next) can be added later as a *proof of extensibility*, but only the GitHub connector is implemented now. See `docs/ADRs.md` ADR-001.

**Target scale:** 100k DAU across 5k tenants (a tenant = a customer org connecting their GitHub). **Posture:** open-source / cloud-portable, event-driven, CQRS, polyglot persistence.

## The signal-confidence principle (governs scope)

We **only ship metrics built on signals reliably present in essentially every active GitHub repo.** If a signal is thin or inconsistently populated, we exclude the corresponding metric entirely rather than ship something shaky.

| Tier | Signals | Policy |
|------|---------|--------|
| **STRONG** (core) | PRs, reviews, review comments, commits, issues + comments + labels + reopen, check runs / commit statuses (CI), branches, refs | Build core metrics on these. |
| **CAPABILITY-GATED** | Deployments / Environments, Releases | Only activate the dependent metric for a tenant *if* the connector detects the signal. Never a core promise. |
| **EXCLUDED (thin)** | Projects v2, Milestones, Iterations | Do **not** build metrics on these. No delivery-divergence / planned-vs-actual / milestone / iteration / scope-creep metrics. |

When adding any metric, classify its inputs against this table first. Excluded = don't build it.

## Document map

| Doc | Purpose |
|-----|---------|
| `docs/PRD.md` | Problem, personas, the insight pillars (4 core + 3 staged), signal-confidence scope, success metrics. |
| `docs/ARCHITECTURE.md` | System design: ingestion, intra-GitHub correlation + identity resolution, CQRS, persistence, scale, diagrams. |
| `docs/AI-ARCHITECTURE.md` | AI subsystem: funnel, RAG, risk scoring, AI-authorship impact, governance, telemetry. |
| `docs/DATA-MODEL.md` | Canonical schema, GitHub event mapping, entity graph, identity resolution, storage. |
| `docs/STATE-MACHINE.md` | Work-item stage FSM (precedence-based) + transition table; spine of all flow/bottleneck metrics. |
| `docs/METRIC-SPEC.md` | Exact metric formulas, exclusions, confidence flags, minimum sample thresholds. |
| `docs/METRICS-ETHICS.md` | Contributor-analytics ethics posture, k-anonymity suppression, individual opt-down, "won't build" list. |
| `docs/GITHUB-APP.md` | Required GitHub App permissions/events, degraded behavior, capability-gated metric gating. |
| `docs/API-CONTRACTS.md` | Webhook intake, GraphQL insight queries, AI/chat, internal gRPC. |
| `docs/ADRs.md` | Decision records incl. single-source/source-agnostic, signal-confidence, consistency & idempotency. |
| `docs/ROADMAP.md` | MVP-first phased build sequence. |
| `docs/REPO-LAYOUT.md` | Proposed monorepo / service structure. |
| `docs/REVIEW-ACTIONS.md` | Tracked doc-change checklist from the product/architecture critique. |
| `docs/requirements/system-requirements.md` | Prioritized functional/non-functional requirements (P0/P1/P2). |
| `docs/requirements/nfr-and-capacity.md` | Consolidated NFRs with derivations + capacity/storage sizing. |
| `docs/requirements/ai-layer-requirements.md` | Detailed AI requirements with acceptance criteria. |

**Reading order:** PRD → ARCHITECTURE → DATA-MODEL → AI-ARCHITECTURE → ADRs → ROADMAP → REPO-LAYOUT.

## The insight pillars (four core P0 + three staged; all on STRONG signals)

**Core pillars (P0 MVP):**
1. **PR flow & bottlenecks** — cycle-time decomposition, review wait, idle time, stuck/stale PRs.
2. **Code review health** — review depth, rubber-stamping, PR size vs. outcome, self-merge risk, hotspot files.
3. **CI reliability** — check pass rate, time-to-green, flaky-check detection.
4. **Recurring blockers** — stuck PRs, flaky CI, rework loops, reopened items, clustered failure themes (AI).

**Staged pillars:**
5. **Contributor & collaboration** *(P1)* — identity resolution, collaboration graph, knowledge concentration / bus factor, load signals. Governed by `METRICS-ETHICS.md`.
6. **Change risk** *(P1, AI)* — PR **revert-risk** scoring; the **incident** half is capability-gated on integrated deploy/incident signals (GitHub lacks incident ground truth).
7. **AI-authorship impact** *(P2, opt-in/experimental)* — declared/labelled AI-generated PRs correlated with rework/revert; low-recall from trailers alone.

Define metrics against `METRIC-SPEC.md` (formula + exclusions + min sample). **Explicitly excluded:** delivery divergence / planned-vs-actual and all Projects v2 / Milestones metrics (thin signal).

## Locked reference stack

- **Languages:** Go (platform/core services), Python 3.12 + FastAPI (AI/ML services).
- **Event backbone:** Apache Kafka (Redpanda portable drop-in).
- **Stream processing:** Apache Flink (entity graph, identity resolution, windowed aggregations).
- **Workflow orchestration:** Temporal (backfills incl. GH Archive, deletion sagas, multi-step inference).
- **OLTP / write model:** PostgreSQL 16 + Citus, sharded by `tenant_id`; logical replication for read replicas.
- **Analytical read store:** ClickHouse (cycle-time, CI, flow metrics).
- **Cache:** Redis. **Search:** OpenSearch. **Vectors:** pgvector (MVP) → Qdrant (scale).
- **Object store / data lake:** S3-compatible (MinIO local).
- **AI gateway:** LiteLLM; **serving:** vLLM + TEI; hosted providers pluggable.
- **PII:** Presidio. **AI telemetry:** Langfuse + OpenTelemetry.
- **AuthN:** Keycloak/OIDC. **AuthZ:** OPA + Postgres RLS backstop. **Secrets:** Vault.
- **Observability:** OTel → Prometheus + Grafana + Tempo + Loki.
- **API:** GraphQL BFF + REST (external), gRPC (internal). **Runtime:** Kubernetes + Helm. **IaC:** Terraform. **CI:** GitHub Actions.

## Conventions

- **Tenant + RBAC scoping is non-negotiable** — injected by the data-access layer, never trusted from the caller; Postgres RLS backstop.
- **CQRS:** canonical GitHub-derived events on Kafka; read models are rebuildable projections.
- **Idempotency:** every consumer/inference job keyed and upserting; replays are safe.
- **Untrusted input:** PR/issue/review/commit text is adversarial — see AI-ARCHITECTURE §Safety.
- **Signal discipline:** classify every new metric against the signal-confidence table before building.
- **IDs:** `FR-`/`NFR-`, `AI-x.y`, `ADR-00x` — reference in code/PRs.

## Status

Design phase, GitHub-only scope. Start at `docs/ROADMAP.md` Phase 0.

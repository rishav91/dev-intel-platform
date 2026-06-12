# Architecture Decision Records (GitHub-only)

Each ADR: context → decision → alternatives → consequences. Reference by ID in code/PRs. Status **Accepted** for v2 unless noted.

## Index
- ADR-001 — Single-source (GitHub), source-agnostic design
- ADR-002 — Signal-confidence principle (exclude thin signals)
- ADR-003 — Event-driven CQRS with event-sourcing-lite
- ADR-004 — Tenant isolation: Citus sharding + Postgres RLS
- ADR-005 — Stream processing engine: Apache Flink (graph + identity)
- ADR-006 — Vector store: pgvector → Qdrant
- ADR-007 — Workflow orchestration: Temporal (API backfill; GH Archive public-only)
- ADR-008 — Model gateway & portability: LiteLLM + vLLM
- ADR-009 — Analytical read store: ClickHouse
- ADR-010 — Consistency model & idempotency contract
- ADR-011 — "AI earns its place" (LLM-as-narrator, not decider)

---

## ADR-001 — Single-source (GitHub), source-agnostic design

**Context.** The original design spanned GitHub, Linear, Slack, Notion. Only GitHub has rich open data; the others would need mostly-synthetic data, and three extra connectors add breadth (plumbing) not architectural depth. Goal: demonstrate depth and ship real data.

**Decision.** Implement **only the GitHub connector**, but keep the architecture **source-agnostic** — a canonical event model and a pluggable connector interface — so a second source (Slack is the natural next, filling the "why" gap) can be added later without core changes.

**Alternatives.** (a) All four sources — broad but shallow, mostly synthetic, cross-source correlation hard to validate. (b) GitHub-only, hard-coded — simplest, but loses the multi-source narrative and extensibility.

**Consequences.** + Real data, full system complexity, deep intra-GitHub correlation + identity resolution, clean "designed for extension, didn't over-build" story. − Lose cross-*tool* correlation as a headline; the "why" context in chat is thinner until a 2nd source lands. The connector seam must stay honestly source-agnostic (enforced by building GitHub against the generic interface, not around it).

---

## ADR-002 — Signal-confidence principle (exclude thin signals)

**Context.** GitHub signals vary in reliability. PRs/reviews/commits/checks are near-universal; Projects v2 / Milestones are inconsistently populated. A metric that's wrong half the time because its source field is sparsely used erodes trust more than it adds value.

**Decision.** Classify every metric's inputs into **STRONG / CAPABILITY-GATED / EXCLUDED** (PRD §6). Build only on STRONG; activate capability-gated metrics per-tenant only when the connector detects the signal; **exclude thin-signal metrics entirely** — notably **delivery divergence / planned-vs-actual** and all Projects v2 / Milestones / iteration metrics.

**Alternatives.** (a) Build everything, caveat the shaky ones — clutters UI, erodes trust. (b) Best-effort fill from heuristics — fabricates planning data we don't have.

**Consequences.** + Fewer, trustworthy metrics; honest product. + Capability detection is a clean, reusable mechanism. − We give up the delivery-divergence pillar; the product centers on flow/review/CI/collaboration instead. (Deliberate — see PRD.)

---

## ADR-003 — Event-driven CQRS with event-sourcing-lite

**Context.** Many independent read concerns (the seven pillars), very different read/write profiles, frequent need to rebuild read models as metric logic evolves.

**Decision.** Canonical GitHub-derived events on Kafka are the backbone; all read models are **projections** rebuildable by replay. Raw events archived to S3. Current-state write model kept in Postgres (not full event sourcing); the log is authoritative for reprojection.

**Alternatives.** (a) CRUD + nightly ETL — no real-time insight, painful reprocessing. (b) Full event sourcing — overkill for current-state queries.

**Consequences.** + Independent read-model scaling, trivial reprocessing, natural audit. − Eventual consistency (seconds), idempotency required everywhere, Kafka ops.

---

## ADR-004 — Tenant isolation: Citus sharding + Postgres RLS

**Context.** 5k tenants, hard isolation, OSS/portable; need partitioning for scale + a non-bypassable safety guarantee.

**Decision.** One logical Postgres, **sharded by `tenant_id` via Citus**; **RLS** on every table keyed on `current_setting('app.tenant_id')`, set per request by the data-access layer. App-layer scope filter + RLS backstop = defense in depth.

**Alternatives.** (a) DB-per-tenant — strongest, infeasible at 5k. (b) Shared tables, app-filter only — one forgotten `WHERE` leaks. (c) Schema-per-tenant — 5k schemas strain migrations.

**Consequences.** + Scales, local joins, RLS makes leaks structurally hard. − Citus learning curve; very large tenants may need dedicated shards (supported); cross-tenant benchmarks need a separate aggregation pipeline.

---

## ADR-005 — Stream processing engine: Apache Flink

**Context.** The intra-GitHub entity graph and contributor identity resolution are **stateful** stream computations (joins across PR/commit/issue/check, identity unification), plus windowed cycle/idle/CI aggregations — must be exactly-once-ish and replayable.

**Decision.** **Apache Flink** for graph building, identity resolution, and aggregations. Native stateful operators, event-time windows, checkpointed exactly-once state, replay from Kafka offsets.

**Alternatives.** (a) Kafka Streams — simpler, weaker for large stateful joins / identity state. (b) Spark Structured Streaming — micro-batch latency. (c) Hand-rolled — reinvents state/windowing.

**Consequences.** + Powerful stateful streaming, exactly-once, rescalable state. − Heaviest operational component. *Note:* MVP may start identity resolution + simple aggregations in **Kafka Streams** and migrate to Flink at Phase 2 if Flink ops is too early — revisit then.

---

## ADR-006 — Vector store: pgvector (MVP) → Qdrant (scale)

**Context.** Per-tenant RAG with strict isolation and GDPR-deletable vectors; avoid premature infra.

**Decision.** Start on **pgvector** (reuses Postgres: RLS isolation, transactional deletes, one fewer system); abstract retrieval behind an interface; migrate to **Qdrant** (native multi-tenant namespaces, better ANN) when recall/latency at scale demands.

**Alternatives.** (a) Qdrant/Weaviate day one — better ANN, extra system before needed. (b) Collection-per-tenant — 5k collections strain stores.

**Consequences.** + Fast start, isolation via existing RLS, simple deletes. − pgvector ANN weaker at scale → planned migration; interface boundary must stay clean.

---

## ADR-007 — Workflow orchestration: Temporal (API backfill; GH Archive public-only)

**Context.** Backfills are long-running and resumable; deletion (purge across stores incl. vectors) is a saga; multi-step inference orchestration. **Correction (2026-06 review):** an earlier draft positioned **GH Archive as the bulk-history backfill path**. GH Archive records only the *public* GitHub event timeline, so it **cannot back-fill private/tenant repos** — the commercial case. For a multi-tenant GitHub App, the **REST/GraphQL API is the source of truth** for tenant history; GH Archive is useful only for public-repo/OSS tenants, demo data, benchmarking, and synthetic-scale testing.

**Decision.** **Temporal** for: (1) **API backfill** — a rate-budgeted, resumable, checkpointed crawl of a tenant's repo history via REST/GraphQL, the source of truth; (2) **optional public-data acceleration** — for public repos or demo/benchmark sets, load GH Archive and reconcile against API truth; (3) deletion sagas; (4) multi-step inference orchestration. Durable execution, checkpointing, retries, visibility.

**Alternatives.** (a) Cron + bespoke state — fragile. (b) Step Functions — AWS lock-in (violates portability). (c) Airflow — batch/DAG, not event-triggered long-lived per-entity workflows. (d) GH Archive as primary backfill — **rejected**: blind to private repos.

**Consequences.** + Resumable, observable, crash-safe; backfill correctness doesn't depend on a repo being public. − Another stateful service; workflow-as-code learning curve. − API backfill is bounded by GitHub's rate limits (REST 5k req/h/install, GraphQL 5k points/h), so onboarding a large org's history is paced by the rate budget, not raw throughput — size and alert there (see `nfr-and-capacity.md`).

---

## ADR-008 — Model gateway & portability: LiteLLM + vLLM

**Context.** Portability requirement; cost control needs model tiering; must survive provider outages.

**Decision.** All model calls via a **LiteLLM** gateway (provider swap = config). Self-host Llama-class on **vLLM** + embeddings on **TEI** for the cheap/high-volume tier; hosted providers pluggable for quality tier. Routing + fallback + circuit breaking at the gateway. Note: PR risk scoring uses a structured GBM model, not an LLM — LLM only for the "why" summary.

**Alternatives.** (a) Direct single-provider SDK — lock-in, no fallback. (b) Build our own gateway — undifferentiated.

**Consequences.** + No lock-in, tiered cost control, resilience. − Self-hosting GPUs adds ops; gateway is critical-path (must be HA).

---

## ADR-009 — Analytical read store: ClickHouse

**Context.** Flow (cycle/idle), CI reliability, and review-health metrics are time-series aggregations over high-volume transitions/checks, queried interactively.

**Decision.** **ClickHouse** for analytical read models; Postgres replicas for drill-down; Redis for hot caching. Polyglot read side, each store fit-for-purpose.

**Alternatives.** (a) Postgres for everything — analytical scans slow at volume. (b) Warehouse (Snowflake/BigQuery) — lock-in + latency, not interactive.

**Consequences.** + Fast columnar analytics, cheap at scale. − Another store + projection path; eventual consistency (acceptable per ADR-003).

---

## ADR-010 — Consistency model & idempotency contract

**Context.** The system is event-driven + CQRS with multiple read models. "Idempotency is a
convention" and "eventual consistency (seconds)" were stated loosely across docs. A reviewer
(or a future implementer) needs the actual guarantees: what is strongly consistent, what is
eventually consistent and how stale, and exactly how replays stay safe.

**Decision — consistency model.**

| Boundary | Guarantee |
|----------|-----------|
| Single-tenant write transaction (canonical entity + its state transition) | **Strong / ACID** within one Postgres tx, scoped by RLS. No cross-entity distributed transactions. |
| Canonical event → read-model projection (ClickHouse, PG replicas, OpenSearch) | **Eventual**, bounded: freshness p95 < 5 min (NFR), typically seconds. Dashboards may serve **bounded-stale** data. |
| Read replica lag | Eventual, seconds; acceptable for drill-down. |
| AI insight projections | Eventual; an insight may trail the event that triggered it by the inference-pipeline latency. |
| Cross-tenant aggregates (benchmarks, P2) | Computed offline in a separate pipeline; never on the hot path. |

There are **no linearizable or cross-shard transactional requirements** — a deliberate
consequence of the GitHub-analytics domain (no money movement, no inventory). This is why
AP-leaning + CQRS is safe here.

**Decision — idempotency keys per stage.** Every stage is keyed and upserting; replays and
webhook redeliveries are safe.

| Stage | Idempotency key | Mechanism |
|-------|-----------------|-----------|
| Webhook intake | `X-GitHub-Delivery` | Dedup at gateway / on the raw topic; duplicate delivery → no-op. |
| Canonical entity write | `(tenant_id, repo, type, node_id)` | `INSERT ... ON CONFLICT DO UPDATE` (upsert). |
| State transition | `(tenant_id, work_item_id, to_stage, occurred_at)` | Upsert; ordered by event-time. |
| Entity edge / identity | deterministic `(tenant_id, src_id, dst_id, relation)` / `(tenant_id, identifier_kind, value_hash)` | Recomputed deterministically; replays converge. |
| Projection write | entity key + version | Upsert into read model; never append. |
| AI inference job | `(tenant_id, event_id, model_version, prompt_version)` | Dedup; LLM output reused on replay (logged) for byte-stability. |

**Decision — replay safety.** Raw events are archived to S3 and the canonical log is retained.
Any read model (or the entire graph) can be **dropped and rebuilt by replaying** the log; because
every consumer is keyed/upserting and ordered by event-time, the rebuilt state is identical to
the original. This is the recovery path for projection bugs and schema changes.

**Alternatives.** (a) Leave it implicit — invites double-counting and divergent rebuilds.
(b) Exactly-once end-to-end everywhere — costly and unnecessary; at-least-once delivery +
idempotent keyed upserts gives effectively-once *results* without the overhead.

**Consequences.** + Clear, defensible guarantees; safe replays/reprocessing; double-counting
structurally prevented. − Consumers must *always* be written keyed/upserting (enforced in
review); dashboards must tolerate bounded staleness (acceptable per NFR).

---

## ADR-011 — "AI earns its place" (LLM-as-narrator, not decider)

**Context.** The product wedge is **trusted GitHub flow intelligence *with evidence*** (PRD §1):
every metric is defined, reproducible, and drill-downable. The standing risk is that an LLM gets
imposed where a deterministic or classical-ML approach is more reliable, cheaper, and more
trustworthy — and worse, that an LLM ends up *producing a number a user trusts* or *a query/action
against our data*, which the trust wedge cannot afford. We need a rule that decides, per feature,
whether an LLM is genuinely required, applied the same way ADR-002 classifies metrics against
signal-confidence.

**Decision.** An LLM is used **only when all three hold**:
1. The value is locked in genuinely **unstructured natural language or code semantics** that
   structure/metadata cannot recover (GitHub's only such surfaces: prose threads, commit messages,
   CI logs, the diff/code itself).
2. **No deterministic or small-ML approach** reaches the needed quality/recall.
3. The **failure mode is tolerable** — the LLM emits a **flag, label, cluster, summary, or
   conversational rendering**, *never* a trusted number, a query against the DB, or an action.

Numbers and decisions come from deterministic logic or classical ML (e.g., the GBM revert-risk
model, ADR-008); **the LLM narrates over results it did not compute.** Classify every proposed AI
feature against this test before building.

**Litmus.** "Reads unstructured text/code → emits a flag, label, summary, or conversation" =
candidate. "Produces a number / a query / an action" = **not** an LLM job.

**Applying the test.**
- *Earns it:* semantic change summary + **intent-vs-diff divergence** (AI-11), failure-theme cluster
  labels (pillar 4), CI-log root-cause on the novel tail, review-comment substance classification,
  NL→validated query (AI-6.1), decision/rationale extraction.
- *Does NOT:* cycle time, flaky detection, bus factor, reviewer recommendation, the revert-risk
  *score* — all deterministic or classical ML.
- *LLM-as-narrator only:* the "why" summary on the GBM revert-risk score (ADR-008, pillar 6).

**Alternatives.** (a) LLM-first everywhere — higher cost, hallucination on numbers users must
trust, harder evals. (b) No LLM at all — loses genuinely-NL features (summarization, rationale
extraction, NL Q&A).

**Consequences.** + AI spend concentrated where it's irreplaceable; the cost funnel (AI-1.2) has
less to filter. + Evals focus on flag/label/summary quality, not numeric correctness; every number
stays deterministically reproducible (trust wedge protected). − Requires review discipline to
reject "just use the LLM" shortcuts; some features wait on a small-ML model instead of a quick LLM
prototype.

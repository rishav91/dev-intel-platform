# ROADMAP — MVP-first build sequence (GitHub-only)

Full system, sequenced so the **P0 slice is a coherent first product**. Each phase has an exit criterion. Map work to requirement IDs in `requirements/`.

> **Stack is phased in, not stood up all at once.** NFR-8.1 lists the *P0-complete target*
> persistence set; each phase below adds only what it needs. Phase 0 = Kafka + Postgres/Citus +
> Redis + MinIO(S3); ClickHouse/OpenSearch land in Phase 2, vectors in Phase 3.

## Phase 0 — Foundations (walking skeleton)
**Goal:** one GitHub event flows end-to-end through the spine.
- Monorepo + CI + local stack (docker-compose: Kafka, Postgres+Citus, Redis, MinIO).
- AuthN (Keycloak/OIDC) + tenant model + RLS + data-access layer with injected tenant/scope.
- GitHub App webhook gateway → Kafka → normalizer → canonical event → Postgres write model.
- OTel tracing wired across services.
**Exit:** a real `pull_request` webhook becomes a `work_item` row, visible only to its tenant, with a trace. (FR-1.1–1.4, FR-2.1–2.5, NFR-7.2)

## Phase 1 — Ingestion depth + correlation + identity
**Goal:** complete GitHub ingestion, correlated, with resolved contributors.
- Full event coverage (PR/review/comment/commit/issue/check); GraphQL detail fetches; rate-limit budgeting; idempotent dedup.
- **API backfill** (Temporal, rate-budgeted, resumable) as the tenant-history source of truth; **GH Archive** optional for public-repo/demo/benchmark data only (not private repos); capability detection (deployments/releases).
- Intra-GitHub entity graph (commit→PR→issue→review→check) + **contributor identity resolution** with confidence (Flink, or Kafka Streams to start per ADR-005).
- State-transition + cycle/idle/CI aggregations.
**Exit:** PRs link to their commits/issues/checks; a contributor is unified across emails/logins; bots flagged. (FR-2.6–2.10, FR-3.1–3.5)

## Phase 2 — Analytics read models (the four P0 pillars)
**Goal:** core value as rebuildable projections.
- Projection workers → ClickHouse + Postgres read views + OpenSearch.
- **Flow/bottleneck**, **recurring blockers**, **code-review health**, **CI reliability** projections with drill-down.
- GraphQL BFF + role-tailored dashboards (portfolio/team/individual); capability-gated fields return null+reason.
- Redis caching + invalidation; projection rebuild tooling.
**Exit:** dashboards show the four pillars with drill-down; a read model can be dropped and replayed. (FR-4.1–4.5, FR-9.1, NFR-8.3, NFR-8.7)

## Phase 3 — AI layer (precompute + governance) — **primary focus**
**Goal:** governed, cost-controlled AI insights. Every AI feature here is justified against
**"AI earns its place" (ADR-011)** — LLM only where the value is locked in language/code semantics
and the output is a flag/label/summary, never a trusted number.
- Inference pipeline: cost funnel (over CI logs/comments) → small-model gate → LLM; idempotent insight projections.
- Embeddings + pgvector, per-tenant isolation + deletion.
- LiteLLM + vLLM/TEI; PII redaction (Presidio) at ingestion.
- Prompt-injection defense + output validation; per-tenant cost telemetry (Langfuse) + audit log.
- Blocker classification + **PR risk scoring** (GBM + LLM "why").
- **Semantic change understanding** (AI-11): grounded change summary + **intent-vs-diff divergence** flag — genuine-LLM, feeds pillars 2 (review health) and 6 (change risk).
**Exit:** blockers classified and PRs risk-scored on ingest; semantic change summaries + intent-vs-diff divergence flagged with evidence; < 5% of comment/CI text hits an LLM; per-tenant cost tracked; injection red-team green in CI; risk model backtested on real reverts. (AI-1.x, AI-2.x, AI-7.x, AI-8.1–8.2, AI-9.x, AI-11.x)

## Phase 4 — Interactive + proactive + collaboration
**Goal:** push/pull intelligence + pillar 5.
- RAG chat with tenant+RBAC retrieval, NL→validated query, grounded citations.
- Proactive insight feed + noise control + lifecycle.
- **Contributor & collaboration** pillar (graph, bus factor, load signals).
- Model routing + response cache; notifications/digests; eval harness gating CI; drift monitoring.
**Exit:** "why did X stall?" answered with citations < 3s; proactive feed + collaboration views live; evals gate releases. (AI-3.x–6.x, AI-8.3–8.4, FR-4.6, FR-9.2–9.4)

## Phase 5 — Scale-hardening + differentiators (P2)
**Goal:** scale and edge.
- Citus rebalancing; pgvector→Qdrant if needed; backpressure tuning; 99.9% + degradation drills.
- **Capability-gated DORA** (deploy freq/lead time/MTTR) where signals present; **AI-authorship impact** (pillar 7); cross-tenant anonymized benchmarks (k-anonymity); custom dashboards/NL metrics.
- **Second source (Slack)** as the proof of the source-agnostic seam — fills the "why" gap.
- Multi-region/residency; SOC2/ISO posture.
**Exit:** load-tested to 100k DAU/5k tenants; P2 differentiators behind flags; Slack connector live as extensibility proof. (FR-4.8–4.9, FR-5.11, NFR-8.4–8.8, ADR-001)

## Sequencing rationale
Spine first (0) de-risks GitHub integration early. Ingestion + correlation + identity (1) before analytics (2) because every pillar depends on the graph and resolved identities. AI (3) **after** there's correlated data to reason over. Interactive/proactive + collaboration (4) build on the precompute layer. P2 (5) — including the second source — is deferred on purpose; adding Slack last *proves* the seam rather than front-loading cost.

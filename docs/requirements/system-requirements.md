# System Requirements — Dev Intelligence Platform (GitHub-only)

Prioritized functional (`FR-`) and non-functional (`NFR-`) requirements. **P0** must-have/safety · **P1** fast-follow · **P2** differentiator. Governed by the signal-confidence principle (see PRD §6): metrics on thin signals are excluded, not deferred. AI requirements are additionally governed by **"AI earns its place" (ADR-011)**: an LLM is used only where the value is locked in unstructured language/code semantics, no deterministic/classical-ML path reaches the needed quality, and the output is a flag/label/summary — never a trusted number, a query, or an action.

## 1. Multi-tenancy & tenant lifecycle

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-1.1 | Provision, suspend, hard-delete a tenant; deletion purges all data incl. projections and embeddings. | P0 |
| FR-1.2 | Per-tenant config: connected GitHub org(s), OAuth/app tokens, sync cadence, feature flags. | P0 |
| FR-1.3 | Tenant-scoped RBAC: org-admin, team-lead, IC roles with visibility scopes (portfolio/team/individual). | P0 |
| FR-1.3a | **Team/ownership source of truth**: `team`, time-versioned `team_membership_history`, `repo_ownership`, `codeowners_snapshot` models backing scope predicates + team baselines (see `DATA-MODEL.md`). | P1 |
| NFR-1.4 | Tenant isolation enforced in the data-access layer (injected filter) **plus** Postgres RLS backstop. | P0 |
| FR-1.5 | Self-serve onboarding: install GitHub App, select repos, backfill, first insight < 30 min. | P1 |
| NFR-1.6 | Noisy-neighbor protection: per-tenant rate/quota limits so one org's backfill can't starve others. | P1 |
| FR-1.7 | Configurable data-residency region per tenant. | P2 |

## 2. GitHub ingestion (single connector, multi-protocol)

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-2.1 | GitHub connector via **GitHub App**: webhooks (live) + REST/GraphQL (detail/backfill). | P0 |
| FR-2.2 | Idempotent, deduplicated intake keyed on `(delivery_id)` / `(repo, entity, node_id)`. | P0 |
| FR-2.3 | Rate-limit budgeting (REST + GraphQL points) with backoff; never drop events. | P0 |
| FR-2.4 | Durable ordered event log (Kafka); raw payloads archived to S3 for replay. | P0 |
| FR-2.5 | Normalize raw GitHub payloads → canonical domain events at the connector boundary. | P0 |
| FR-2.6 | **API backfill (tenant truth)**: historical load via REST/GraphQL, rate-budgeted + resumable, as the source of truth for private/tenant repos. GH Archive is *not* a tenant-history path (public timeline only) — used for public-repo/demo data, OSS tenants, benchmarking, and synthetic-scale testing. | P1 |
| FR-2.7 | Resumable, checkpointed backfill (Temporal); crash resumes, not restarts. | P1 |
| FR-2.8 | Connector health + auto token/installation refresh; alert admin on breakage. | P1 |
| FR-2.9 | **Source-agnostic connector framework**: canonical event interface so a 2nd source can be added without core changes (not implemented now). | P1 |
| FR-2.10 | Capability detection: connector reports which signals a repo emits (deployments/releases) to gate dependent metrics. | P1 |
| FR-2.11 | **App permission handling**: declare required GitHub App permissions; detect granted scopes per installation and **degrade gracefully** when one is withheld (see `GITHUB-APP.md`). | P1 |

## 3. Canonical model, intra-GitHub correlation & identity resolution

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-3.1 | Canonical schema for work item, contributor, review, check-run, state transition — source-agnostic. | P0 |
| FR-3.2 | **Intra-GitHub entity graph**: link commit→PR→issue→review→check via refs, `closes #`, PR-commit membership, check-run association. | P0 |
| FR-3.3 | **Contributor identity resolution**: unify a person across commit emails, GitHub logins, and bots; flag bot/automation actors. | P0 |
| FR-3.4 | Confidence scoring on inferred links/identities; low-confidence flagged, not silently asserted. | P1 |
| NFR-3.5 | Correlation + resolution deterministic and re-runnable; replay rebuilds the graph identically. | P1 |
| FR-3.6 | Human override/correction of bad links/identities, fed back into the resolver. | P2 |

## 4. Analytics & insights (four core pillars P0, three staged, read side / CQRS)

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-4.1 | **PR flow & bottlenecks**: per-stage cycle/idle time; stuck/stale PRs; review-queue backlog; drill-down to the item. | P0 |
| FR-4.2 | **Recurring blockers**: stuck PRs, reopened items, rework loops, flaky-CI clusters. | P0 |
| FR-4.3 | **Code review health**: review depth, rubber-stamp rate, PR-size vs. outcome, self-merge risk, hotspot files. *(P1 AI enrichment: intent-vs-diff divergence — FR-5.12.)* | P0 |
| FR-4.4 | **CI reliability**: check pass rate, time-to-green, flaky-check detection. | P0 |
| FR-4.5 | Read models are independently materialized, rebuildable projections. | P0 |
| FR-4.6 | **Contributor & collaboration**: collaboration graph, knowledge concentration / bus factor, load signals. | P1 |
| FR-4.7 | Outlier detection (percentile vs. team/historical baseline, not fixed thresholds). | P1 |
| FR-4.7a | Every metric has a **defined formula, exclusions, confidence, and minimum sample threshold** (`METRIC-SPEC.md`); below threshold it is suppressed, not shown low-confidence. | P0 |
| FR-4.8 | **Capability-gated DORA**: deploy frequency / deploy lead time / MTTR — only when deployment signals present. | P2 |
| FR-4.9 | Cross-tenant anonymized benchmarks with k-anonymity threshold (≥N tenants). | P2 |
| — | ~~Delivery divergence / planned-vs-actual / milestone metrics~~ — **EXCLUDED** (thin signal). | — |

## 5. AI layer

Detailed: [`ai-layer-requirements.md`](ai-layer-requirements.md). Summary:

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-5.1 | Async event-driven inference (precompute), not request-time. | P0 |
| FR-5.2 | Cost-control funnel over high-volume text (CI logs, comments): filter → small-model gate → LLM. | P0 |
| FR-5.3 | RAG over per-tenant GitHub data with tenant **and** RBAC-scoped retrieval. | P0 |
| FR-5.4 | Prompt-injection defense (PR/issue/comment text is untrusted). | P0 |
| FR-5.5 | Structured-output validation; NL→validated query object, never raw SQL. | P0 |
| FR-5.6 | Proactive insight engine ("insights that find you"). | P1 |
| FR-5.7 | Conversational Q&A with citations + drill-down. | P1 |
| FR-5.8 | Model routing/gateway (small vs. large), vendor-abstracted + fallback. | P1 |
| FR-5.9 | LLM response caching. | P1 |
| FR-5.10 | **PR revert-risk** scoring model (pillar 6); incident-likelihood half capability-gated on integrated deploy/incident signals. | P1 |
| FR-5.11 | AI-authorship detection + rework/revert correlation (pillar 7) — **opt-in/experimental**, capability-gated, ideally driven by explicit tool/tenant-policy labels (low-recall from trailers alone). | P2 |
| FR-5.12 | **Semantic change understanding** (AI-11, genuine-LLM per ADR-011): grounded change summary + **intent-vs-diff divergence** (stated intent vs. actual diff) emitted as a flag-with-evidence; feeds review health (FR-4.3, pillar 2) and revert-risk (FR-5.10, pillar 6), never a standalone trusted score. | P1 |

## 6. Data governance & security

| ID | Requirement | Priority |
|----|-------------|----------|
| NFR-6.1 | PII detection/redaction at ingestion before any prompt or vector store. | P0 |
| NFR-6.2 | Per-tenant secret/token management (encrypted, scoped, rotatable) via Vault. | P0 |
| NFR-6.3 | Encryption in transit and at rest across all stores. | P0 |
| NFR-6.4 | Audit log of every inference (who, retrieved refs, model, output, scope). | P0 |
| NFR-6.5 | GDPR deletion that also purges embeddings + derived projections. | P0 |
| NFR-6.6 | Per-tenant retention policies. | P1 |
| NFR-6.7 | SOC2 / ISO 27001-aligned controls. | P2 |

## 7. Telemetry & observability

| ID | Requirement | Priority |
|----|-------------|----------|
| NFR-7.1 | Per-tenant token cost attribution. | P0 |
| NFR-7.2 | Distributed tracing, metrics, structured logs (OpenTelemetry). | P0 |
| NFR-7.3 | LLM eval harness with golden datasets, gating CI on prompt/model change. | P1 |
| NFR-7.4 | Retrieval-quality + output-drift monitoring. | P1 |
| NFR-7.5 | Per-feature latency/error SLOs with alerting. | P1 |

## 8. Platform, persistence & scale

| ID | Requirement | Priority |
|----|-------------|----------|
| NFR-8.1 | Polyglot persistence — the **P0-complete target** architecture: Kafka, Postgres+Citus (write), ClickHouse (metrics), OpenSearch (search), pgvector/Qdrant (vectors), Redis (cache), S3 (lake). **Phased in per `ROADMAP.md`, not all stood up at once** — Phase 0 = Kafka + Postgres/Citus + Redis + MinIO(S3); ClickHouse/OpenSearch arrive with the read models (Phase 2); vectors with the AI tier (Phase 3). | P0 |
| NFR-8.2 | Tenant sharding + read replicas. | P0 |
| NFR-8.3 | Caching tiers (read-model + LLM response) with correct invalidation. | P0 |
| NFR-8.4 | Horizontal scale to 100k DAU / 5k tenants with documented capacity model. | P1 |
| NFR-8.5 | Backpressure on the inference pipeline (interactive prioritized over batch). | P1 |
| NFR-8.6 | 99.9% availability with graceful degradation. | P1 |
| NFR-8.7 | Projection rebuild via event-log replay. | P1 |
| NFR-8.8 | Multi-region active-active. | P2 |

## 9. Delivery surfaces

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-9.1 | Role-tailored dashboards for the **four core pillars** (portfolio/team/individual); staged pillars 5–7 added as they ship. | P0 |
| FR-9.2 | Authenticated tenant-scoped GraphQL/REST API over read models. | P1 |
| FR-9.3 | Proactive notifications (Slack/email digest) of new risks/blockers. | P1 |
| FR-9.4 | Save/share/export insights. | P1 |
| FR-9.5 | Custom dashboards + NL-driven metrics. | P2 |

## P0 summary (the MVP)

Tenant lifecycle + isolation; GitHub App connector with idempotent intake + backfill; canonical model + intra-GitHub graph + identity resolution; four P0 pillars (flow/bottleneck, recurring blockers, review health, CI reliability) as rebuildable projections; async AI inference with cost funnel; tenant+RBAC-scoped RAG; injection + PII defense; per-tenant cost telemetry; role-tailored dashboards.

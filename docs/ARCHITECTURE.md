# ARCHITECTURE — Dev Intelligence Platform (GitHub-only)

System design. Locked OSS stack (see `../CLAUDE.md`). Event-driven, CQRS, polyglot persistence, multi-tenant. Single source (GitHub) with a source-agnostic seam. Diagrams are Mermaid.

## 1. Design tenets

1. **The canonical model is the product.** Ingest GitHub completely, normalize once (write side); every pillar is a projection (read side).
2. **Signal discipline.** Build metrics only on STRONG GitHub signals; capability-gate the rest; exclude thin signals (PRD §6).
3. **Precompute over request-time.** Insights + AI scoring computed on ingestion events, served from materialized read models.
4. **Isolation in depth.** Tenant + RBAC scoping at the app layer, Postgres RLS backstop, per-tenant vector namespaces.
5. **Rebuildable.** Any read model rebuilt by replaying the Kafka log (raw events archived in S3).
6. **Source-agnostic, single-source impl.** Canonical events + pluggable connector interface; only GitHub built (ADR-001).

## 2. High-level architecture

```mermaid
flowchart TB
    GH[GitHub org<br/>App webhooks + REST/GraphQL<br/>tenant-history source of truth]:::src
    GHA[GH Archive<br/>public-repo / demo / benchmark data only]:::src

    subgraph ING[Ingestion tier - Go]
        WH[Webhook Gateway<br/>verify + fast ack]
        CONN[GitHub connector<br/>intake + backfill + rate-limit budget + capability detect]
        NORM[Normalizer<br/>raw to canonical events]
    end

    subgraph BUS[Event backbone]
        K[(Apache Kafka<br/>raw + canonical topics)]
        S3[(S3 / MinIO<br/>raw archive + lake)]
    end

    subgraph CORR[Correlation tier - Flink]
        GRAPH[Entity graph builder<br/>commit-PR-issue-review-check]
        IDR[Contributor identity resolution]
        AGG[Windowed aggregations<br/>cycle/idle time, CI stats]
        G[(Graph + identity<br/>Postgres)]
    end

    subgraph WRITE[Write model]
        PG[(PostgreSQL + Citus<br/>sharded by tenant_id)]
    end

    subgraph PROJ[Projection workers - Go]
        P1[Flow / bottleneck]
        P2[Review health]
        P3[CI reliability]
        P4[Blockers / collaboration]
    end

    subgraph READ[Read stores]
        CH[(ClickHouse<br/>flow / CI metrics)]
        RPG[(Postgres replicas<br/>denormalized views)]
        OS[(OpenSearch)]
        RED[(Redis cache)]
    end

    subgraph AI[AI tier - Python]
        AIP[Inference pipeline<br/>funnel + classify + risk + summarize]
        VEC[(pgvector / Qdrant)]
        GW[LiteLLM -> vLLM / hosted]
    end

    subgraph SERVE[Serving]
        BFF[GraphQL BFF + REST]
        NTF[Notifications / digests]
    end

    USERS([Users / dashboards]):::ext

    GH -->|webhooks + API backfill| WH --> CONN --> NORM --> K
    GHA -. public-data only .-> CONN
    CONN -. detail fetch .-> NORM
    K --> S3
    K --> GRAPH --> G
    K --> IDR --> G
    K --> AGG --> WRITE
    NORM --> PG
    K --> PROJ --> CH & RPG & OS
    K --> AIP --> VEC
    AIP --> PG
    G --> BFF
    CH & RPG & OS & RED --> BFF
    AIP <--> GW
    BFF <--> AIP
    BFF --> USERS
    NTF --> USERS

    classDef src fill:#e8f0fe,stroke:#4285f4;
    classDef ext fill:#fef7e0,stroke:#f9ab00;
```

## 3. Tiers

### 3.1 Ingestion (Go)
- **Webhook Gateway** terminates GitHub App webhooks, verifies HMAC, writes to Kafka `raw.github` immediately (fast ack).
- **GitHub connector** owns App installation tokens, **rate-limit budgeting** across REST points + GraphQL point cost, detail fetches (a webhook often needs a follow-up GraphQL query for full context), **capability detection** (does this repo emit deployments/releases? → gates DORA metrics), and **backfill** — tenant history via the **REST/GraphQL API** (Temporal-orchestrated, rate-budgeted, resumable/checkpointed). This API path is the **source of truth** for private/tenant repos. **GH Archive** is used only for *public*-repo demo/OSS data, benchmarking, and synthetic-scale testing — it carries the public GitHub timeline, not private repos, so it cannot back-fill a tenant's private history. Where a tenant's repos *are* public, GH Archive may accelerate the crawl, reconciled against API truth.
- **Normalizer** maps GitHub payloads → canonical events on `canonical.events`. Idempotency: `(delivery_id)` for webhooks, `(repo, node_id, updated_at)` for fetched entities.

### 3.2 Correlation (Apache Flink)
Two stateful jobs:
- **Entity graph builder** — links the intra-GitHub graph: PR↔commits (PR commit list), PR↔issue (`closes #`, `Refs:` trailers, timeline cross-refs), commit↔check-run (head SHA), review↔PR. Emits edges with confidence.
- **Contributor identity resolution** — unifies actors across commit author/committer emails, GitHub logins, and noreply addresses; classifies bots/automation (`[bot]` suffix, known apps). Emits a stable `contributor_id` mapping with confidence.
- **Windowed aggregations** — time-in-stage, idle time, CI pass/flake stats over the work-item state machine.
Deterministic & replayable: replaying the log rebuilds graph + identity map identically.

### 3.3 Write model (PostgreSQL + Citus)
Canonical entities (`work_item`, `contributor`, `review`, `check_run`, `state_transition`, `entity_edge`, `identity_link`) sharded by `tenant_id`. Source of truth; not queried by dashboards directly.

### 3.4 Read side / CQRS (Go projection workers)
Materialize independent, rebuildable read models:
- **ClickHouse** — flow metrics (cycle/idle time), CI reliability (pass rate, time-to-green, flake rate), review-health aggregates.
- **Postgres read replicas** — denormalized entity views for drill-down.
- **OpenSearch** — full-text over PR/issue/review bodies.
- **Redis** — hot per-tenant response cache, invalidated on projection update.

### 3.5 AI tier (Python) — see `AI-ARCHITECTURE.md`
Async funnel (filter → small-model gate → LLM) over GitHub text; blocker classification, **PR risk scoring**, **AI-authorship detection**, summarization; embeddings; tenant+RBAC-scoped RAG; LiteLLM gateway; Langfuse telemetry.

### 3.6 Serving
GraphQL BFF (external) composes read models + graph; REST for simple integrations; gRPC internal. Notifications push proactive insights.

## 4. CQRS write→read flow

```mermaid
sequenceDiagram
    participant GH as GitHub
    participant WG as Webhook GW
    participant CN as Connector
    participant K as Kafka
    participant FL as Flink
    participant PJ as Projector
    participant CH as ClickHouse
    participant AI as AI pipeline
    participant U as User / BFF

    GH->>WG: pull_request review_requested
    WG->>K: raw.github (fast ack)
    K->>CN: enrich (GraphQL detail fetch)
    CN->>K: canonical.events (review.requested)
    K->>FL: canonical event
    FL->>K: agg.review_wait + graph edge
    K->>PJ: canonical + agg
    PJ->>CH: upsert review-wait / bottleneck projection
    K->>AI: enqueue (classify blocker / score risk)
    AI->>CH: upsert insight projection
    U->>CH: dashboard query (read-only, cached)
```

## 5. Multi-tenancy & isolation

```mermaid
flowchart LR
    REQ[Request<br/>tenant_id + user scope] --> MW[AuthZ middleware<br/>OIDC + OPA]
    MW --> DAL[Data-access layer<br/>injects tenant + scope filter]
    DAL --> RLS[(Postgres RLS backstop)]
    DAL --> VNS[Per-tenant vector namespace]
    MW -. denies .-> X[out-of-scope access]
```

- **Tenant boundary:** Citus shard key + Postgres RLS on `current_setting('app.tenant_id')`; the DAL sets the session var per request — a forgotten filter is non-exploitable.
- **RBAC scope:** OPA decides portfolio/team/individual visibility; the resulting predicate is injected into every query *and* AI retrieval (two-level). The predicate resolves against the **team/ownership source of truth** — `team`, time-versioned `team_membership_history`, `repo_ownership`, `codeowners_snapshot` (see `DATA-MODEL.md`) — so "team" scope and team baselines are computed as-of the relevant time, not from a single mutable `team_id`.
- **Vectors:** per-tenant namespace; deletion purges vectors.
- **Noisy neighbor:** per-tenant GitHub-API + inference budgets; fair scheduling.

## 6. Scale & capacity model (100k DAU / 5k tenants)

| Workload | Estimate | Implication |
|----------|----------|-------------|
| Avg users/tenant | 20 (but **highly skewed** — model whales, see below) | Many small shards + a few large ones. |
| Interactive chat | ~50k q/day ≈ **0.6 QPS avg, ~10 peak** | Interactive RAG is **not** the bottleneck. |
| GitHub events (PR/review/commit/issue/check) | per-tenant repos' activity; large orgs dominate | Webhook-first + GraphQL batching; budget per tenant. |
| High-volume text (CI logs, comments) | the firehose | Funnel before embed/LLM (< 5% reaches an LLM). |
| Backfill | tenant history via **rate-budgeted, resumable API** crawl; GH Archive only for public repos | Private repos aren't in GH Archive; the binding constraint is GitHub's API rate limit, so budget + checkpoint it. |

**Tenant distribution (don't size on the flat average).** "20 users/tenant" hides whale tenants
that dominate load. Size by the real drivers, modeled per cohort (long-tail vs. whale):

| Driver | Long-tail tenant | Whale tenant | Implication |
|--------|------------------|--------------|-------------|
| Repos | tens | thousands | Citus colocation; dedicated shards for whales (ADR-004). |
| PRs/day, check-runs/day | tens–hundreds | tens of thousands | Per-tenant Kafka partitioning + rate-budget tiers. |
| Retained history | months | years | Cold-history tiered to the lake; backfill cost scales here. |
| CI-log volume | small | very large | The funnel's most important input — gate before embed/LLM. |
| GraphQL point budget | rarely binding | continuously binding | Per-installation token bucket; whales need fair-scheduling + tiering. |

See `requirements/nfr-and-capacity.md` §1/§5 for the per-driver derivations.

**Sharding:** Citus by `tenant_id`, co-locating an org's data. **Read scaling:** ClickHouse + Postgres replicas + Redis. **Kafka:** partition by `tenant_id` for per-tenant ordering.

## 7. Failure modes & degradation

| Failure | Behavior |
|---------|----------|
| AI tier down | Dashboards serve last-known projections (stale-but-served); chat unavailable. Core analytics unaffected. |
| GitHub API outage / rate-limit | Webhook backlog buffers in Kafka; backfill resumes from checkpoint; GraphQL detail fetches retried with budget. |
| Projection bug / schema change | Drop + replay log to rebuild; S3 raw archive is the safety net. |
| Model provider 429/outage | LiteLLM fails over; breaker → cached responses. |
| Kafka lag | Backpressure sheds low-priority batch inference; interactive prioritized. |

## 8. Cross-cutting

- **Observability:** OTel across Go/Python → Tempo/Prometheus/Grafana/Loki; AI spans → Langfuse.
- **Secrets:** Vault; GitHub App keys + per-tenant installation tokens encrypted, scoped, rotatable.
- **Deploy:** Kubernetes + Helm; Terraform; GitHub Actions CI with eval + red-team gates.

See `ADRs.md` for the single-source/source-agnostic and signal-confidence decisions, and `DATA-MODEL.md` for schemas + identity resolution.

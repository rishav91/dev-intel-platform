# NFRs & Capacity Model

Consolidated non-functional requirements with **derivations** (not platitudes), plus the
back-of-envelope capacity/storage sizing. Single citable source; the scattered SLOs in PRD §7,
ARCHITECTURE §6, and CLAUDE.md defer to this.

**Scale inputs:** 100k DAU, 5k tenants (~20 users/tenant *mean — heavily skewed*), GitHub-only,
OSS/portable. **Do not size on the mean:** load is dominated by a small number of **whale tenants**
(see §1a). Size by per-tenant drivers (repos, PRs/day, check-runs/day, retained history, CI-log
volume, GraphQL point budget), modeled per cohort.

## 1. Throughput

| Metric | Target | Derivation |
|--------|--------|------------|
| Interactive chat | ~0.6 QPS avg, ~10 QPS peak | ~10% of DAU chat × ~5 q/day ≈ 50k q/day ÷ 86,400s ≈ 0.58 avg; peak ≈ 10× over the busy ~10% of the day. |
| GitHub ingest events | ~10²–10³ events/sec | ~5k tenants × ~2k events/day ≈ 10M/day ÷ 86,400 ≈ 116/s avg; bursty to ~10³/s. **Binding constraint is GitHub's API rate limit (REST 5k req/h/install; GraphQL 5k points/h), not our compute** — hence webhook-first + GraphQL batching + a **rate-budgeted, resumable API backfill** (GH Archive only accelerates *public*-repo history; private repos aren't in it). |
| High-volume text (CI logs, comments) | the firehose | Funnelled: deterministic filter → small-model gate → **< 5% reaches an LLM**. |

**Takeaway:** this is not a QPS-bound system. Peak chat is ~10 QPS. Engineering effort goes to
correlation correctness, isolation, and AI cost — not raw request volume.

## 1a. Tenant distribution (whale vs. long-tail)

The flat "20 users/tenant" mean is misleading; a few orgs carry most of the load. Provision and
alert per cohort, on the real drivers:

| Driver | Long-tail tenant (~p50) | Whale tenant (~p99) | Sizing implication |
|--------|--------------------------|----------------------|--------------------|
| Repos | tens | thousands | Citus colocation by `tenant_id`; **dedicated shards for whales** (ADR-004). |
| PRs/day | tens | thousands | Per-tenant Kafka partition + rate-budget tier. |
| Check-runs/day | hundreds | tens of thousands | Dominates ClickHouse + flake-detection volume. |
| Retained history | months | years | Backfill cost + write-model size scale here; tier cold history to the lake. |
| CI-log volume | small | very large | The funnel's primary input — gate before embed/LLM; whales drive AI cost. |
| GraphQL point budget | rarely binding | **continuously binding** | Per-installation token bucket; whales need fair-scheduling + budget tiers so one org's backfill can't starve others (NFR-1.6). |

**Rule:** capacity, rate budgets, and cost alerts are **per-tenant-cohort**, not per-mean. The first
thing to saturate is a whale's **GitHub API rate budget** during backfill — size and alert there first.

## 2. Latency / freshness SLOs

| Path | SLO |
|------|-----|
| Webhook → 202 ack | < 200 ms (fast-ack; no processing on hot path) |
| Webhook → canonical event | p95 < 60 s |
| Canonical event → updated projection (insight freshness) | p95 < 5 min |
| Dashboard read (cached) | p95 < 300 ms |
| Chat answer (RAG) | < 3 s median, < 5 s p99 (retrieval budget < 500 ms) |

## 3. Availability

| Target | Budget | Notes |
|--------|--------|-------|
| 99.9% | ~8.77 h/yr (~43 min/month) downtime | **Graceful degradation:** if the AI tier is down, dashboards serve last-known projections (stale-but-served); only chat/new-insights degrade. Core analytics stay up. |

## 4. Consistency

See ADR-010 for the full contract. Summary: **strong/ACID within a single-tenant write
transaction** (RLS-scoped); **eventual, bounded-stale (p95 < 5 min)** on all read models; no
distributed/linearizable transactions anywhere (domain doesn't require it).

## 5. Storage sizing (order-of-magnitude)

| Data | Estimate | Basis |
|------|----------|-------|
| Write model (Postgres/Citus) | low single-digit TB | canonical rows ~1 KB; PRs/issues/reviews/checks/transitions across 5k tenants' history; sharded by tenant. |
| Analytical (ClickHouse) | grows with transitions/checks; columnar-compressed | time-series of stage transitions + check runs; partitioned by month. |
| Vectors (pgvector → Qdrant) | ~hundreds of GB/yr | ~150k embeddable items/day × ~4–8 KB/vector (incl. chunks); excludes funnelled-out Slack-equivalent noise (CI logs/comments mostly filtered pre-embed). |
| Search (OpenSearch) | moderate | PR/issue/review bodies. |
| Raw archive (S3/MinIO) | **largest, cheapest** | full raw event archive for replay/reprocessing; lifecycle-tiered. |
| Cache (Redis) | small, hot-set only | per-tenant read-model + LLM response cache. |

**Embedding workload reality:** the naive "embed everything" number is ~25M Slack-equivalent
msgs/day — absurd. The funnel turns the real embed workload into ~150k items/day, which is
tractable on TEI/vLLM batches. This is the single most important capacity decision.

## 6. AI cost control

| Lever | Target |
|-------|--------|
| LLM hit rate on high-volume text | < 5% (funnel) |
| Precompute vs request-time | ~90%+ of inference is async precompute; interactive QPS is tiny |
| Caching | semantic + exact LLM response cache |
| Per-tenant cost | attributed, dashboarded, alerted on spikes; per-tenant budgets |

## 7. Scaling thresholds (10× / 100×)

| Dimension | At 10× | At 100× |
|-----------|--------|---------|
| Ingest throughput | add Kafka partitions; scale connector/normalizer replicas | dedicated ingest clusters per region; per-tenant rate-budget tiers |
| Write model | Citus shard rebalance | dedicated shards for whale tenants; archive cold history to lake |
| Read models | more ClickHouse nodes + PG replicas; widen Redis | pre-aggregated rollups; per-tenant materialized cohorts |
| Vectors | pgvector → **Qdrant** (ADR-006) | namespace sharding; ANN tuning |
| Inference | scale vLLM pool; deepen funnel | model-tier routing pressure; regional inference; stronger backpressure |

The first thing to break under load is **ingestion / GitHub rate budgets**, not serving — size
and alert there first.

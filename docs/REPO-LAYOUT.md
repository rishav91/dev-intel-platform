# REPO-LAYOUT — proposed monorepo structure (GitHub-only)

Single monorepo, polyglot (Go + Python), service-per-bounded-context. One real connector (GitHub) built against a **source-agnostic interface** so a second source slots in later. Designed for Claude Code to scaffold incrementally per the roadmap.

```
dev-intel-platform/
├── CLAUDE.md                      # orientation (read first)
├── docs/                          # this design set
│   ├── PRD.md
│   ├── ARCHITECTURE.md
│   ├── AI-ARCHITECTURE.md
│   ├── DATA-MODEL.md
│   ├── API-CONTRACTS.md
│   ├── ADRs.md
│   ├── ROADMAP.md
│   ├── REPO-LAYOUT.md
│   └── requirements/
│       ├── system-requirements.md
│       └── ai-layer-requirements.md
│
├── services/
│   ├── webhook-gateway/           # Go — verify + ack GitHub webhooks → Kafka (Phase 0)
│   ├── connector-github/          # Go — App auth, intake, GraphQL enrich, backfill, capability detect (Phase 0/1)
│   ├── normalizer/                # Go — raw GitHub → canonical events (Phase 0)
│   ├── correlation/               # Flink (Java/Scala) — entity graph + identity resolution + aggs (Phase 1)
│   ├── projector-flow/            # Go — bottleneck/cycle-time → ClickHouse (Phase 2)
│   ├── projector-review/          # Go — review-health → ClickHouse/Postgres (Phase 2)
│   ├── projector-ci/              # Go — CI reliability/flake → ClickHouse (Phase 2)
│   ├── projector-collab/          # Go — collaboration graph (Phase 4)
│   ├── bff/                       # Go — GraphQL + REST, authZ, scope injection (Phase 2)
│   ├── notifications/             # Go — digests/alerts (Phase 4)
│   ├── ai-inference/              # Python/FastAPI — funnel, blocker classify, summarize (Phase 3)
│   ├── ai-risk/                   # Python — PR risk scoring (GBM + LLM why) (Phase 3)
│   ├── ai-embeddings/             # Python — chunk + embed (Phase 3)
│   ├── ai-retrieval/              # Python — RAG, tenant+RBAC scoping (Phase 4)
│   └── ai-assistant/              # Python — NL→query, chat compose (Phase 4)
│
├── libs/
│   ├── go/
│   │   ├── events/                # canonical event schemas + (de)serialization
│   │   ├── connector/             # SOURCE-AGNOSTIC connector interface (GitHub is impl #1)
│   │   ├── tenancy/               # tenant/scope context, RLS session, OPA client
│   │   ├── kafka/                 # producer/consumer + idempotency helpers
│   │   └── observability/         # OTel setup
│   └── python/
│       ├── events/                # shared event schemas (generated from one source)
│       ├── tenancy/               # scope predicate + retrieval-filter enforcement
│       ├── modelgw/               # LiteLLM client, routing, fallback
│       └── governance/            # Presidio redaction, output validation, Langfuse
│
├── schemas/
│   ├── events/                    # JSON Schema / Avro for canonical events (single source of truth)
│   ├── proto/                     # gRPC service definitions
│   └── graphql/                   # BFF schema
│
├── db/
│   ├── migrations/                # Postgres/Citus migrations (+ RLS policies)
│   └── clickhouse/                # ClickHouse DDL
│
├── ingest/
│   ├── apibackfill/               # API backfill loader — tenant-history source of truth (Temporal) (Phase 1)
│   └── gharchive/                 # GH Archive loader — public-repo/demo/benchmark data only (Phase 1)
│
├── deploy/
│   ├── helm/                      # per-service charts
│   ├── terraform/                 # infra
│   └── docker-compose.dev.yml     # local stack (Phase 0)
│
├── evals/                         # AI golden datasets + runners (gates CI) (Phase 3/4)
├── tests/
│   ├── integration/
│   └── red-team/                  # tenant-isolation + prompt-injection suites (CI-gated)
└── .github/workflows/             # CI: build, test, eval-gate, red-team
```

## Conventions
- **Build GitHub against `libs/go/connector`, not around it.** The source-agnostic interface (fetch/normalize/capability-detect) is what makes a 2nd source cheap later. Don't leak GitHub specifics past the connector boundary.
- **Event schemas are the contract** (`schemas/events/`), generated into `libs/*/events` — never hand-duplicated.
- **Tenancy is a library** (`libs/*/tenancy`); every service uses it so tenant+scope filtering is impossible to "forget."
- **Each service owns its read model**; communicate via events or gRPC, never reach into another's store.
- **AI changes run `evals/`; isolation/injection suites in `tests/red-team/` must be green to merge.**
- **Signal discipline:** before adding a metric/projection, classify its inputs against the signal-confidence table (CLAUDE.md). Excluded = don't build.

## Suggested first commands for Claude Code
1. Scaffold `deploy/docker-compose.dev.yml` + `schemas/events/` + `libs/*/events` + `libs/*/tenancy` + `libs/go/connector`.
2. Build `webhook-gateway` → `connector-github` (intake only) → `normalizer` → write-model migration with RLS.
3. Add a red-team isolation test before onboarding a second tenant. Lock isolation in early.
4. Defer Flink: start identity resolution in Kafka Streams (ADR-005) if Flink ops is premature.

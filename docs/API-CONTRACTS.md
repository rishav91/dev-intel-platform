# API-CONTRACTS — Dev Intelligence Platform (GitHub-only)

External: GraphQL BFF + REST. Internal: gRPC. All external calls carry an OIDC bearer; `tenant_id` + RBAC scope derive from the token, never from params. Every response is tenant+scope filtered server-side.

## 1. Auth model

- Bearer JWT (Keycloak/OIDC); claims: `tenant_id`, `sub`, `roles`, `scope` (portfolio|team|individual + team_ids).
- Middleware sets Postgres `app.tenant_id` and builds the OPA-evaluated scope predicate. Handlers never read `tenant_id` from body/query.

## 2. GitHub webhook intake (REST)

```
POST /webhooks/github
Headers: X-Hub-Signature-256 (HMAC), X-GitHub-Delivery, X-GitHub-Event
Body: GitHub webhook payload
```
- Verify HMAC → resolve installation→tenant → write to Kafka `raw.github` → `202 Accepted`. No processing on the request path.
- Idempotency: dedup on `X-GitHub-Delivery`; duplicates → `202` no-op.
- Enqueue failure → `503` so GitHub retries.

## 3. Connector management (REST)

```
POST   /api/v1/connectors/github            # begin GitHub App install flow
GET    /api/v1/connectors/github            # status, rate-budget, detected capabilities
POST   /api/v1/connectors/github/backfill   # resumable backfill (GH Archive + API) via Temporal
DELETE /api/v1/connectors/github            # disconnect + schedule data purge
```
`GET` returns detected capabilities (e.g. `{ "deployments": true, "releases": false }`) so the UI shows only metrics whose signals exist (signal-confidence gating).

## 4. Insights & analytics (GraphQL BFF)

```graphql
type Query {
  # Pillar 1 — PR flow & bottlenecks
  bottlenecks(repo: ID, teamId: ID, window: TimeWindow!): [StageBottleneck!]!

  # Pillar 2 — code review health
  reviewHealth(teamId: ID, window: TimeWindow!): ReviewHealth!

  # Pillar 3 — CI reliability
  ciReliability(repo: ID, window: TimeWindow!): CIReliability!

  # Pillar 4 — recurring blockers (clustered)
  recurringBlockers(teamId: ID, window: TimeWindow!): [BlockerCluster!]!

  # Pillar 5 — contributor & collaboration
  collaboration(teamId: ID, window: TimeWindow!): CollaborationGraph!

  # Pillar 6 — change risk (AI)
  riskyPRs(repo: ID, threshold: Float = 0.6): [PRRisk!]!

  # Proactive feed (scope-aware)
  insightFeed(state: InsightState = ACTIVE, limit: Int = 20): [Insight!]!

  # Drill-down: full correlated context for one work item
  workItem(id: ID!): WorkItemDetail
}

type StageBottleneck {
  stage: String!            # open|in_review|changes_requested|approved
  medianIdle: Duration!
  p90Idle: Duration!
  itemsStuck: Int!
  exampleItems: [WorkItemRef!]!
}

type ReviewHealth {
  medianCommentsPerPR: Float!
  rubberStampRate: Float!         # approvals with 0 comments
  selfMergeRate: Float!
  oversizedPRRate: Float!         # PRs above size threshold
  hotspotFiles: [FileHotspot!]!
}

type CIReliability {
  passRate: Float!
  medianTimeToGreen: Duration!
  flakyChecks: [FlakyCheck!]!     # name, flake rate, PR-hours cost
}

type PRRisk {
  workItem: WorkItemRef!
  score: Float!
  topFactors: [String!]!          # explainability
  similarRevertedPRs: [WorkItemRef!]!
}

type Insight {
  id: ID!
  kind: InsightKind!  # BOTTLENECK|RECURRING_BLOCKER|REVIEW_HEALTH|CI_RELIABILITY|COLLAB|CHANGE_RISK|AI_AUTHORSHIP
  severity: Severity!
  confidence: Float!
  summary: String!
  evidence: [Citation!]!
  scope: Scope!
  state: InsightState!
}

type Citation { kind: String!, id: ID!, title: String, sourceUrl: String }

type Mutation {
  setInsightState(id: ID!, state: InsightState!, reason: String): Insight!
  overrideIdentity(contributorId: ID!, merge: [ID!], split: [ID!]): Contributor!  # FR-3.6
}
```
All resolvers enforce tenant + scope; out-of-scope `repo`/`teamId` returns empty, never another team's data. Capability-gated fields (e.g. DORA) return `null` + a reason when the signal is absent.

## 5. Conversational AI (REST, streaming)

```
POST /api/v1/assistant/query
Body: { "message": "why did the auth refactor take so long?", "conversationId": "uuid?" }
Response: text/event-stream (tokens) then final JSON:
{
  "answer": "...",
  "citations": [{"kind":"work_item","id":"...","sourceUrl":"https://github.com/org/repo/pull/482"}],
  "confidence": 0.81,
  "queryPlan": { "validated": true, "scope": "team:auth" }
}
```
- LLM emits a **validated structured query object** (not SQL); server injects tenant+scope, validates, executes against read models, composes a grounded answer.
- Ungrounded answers withheld/flagged. Every call logged to Langfuse (model/prompt version, retrieved refs, cost, effective scope).

## 6. Internal gRPC

```protobuf
service Inference {
  rpc EnqueueScoring(ScoringRequest) returns (Ack);          // async
  rpc Retrieve(RetrieveRequest) returns (RetrieveResponse);  // tenant+scope REQUIRED
}
message RetrieveRequest {
  string tenant_id = 1;        // from auth context
  string scope_predicate = 2;  // OPA-derived; enforced
  string query = 3;
  int32  k = 4;
}
```
`tenant_id` + `scope_predicate` mandatory; retrieval rejects calls missing them.

## 7. Conventions

- `/api/v1`; GraphQL evolved additively.
- Cursor pagination; time windows `{from, to, granularity}`.
- Errors: RFC 7807 (REST), typed (GraphQL).
- Per-tenant + per-user token-bucket rate limits; `429` + `Retry-After`.
- `Idempotency-Key` on mutating POSTs.

# AI Layer — Detailed Requirements (GitHub-only)

Detailed expansion of the AI subsystem for the GitHub-only design. Each requirement has a priority and **testable acceptance criteria**. Untrusted input here is **GitHub text** (PR/issue/review/commit/comment bodies, CI logs). High-volume text to funnel = CI logs + review/issue comments.

Priorities: **P0** must-have · **P1** fast-follow · **P2** later. ID prefix `AI-<area>.<n>`.

## 1. Inference orchestration & pipeline

### AI-1.1 — Async event-driven inference [P0]
Inference triggered by canonical GitHub events, not user requests.
- New PR/review/check event enqueues relevant jobs within 5s.
- Dashboard/load paths issue zero LLM calls (verified by tracing).
- Results written to read models, not returned inline.

### AI-1.2 — Cost-control funnel [P0]
Expensive calls only on high-signal text.
- Stage 1 deterministic filter drops noise (bot comments, trivial diffs, passing-check logs) before any model.
- Stage 2 small-model/embedding gate; only above-threshold items reach the LLM.
- < 5% of raw comment/CI-log volume reaches an LLM; per-stage drop rates observable.
- Thresholds per-tenant tunable without deploy.

### AI-1.3 — Idempotent inference & projection writes [P0]
- Jobs keyed by `(tenant_id, event_id, model_version, prompt_version)`; replays deduped.
- Re-runs upsert projections, not append.
- Event-log replay rebuilds AI read models to identical state (logged-output reuse for LLM stages).

### AI-1.4 — Backpressure & priority [P1]
- Interactive (chat) prioritized over batch precompute on the shared model pool.
- Queue depth/lag exported with alerts; overload sheds/defers batch, never interactive.
- Per-tenant fair scheduling; one org's backfill can't delay another's interactive path.

### AI-1.5 — Retry & dead-lettering [P1]
- Failed jobs retry with capped backoff, then dead-letter; DLQ inspectable/replayable.
- Provider 429/5xx distinguished from permanent (bad input) failures.

## 2. Embeddings & vector store

### AI-2.1 — Embedding pipeline [P0]
- Eligible content (PRs, issues, review threads; funnel-passed comments) chunked content-type-aware (code diff vs. prose vs. thread) and embedded within ingestion-lag SLO.
- Embedding model versioned; version stored per vector.

### AI-2.2 — Per-tenant vector isolation [P0]
- Tenant scoping structural (namespace/partition or injected filter), not caller-supplied.
- DB-level backstop (RLS) blocks cross-tenant reads.
- Red-team cross-tenant retrieval returns zero foreign vectors.

### AI-2.3 — Vector lifecycle & deletion [P0]
- Source update re-embeds and replaces stale chunks.
- Tenant/repo deletion purges vectors within the deletion SLO (verified by post-deletion query).
- Model upgrade triggers resumable re-embed backfill.

### AI-2.4 — Store portability [P1]
- Retrieval behind an interface; pgvector ↔ Qdrant swap needs no caller change.
- Recall/latency benchmarks documented at target scale.

## 3. Retrieval / RAG

### AI-3.1 — Two-level authorization on retrieval [P0]
Scoped by tenant **and** the requesting user's RBAC scope.
- Team-lead query never retrieves out-of-team individual data; IC never retrieves portfolio-wide.
- Filter injected by framework, asserted on every retrieval, tested per role.
- Audit log records effective scope.

### AI-3.2 — Grounding & citations [P1]
- Answers cite specific PRs/reviews/issues/CI runs with in-product + source URLs.
- Ungrounded answers suppressed or flagged low-confidence, never fabricated.

### AI-3.3 — Retrieval quality & reranking [P1]
- Hybrid (vector + BM25 over OpenSearch) + rerank.
- Precision@k tracked vs. labeled eval set; regressions gate releases (AI-8.3).

### AI-3.4 — Context-window budgeting [P1]
- Budgeter caps + prioritizes context; truncation logged, never silent.

## 4. Model routing & gateway

### AI-4.1 — Tiered routing [P1]
- High-volume classification → small/local model; summarization/chat → larger model.
- Policy config-driven; per-route cost/latency tracked.

### AI-4.2 — Vendor-abstracted gateway [P1]
- All calls via one gateway (LiteLLM-style); provider swap = config.
- ≥1 self-hostable OSS model wired end-to-end.

### AI-4.3 — Fallback & circuit breaking [P1]
- Primary failure → configured fallback; repeated failures trip breaker → serve cached/last-known.

### AI-4.4 — Versioning + canary [P2]
- Model+prompt version on every inference and projection row.
- New versions canary a tenant cohort gated by eval+cost; one-click rollback.

## 5. Proactive insights engine

### AI-5.1 — Background generation [P1]
- Scheduled/streamed scans produce insights tied to entities, each with severity + confidence + evidence.

### AI-5.2 — Noise control & dedup [P1]
- Near-duplicate causes collapsed; per-user volume capped, ranked by severity×confidence.
- Dismissed cause doesn't immediately re-fire.

### AI-5.3 — Lifecycle & feedback [P2]
- Surface → ack → dismiss/snooze/resolve tracked; dismiss reasons feed the relevance model.

## 6. Conversational Q&A / NL interface

### AI-6.1 — NL→validated structured query [P0]
- LLM emits a constrained query object/DSL validated against schema before execution; never raw SQL.
- Tenant + RBAC scope injected by the system, not the model.
- Malformed/out-of-schema queries rejected.

### AI-6.2 — Multi-turn grounded conversation [P1]
- Session context retained within the user's auth scope; each answer drill-downs with citations.

### AI-6.3 — Save / share [P1]
- Saved/shared insights respect recipient RBAC (no exposure beyond recipient rights).

## 7. AI safety & governance

### AI-7.1 — Prompt-injection defense [P0]
GitHub text (PR bodies, comments, commit messages) is untrusted/adversarial.
- Instructions vs. retrieved data strictly separated (data structurally fenced).
- No tool/query/action executed because ingested text "asked."
- Red-team injection suite (payloads embedded in PR/issue/comment bodies) fails to alter behavior or cross scope; runs in CI.

### AI-7.2 — PII redaction before prompt/embedding [P0]
- Presidio redaction at ingestion before embed/prompt; verified on labeled PII set.
- Redaction events logged (type, not value).

### AI-7.3 — Structured-output validation [P0]
- Outputs schema-conformant or rejected/retried; numeric/factual fields sanity-checked.

### AI-7.4 — Hallucination guardrails [P1]
- Unsupported claims withheld/low-confidence; hallucination rate tracked on evals.

### AI-7.5 — Jailbreak resistance [P1]
- Attempts to reveal system prompts or other tenants' data refused; regression suite.

## 8. Telemetry, evals & feedback

### AI-8.1 — Per-tenant token cost attribution [P0]
- Tokens/model/cost per inference, aggregatable per tenant/feature; spike alerts; per-tenant budgets.

### AI-8.2 — LLM tracing & audit [P0]
- Each inference logs inputs (or hash), retrieved refs, prompt+model version, output, latency, cost, effective scope.
- OTel spans + Langfuse; portable.

### AI-8.3 — Eval harness gating CI [P1]
- Golden datasets per feature; prompt/model change runs evals; regression beyond threshold blocks merge.

### AI-8.4 — Drift & quality monitoring [P1]
- Input/output drift monitored; sampled prod outputs re-scored.

### AI-8.5 — Human feedback loop [P2]
- Thumbs + dismiss reasons captured; flow into eval sets + small-model fine-tuning.

### AI-8.6 — Training-data governance [P2]
- Fine-tuning corpora provably free of cross-tenant data; lineage auditable.

## 9. Change-risk scoring (pillar 6)

### AI-9.1 — PR risk model [P1]
- Score each PR's revert/incident likelihood from size, files touched, review depth, author history, CI signal.
- Score is explainable (top contributing factors surfaced).
- Backtested against historical reverts; AUC/precision tracked on an eval set.

### AI-9.2 — Risk surfacing [P1]
- High-risk PRs surfaced proactively with evidence and "resembles reverted PRs" comparators.

## 10. AI-authorship impact (pillar 7)

### AI-10.1 — AI-authorship detection [P2]
- Detect AI-generated PRs/commits via metadata + `Co-authored-by` trailers + heuristics; confidence-scored.
- Detection precision/recall tracked on a labeled set.

### AI-10.2 — Impact correlation [P2]
- Correlate AI authorship with rework/revert/cycle-time, as a read model; tenant-scoped, no cross-tenant aggregation without privacy gating.

## Interview deep-dive map

- **AI-2.2 / AI-3.1** — isolation + RBAC retrieval (defense in depth).
- **AI-7.1** — injection from GitHub text (instruction/data separation, no action-from-content, CI red-team).
- **AI-1.2 / AI-8.1** — funnel + cost attribution (precompute keeps request-time QPS low).
- **AI-1.3** — idempotent inference + rebuildable projections (reprocess on model upgrade).
- **AI-6.1** — NL→validated query (safe data access, no raw SQL).
- **AI-9.1** — explainable risk scoring backtested on real reverts (ties AI to a concrete outcome).

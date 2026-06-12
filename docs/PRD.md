# PRD — Dev Intelligence Platform (GitHub-only)

**Status:** Draft v2 (GitHub-only) · **Owner:** Rishav · **Last updated:** 2026-06

> Supersedes the four-source v1 (preserved in `../docs_bkp/`). Scope is now **GitHub as single source, source-agnostic design**.

## 1. Problem

Engineering delivery slows for reasons that are visible in GitHub but never assembled: PRs waiting days for review, CI flaking repeatedly, review knowledge concentrated in two people, large risky changes merging without scrutiny. Each signal exists as a raw event; none is correlated into "here is your bottleneck and why." Leaders learn about it in retros, after the cost is paid.

GitHub alone holds enough to answer most engineering-flow and code-health questions — if you ingest it completely, correlate its internal graph (commit→PR→issue→review→check), resolve contributor identity, and reason over it. We deliberately scope to GitHub to go **deep** (real data, full correlation, governed AI) rather than wide across tools with shallow, partly-synthetic coverage.

**Product wedge.** The wedge is **trusted GitHub flow intelligence *with evidence*** — every metric is defined, sampled, and drill-downable to the exact PRs/reviews/CI runs behind it. The AI layer *supports* that wedge (summarize causes, cluster recurring blockers, answer grounded questions); it does not lead the narrative. Sensitive, surveillance-adjacent signals are governed by an explicit ethics posture (see `METRICS-ETHICS.md`); metric formulas and confidence are specified in `METRIC-SPEC.md`.

**AI earns its place (ADR-011).** An LLM is used only where the value is locked in unstructured language or code semantics, no deterministic/classical-ML path reaches the needed quality, and the failure mode is tolerable — it emits a **flag, label, cluster, summary, or conversation, never a trusted number, a query, or an action**. Every number and decision comes from deterministic logic or classical ML (e.g., the GBM revert-risk model); the LLM only narrates over results it did not compute. This keeps the trust wedge intact and AI spend concentrated where it's irreplaceable.

## 2. Goals & non-goals

**Goals**
- Ingest GitHub completely (PRs, reviews, commits, issues, CI checks) per tenant org, via live webhooks + **rate-budgeted, resumable API backfill** as the source of truth for tenant history. (GH Archive is *not* a tenant-history path — it carries only the public timeline; it's used for public-repo/demo data, OSS tenants, benchmarking, and synthetic-scale testing.)
- Build an intra-GitHub entity graph + contributor identity resolution.
- **MVP delivers the four core pillars** (1–4 in §4) with evidence and drill-down; pillars 5–7 are **staged capabilities** (see §4).
- Proactive (push) + conversational (pull) insights, grounded in citations.
- Multi-tenant safety: strict isolation, RBAC-scoped retrieval, PII control, auditable AI.
- Keep the architecture **source-agnostic** so a 2nd source can be added later (not built now).

**Non-goals (v2)**
- Any source other than GitHub (design-ready, not implemented).
- **Delivery divergence / planned-vs-actual** and any Projects v2 / Milestones / Iteration metric — excluded by the signal-confidence principle (thin signal).
- Writing code or acting in GitHub on the user's behalf.
- Cross-tenant benchmarking (P2, gated on privacy design).

## 3. Personas

| Persona | Scope | Primary need |
|---------|-------|--------------|
| **Eng Executive** | Portfolio | Where is delivery flow at risk across teams, and why? |
| **Eng Manager / Team Lead** | Team | What's blocking my team's PRs? Who's overloaded? Is review healthy? |
| **Platform / DevEx** | Cross-team | Where is CI flaky? Where is friction concentrated? |
| **IC Developer** | Self / team | Reduce review/CI friction; fair view of my own flow. |
| **Tenant Admin** | Org config | Connect GitHub org, manage roles, control data/retention. |

RBAC scopes (portfolio / team / individual) gate what each persona sees — and what the AI may retrieve on their behalf.

## 4. The insight pillars (four core + three staged)

All built on **STRONG** GitHub signals (see §6). **Stage tags:** pillars 1–4 are the **P0 MVP**;
5 (collaboration) and 6 (change risk) are **P1**; 7 (AI-authorship) is **P2, opt-in/experimental**.
A metric appears only once its inputs and minimum sample threshold are met (`METRIC-SPEC.md`).

### Core pillars (P0 MVP)

1. **PR flow & bottlenecks.** Decompose PR cycle time into stages — open→first-review, review-wait, rework, approve→merge — and surface idle time, stuck/stale PRs, and review-queue backlogs per repo/team.
2. **Code review health.** Review depth (comments per PR), approval-without-comment ("rubber-stamp") rate, PR-size distribution and its correlation with cycle time and reverts, self-merge / no-review governance risk, and hotspot files (high churn × high revert). *(AI-assisted enrichment, P1: **intent-vs-diff divergence** — a PR whose stated intent diverges from what the code actually changes is flagged for scrutiny; see pillar 6 and `AI-ARCHITECTURE.md` §9.3.)*
3. **CI reliability.** Check pass rate, time-to-green, and **flaky-check detection** (same check failing/retried across PRs) — CI as a quantified recurring blocker.
4. **Recurring blockers.** Stuck PRs (idle > N days, blocked on review or failing checks), reopened issues/PRs, rework loops, and AI-clustered repeating failure themes from CI logs and review comments.
### Staged pillars

5. **Contributor & collaboration** *(P1)*. Contributor **identity resolution** (across emails/accounts/bots), the collaboration graph (who reviews whom — silos/islands), knowledge concentration / bus-factor (review ownership vs. CODEOWNERS), and load signals (after-hours/weekend concentration). **Governed by the metrics-ethics posture** (`METRICS-ETHICS.md`): aggregate-not-rank, min-team-size suppression, individual opt-down.
6. **Change risk & prediction** *(P1, AI)*. **PR revert-risk** scoring from size, files touched, review depth, and author history; "this PR resembles ones later reverted." The score itself is a **GBM over structured features** (deterministic, backtested); an LLM supplies only the natural-language "why." **Intent-vs-diff divergence** (also feeds pillar 2) is a genuine-LLM signal: the model reads the diff against the PR's stated intent (title/description/linked issue) and flags mismatch — a PR that *says* "rename variables" but changes auth logic is both a review-health red flag and a risk input. **Incident** likelihood is *capability-gated* — only surfaced when deployment/incident signals are integrated; GitHub alone does not hold incident ground truth, so we do not claim it by default.
7. **AI-authorship impact** *(P2, opt-in/experimental)*. Detection from commit metadata / `Co-authored-by` trailers and explicit tool labels is **low-recall**, so this is framed as an experimental, capability-gated metric (ideally fed by explicit tool/tenant-policy labels, not inference), not a default insight. Correlates declared AI authorship with rework/revert rates.

**Explicitly excluded (thin signal):** delivery divergence, milestone/iteration burn, planned-vs-actual, scope creep.

## 5. Core use cases

1. **Bottleneck explorer** — where PRs sit idle across stages; drill to the specific stuck PRs and the awaited reviewer.
2. **Review-health review** — flag rubber-stamping, oversized PRs, self-merges; identify review bottleneck people/teams.
3. **Flaky-CI hunt** — rank checks by flakiness and the PR-hours they cost.
4. **Proactive feed** — "team X's review queue is backing up," "PR #482 idle 6 days," scoped to the viewer's role.
5. **Conversational Q&A** — "why did the auth refactor take so long?" → grounded answer citing the exact PRs, reviews, and CI runs.
6. **Onboarding & backfill** — connect a GitHub org, backfill history via the **rate-budgeted API** (GH Archive only for public repos), first insight within 30 minutes.

## 6. Signal-confidence scope (the governing rule)

| Tier | Signals | Used for | Policy |
|------|---------|----------|--------|
| **STRONG** | PRs, reviews, review comments, commits, issues+comments+labels+reopen, check runs/statuses, branches | All pillars (4 core + staged) | Core; always built. |
| **CAPABILITY-GATED** | Deployments/Environments, Releases | Deploy frequency, deploy lead time, MTTR | Activated per-tenant only if detected; never a core promise. |
| **EXCLUDED (thin)** | Projects v2, Milestones, Iterations | (delivery divergence, planned-vs-actual) | Not built. |

Rationale: a metric that's wrong half the time because the underlying field is inconsistently populated erodes trust more than the metric adds. Fewer, trustworthy metrics > many shaky ones.

**Applying the principle to pillars 6–7:** PR **revert** signal is STRONG (reverts are detectable in GitHub); **incident** ground truth is *not* in GitHub, so the incident half of pillar 6 is CAPABILITY-GATED on integrated deploy/incident signals. **AI-authorship** detection from trailers/metadata is a thin, low-recall signal — treated as opt-in/experimental (pillar 7, P2), ideally driven by explicit tool/tenant-policy labels rather than inference.

## 7. Success metrics

**Product**
- Time-to-first-insight after onboarding < 30 min (P0).
- ≥ 60% of proactive insights marked useful by week 4.
- "Explain this slow PR" answered with correct citations in < 3s median.

**Technical / SLO**
- Webhook → canonical event p95 < 60s.
- Insight freshness (event → updated projection) p95 < 5 min.
- Zero cross-tenant data exposure (continuously red-teamed).
- AI unit cost bounded: high-volume text (CI logs, comments) funnelled — < 5% reaches an LLM; per-tenant cost tracked.
- Availability 99.9%; stale-but-served read models if AI tier down.

## 8. Risks

| Risk | Mitigation |
|------|------------|
| Contributor identity resolution errors (bots, multiple emails) | Confidence-scored resolution + human override; deterministic, re-runnable. |
| CI-log / comment volume drives AI cost | Cost funnel + precompute + response cache + per-tenant budgets. |
| Tenant data leakage via AI retrieval | Two-level (tenant+RBAC) scoping injected structurally + RLS backstop + red-team CI. |
| Prompt injection from PR/issue/comment text | Instruction/data separation, no action-from-content, injection suite in CI. |
| GitHub API rate limits throttle backfill | Per-tenant budgeting, GraphQL batching, resumable/checkpointed backfill; GH Archive accelerates *public*-repo backfill only (private repos aren't in it). |
| Over-claiming incident risk / AI-authorship | Pillar 6 incident half capability-gated on deploy/incident signals; pillar 7 opt-in/experimental with confidence language (see §4). |
| Over-claiming on capability-gated signals | Signal-confidence gating; metric hidden unless its signal is present. |

See `ARCHITECTURE.md` for how the design addresses each.

# PRD — Dev Intelligence Platform (GitHub-only)

**Status:** Draft v2 (GitHub-only) · **Owner:** Rishav · **Last updated:** 2026-06

> Supersedes the four-source v1 (preserved in `../docs_bkp/`). Scope is now **GitHub as single source, source-agnostic design**.

## 1. Problem

Engineering delivery slows for reasons that are visible in GitHub but never assembled: PRs waiting days for review, CI flaking repeatedly, review knowledge concentrated in two people, large risky changes merging without scrutiny. Each signal exists as a raw event; none is correlated into "here is your bottleneck and why." Leaders learn about it in retros, after the cost is paid.

GitHub alone holds enough to answer most engineering-flow and code-health questions — if you ingest it completely, correlate its internal graph (commit→PR→issue→review→check), resolve contributor identity, and reason over it. We deliberately scope to GitHub to go **deep** (real data, full correlation, governed AI) rather than wide across tools with shallow, partly-synthetic coverage.

## 2. Goals & non-goals

**Goals**
- Ingest GitHub completely (PRs, reviews, commits, issues, CI checks) per tenant org, with real backfill from GH Archive + live webhooks.
- Build an intra-GitHub entity graph + contributor identity resolution.
- Deliver the **seven insight pillars** (§4) with evidence and drill-down.
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

## 4. The seven insight pillars

All built on **STRONG** GitHub signals (see §6).

1. **PR flow & bottlenecks.** Decompose PR cycle time into stages — open→first-review, review-wait, rework, approve→merge — and surface idle time, stuck/stale PRs, and review-queue backlogs per repo/team.
2. **Code review health.** Review depth (comments per PR), approval-without-comment ("rubber-stamp") rate, PR-size distribution and its correlation with cycle time and reverts, self-merge / no-review governance risk, and hotspot files (high churn × high revert).
3. **CI reliability.** Check pass rate, time-to-green, and **flaky-check detection** (same check failing/retried across PRs) — CI as a quantified recurring blocker.
4. **Recurring blockers.** Stuck PRs (idle > N days, blocked on review or failing checks), reopened issues/PRs, rework loops, and AI-clustered repeating failure themes from CI logs and review comments.
5. **Contributor & collaboration.** Contributor **identity resolution** (across emails/accounts/bots), the collaboration graph (who reviews whom — silos/islands), knowledge concentration / bus-factor (review ownership vs. CODEOWNERS), and load signals (after-hours/weekend concentration).
6. **Change risk & prediction** (AI). PR risk scoring — revert/incident likelihood from size, files touched, review depth, and author history; "this PR resembles ones later reverted."
7. **AI-authorship impact** (AI). Detect AI-generated PRs (commit metadata, `Co-authored-by` trailers) and correlate AI authorship with rework/revert rates.

**Explicitly excluded (thin signal):** delivery divergence, milestone/iteration burn, planned-vs-actual, scope creep.

## 5. Core use cases

1. **Bottleneck explorer** — where PRs sit idle across stages; drill to the specific stuck PRs and the awaited reviewer.
2. **Review-health review** — flag rubber-stamping, oversized PRs, self-merges; identify review bottleneck people/teams.
3. **Flaky-CI hunt** — rank checks by flakiness and the PR-hours they cost.
4. **Proactive feed** — "team X's review queue is backing up," "PR #482 idle 6 days," scoped to the viewer's role.
5. **Conversational Q&A** — "why did the auth refactor take so long?" → grounded answer citing the exact PRs, reviews, and CI runs.
6. **Onboarding & backfill** — connect a GitHub org, backfill history (GH Archive + API), first insight within 30 minutes.

## 6. Signal-confidence scope (the governing rule)

| Tier | Signals | Used for | Policy |
|------|---------|----------|--------|
| **STRONG** | PRs, reviews, review comments, commits, issues+comments+labels+reopen, check runs/statuses, branches | All seven pillars | Core; always built. |
| **CAPABILITY-GATED** | Deployments/Environments, Releases | Deploy frequency, deploy lead time, MTTR | Activated per-tenant only if detected; never a core promise. |
| **EXCLUDED (thin)** | Projects v2, Milestones, Iterations | (delivery divergence, planned-vs-actual) | Not built. |

Rationale: a metric that's wrong half the time because the underlying field is inconsistently populated erodes trust more than the metric adds. Fewer, trustworthy metrics > many shaky ones.

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
| GitHub API rate limits throttle backfill | Per-tenant budgeting, GraphQL batching, GH Archive for bulk history, resumable backfill. |
| Over-claiming on capability-gated signals | Signal-confidence gating; metric hidden unless its signal is present. |

See `ARCHITECTURE.md` for how the design addresses each.

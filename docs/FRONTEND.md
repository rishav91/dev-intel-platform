# FRONTEND — Dev Intelligence Platform (GitHub-only)

The web client for the platform. It renders the four core pillars (+ staged) with
**evidence and drill-down**, scoped per persona, over the GraphQL BFF. This doc is
the plan; the app is **built in Phase 2** alongside the BFF (`ROADMAP.md` Phase 2),
not before — the dashboards render projections that don't exist until the Phase-2
projectors populate the read models.

> **Design decisions (locked):**
> - **Vite + React SPA** (TypeScript). Authenticated internal tool → no SSR/SEO need; a
>   lean SPA over the BFF beats a Next.js server runtime here.
> - **No early build.** The frontend waits for the Phase-2 BFF. Until then there is
>   nothing real to render beyond raw work items, and building against an unstable
>   contract is wasted motion. Plan now, build when `bff/` lands.

## 1. What it is

A **multi-tenant analytics web app**. Its defining UX pattern is the product wedge
from `PRD.md` §1: *every metric is drill-downable to the exact PRs/reviews/CI runs
behind it.* Drill-down is the **spine of the UI**, not a feature.

Three platform principles translate into hard UI rules:

| Principle (source) | UI rule |
|--------------------|---------|
| **AI earns its place** (ADR-011, PRD §1) | Computed numbers and LLM narration are *visually distinct*. AI text always carries citations; a number is **never** sourced from an LLM. |
| **Metrics ethics** (`METRICS-ETHICS.md`) | No leaderboards / per-person ranking. Small cohorts render as **"suppressed (k<min)"**. Opted-down individuals are honored everywhere. ICs get a fair self-view. Identity is overridable. |
| **Signal-confidence** (PRD §6) | Capability-gated metrics (DORA) render an explicit "signal not present in your repos" state — never a fake `0`. Every metric shows **confidence + sample size**; below min-sample → "insufficient data". |
| **Scope safety** (`API-CONTRACTS.md` §1) | Scope is enforced server-side from the OIDC token. The scope switcher *requests*, it never *grants*; out-of-scope queries return empty by contract. |
| **Freshness SLO** (PRD §7) | Each view shows "updated Xm ago"; serve **stale-but-served** read models with a banner when the AI tier is down. |

## 2. Information architecture — pages × personas × pillars

Personas and pillars are from `PRD.md` §3–4; queries from `API-CONTRACTS.md` §4–5.

| Page | Persona | Pillar / use case | Backs onto (API) |
|------|---------|-------------------|------------------|
| **Onboarding / Connect GitHub** | Tenant Admin | UC6 | `POST/GET /connectors/github`, `/backfill` |
| **Portfolio Overview** | Eng Exec | cross-pillar risk | `insightFeed` + aggregated pillar queries |
| **Team Dashboard** | Eng Manager | pillars 1–4 summary | `bottlenecks`, `reviewHealth`, `ciReliability`, `recurringBlockers` |
| **Bottleneck Explorer** | Manager / DevEx | pillar 1 / UC1 | `bottlenecks(repo,teamId,window)` |
| **Review Health** | Manager | pillar 2 / UC2 | `reviewHealth(teamId,window)` |
| **CI Reliability / Flaky Hunt** | Platform / DevEx | pillar 3 / UC3 | `ciReliability(repo,window)` |
| **Recurring Blockers** | Manager / DevEx | pillar 4 | `recurringBlockers` (AI clusters) |
| **Risky PRs** *(P1)* | Manager | pillar 6 | `riskyPRs(repo,threshold)` |
| **Collaboration** *(P1, governed)* | Manager | pillar 5 | `collaboration(teamId,window)` |
| **Work Item Detail** | all | the evidence hub | `workItem(id)` |
| **Insight Feed** | all (scoped) | proactive / UC4 | `insightFeed`, `setInsightState` |
| **Assistant** *(P4)* | all | conversational / UC5 | `POST /assistant/query` (SSE) |
| **My Flow** | IC Developer | fair self-view | scoped pillar queries |
| **Admin / Settings** | Tenant Admin | governance | `overrideIdentity`, roles, retention/purge, opt-down |

## 3. Key page content

**Onboarding / Connect GitHub** — the make-or-break first run (success metric:
*first insight < 30 min*, PRD §7). GitHub App install CTA → live **backfill progress**
(repos crawled, rate-budget headroom, ETA) → **capability-detection** card
(`{deployments, releases}` → which metrics will light up) → "first insight ready"
hand-off. The one screen worth scaffolding earliest once the BFF exists.

**Team Dashboard** — the manager's home: four pillar cards (cycle-time decomposition,
review-health summary, CI pass-rate/flake, top recurring blockers), each → its
explorer. Every card header: time-window picker, scope chip, freshness, confidence/
sample badge.

**Bottleneck Explorer** — stacked stage-duration bars
(open→first-review→review-wait→rework→approve→merge) with median/p90, stuck counts
per stage, and **example stuck PRs that open the evidence drawer** (`StageBottleneck.exampleItems`).

**Work Item Detail** — the heart of the trust wedge. Renders the **correlated graph**
built in P1.E/F: the PR with its linked commits, issue(s), reviews, and check runs;
the **state-transition timeline** (P1.D) with idle/cycle annotated; **resolved
contributors** (P1.F) with bot flags; deep-links to GitHub. Every metric elsewhere is
one click from this page.

**Collaboration** *(governed)* — review graph (who reviews whom), bus-factor vs
CODEOWNERS, load signals — **aggregate-not-rank**, suppressed small cohorts, opted-down
individuals honored. The ethics constraints are visible UI states, not fine print.

**Assistant** — streaming answer (SSE) with an always-present **citations rail**;
ungrounded answers withheld/flagged; shows effective scope ("answering as team:auth").
A companion surface, never the headline.

## 4. Cross-cutting components (build once)

- **Scope switcher** (portfolio / team / individual) — requests scope; server enforces.
- **Time-window picker** → `{from, to, granularity}`.
- **Evidence drawer** — universal drill-down to work items / citations.
- **Confidence + sample-size badge**, **capability-gated empty state**, **k-anon
  suppression state**, **freshness / stale banner**.
- **Computed-vs-AI treatment** — distinct visual style for LLM narration + citations.

## 5. Stack & placement

- New top-level **`apps/web/`** (the `services/` tree is backend-only).
- **TypeScript + React + Vite** SPA.
- **GraphQL client** (urql or Apollo) with **codegen from `schemas/graphql/`** — the
  schema is the contract; types are generated, never hand-written.
- **Charts**: visx or ECharts (dense, drill-down-friendly).
- **Auth**: Keycloak/OIDC (`oidc-client-ts`); bearer token carries `tenant_id`/scope.
- **Assistant**: SSE (`POST /assistant/query`).

## 6. Phasing (aligned to `ROADMAP.md`)

| Stage | Lands with | Scope |
|-------|-----------|-------|
| **FE-0** | Phase 2 (BFF) | App shell, OIDC, scope switcher, Onboarding, Team Dashboard + bottleneck/review/CI explorers + Work Item Detail + Insight Feed |
| **FE-1** | Phase 2/4 (staged data exposed) | Collaboration (governed), Risky PRs, My Flow |
| **FE-2** | Phase 4 | Assistant chat, proactive notification UI, identity-override admin (`overrideIdentity`) |

## 7. Open items

- Component/design system (headless + Tailwind vs a component kit) — decide at FE-0 start.
- Read-model shape for the Work Item Detail graph (drives the `workItem(id)` resolver).

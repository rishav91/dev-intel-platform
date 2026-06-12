# REVIEW-ACTIONS — doc-change checklist from product/architecture critique

Tracked actions from the PRD + architecture review (2026-06). These are **documentation /
scope-spec fixes** for what gets built *after* the walking skeleton. None block the M0 greenlight
(one PR → tenant-scoped `work_item` + trace); see `IMPLEMENTATION-PLAN.md` for build tasks.

`[ ]` = todo. Each item notes: verdict, the concrete change, affected docs, and when to do it.
The "stack too heavy for Phase 0" critique item was **dropped** — the ROADMAP already stages the
stack correctly; only the NFR-8.1 *wording* needs reconciling (T1-6 below).

---

## Tier 1 — Do now (real errors / scope inconsistencies; doc-only, prevent rework)

### T1-1 · Reposition GH Archive (factual error) — **highest priority**
GH Archive is the *public* GitHub timeline; it cannot back-fill private customer repos, which are
the commercial case. API backfill is the source of truth.
- [x] PRD §2/§5/§8: change "real backfill from GH Archive + live webhooks" → "API backfill (tenant
      truth) + live webhooks; GH Archive for public-repo demo/OSS tenants, benchmarking, synthetic scale."
- [x] ARCHITECTURE §3.1 (line ~96) + §6: demote GH Archive from bulk-history path to
      public-data acceleration/testing.
- [x] ROADMAP Phase 1 + IMPLEMENTATION-PLAN **P1.G**: reframe the Temporal backfill workflow around
      the API (rate-budgeted, resumable) as primary; GH Archive optional.
- [x] Add/append an ADR note recording the correction.
- *Docs:* PRD, ARCHITECTURE, ROADMAP, IMPLEMENTATION-PLAN, ADRs. *Refs:* FR-2.6, FR-2.7.

### T1-2 · Make MVP = four core pillars consistent (internal contradiction)
FR-4.1–4.4 are P0, FR-5.10 (pillar 6) is P1, FR-5.11 (pillar 7) is P2 — but **FR-9.1 says
"seven-pillar dashboards … P0"** and PRD §2 goal promises all seven. Reconcile to four.
- [x] PRD §2 goal: "deliver the seven pillars" → "MVP delivers four core pillars; pillars 5–7 are staged."
- [x] PRD §4: add stage tags to the pillar list (P0 ×4, P1 collaboration/change-risk, P2 AI-authorship).
- [x] system-requirements **FR-9.1**: "seven pillars" → "four core pillars" at P0.
- *Docs:* PRD, system-requirements. *Refs:* FR-4.1–4.4, FR-5.10, FR-5.11, FR-9.1.

### T1-3 · Reframe surveillance-adjacent metrics + add ethics posture
Pillar 5 (after-hours/weekend load, bus factor) and pillar 7 (AI-authorship) are sensitive; no
ethics guardrails exist. Protects the "trusted intelligence" wedge.
- [x] Add a **metrics-ethics** section (PRD or new short doc): k-anonymity / min-team-size
      suppression, individual opt-down, aggregate-not-rank framing, explicit "no individual
      productivity scoring."
- [x] PRD §1 narrative: lead with "trusted GitHub flow intelligence *with evidence*"; AI
      summarizes/clusters/answers, doesn't headline.
- *Docs:* PRD (+ optional `docs/METRICS-ETHICS.md`).

---

## Tier 2 — Before the relevant build phase (spec gaps)

### T2-1 · Add team / ownership domain models (needed for Phase 2 dashboards + Phase 4 collab)
Product leans on team views, manager scope, bus-factor "vs. CODEOWNERS", but DATA-MODEL only has a
nullable `contributor.team_id`. Spec now; build later.
- [x] DATA-MODEL: add `team`, `team_membership_history` (**temporal** — as-of-time correct),
      `repo_ownership`, `codeowners_snapshot`, and a documented scope-predicate derivation
      (portfolio/team/individual → query filter).
- [x] Note the CODEOWNERS dependency on the `contents` permission (see T2-3).
- *Docs:* DATA-MODEL, ARCHITECTURE §5. *Refs:* FR-1.3, FR-3.x, pillar 5. *When:* before Phase 2.

### T2-2 · Rework the review FSM to precedence-based (needed for Phase 1 P1.D)
`Open → InReview` only on `review_requested` undercounts PRs reviewed without a request, CODEOWNERS
auto-requests, direct approvals from Open, merges from stale ChangesRequested.
- [x] STATE-MACHINE: define **precedence rules** over observed events (review submissions, requested
      reviewers, draft state, approvals, dismissals, pushes, mergeability); add an "InReview entered
      by review activity even absent a request" edge.
- *Docs:* STATE-MACHINE. *Refs:* FR-4.1 inputs, IMPLEMENTATION-PLAN P1.D. *When:* before/with Phase 1 P1.D.

### T2-3 · Document GitHub App permissions + degraded behavior (onboarding + capability inputs)
No doc lists required scopes or what happens when a customer withholds one.
- [x] Add a permissions matrix: metadata, pull_requests, issues, checks, contents, members,
      deployments — each with required-for and degraded-behavior-if-absent.
- [x] Tie to existing capability detection (CODEOWNERS needs `contents`; collab needs `members`).
- *Docs:* ARCHITECTURE §3.1 or new `docs/GITHUB-APP.md`, system-requirements. *Refs:* FR-2.1, FR-2.10. *When:* before Phase 1 P1.A.

---

## Tier 3 — Before Phase 2 (measurement trust)

### T3-1 · Add a metric-definition spec
"Rubber-stamp," "flaky," "self-merge," "hotspot," "review depth," "PR-hours cost," "stuck" are used
without formulas — directly undermines the trust thesis.
- [x] New `docs/METRIC-SPEC.md`: one row per metric — exact formula, inclusions/exclusions (bots
      excluded per FSM), minimum sample size before display, confidence/quality flag.
- *Docs:* new METRIC-SPEC. *Refs:* FR-4.1–4.4. *When:* prerequisite for Phase 2.

### T3-2 · Refine capacity model for whale tenants
Headline model uses flat averages (20 users/tenant); whale-tenant *mitigation* exists
(nfr-and-capacity ~line 71) but the *sizing* doesn't model the distribution.
- [x] nfr-and-capacity: model by repos, PRs/day, check-runs/day, retained history, CI-log volume,
      GraphQL point budget; show whale vs. long-tail.
- *Docs:* nfr-and-capacity, ARCHITECTURE §6.

### T3-3 · Reconcile NFR-8.1 wording with the staged roadmap
NFR-8.1 marks the whole polyglot stack P0, reading as "build it all first," while the ROADMAP
stages it. (Wording only — not a re-architecture.)
- [x] system-requirements NFR-8.1: clarify it describes the **P0-complete target** architecture;
      cross-reference ROADMAP phase-in (Phase 0 = Kafka/Postgres+Citus/Redis/MinIO).
- *Docs:* system-requirements, ROADMAP.

### T3-4 · Reframe pillars 6 & 7 against the signal-confidence principle
"Revert/**incident** likelihood" claims ground truth GitHub lacks; AI-authorship from
`Co-authored-by` trailers is low-recall.
- [x] PRD §4 + signal table: rename pillar 6 → "revert-risk"; capability-gate the incident half on
      deployment/incident signals.
- [x] Reframe pillar 7 as opt-in/experimental, fed by explicit tool labels/tenant policy (not inference).
- *Docs:* PRD §4/§6, system-requirements FR-5.10/5.11. *When:* with Phase 2 framing (already staged P1/P2).

---

## Sequencing summary

| When | Items |
|------|-------|
| **Now (doc-only)** | T1-1 GH Archive · T1-2 pillar count · T1-3 ethics + wedge framing |
| **Before Phase 1** | T2-2 FSM precedence · T2-3 permissions matrix · T2-1 team model (spec now, build later) |
| **Before Phase 2** | T3-1 metric spec · T3-2 capacity · T3-3 NFR-8.1 wording · T3-4 pillars 6/7 reframe |

**Greenlight unaffected:** none of these block M0 (walking-skeleton verification). They are
scope/correctness fixes for the post-skeleton build.

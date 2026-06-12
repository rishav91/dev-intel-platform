# METRIC-SPEC — exact metric definitions

The product's wedge is **trust**, which requires every metric to be *defined*: one formula, explicit
exclusions, a confidence/quality flag, and a **minimum sample threshold** below which it is
**suppressed** (not shown low-confidence). This doc is the single source of truth for those
definitions; projections (Phase 2) and dashboards must implement these, not ad-hoc variants. Terms
like "rubber-stamp," "flaky," "self-merge," "hotspot," "stuck" are defined here and nowhere else.

## Conventions

- **Stages** reference `STATE-MACHINE.md`; durations are computed on the append-only transition
  timeline, ordered by `occurred_at`.
- **Bots** (`contributor.is_bot=true`) are excluded from review-wait, reviewer-load, and authorship
  human metrics, but still drive stage transitions.
- **Time** uses business-aware options where noted; default is wall-clock UTC, configurable per tenant.
- **Suppression** = hide the metric when `n < min_sample`; UI shows "insufficient data," never a
  low-`n` number. Individual-attributed metrics additionally obey `METRICS-ETHICS.md` k-anonymity.
- **Confidence flag** = {high | medium | low}, driven by sample size, signal completeness
  (capability gating), and identity-resolution confidence of the actors involved.

## Pillar 1 — PR flow & bottlenecks

| Metric | Formula | Exclusions | Min sample | Notes |
|--------|---------|-----------|------------|-------|
| **Cycle time** | `merged_at − created_at` (drafts: from first `Open`); optionally minus draft time | open/closed-unmerged PRs; bot-authored if configured | ≥ 20 merged PRs in window | Reported as median + p90, never mean alone. |
| **Review wait (time-to-first-review)** | time in `Open` before first `InReview` | draft time; PRs never reviewed | ≥ 20 PRs | The "open, no review yet" idle window. |
| **Time-in-stage** | `next_transition.occurred_at − enter_stage.occurred_at` (= `idle_before`) | terminal stages | ≥ 20 transitions/stage | Per-stage median + p90. |
| **Stuck / stale PR** | currently in a non-terminal stage with time-in-stage **> team/historical p90 baseline** | merged/closed PRs | baseline needs ≥ 30 historical PRs | **Outlier detection, not a fixed day count** (FR-4.7). |
| **Bottleneck stage** | a stage whose median or p90 time-in-stage **> baseline** across many items | low-volume stages | ≥ 30 items through the stage | Flags *where* flow stalls, team-scoped. |

## Pillar 2 — Code review health

| Metric | Formula | Exclusions | Min sample | Notes |
|--------|---------|-----------|------------|-------|
| **Review depth** | review comments per PR (`sum(review.comment_count)` + review-comment events) / reviewed PRs | bot comments; self-comments | ≥ 20 reviewed PRs | Distribution, not a single number. |
| **Rubber-stamp rate** | share of PRs `approved` with **0 review comments** and approval within a short window of request (configurable, default < 5 min) | bot approvals; trivial-size PRs below a LOC floor (configurable) | ≥ 20 approved PRs | Framed as a process signal, not per-person blame (`METRICS-ETHICS.md`). |
| **PR size vs. outcome** | correlation of `additions+deletions` (and `changed_files`) buckets with cycle time and revert rate | reverts/merges from bots | ≥ 50 PRs | Size buckets: XS/S/M/L/XL by percentile. |
| **Self-merge rate** | share of PRs merged by the author **with no non-author approval** | bot-authored; solo-maintainer repos (flagged, not counted) | ≥ 20 merged PRs | Governance risk signal. |
| **Hotspot file** | files with **high churn × high revert association** (commits touching the file that were later reverted) | vendored/generated paths (configurable globs) | ≥ 10 changes to the file | Ranked list; drill-down to the PRs. |
| **Intent-vs-diff divergence** *(AI, P1)* | LLM compares stated intent (title/description/linked-issue text) vs. the diff; flag + evidence_refs, confidence-scored (AI-11.2) | trivial-size PRs below LOC floor; bot-authored; PRs with no description | ≥ 5 flagged PRs before trend-reporting | A **flag with evidence**, not a trusted number; also a change-risk input (pillar 6). Confidence = low/medium; surfaced for scrutiny, not blame. |

## Pillar 3 — CI reliability

| Metric | Formula | Exclusions | Min sample | Notes |
|--------|---------|-----------|------------|-------|
| **Check pass rate** | `success / (success + failure)` per check name, per window | `cancelled`/`skipped`; in-progress | ≥ 30 runs/check | Per check name, not aggregate only. |
| **Time-to-green** | first `success` time − first run time, per PR head | PRs with no checks | ≥ 20 PRs with checks | Median + p90. |
| **Flaky check** | a check that **fails then passes on the same head SHA with no intervening change** (`was_retried` + conclusion flip), or alternates pass/fail across re-runs of identical input | first-time failures that were fixed by a new commit | ≥ 10 runs/check name | Distinct from a genuinely failing check; ranked by flake rate × PR-hours cost. |
| **PR-hours cost (of a flake)** | Σ over affected PRs of (wall-clock time the PR sat blocked on the flaky check) | non-blocking/optional checks | ≥ 5 affected PRs | Quantifies a flake's drag; the unit for the flaky-CI hunt (PRD §5). |

## Pillar 4 — Recurring blockers

| Metric | Formula | Exclusions | Min sample | Notes |
|--------|---------|-----------|------------|-------|
| **Rework loops** | count of `ChangesRequested → InReview` edges per PR | bot-driven syncs | ≥ 20 PRs | High counts = churn signal. |
| **Reopen churn** | count of `Closed → Open` (PR) / reopen (issue) per item | — | ≥ 20 items | Reopens count toward churn. |
| **Stuck-on-CI / stuck-on-review** | stuck PRs (pillar 1) partitioned by whether they sit in `ChangesRequested`/failing-check vs. `Open`/`InReview` | — | inherits stuck threshold | Attributes the blocker. |
| **Clustered failure themes** *(AI)* | embeddings + clustering over funnelled CI-log / review-comment text | funnelled-out (<5% reaches model) | cluster ≥ 5 instances | Confidence = low/medium; evidence-cited. |

## Staged pillars (5–7)

Defined here for completeness; **subject to `METRICS-ETHICS.md` suppression (k ≥ 5)** and staging
(5 = P1, 6 = P1, 7 = P2 opt-in).

| Metric | Formula | Min cohort | Notes |
|--------|---------|------------|-------|
| **Bus factor** (5) | min contributors whose removal drops review/author coverage of a repo below a threshold (e.g., 50%), vs. `codeowners_snapshot` | k ≥ 5 | Org-risk framing, not individual. |
| **Collaboration graph** (5) | reviewer→author edges, weighted; silo/island detection | k ≥ 5 | Aggregate only. |
| **After-hours load** (5) | share of a team's activity outside configured hours, timezone-aware | k ≥ 5; opt-in individual | Burnout-risk framing; opt-down. |
| **Revert-risk** (6) | GBM over size, files-touched, review depth, author-history; LLM for the "why" only | ≥ 200 PRs incl. ≥ 20 reverts to train | Backtested on real reverts; **incident** half capability-gated (deploy/incident signals). |
| **AI-authorship** (7) | declared via tool/tenant-policy labels or trailers; correlate with rework/revert | opt-in | Low-recall from inference → experimental, confidence=low. |

## How thresholds & confidence are applied

1. Projection computes the metric **and** its `n` + confidence.
2. BFF/serving **suppresses** below `min_sample` (and below k-anonymity for individual-attributed).
3. The UI shows the confidence flag + a drill-down to the underlying entities (the "with evidence"
   promise). Capability-gated inputs missing → metric hidden with a reason (`GITHUB-APP.md`).

See `STATE-MACHINE.md` (durations), `DATA-MODEL.md` (entities), `METRICS-ETHICS.md` (suppression),
`system-requirements.md` FR-4.x / FR-4.7a.

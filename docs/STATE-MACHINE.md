# STATE-MACHINE — Work-item lifecycle

The `current_stage` finite-state machine. This is the **spine of every Pillar-1 metric** —
cycle time, time-in-stage, idle time, and "stuck" detection are all derived from transitions
through these states. Defining it precisely here keeps the connector, the Flink aggregations,
and the projections in agreement.

Driven **only by STRONG GitHub signals** (PR/issue/review events). No Projects v2 / Milestones
inputs (per the signal-confidence principle).

## 1. PR lifecycle

```mermaid
stateDiagram-v2
    [*] --> Draft : opened draft=true
    [*] --> Open : opened draft=false
    Draft --> Open : ready_for_review
    Open --> InReview : review_requested
    InReview --> ChangesRequested : review changes_requested
    InReview --> Approved : review approved
    ChangesRequested --> InReview : synchronize (new commits = rework)
    Approved --> InReview : synchronize (post-approval push)
    Open --> Merged : closed merged=true
    InReview --> Merged : closed merged=true
    Approved --> Merged : closed merged=true
    Open --> Closed : closed merged=false
    InReview --> Closed : closed merged=false
    ChangesRequested --> Closed : closed merged=false
    Closed --> Open : reopened
    Merged --> [*]
    Closed --> [*]
```

## 2. Issue lifecycle

Issues have no review sub-states; they are deliberately simple.

```mermaid
stateDiagram-v2
    [*] --> Open : opened
    Open --> Closed : closed
    Closed --> Open : reopened
    Closed --> [*]
```

## 3. Transition table (PR)

| From | To | GitHub trigger | Notes / metric meaning |
|------|----|----------------|------------------------|
| — | Draft | `pull_request.opened` (draft=true) | Work started, not ready. Draft time excluded from review-wait. |
| — | Open | `pull_request.opened` (draft=false) | Ready, awaiting review request. |
| Draft | Open | `pull_request.ready_for_review` | Start of the "open, no review yet" idle window. |
| Open | InReview | `pull_request.review_requested` | First-review-wait ends. |
| InReview | ChangesRequested | `pull_request_review.submitted` state=`changes_requested` | Enters rework. |
| InReview | Approved | `pull_request_review.submitted` state=`approved` | Ready to merge. |
| ChangesRequested | InReview | `pull_request.synchronize` (new commits) | **Rework loop** — count of these = rework signal (Pillar 4). |
| Approved | InReview | `pull_request.synchronize` | Post-approval push; re-review needed. |
| Open / InReview / Approved | Merged | `pull_request.closed` (merged=true) | Cycle complete. |
| Open / InReview / ChangesRequested | Closed | `pull_request.closed` (merged=false) | Abandoned/superseded. |
| Closed | Open | `pull_request.reopened` | Resets to Open; a reopen counts toward churn. |

`review_dismissed` returns an Approved/ChangesRequested PR to `InReview`. Bot/automation actors
(resolved via identity resolution, `is_bot=true`) are excluded from review-wait and reviewer-load
metrics but still drive stage transitions.

## 4. Derived metrics

For each work item, transitions form an ordered timeline. From it:

- **Time-in-stage(s)** = `next_transition.occurred_at − enter_stage.occurred_at`. Materialized
  on the transition as `idle_before` (time the item sat in the prior stage before this transition).
- **Cycle time** = `merged_at − created_at` (or first-`Open` for drafts), optionally minus draft time.
- **Review wait** = time in `Open` before first `InReview`.
- **Rework time** = cumulative time in `ChangesRequested` + `InReview` after the first
  `ChangesRequested`; **rework loops** = count of `ChangesRequested → InReview` edges.
- **Stuck / stale** = currently in a non-terminal stage with time-in-stage above the
  team/historical percentile baseline (outlier detection, not a fixed threshold).
- **Bottleneck** (Pillar 1) = a stage whose median/p90 time-in-stage exceeds baseline across many items.

## 5. Implementation notes

- The connector maps each GitHub event to a `(to_stage, trigger)`; the Flink aggregation computes
  `idle_before` and emits `state_transition` rows (see DATA-MODEL `state_transition`).
- The FSM is **append-only and replayable**: re-running the event log reproduces the identical
  transition timeline (ADR-003 / ADR-010).
- Out-of-order events (webhook redelivery, backfill vs. live) are ordered by event `occurred_at`,
  not arrival time; Flink uses event-time windows so late events slot correctly.
- Unknown/unhandled actions are recorded as `work_item.updated` with no stage change (no spurious
  transition).

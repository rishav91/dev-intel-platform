# METRICS-ETHICS — posture, guardrails, and suppression rules

The product surfaces contributor-level and collaboration signals (pillar 5: collaboration graph,
bus factor, after-hours/weekend load; pillar 7: AI-authorship). These are **sensitive** — used
carelessly they become surveillance and destroy the "trusted intelligence" wedge. This doc is the
binding policy: what we will and won't compute, how individual data is protected, and the
suppression thresholds enforced in the read/serving layers. It is a P0 governance requirement, not
a nice-to-have.

## 1. Principles

1. **Improve the system, don't rank the people.** Metrics target *flow and process* (where work
   stalls, where CI flakes, where review knowledge concentrates), never individual performance
   ranking. **No individual productivity score, leaderboard, or stack-rank — ever.**
2. **Aggregate, not accusatory.** Contributor signals are shown as aggregates and patterns
   (e.g., "review knowledge is concentrated in 2 people") — not as per-person scorecards handed to
   managers for evaluation.
3. **Evidence over inference.** Prefer observable, drill-downable facts. Inferred/low-recall signals
   (AI-authorship, "after-hours" intent) carry explicit confidence and are opt-in.
4. **Consent and transparency.** Tenants and individuals can see what is measured about them and can
   opt down (§3). Onboarding discloses the contributor-analytics scope.
5. **Minimize.** Raw PII (emails) is hashed at ingestion (`identity_link.identifier_value_hash`);
   we store the unification, not the raw identifiers.

## 2. Suppression thresholds (enforced in serving)

Aggregates that could re-identify or single out an individual are suppressed unless a minimum
cohort size is met. Enforced in the read/serving layer (BFF + AI retrieval), not left to the UI.

| Rule | Threshold | Behavior below threshold |
|------|-----------|--------------------------|
| **Min team/cohort size (k-anonymity)** | `k ≥ 5` contributors in any grouped aggregate | Aggregate suppressed; UI shows "below reporting threshold," not a smaller-n number. |
| **Min sample per metric** | per `METRIC-SPEC.md` (e.g., ≥ N PRs) | Metric hidden, not shown low-confidence. |
| **Individual drill-down** | only the viewer's own data, or team-scoped aggregates the viewer's RBAC permits | Out-of-scope individual data is non-retrievable (RBAC + RLS). |
| **Cross-tenant benchmarks** | k-anonymity ≥ N tenants (FR-4.9) | Not shown; computed offline only. |

## 3. Individual controls

- **Opt-down.** An individual can opt out of being shown in *individual-attributed* collaboration/
  load views; they still contribute to suppressed, k-anonymized aggregates used for flow health.
- **After-hours / weekend load** is framed as a **team well-being / burnout-risk** signal at the
  aggregate level, never as an individual diligence/commitment measure. Timezone-aware; configurable
  per tenant; off by default for individual attribution.
- **Bus factor / knowledge concentration** is presented as an org *risk* ("single point of failure
  in repo X's reviews"), not as a judgment of the concentrated individual.

## 4. Pillar-specific rules

| Pillar / metric | Rule |
|-----------------|------|
| 5 · Collaboration graph | Aggregate silos/islands; k-anonymized; no "who is isolated" callouts on individuals. |
| 5 · Bus factor | Org-risk framing; suppressed below k. |
| 5 · After-hours/weekend load | Aggregate burnout-risk; individual attribution opt-in + opt-down; timezone-correct. |
| 6 · Revert-risk | Allowed (process signal on the *change*, not the person); incident half capability-gated. |
| 7 · AI-authorship | Opt-in/experimental; tenant-policy/explicit-label driven; never used to evaluate individuals. |

## 5. Tenant configuration

- Tenant admins enable/disable pillar 5 load signals and pillar 7 entirely.
- Suppression thresholds (`k`) are configurable upward, not below the floor (`k ≥ 5`).
- All settings + opt-downs are auditable (NFR-6.4).

## 6. Hard "won't build" list

- Individual productivity scores, rankings, or leaderboards.
- Manager-facing per-person scorecards for performance evaluation.
- Surveillance framing of after-hours activity (diligence/commitment).
- Any individual-attributed metric below the k-anonymity floor.

See `PRD.md` §4 (pillar staging), `METRIC-SPEC.md` (formulas + sample thresholds), and
`system-requirements.md` FR-4.6 / FR-5.11 / NFR-6.x.

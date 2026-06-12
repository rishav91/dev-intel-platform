# GITHUB-APP — required permissions, events, and degraded behavior

The connector is a **GitHub App** (FR-2.1). This doc is the contract for what the App requests, why,
and **what happens when a customer withholds a permission** — onboarding must degrade gracefully, not
fail, and capability detection (FR-2.10/FR-2.11) records which signals each installation actually
grants. Permissions also gate which pillars/metrics light up for a tenant.

## 1. Permission matrix

Permissions are requested at the **least privilege** needed; all read-only (we never write to GitHub,
PRD §2 non-goals). "Degraded behavior" is what the product does when the scope is **not** granted.

| Permission | Access | Required for | Degraded behavior if withheld |
|------------|--------|--------------|-------------------------------|
| **Metadata** | read | Mandatory baseline (repos, refs, branches). Auto-granted. | Cannot operate; installation rejected at onboarding. |
| **Pull requests** | read | Pillar 1 (flow), 2 (review health), 3 wiring; PR/review/comment events + detail. | **Core product unavailable.** Block onboarding with a clear "pull_requests is required" message. |
| **Checks** | read | Pillar 3 (CI reliability): check_run/check_suite. | CI reliability + flaky-detection hidden; fall back to commit **statuses** (Metadata/commit statuses) where present, lower fidelity. Flag pillar 3 as degraded. |
| **Contents** | read | Commit detail, **CODEOWNERS** snapshot (bus factor, repo ownership), revert detection on diffs. | No CODEOWNERS → bus-factor "vs. owners" downgrades to observed-reviewers only; revert detection falls back to message heuristics (`Revert "..."`). |
| **Issues** | read | Issue lifecycle, comments, labels, reopen (pillar 4 blockers/rework on issues). | Issue-based blocker/rework metrics hidden; PR-side metrics unaffected. |
| **Members** | read | Org **teams + membership** → team scope, portfolio rollups, collaboration (pillar 5). | No team source of truth → **team/portfolio dashboards degrade to repo-grouped views**; individual + repo scopes still work. Collaboration graph still computable from review edges, but team baselines unavailable. |
| **Administration / repo hooks** | read (or App-managed webhooks) | Webhook delivery setup, repo list. | Use App-level webhook + REST repo enumeration; no per-repo hook management. |
| **Deployments** | read | Capability-gated **DORA** (deploy freq, lead time, MTTR) + incident half of pillar 6 (change risk). | Capability-gated off (default). DORA + incident-likelihood not promised; revert-risk (STRONG) still shipped. |
| **Actions** *(optional)* | read | Richer CI/workflow-run + **log** access for blocker clustering (pillar 4 AI). | Fall back to check conclusions + annotations; deeper log-theme clustering unavailable. |

**Excluded by design** (signal-confidence, PRD §6): Projects v2, Milestones, Iterations — not
requested, not built on.

## 2. Subscribed webhook events

`pull_request`, `pull_request_review`, `pull_request_review_comment`, `issue_comment`, `issues`,
`push`, `check_run`, `check_suite`, `status`, `member` / `membership` / `team` (with Members),
`deployment` / `deployment_status` / `release` (capability-gated, with Deployments).

## 3. Capability detection → metric gating

On install and first sync the connector records, per installation, which permissions were granted
and which signals each repo actually emits (FR-2.10/FR-2.11). The result is a **per-tenant capability
map** that:

- **Gates pillars/metrics** — a metric is shown only if its inputs' permissions are granted and the
  signal is present (else hidden with a reason, never a shaky value).
- **Surfaces a coverage panel** at onboarding: "Connected. Team views need the *Members* permission —
  grant it to enable portfolio rollups." So withheld scopes are an explainable downgrade, not a silent gap.
- Feeds the **capability-gated** fields in the GraphQL BFF (return `null` + reason, per ROADMAP Phase 2).

## 4. Auth & token handling

App JWT signed with the App private key (Vault, NFR-6.2) → short-lived **installation access
tokens**, cached + refreshed per installation, scoped to granted permissions. Token/installation
health is monitored with admin alerts on breakage (FR-2.8). All token use is rate-budgeted per
installation (REST 5k req/h, GraphQL 5k points/h) — see `nfr-and-capacity.md` §1/§1a.

See `system-requirements.md` FR-2.1/FR-2.10/FR-2.11, `PRD.md` §6 (signal tiers), and
`DATA-MODEL.md` §7 (team/ownership models that the Members + Contents scopes populate).

// Package github implements connector.Source for GitHub.
//
// P1.B extends coverage from PR-only to the full STRONG signal set
// (signal-confidence table, CLAUDE.md): pull requests, reviews, review/issue
// comments, commits (push), issues, and CI checks (check_run / status). Each is
// mapped to a canonical event and the entities the normalizer persists. Other
// event types are recognized and skipped.
//
// Normalize is pure: bytes in, normalized output out — no DB or network. The
// installation→tenant resolution, persistence, and idempotency live in the
// normalizer.
package github

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dev-intel/platform/libs/go/connector"
	"github.com/dev-intel/platform/libs/go/events"
)

// Source is the GitHub connector.
type Source struct{}

func New() *Source { return &Source{} }

func (s *Source) Name() string { return "github" }

// Normalize maps a GitHub raw event into canonical entities. It dispatches on
// the X-GitHub-Event type; unrecognized/out-of-scope types are skipped.
func (s *Source) Normalize(raw connector.RawEvent) (connector.Result, error) {
	switch raw.EventType {
	case "pull_request":
		return normalizePullRequest(raw.Body)
	case "pull_request_review":
		return normalizeReview(raw.Body)
	case "pull_request_review_comment":
		return normalizeReviewComment(raw.Body)
	case "issue_comment":
		return normalizeIssueComment(raw.Body)
	case "issues":
		return normalizeIssue(raw.Body)
	case "push":
		return normalizePush(raw.Body)
	case "check_run":
		return normalizeCheckRun(raw.Body)
	case "status":
		return normalizeStatus(raw.Body)
	case "check_suite":
		// check_suite aggregates check_runs; persisting it too would double-count
		// CI signals (METRIC-SPEC flake/pass-rate derive from check_run rows).
		// Recognized but intentionally not materialized.
		return connector.Result{Skip: true}, nil
	default:
		// Recognized event we don't build metrics on (signal-confidence table).
		return connector.Result{Skip: true}, nil
	}
}

// --- shared payload fragments ---

type installation struct {
	ID int64 `json:"id"`
}

type repository struct {
	FullName string `json:"full_name"`
}

type actor struct {
	Login string `json:"login"`
}

// --- pull_request ---

type pullRequest struct {
	NodeID       string     `json:"node_id"`
	Number       int        `json:"number"`
	Title        string     `json:"title"`
	State        string     `json:"state"` // open | closed
	Merged       bool       `json:"merged"`
	ChangedFiles int        `json:"changed_files"`
	Additions    int        `json:"additions"`
	Deletions    int        `json:"deletions"`
	CreatedAt    time.Time  `json:"created_at"`
	MergedAt     *time.Time `json:"merged_at"`
	ClosedAt     *time.Time `json:"closed_at"`
	User         actor      `json:"user"`
}

// prEnrichment mirrors the `_enrichment` block the connector-github enricher
// (P1.C) injects into the raw PR payload before it reaches the normalizer. Absent
// for un-enriched events (degraded mode / before the enricher ran).
type prEnrichment struct {
	Additions    *int                   `json:"additions"`
	Deletions    *int                   `json:"deletions"`
	ChangedFiles *int                   `json:"changed_files"`
	CommitOIDs   []string               `json:"commit_oids"`
	Files        []connector.FileChange `json:"files"`
}

type prPayload struct {
	Action       string        `json:"action"`
	Number       int           `json:"number"`
	PullRequest  pullRequest   `json:"pull_request"`
	Repository   repository    `json:"repository"`
	Installation installation  `json:"installation"`
	Enrichment   *prEnrichment `json:"_enrichment"`
}

func normalizePullRequest(body []byte) (connector.Result, error) {
	var p prPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return connector.Result{}, fmt.Errorf("github: decode pull_request: %w", err)
	}
	if p.Installation.ID == 0 {
		return connector.Result{}, fmt.Errorf("github: missing installation id")
	}

	// The PR number lives at the event's top level; some payloads omit it from the
	// nested pull_request object. Prefer the top-level value when the nested one is absent.
	if p.PullRequest.Number == 0 {
		p.PullRequest.Number = p.Number
	}

	wi := prWorkItem(p.PullRequest, p.Repository.FullName)
	applyEnrichment(&wi, p.Enrichment)
	wi.Event = workItemEvent(p.Action)
	wi.OccurredAt = workItemOccurredAt(wi)

	return connector.Result{
		InstallationID: p.Installation.ID,
		WorkItems:      []connector.WorkItem{wi},
	}, nil
}

// applyEnrichment overlays GraphQL-fetched detail onto a PR work item. The
// authoritative size counts override the webhook's (which can be stale or absent
// on non-PR events carrying a PR object); commit OIDs + file churn ride into the
// canonical payload for downstream correlation/analysis.
func applyEnrichment(wi *connector.WorkItem, enr *prEnrichment) {
	if enr == nil {
		return
	}
	if enr.ChangedFiles != nil {
		wi.ChangedFiles = *enr.ChangedFiles
	}
	if enr.Additions != nil {
		wi.Additions = *enr.Additions
	}
	if enr.Deletions != nil {
		wi.Deletions = *enr.Deletions
	}
	wi.CommitOIDs = enr.CommitOIDs
	wi.Files = enr.Files
}

// prWorkItem builds a canonical work item from a PR object. Event/OccurredAt are
// set by the caller (so the same builder serves both an emitted PR and the
// ensure-only PR a review links to).
func prWorkItem(pr pullRequest, repo string) connector.WorkItem {
	status, stage := prStatusAndStage(pr)
	return connector.WorkItem{
		Type:         "pr",
		Repo:         repo,
		NodeID:       pr.NodeID,
		Number:       pr.Number,
		Title:        pr.Title,
		Status:       status,
		CurrentStage: stage,
		AuthorLogin:  pr.User.Login,
		ChangedFiles: pr.ChangedFiles,
		Additions:    pr.Additions,
		Deletions:    pr.Deletions,
		CreatedAt:    pr.CreatedAt,
		MergedAt:     pr.MergedAt,
		ClosedAt:     pr.ClosedAt,
	}
}

func prStatusAndStage(pr pullRequest) (status, stage string) {
	switch {
	case pr.Merged:
		return "merged", "merged"
	case pr.State == "closed":
		return "closed", "closed"
	default:
		return "open", "open"
	}
}

// --- pull_request_review ---

type reviewPayload struct {
	Action string `json:"action"`
	Review struct {
		NodeID      string    `json:"node_id"`
		State       string    `json:"state"` // approved | changes_requested | commented | dismissed
		SubmittedAt time.Time `json:"submitted_at"`
		User        actor     `json:"user"`
	} `json:"review"`
	PullRequest  pullRequest  `json:"pull_request"`
	Repository   repository   `json:"repository"`
	Installation installation `json:"installation"`
}

func normalizeReview(body []byte) (connector.Result, error) {
	var p reviewPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return connector.Result{}, fmt.Errorf("github: decode pull_request_review: %w", err)
	}
	if p.Installation.ID == 0 {
		return connector.Result{}, fmt.Errorf("github: missing installation id")
	}
	// Only "submitted" carries a meaningful new review; edited/dismissed are
	// out of scope for review-health metrics here.
	if p.Action != "submitted" {
		return connector.Result{Skip: true}, nil
	}

	// Ensure-only PR so the review has a work_item to link even if the PR webhook
	// hasn't landed yet (Event left empty → upsert-if-absent, no canonical emit).
	pr := prWorkItem(p.PullRequest, p.Repository.FullName)

	rv := connector.Review{
		SourceID:      p.Review.NodeID,
		Repo:          p.Repository.FullName,
		PRNodeID:      p.PullRequest.NodeID,
		ReviewerLogin: p.Review.User.Login,
		State:         strings.ToLower(p.Review.State),
		SubmittedAt:   p.Review.SubmittedAt,
	}

	return connector.Result{
		InstallationID: p.Installation.ID,
		WorkItems:      []connector.WorkItem{pr},
		Reviews:        []connector.Review{rv},
	}, nil
}

// --- comments (pull_request_review_comment, issue_comment) ---

type comment struct {
	NodeID    string    `json:"node_id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	User      actor     `json:"user"`
}

type reviewCommentPayload struct {
	Action      string  `json:"action"`
	Comment     comment `json:"comment"`
	PullRequest struct {
		Number int `json:"number"`
	} `json:"pull_request"`
	Repository   repository   `json:"repository"`
	Installation installation `json:"installation"`
}

func normalizeReviewComment(body []byte) (connector.Result, error) {
	var p reviewCommentPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return connector.Result{}, fmt.Errorf("github: decode pull_request_review_comment: %w", err)
	}
	if p.Installation.ID == 0 {
		return connector.Result{}, fmt.Errorf("github: missing installation id")
	}
	if p.Action != "created" {
		return connector.Result{Skip: true}, nil
	}
	return connector.Result{
		InstallationID: p.Installation.ID,
		Comments: []connector.Comment{{
			SourceID:     p.Comment.NodeID,
			Repo:         p.Repository.FullName,
			ParentType:   "pr",
			ParentNumber: p.PullRequest.Number,
			AuthorLogin:  p.Comment.User.Login,
			Body:         p.Comment.Body,
			CreatedAt:    p.Comment.CreatedAt,
		}},
	}, nil
}

type issueCommentPayload struct {
	Action string `json:"action"`
	Issue  struct {
		Number      int             `json:"number"`
		PullRequest json.RawMessage `json:"pull_request"` // present iff the issue is a PR
	} `json:"issue"`
	Comment      comment      `json:"comment"`
	Repository   repository   `json:"repository"`
	Installation installation `json:"installation"`
}

func normalizeIssueComment(body []byte) (connector.Result, error) {
	var p issueCommentPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return connector.Result{}, fmt.Errorf("github: decode issue_comment: %w", err)
	}
	if p.Installation.ID == 0 {
		return connector.Result{}, fmt.Errorf("github: missing installation id")
	}
	if p.Action != "created" {
		return connector.Result{Skip: true}, nil
	}
	parent := "issue"
	if len(p.Issue.PullRequest) > 0 {
		parent = "pr" // issue_comment on a PR carries a pull_request sub-object
	}
	return connector.Result{
		InstallationID: p.Installation.ID,
		Comments: []connector.Comment{{
			SourceID:     p.Comment.NodeID,
			Repo:         p.Repository.FullName,
			ParentType:   parent,
			ParentNumber: p.Issue.Number,
			AuthorLogin:  p.Comment.User.Login,
			Body:         p.Comment.Body,
			CreatedAt:    p.Comment.CreatedAt,
		}},
	}, nil
}

// --- issues ---

type issuePayload struct {
	Action string `json:"action"`
	Issue  struct {
		NodeID    string     `json:"node_id"`
		Number    int        `json:"number"`
		Title     string     `json:"title"`
		State     string     `json:"state"` // open | closed
		CreatedAt time.Time  `json:"created_at"`
		ClosedAt  *time.Time `json:"closed_at"`
		User      actor      `json:"user"`
	} `json:"issue"`
	Repository   repository   `json:"repository"`
	Installation installation `json:"installation"`
}

func normalizeIssue(body []byte) (connector.Result, error) {
	var p issuePayload
	if err := json.Unmarshal(body, &p); err != nil {
		return connector.Result{}, fmt.Errorf("github: decode issues: %w", err)
	}
	if p.Installation.ID == 0 {
		return connector.Result{}, fmt.Errorf("github: missing installation id")
	}

	status, stage := "open", "open"
	if p.Issue.State == "closed" {
		status, stage = "closed", "closed"
	}
	wi := connector.WorkItem{
		Type:         "issue",
		Repo:         p.Repository.FullName,
		NodeID:       p.Issue.NodeID,
		Number:       p.Issue.Number,
		Title:        p.Issue.Title,
		Status:       status,
		CurrentStage: stage,
		AuthorLogin:  p.Issue.User.Login,
		CreatedAt:    p.Issue.CreatedAt,
		ClosedAt:     p.Issue.ClosedAt,
		Event:        workItemEvent(p.Action),
	}
	wi.OccurredAt = workItemOccurredAt(wi)

	return connector.Result{
		InstallationID: p.Installation.ID,
		WorkItems:      []connector.WorkItem{wi},
	}, nil
}

// --- push (commits) ---

type pushPayload struct {
	Commits []struct {
		ID        string    `json:"id"` // sha
		Message   string    `json:"message"`
		Timestamp time.Time `json:"timestamp"`
		Author    struct {
			Name     string `json:"name"`
			Email    string `json:"email"`
			Username string `json:"username"`
		} `json:"author"`
	} `json:"commits"`
	Repository   repository   `json:"repository"`
	Installation installation `json:"installation"`
}

func normalizePush(body []byte) (connector.Result, error) {
	var p pushPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return connector.Result{}, fmt.Errorf("github: decode push: %w", err)
	}
	if p.Installation.ID == 0 {
		return connector.Result{}, fmt.Errorf("github: missing installation id")
	}
	if len(p.Commits) == 0 {
		// Branch create/delete or a forced push with no listed commits.
		return connector.Result{Skip: true}, nil
	}

	items := make([]connector.WorkItem, 0, len(p.Commits))
	for _, c := range p.Commits {
		items = append(items, connector.WorkItem{
			Type:        "commit",
			Repo:        p.Repository.FullName,
			NodeID:      c.ID, // commit sha (push payloads carry no graphql node id)
			Title:       firstLine(c.Message),
			Status:      "observed",
			AuthorLogin: c.Author.Username,
			CreatedAt:   c.Timestamp,
			OccurredAt:  c.Timestamp,
			Event:       events.CommitObserved,
		})
	}
	return connector.Result{InstallationID: p.Installation.ID, WorkItems: items}, nil
}

// --- check_run / status ---

type checkRunPayload struct {
	Action   string `json:"action"`
	CheckRun struct {
		NodeID      string     `json:"node_id"`
		Name        string     `json:"name"`
		HeadSHA     string     `json:"head_sha"`
		Status      string     `json:"status"` // queued | in_progress | completed
		Conclusion  string     `json:"conclusion"`
		StartedAt   *time.Time `json:"started_at"`
		CompletedAt *time.Time `json:"completed_at"`
	} `json:"check_run"`
	Repository   repository   `json:"repository"`
	Installation installation `json:"installation"`
}

func normalizeCheckRun(body []byte) (connector.Result, error) {
	var p checkRunPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return connector.Result{}, fmt.Errorf("github: decode check_run: %w", err)
	}
	if p.Installation.ID == 0 {
		return connector.Result{}, fmt.Errorf("github: missing installation id")
	}
	// Only completed runs carry a conclusion (the CI-reliability signal).
	if p.CheckRun.Status != "completed" {
		return connector.Result{Skip: true}, nil
	}
	return connector.Result{
		InstallationID: p.Installation.ID,
		CheckRuns: []connector.CheckRun{{
			SourceID:    p.CheckRun.NodeID,
			Repo:        p.Repository.FullName,
			HeadSHA:     p.CheckRun.HeadSHA,
			Name:        p.CheckRun.Name,
			Conclusion:  p.CheckRun.Conclusion,
			StartedAt:   p.CheckRun.StartedAt,
			CompletedAt: p.CheckRun.CompletedAt,
		}},
	}, nil
}

type statusPayload struct {
	ID           int64        `json:"id"`
	SHA          string       `json:"sha"`
	Context      string       `json:"context"`
	State        string       `json:"state"` // pending | success | failure | error
	UpdatedAt    *time.Time   `json:"updated_at"`
	Repository   repository   `json:"repository"`
	Installation installation `json:"installation"`
}

func normalizeStatus(body []byte) (connector.Result, error) {
	var p statusPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return connector.Result{}, fmt.Errorf("github: decode status: %w", err)
	}
	if p.Installation.ID == 0 {
		return connector.Result{}, fmt.Errorf("github: missing installation id")
	}
	// Legacy commit-status API: skip the transient "pending" tick; record the
	// terminal state as a check on the SHA (correlated to a PR later by head SHA).
	if p.State == "pending" {
		return connector.Result{Skip: true}, nil
	}
	return connector.Result{
		InstallationID: p.Installation.ID,
		CheckRuns: []connector.CheckRun{{
			SourceID:    fmt.Sprintf("status:%d", p.ID),
			Repo:        p.Repository.FullName,
			HeadSHA:     p.SHA,
			Name:        p.Context,
			Conclusion:  statusConclusion(p.State),
			CompletedAt: p.UpdatedAt,
		}},
	}, nil
}

// statusConclusion maps the legacy commit-status state onto the check_run
// conclusion vocabulary (METRIC-SPEC CI metrics treat them uniformly).
func statusConclusion(state string) string {
	switch state {
	case "success":
		return "success"
	case "failure", "error":
		return "failure"
	default:
		return state
	}
}

// --- helpers ---

// workItemEvent maps a PR/issue webhook action to a canonical event type.
func workItemEvent(action string) events.EventType {
	switch action {
	case "opened":
		return events.WorkItemCreated
	case "closed", "reopened", "ready_for_review", "converted_to_draft":
		return events.WorkItemStateChanged
	default:
		return events.WorkItemUpdated
	}
}

// workItemOccurredAt picks the source timestamp that best dates the event:
// the close/merge time for a terminal transition, else creation time.
func workItemOccurredAt(wi connector.WorkItem) time.Time {
	switch {
	case wi.MergedAt != nil:
		return *wi.MergedAt
	case wi.ClosedAt != nil:
		return *wi.ClosedAt
	default:
		return wi.CreatedAt
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

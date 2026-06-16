// Package connector defines the SOURCE-AGNOSTIC ingestion interface.
// GitHub is implementation #1 (sub-package github). Building every source
// against this interface — rather than around GitHub specifics — is what keeps
// a second source (e.g. Slack) cheap to add later. See ADR-001.
package connector

import (
	"time"

	"github.com/dev-intel/platform/libs/go/events"
)

// RawEvent is an untyped inbound event as received at the edge.
type RawEvent struct {
	DeliveryID string // idempotency key (e.g. X-GitHub-Delivery)
	EventType  string // source event name (e.g. "pull_request")
	Body       []byte // raw payload
	TraceID    string // propagated correlation id
}

// WorkItem is the normalized work item a source produced (tenant_id is stamped
// later by the platform after resolving the installation → tenant mapping).
type WorkItem struct {
	Type         string // pr | issue | commit
	Repo         string // owner/name
	NodeID       string // source global id
	Number       int
	Title        string
	Status       string // open | closed | merged
	CurrentStage string // open | in_review | merged | closed
	AuthorLogin  string
	ChangedFiles int
	Additions    int
	Deletions    int
	CreatedAt    time.Time
	MergedAt     *time.Time
	ClosedAt     *time.Time

	// Event is the canonical event type to emit for this item. An empty Event
	// means "ensure this row exists but do not emit" — used for the parent PR a
	// review references, so the review has a work_item to link to even if the PR
	// webhook hasn't arrived yet (out-of-order delivery). Ensure-only upserts
	// never clobber an existing richer row.
	Event events.EventType
	// OccurredAt is the source timestamp for the emitted canonical event.
	OccurredAt time.Time

	// Enriched fields (P1.C) — present when the connector-github enricher attached
	// GraphQL detail the webhook lacked. CommitOIDs feed PR↔commit correlation
	// (P1.E); Files feed review-health hotspots and Phase-3 semantic analysis.
	// They ride in the canonical event payload, not work_item columns.
	CommitOIDs []string
	Files      []FileChange
}

// FileChange is one file's churn in a PR (P1.C enrichment). Patch is the unified
// diff, populated only when patch fetching is enabled upstream.
type FileChange struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// Review is a normalized PR review (review.submitted). It links to its PR by
// PRNodeID; the platform resolves that to a work_item_id and ReviewerLogin to a
// contributor at persist time.
type Review struct {
	SourceID      string // GitHub review node id (idempotency)
	Repo          string
	PRNodeID      string // parent PR's global node id (for correlation)
	ReviewerLogin string
	State         string // approved | changes_requested | commented | dismissed
	CommentCount  int
	SubmittedAt   time.Time
}

// CheckRun is a normalized CI check (check.completed). work_item_id is resolved
// later by head-SHA correlation (P1.E), so only HeadSHA is carried here.
type CheckRun struct {
	SourceID    string // GitHub check_run / status id (idempotency)
	Repo        string
	HeadSHA     string
	Name        string
	Conclusion  string // success | failure | cancelled | timed_out | "" (in-progress)
	StartedAt   *time.Time
	CompletedAt *time.Time
}

// Comment is a normalized PR-review or issue comment (comment.added). It has no
// dedicated table; it is emitted as a canonical event for downstream consumers.
type Comment struct {
	SourceID     string // comment node id (idempotency)
	Repo         string
	ParentType   string // pr | issue
	ParentNumber int
	AuthorLogin  string
	Body         string
	CreatedAt    time.Time
}

// Result is the outcome of normalizing one RawEvent. A single webhook can yield
// several entities (e.g. a push → many commits), so each kind is its own slice.
type Result struct {
	// InstallationID identifies which install (→ tenant) the event belongs to.
	InstallationID int64
	// WorkItems are normalized PRs/issues/commits (and ensure-only link targets).
	WorkItems []WorkItem
	// Reviews are normalized PR reviews.
	Reviews []Review
	// CheckRuns are normalized CI checks.
	CheckRuns []CheckRun
	// Comments are normalized review/issue comments (event-only).
	Comments []Comment
	// Skip is true when the event is recognized but not relevant in this phase.
	Skip bool
}

// Empty reports whether a Result produced no entities at all (e.g. an action the
// connector recognized but had nothing to normalize).
func (r Result) Empty() bool {
	return len(r.WorkItems) == 0 && len(r.Reviews) == 0 && len(r.CheckRuns) == 0 && len(r.Comments) == 0
}

// Source normalizes raw events into canonical work items. Implementations are
// pure (no DB/network): given bytes, produce normalized output.
type Source interface {
	Name() string
	Normalize(RawEvent) (Result, error)
}

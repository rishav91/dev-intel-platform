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
}

// Result is the outcome of normalizing one RawEvent.
type Result struct {
	// InstallationID identifies which install (→ tenant) the event belongs to.
	InstallationID int64
	// EventType is the canonical event type to emit for the items.
	EventType events.EventType
	// Items are the normalized work items (usually one).
	Items []WorkItem
	// Skip is true when the event is recognized but not relevant in this phase.
	Skip bool
}

// Source normalizes raw events into canonical work items. Implementations are
// pure (no DB/network): given bytes, produce normalized output.
type Source interface {
	Name() string
	Normalize(RawEvent) (Result, error)
}

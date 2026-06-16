// Package events defines the source-agnostic canonical domain event.
// This mirrors schemas/events/canonical_event.schema.json, which is the
// contract of record. Keep them in sync (in a later phase, generate this
// from the schema).
package events

import (
	"time"

	"github.com/google/uuid"
)

// Kafka topics.
const (
	TopicRawGitHub = "raw.github"
	// TopicEnrichedGitHub carries raw events after the connector-github enricher
	// has (best-effort) attached GraphQL-fetched detail the webhook lacked (P1.C).
	// The normalizer consumes this, not raw.github directly; archiver still reads
	// raw.github so the immutable raw archive is never coupled to enrichment.
	TopicEnrichedGitHub = "enriched.github"
	TopicCanonical      = "canonical.events"
	// TopicDeadLetter holds messages the normalizer can't process and won't
	// retry (permanently bad: undecodable, missing required fields). Kept for
	// inspection/replay rather than dropped silently.
	TopicDeadLetter = "raw.github.dlq"

	// HeaderTraceParent carries W3C trace context (OpenTelemetry) so a request is
	// one trace across every service + the Kafka/outbox hops. This is the header
	// propagation actually depends on. See libs/go/observability.
	HeaderTraceParent = "traceparent"
	// HeaderTraceID is the human-readable 32-hex trace id, set alongside
	// traceparent for convenience (log/debug); nothing parses it for propagation.
	HeaderTraceID     = "trace-id"
	HeaderGitHubEvent = "x-github-event"
	HeaderGitHubDeliv = "x-github-delivery"
	// HeaderDLQReason carries why a message was dead-lettered.
	HeaderDLQReason = "dlq-reason"
	// HeaderEnrichStatus records what the enricher did: enriched | skipped |
	// disabled | failed | rate_limited (see services/connector-github).
	HeaderEnrichStatus = "enrich-status"
	// HeaderOccurredAt carries the source event time (RFC3339) the enricher
	// stamped, so downstream ordering uses source time, not arrival (P1.C).
	HeaderOccurredAt = "occurred-at"
)

// EventType enumerates canonical event types. Mirrors the enum in
// schemas/events/canonical_event.schema.json — keep them in sync.
type EventType string

const (
	WorkItemCreated      EventType = "work_item.created"
	WorkItemUpdated      EventType = "work_item.updated"
	WorkItemStateChanged EventType = "work_item.state_changed"
	WorkItemClosed       EventType = "work_item.closed"
	// P1.B — full STRONG-signal coverage beyond pull_request.
	ReviewSubmitted EventType = "review.submitted"
	CommentAdded    EventType = "comment.added"
	CommitObserved  EventType = "commit.observed"
	CheckCompleted  EventType = "check.completed"
)

// Source identifies the origin system. GitHub only in this phase.
const SourceGitHub = "github"

// CanonicalEvent is the normalized envelope flowing on TopicCanonical.
type CanonicalEvent struct {
	EventID       string         `json:"event_id"`
	TenantID      string         `json:"tenant_id"`
	Type          EventType      `json:"type"`
	Source        string         `json:"source"`
	SourceEventID string         `json:"source_event_id"`
	OccurredAt    time.Time      `json:"occurred_at"`
	Version       int            `json:"version"`
	Payload       map[string]any `json:"payload"`
}

// New builds a canonical event with a fresh event_id and version 1.
func New(tenantID string, t EventType, sourceEventID string, occurredAt time.Time, payload map[string]any) CanonicalEvent {
	return CanonicalEvent{
		EventID:       uuid.NewString(),
		TenantID:      tenantID,
		Type:          t,
		Source:        SourceGitHub,
		SourceEventID: sourceEventID,
		OccurredAt:    occurredAt,
		Version:       1,
		Payload:       payload,
	}
}

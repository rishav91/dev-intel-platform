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
	TopicRawGitHub     = "raw.github"
	TopicCanonical     = "canonical.events"
	HeaderTraceID      = "trace-id"
	HeaderGitHubEvent  = "x-github-event"
	HeaderGitHubDeliv  = "x-github-delivery"
)

// EventType enumerates canonical event types (subset implemented in Phase 0).
type EventType string

const (
	WorkItemCreated      EventType = "work_item.created"
	WorkItemUpdated      EventType = "work_item.updated"
	WorkItemStateChanged EventType = "work_item.state_changed"
	WorkItemClosed       EventType = "work_item.closed"
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

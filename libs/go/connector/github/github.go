// Package github implements connector.Source for GitHub.
// Phase 0 handles `pull_request` events end-to-end; other event types are
// recognized and skipped (wired in Phase 1).
package github

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/dev-intel/platform/libs/go/connector"
	"github.com/dev-intel/platform/libs/go/events"
)

// Source is the GitHub connector.
type Source struct{}

func New() *Source { return &Source{} }

func (s *Source) Name() string { return "github" }

// --- minimal payload shapes (only fields we use) ---

type prPayload struct {
	Action      string `json:"action"`
	Number      int    `json:"number"`
	PullRequest struct {
		NodeID       string     `json:"node_id"`
		Title        string     `json:"title"`
		State        string     `json:"state"` // open | closed
		Merged       bool       `json:"merged"`
		ChangedFiles int        `json:"changed_files"`
		Additions    int        `json:"additions"`
		Deletions    int        `json:"deletions"`
		CreatedAt    time.Time  `json:"created_at"`
		MergedAt     *time.Time `json:"merged_at"`
		ClosedAt     *time.Time `json:"closed_at"`
		User         struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"pull_request"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

// Normalize maps a GitHub raw event into canonical work items.
func (s *Source) Normalize(raw connector.RawEvent) (connector.Result, error) {
	if raw.EventType != "pull_request" {
		// Recognized but out of scope for Phase 0.
		return connector.Result{Skip: true}, nil
	}

	var p prPayload
	if err := json.Unmarshal(raw.Body, &p); err != nil {
		return connector.Result{}, fmt.Errorf("github: decode pull_request: %w", err)
	}
	if p.Installation.ID == 0 {
		return connector.Result{}, fmt.Errorf("github: missing installation id")
	}

	status, stage := statusAndStage(p)

	wi := connector.WorkItem{
		Type:         "pr",
		Repo:         p.Repository.FullName,
		NodeID:       p.PullRequest.NodeID,
		Number:       p.Number,
		Title:        p.PullRequest.Title,
		Status:       status,
		CurrentStage: stage,
		AuthorLogin:  p.PullRequest.User.Login,
		ChangedFiles: p.PullRequest.ChangedFiles,
		Additions:    p.PullRequest.Additions,
		Deletions:    p.PullRequest.Deletions,
		CreatedAt:    p.PullRequest.CreatedAt,
		MergedAt:     p.PullRequest.MergedAt,
		ClosedAt:     p.PullRequest.ClosedAt,
	}

	return connector.Result{
		InstallationID: p.Installation.ID,
		EventType:      eventType(p.Action),
		Items:          []connector.WorkItem{wi},
	}, nil
}

func statusAndStage(p prPayload) (status, stage string) {
	switch {
	case p.PullRequest.Merged:
		return "merged", "merged"
	case p.PullRequest.State == "closed":
		return "closed", "closed"
	default:
		return "open", "open"
	}
}

func eventType(action string) events.EventType {
	switch action {
	case "opened":
		return events.WorkItemCreated
	case "closed", "reopened", "ready_for_review", "converted_to_draft":
		return events.WorkItemStateChanged
	default:
		return events.WorkItemUpdated
	}
}

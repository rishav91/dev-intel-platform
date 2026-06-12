package github

import (
	"testing"

	"github.com/dev-intel/platform/libs/go/connector"
	"github.com/dev-intel/platform/libs/go/events"
)

// Table-driven coverage of the GitHub connector's Normalize over fixture
// payloads — the normalization spine, exercisable without any infra.
func TestNormalize(t *testing.T) {
	const prOpened = `{
	  "action": "opened", "number": 482,
	  "pull_request": {
	    "node_id": "PR_1", "title": "Add feature", "state": "open", "merged": false,
	    "changed_files": 7, "additions": 210, "deletions": 64,
	    "created_at": "2026-06-11T10:00:00Z", "merged_at": null, "closed_at": null,
	    "user": { "login": "alice" }
	  },
	  "repository": { "full_name": "acme/app" },
	  "installation": { "id": 42424242 }
	}`

	const prMerged = `{
	  "action": "closed", "number": 99,
	  "pull_request": {
	    "node_id": "PR_2", "title": "Merge me", "state": "closed", "merged": true,
	    "created_at": "2026-06-10T09:00:00Z", "merged_at": "2026-06-11T09:00:00Z",
	    "closed_at": "2026-06-11T09:00:00Z", "user": { "login": "bob" }
	  },
	  "repository": { "full_name": "acme/app" },
	  "installation": { "id": 42424242 }
	}`

	const prClosedUnmerged = `{
	  "action": "closed", "number": 7,
	  "pull_request": {
	    "node_id": "PR_3", "title": "Abandoned", "state": "closed", "merged": false,
	    "created_at": "2026-06-10T09:00:00Z", "closed_at": "2026-06-11T09:00:00Z",
	    "user": { "login": "carol" }
	  },
	  "repository": { "full_name": "acme/app" },
	  "installation": { "id": 42424242 }
	}`

	const prReopened = `{
	  "action": "reopened", "number": 7,
	  "pull_request": { "node_id": "PR_3", "title": "Back", "state": "open", "merged": false,
	    "created_at": "2026-06-10T09:00:00Z", "user": { "login": "carol" } },
	  "repository": { "full_name": "acme/app" },
	  "installation": { "id": 42424242 }
	}`

	cases := []struct {
		name        string
		eventType   string
		body        string
		wantSkip    bool
		wantErr     bool
		wantStatus  string
		wantStage   string
		wantEvent   events.EventType
		wantInstall int64
		wantAuthor  string
	}{
		{
			name: "pr opened", eventType: "pull_request", body: prOpened,
			wantStatus: "open", wantStage: "open",
			wantEvent: events.WorkItemCreated, wantInstall: 42424242, wantAuthor: "alice",
		},
		{
			name: "pr merged", eventType: "pull_request", body: prMerged,
			wantStatus: "merged", wantStage: "merged",
			wantEvent: events.WorkItemStateChanged, wantInstall: 42424242, wantAuthor: "bob",
		},
		{
			name: "pr closed unmerged", eventType: "pull_request", body: prClosedUnmerged,
			wantStatus: "closed", wantStage: "closed",
			wantEvent: events.WorkItemStateChanged, wantInstall: 42424242, wantAuthor: "carol",
		},
		{
			name: "pr reopened", eventType: "pull_request", body: prReopened,
			wantStatus: "open", wantStage: "open",
			wantEvent: events.WorkItemStateChanged, wantInstall: 42424242, wantAuthor: "carol",
		},
		{
			name: "non-pr event skipped", eventType: "issues", body: `{"action":"opened"}`,
			wantSkip: true,
		},
		{
			name: "missing installation id", eventType: "pull_request",
			body:    `{"action":"opened","pull_request":{"node_id":"x"},"repository":{"full_name":"a/b"}}`,
			wantErr: true,
		},
		{
			name: "malformed json", eventType: "pull_request", body: `{not json`,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			res, err := s.Normalize(connector.RawEvent{EventType: tc.eventType, Body: []byte(tc.body)})

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (res=%+v)", res)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantSkip {
				if !res.Skip {
					t.Fatalf("expected Skip=true, got %+v", res)
				}
				return
			}
			if res.Skip {
				t.Fatalf("unexpected Skip=true")
			}
			if len(res.Items) != 1 {
				t.Fatalf("want 1 item, got %d", len(res.Items))
			}
			wi := res.Items[0]
			if wi.Status != tc.wantStatus {
				t.Errorf("status: want %q got %q", tc.wantStatus, wi.Status)
			}
			if wi.CurrentStage != tc.wantStage {
				t.Errorf("stage: want %q got %q", tc.wantStage, wi.CurrentStage)
			}
			if wi.AuthorLogin != tc.wantAuthor {
				t.Errorf("author: want %q got %q", tc.wantAuthor, wi.AuthorLogin)
			}
			if res.EventType != tc.wantEvent {
				t.Errorf("event type: want %q got %q", tc.wantEvent, res.EventType)
			}
			if res.InstallationID != tc.wantInstall {
				t.Errorf("installation: want %d got %d", tc.wantInstall, res.InstallationID)
			}
			if wi.Type != "pr" {
				t.Errorf("type: want pr got %q", wi.Type)
			}
		})
	}
}

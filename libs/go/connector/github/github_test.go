package github

import (
	"testing"

	"github.com/dev-intel/platform/libs/go/connector"
	"github.com/dev-intel/platform/libs/go/events"
)

// Table-driven coverage of the GitHub connector's Normalize over fixture
// payloads — the normalization spine, exercisable without any infra.
func TestNormalizePullRequest(t *testing.T) {
	const prOpened = `{
	  "action": "opened", "number": 482,
	  "pull_request": {
	    "node_id": "PR_1", "number": 482, "title": "Add feature", "state": "open", "merged": false,
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
	    "node_id": "PR_2", "number": 99, "title": "Merge me", "state": "closed", "merged": true,
	    "created_at": "2026-06-10T09:00:00Z", "merged_at": "2026-06-11T09:00:00Z",
	    "closed_at": "2026-06-11T09:00:00Z", "user": { "login": "bob" }
	  },
	  "repository": { "full_name": "acme/app" },
	  "installation": { "id": 42424242 }
	}`

	const prClosedUnmerged = `{
	  "action": "closed", "number": 7,
	  "pull_request": {
	    "node_id": "PR_3", "number": 7, "title": "Abandoned", "state": "closed", "merged": false,
	    "created_at": "2026-06-10T09:00:00Z", "closed_at": "2026-06-11T09:00:00Z",
	    "user": { "login": "carol" }
	  },
	  "repository": { "full_name": "acme/app" },
	  "installation": { "id": 42424242 }
	}`

	const prReopened = `{
	  "action": "reopened", "number": 7,
	  "pull_request": { "node_id": "PR_3", "number": 7, "title": "Back", "state": "open", "merged": false,
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
			name: "unhandled event skipped", eventType: "fork", body: `{"action":"created"}`,
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
			if len(res.WorkItems) != 1 {
				t.Fatalf("want 1 work item, got %d", len(res.WorkItems))
			}
			wi := res.WorkItems[0]
			if wi.Status != tc.wantStatus {
				t.Errorf("status: want %q got %q", tc.wantStatus, wi.Status)
			}
			if wi.CurrentStage != tc.wantStage {
				t.Errorf("stage: want %q got %q", tc.wantStage, wi.CurrentStage)
			}
			if wi.AuthorLogin != tc.wantAuthor {
				t.Errorf("author: want %q got %q", tc.wantAuthor, wi.AuthorLogin)
			}
			if wi.Event != tc.wantEvent {
				t.Errorf("event type: want %q got %q", tc.wantEvent, wi.Event)
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

func TestNormalizeReview(t *testing.T) {
	const submitted = `{
	  "action": "submitted",
	  "review": { "node_id": "RV_1", "state": "approved", "submitted_at": "2026-06-12T10:00:00Z",
	    "user": { "login": "reviewer1" } },
	  "pull_request": { "node_id": "PR_1", "number": 482, "title": "Add feature", "state": "open",
	    "created_at": "2026-06-11T10:00:00Z", "user": { "login": "alice" } },
	  "repository": { "full_name": "acme/app" },
	  "installation": { "id": 42424242 }
	}`

	s := New()
	res, err := s.Normalize(connector.RawEvent{EventType: "pull_request_review", Body: []byte(submitted)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Reviews) != 1 {
		t.Fatalf("want 1 review, got %d", len(res.Reviews))
	}
	rv := res.Reviews[0]
	if rv.SourceID != "RV_1" || rv.PRNodeID != "PR_1" || rv.ReviewerLogin != "reviewer1" || rv.State != "approved" {
		t.Errorf("unexpected review: %+v", rv)
	}
	// The PR is emitted as an ensure-only link target (no canonical event).
	if len(res.WorkItems) != 1 {
		t.Fatalf("want 1 ensure-only work item, got %d", len(res.WorkItems))
	}
	if pr := res.WorkItems[0]; pr.NodeID != "PR_1" || pr.Event != "" {
		t.Errorf("PR link target should be ensure-only: %+v", pr)
	}

	// A non-submitted action is skipped.
	res, err = s.Normalize(connector.RawEvent{EventType: "pull_request_review",
		Body: []byte(`{"action":"dismissed","installation":{"id":1}}`)})
	if err != nil || !res.Skip {
		t.Fatalf("expected skip for dismissed review, got res=%+v err=%v", res, err)
	}
}

func TestNormalizeComments(t *testing.T) {
	const reviewComment = `{
	  "action": "created",
	  "comment": { "node_id": "RC_1", "body": "nit", "created_at": "2026-06-12T11:00:00Z",
	    "user": { "login": "reviewer1" } },
	  "pull_request": { "number": 482 },
	  "repository": { "full_name": "acme/app" },
	  "installation": { "id": 42424242 }
	}`
	const issueCommentOnPR = `{
	  "action": "created",
	  "issue": { "number": 482, "pull_request": { "url": "x" } },
	  "comment": { "node_id": "IC_1", "body": "lgtm", "created_at": "2026-06-12T12:00:00Z",
	    "user": { "login": "bob" } },
	  "repository": { "full_name": "acme/app" },
	  "installation": { "id": 42424242 }
	}`
	const issueCommentOnIssue = `{
	  "action": "created",
	  "issue": { "number": 13 },
	  "comment": { "node_id": "IC_2", "body": "repro?", "created_at": "2026-06-12T12:30:00Z",
	    "user": { "login": "carol" } },
	  "repository": { "full_name": "acme/app" },
	  "installation": { "id": 42424242 }
	}`

	s := New()
	cases := []struct {
		name       string
		eventType  string
		body       string
		wantSource string
		wantParent string
		wantNumber int
	}{
		{"review comment", "pull_request_review_comment", reviewComment, "RC_1", "pr", 482},
		{"issue comment on pr", "issue_comment", issueCommentOnPR, "IC_1", "pr", 482},
		{"issue comment on issue", "issue_comment", issueCommentOnIssue, "IC_2", "issue", 13},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := s.Normalize(connector.RawEvent{EventType: tc.eventType, Body: []byte(tc.body)})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(res.Comments) != 1 {
				t.Fatalf("want 1 comment, got %d", len(res.Comments))
			}
			cm := res.Comments[0]
			if cm.SourceID != tc.wantSource || cm.ParentType != tc.wantParent || cm.ParentNumber != tc.wantNumber {
				t.Errorf("unexpected comment: %+v", cm)
			}
		})
	}

	// Non-created actions are skipped.
	res, err := s.Normalize(connector.RawEvent{EventType: "issue_comment",
		Body: []byte(`{"action":"edited","installation":{"id":1}}`)})
	if err != nil || !res.Skip {
		t.Fatalf("expected skip for edited comment, got res=%+v err=%v", res, err)
	}
}

func TestNormalizeIssue(t *testing.T) {
	const opened = `{
	  "action": "opened",
	  "issue": { "node_id": "I_1", "number": 13, "title": "Bug", "state": "open",
	    "created_at": "2026-06-12T08:00:00Z", "user": { "login": "carol" } },
	  "repository": { "full_name": "acme/app" },
	  "installation": { "id": 42424242 }
	}`
	const closed = `{
	  "action": "closed",
	  "issue": { "node_id": "I_1", "number": 13, "title": "Bug", "state": "closed",
	    "created_at": "2026-06-12T08:00:00Z", "closed_at": "2026-06-12T09:00:00Z", "user": { "login": "carol" } },
	  "repository": { "full_name": "acme/app" },
	  "installation": { "id": 42424242 }
	}`

	s := New()
	res, err := s.Normalize(connector.RawEvent{EventType: "issues", Body: []byte(opened)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.WorkItems) != 1 {
		t.Fatalf("want 1 work item, got %d", len(res.WorkItems))
	}
	wi := res.WorkItems[0]
	if wi.Type != "issue" || wi.Status != "open" || wi.Event != events.WorkItemCreated {
		t.Errorf("unexpected issue work item: %+v", wi)
	}

	res, err = s.Normalize(connector.RawEvent{EventType: "issues", Body: []byte(closed)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wi := res.WorkItems[0]; wi.Status != "closed" || wi.Event != events.WorkItemStateChanged {
		t.Errorf("unexpected closed issue: %+v", wi)
	}
}

func TestNormalizePush(t *testing.T) {
	const push = `{
	  "commits": [
	    { "id": "sha1", "message": "fix: bug\n\nbody", "timestamp": "2026-06-12T10:00:00Z",
	      "author": { "name": "Alice", "email": "a@x.com", "username": "alice" } },
	    { "id": "sha2", "message": "chore: tidy", "timestamp": "2026-06-12T10:05:00Z",
	      "author": { "name": "Bob", "email": "b@x.com", "username": "bob" } }
	  ],
	  "repository": { "full_name": "acme/app" },
	  "installation": { "id": 42424242 }
	}`

	s := New()
	res, err := s.Normalize(connector.RawEvent{EventType: "push", Body: []byte(push)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.WorkItems) != 2 {
		t.Fatalf("want 2 commits, got %d", len(res.WorkItems))
	}
	c1 := res.WorkItems[0]
	if c1.Type != "commit" || c1.NodeID != "sha1" || c1.Title != "fix: bug" || c1.Event != events.CommitObserved {
		t.Errorf("unexpected commit work item: %+v", c1)
	}

	// An empty push (branch create/delete) is skipped.
	res, err = s.Normalize(connector.RawEvent{EventType: "push",
		Body: []byte(`{"commits":[],"installation":{"id":1}}`)})
	if err != nil || !res.Skip {
		t.Fatalf("expected skip for empty push, got res=%+v err=%v", res, err)
	}
}

func TestNormalizeChecks(t *testing.T) {
	const checkRunCompleted = `{
	  "action": "completed",
	  "check_run": { "node_id": "CR_1", "name": "build", "head_sha": "sha1", "status": "completed",
	    "conclusion": "success", "started_at": "2026-06-12T10:00:00Z", "completed_at": "2026-06-12T10:05:00Z" },
	  "repository": { "full_name": "acme/app" },
	  "installation": { "id": 42424242 }
	}`
	const checkRunInProgress = `{
	  "action": "created",
	  "check_run": { "node_id": "CR_2", "name": "build", "head_sha": "sha1", "status": "in_progress" },
	  "repository": { "full_name": "acme/app" },
	  "installation": { "id": 42424242 }
	}`
	const statusSuccess = `{
	  "id": 555, "sha": "sha2", "context": "ci/lint", "state": "success",
	  "updated_at": "2026-06-12T11:00:00Z",
	  "repository": { "full_name": "acme/app" },
	  "installation": { "id": 42424242 }
	}`
	const statusPending = `{ "id": 556, "sha": "sha2", "context": "ci/lint", "state": "pending",
	  "repository": { "full_name": "acme/app" }, "installation": { "id": 42424242 } }`
	const checkSuite = `{ "action": "completed", "check_suite": { "head_sha": "sha1" },
	  "repository": { "full_name": "acme/app" }, "installation": { "id": 42424242 } }`

	s := New()

	res, err := s.Normalize(connector.RawEvent{EventType: "check_run", Body: []byte(checkRunCompleted)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.CheckRuns) != 1 {
		t.Fatalf("want 1 check run, got %d", len(res.CheckRuns))
	}
	if cr := res.CheckRuns[0]; cr.SourceID != "CR_1" || cr.Conclusion != "success" || cr.HeadSHA != "sha1" {
		t.Errorf("unexpected check run: %+v", cr)
	}

	res, _ = s.Normalize(connector.RawEvent{EventType: "check_run", Body: []byte(checkRunInProgress)})
	if !res.Skip {
		t.Errorf("expected skip for in-progress check run, got %+v", res)
	}

	res, err = s.Normalize(connector.RawEvent{EventType: "status", Body: []byte(statusSuccess)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.CheckRuns) != 1 {
		t.Fatalf("want 1 check run from status, got %d", len(res.CheckRuns))
	}
	if cr := res.CheckRuns[0]; cr.SourceID != "status:555" || cr.Conclusion != "success" || cr.Name != "ci/lint" {
		t.Errorf("unexpected status check run: %+v", cr)
	}

	res, _ = s.Normalize(connector.RawEvent{EventType: "status", Body: []byte(statusPending)})
	if !res.Skip {
		t.Errorf("expected skip for pending status, got %+v", res)
	}

	res, _ = s.Normalize(connector.RawEvent{EventType: "check_suite", Body: []byte(checkSuite)})
	if !res.Skip {
		t.Errorf("expected skip for check_suite (aggregate), got %+v", res)
	}
}

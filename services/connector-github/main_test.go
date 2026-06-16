package main

import (
	"encoding/json"
	"testing"

	"github.com/dev-intel/platform/libs/go/githubapp"
)

// mergeEnrichment must add the _enrichment block without disturbing any original
// field — the normalizer still parses the same webhook shape it always did.
func TestMergeEnrichmentPreservesOriginal(t *testing.T) {
	const body = `{"action":"opened","number":482,` +
		`"pull_request":{"node_id":"PR_1","title":"x"},` +
		`"repository":{"full_name":"acme/app"},"installation":{"id":42}}`

	enr := githubapp.PullRequestEnrichment{
		Additions: 210, Deletions: 64, ChangedFiles: 2,
		CommitOIDs: []string{"sha1", "sha2"},
		Files:      []githubapp.FileChange{{Path: "a.go", Additions: 100, Deletions: 4}},
	}

	out, err := mergeEnrichment([]byte(body), enr)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("decode merged: %v", err)
	}
	// Original fields survive untouched.
	for _, k := range []string{"action", "number", "pull_request", "repository", "installation"} {
		if _, ok := m[k]; !ok {
			t.Errorf("merged payload dropped original field %q", k)
		}
	}
	// The enrichment block round-trips.
	raw, ok := m["_enrichment"]
	if !ok {
		t.Fatal("merged payload missing _enrichment")
	}
	var got githubapp.PullRequestEnrichment
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode _enrichment: %v", err)
	}
	if got.Additions != 210 || len(got.CommitOIDs) != 2 || got.Files[0].Path != "a.go" {
		t.Errorf("enrichment not preserved: %+v", got)
	}
}

// With no client configured the enricher must pass events through verbatim,
// marking why — so the pipeline runs in dev without GitHub credentials.
func TestEnrichPassThroughWhenDisabled(t *testing.T) {
	c := &connector{client: nil}
	const body = `{"number":1,"pull_request":{"created_at":"2026-06-12T10:00:00Z"},` +
		`"repository":{"full_name":"acme/app"},"installation":{"id":42}}`

	out, status, occurred := c.enrich(nil, "pull_request", []byte(body), "trace")
	if status != statusDisabled {
		t.Errorf("status: want %q got %q", statusDisabled, status)
	}
	if string(out) != body {
		t.Errorf("body should pass through unchanged when disabled")
	}
	if occurred.IsZero() {
		t.Errorf("occurred_at should be stamped from the source even when disabled")
	}
}

// Non-PR events are skipped (not enrichable today) and pass straight through.
func TestEnrichSkipsNonPR(t *testing.T) {
	c := &connector{client: nil}
	body := []byte(`{"action":"created"}`)
	out, status, _ := c.enrich(nil, "issue_comment", body, "trace")
	if status != statusSkipped {
		t.Errorf("status: want %q got %q", statusSkipped, status)
	}
	if string(out) != string(body) {
		t.Errorf("non-PR body should pass through unchanged")
	}
}

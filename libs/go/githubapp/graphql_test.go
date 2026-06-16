package githubapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// graphqlServer stubs token minting + the /graphql endpoint, returning the given
// PR enrichment body and a rateLimit block.
func graphqlServer(t *testing.T, prJSON string, remaining int) *httptest.Server {
	t.Helper()
	now := time.Now()
	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations/42/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token": "v1.tok", "expires_at": now.Add(time.Hour).Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		body := `{"data":{"repository":{"pullRequest":` + prJSON + `},` +
			`"rateLimit":{"cost":1,"remaining":` + strconv.Itoa(remaining) + `,` +
			`"resetAt":"` + now.Add(time.Hour).Format(time.RFC3339) + `"}}}`
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	return httptest.NewServer(mux)
}

func TestEnrichPullRequest(t *testing.T) {
	const pr = `{
	  "additions": 210, "deletions": 64, "changedFiles": 3,
	  "commits": { "nodes": [ {"commit":{"oid":"sha1"}}, {"commit":{"oid":"sha2"}} ] },
	  "files": { "nodes": [
	    {"path":"a.go","additions":100,"deletions":4},
	    {"path":"b.go","additions":110,"deletions":60}
	  ] }
	}`
	srv := graphqlServer(t, pr, 4990)
	defer srv.Close()
	c := newTestClient(t, srv)

	enr, err := c.EnrichPullRequest(context.Background(), 42, "acme/app", 482)
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if enr.Additions != 210 || enr.Deletions != 64 || enr.ChangedFiles != 3 {
		t.Errorf("counts: %+v", enr)
	}
	if len(enr.CommitOIDs) != 2 || enr.CommitOIDs[0] != "sha1" {
		t.Errorf("commit oids: %v", enr.CommitOIDs)
	}
	if len(enr.Files) != 2 || enr.Files[0].Path != "a.go" || enr.Files[1].Deletions != 60 {
		t.Errorf("files: %+v", enr.Files)
	}

	// The rateLimit block must have refreshed the GraphQL budget.
	if rem, known := c.Budget(42).Remaining(GraphQL); !known || rem != 4990 {
		t.Errorf("graphql budget not observed: rem=%d known=%v", rem, known)
	}
}

func TestEnrichPullRequestBadRepo(t *testing.T) {
	srv := graphqlServer(t, `{}`, 5000)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.EnrichPullRequest(context.Background(), 42, "no-slash", 1); err == nil {
		t.Fatal("expected error for malformed repo")
	}
}

func TestGraphQLErrors(t *testing.T) {
	now := time.Now()
	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations/42/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token": "v1.tok", "expires_at": now.Add(time.Hour).Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errors":[{"message":"Could not resolve to a Repository"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.EnrichPullRequest(context.Background(), 42, "acme/app", 1)
	var ge *GraphQLError
	if !errors.As(err, &ge) {
		t.Fatalf("expected GraphQLError, got %v", err)
	}
}

func TestGraphQLRateLimited(t *testing.T) {
	srv := graphqlServer(t, `{}`, 5000)
	defer srv.Close()
	c := newTestClient(t, srv)

	// Pre-exhaust the GraphQL pool.
	c.Budget(42).ObserveGraphQL(1, time.Now().Add(time.Hour))

	_, err := c.GraphQL(context.Background(), 42, prEnrichQuery,
		map[string]any{"owner": "acme", "name": "app", "number": 1}, 5)
	var rl *RateLimitedError
	if !errors.As(err, &rl) {
		t.Fatalf("expected RateLimitedError, got %v", err)
	}
	if rl.Resource != GraphQL {
		t.Errorf("wrong resource: %v", rl.Resource)
	}
}

func TestFetchPullRequestPatches(t *testing.T) {
	now := time.Now()
	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations/42/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token": "v1.tok", "expires_at": now.Add(time.Hour).Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/repos/acme/app/pulls/482/files", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "4998")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(now.Add(time.Hour).Unix(), 10))
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"filename": "a.go", "additions": 3, "deletions": 1, "patch": "@@ small @@"},
			{"filename": "big.go", "additions": 9, "deletions": 0, "patch": "0123456789ABCDEF"},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(t, srv)

	// maxBytes=12 → the 16-byte patch is dropped+truncated, the 11-byte one kept.
	files, err := c.FetchPullRequestPatches(context.Background(), 42, "acme/app", 482, 12)
	if err != nil {
		t.Fatalf("fetch patches: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("want 2 files, got %d", len(files))
	}
	if files[0].Patch != "@@ small @@" || files[0].Truncated {
		t.Errorf("small patch should be kept: %+v", files[0])
	}
	if files[1].Patch != "" || !files[1].Truncated {
		t.Errorf("oversized patch should be truncated: %+v", files[1])
	}
}

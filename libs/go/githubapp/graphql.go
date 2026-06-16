package githubapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GraphQLError aggregates errors GitHub returned in a GraphQL response body
// (HTTP 200 with a non-empty `errors` array — the GraphQL failure mode).
type GraphQLError struct{ Messages []string }

func (e *GraphQLError) Error() string {
	return "githubapp: graphql: " + strings.Join(e.Messages, "; ")
}

// GraphQL issues an installation-scoped GraphQL query after reserving GraphQL
// budget (a separate pool from REST — ratelimit.go). If the query selects a
// top-level `rateLimit { cost remaining resetAt }`, the authoritative cost feeds
// back into the budget via ObserveGraphQL. estCost is the pre-call point estimate
// used only for the optimistic reservation; GitHub computes the real cost.
//
// This is the single seam P1.C enrichment and P1.G backfill use for GraphQL.
func (c *Client) GraphQL(ctx context.Context, installationID int64, query string, variables map[string]any, estCost int) (json.RawMessage, error) {
	budget := c.budgets.For(installationID)
	if wait, ok := budget.Reserve(GraphQL, estCost); !ok {
		return nil, &RateLimitedError{Resource: GraphQL, RetryAfter: wait}
	}

	tok, err := c.tokens.Token(ctx, installationID)
	if err != nil {
		return nil, err
	}

	reqBody, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBase+"/graphql", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+tok.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("githubapp: graphql status %d: %s", resp.StatusCode, body)
	}

	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("githubapp: decode graphql: %w", err)
	}
	if len(env.Errors) > 0 {
		msgs := make([]string, len(env.Errors))
		for i, e := range env.Errors {
			msgs[i] = e.Message
		}
		return nil, &GraphQLError{Messages: msgs}
	}
	observeGraphQLRate(env.Data, budget)
	return env.Data, nil
}

// observeGraphQLRate refreshes the GraphQL budget from a `rateLimit` block in the
// response data, if the query selected one. Best-effort: absence is a no-op.
func observeGraphQLRate(data json.RawMessage, budget *Budget) {
	var d struct {
		RateLimit *struct {
			Remaining int       `json:"remaining"`
			ResetAt   time.Time `json:"resetAt"`
		} `json:"rateLimit"`
	}
	if err := json.Unmarshal(data, &d); err != nil || d.RateLimit == nil {
		return
	}
	budget.ObserveGraphQL(d.RateLimit.Remaining, d.RateLimit.ResetAt)
}

// FileChange is one file's churn in a PR. Patch is the unified diff, populated
// only when patch fetching is enabled (REST; GitHub's GraphQL API does not expose
// patch text). Truncated marks a patch dropped for exceeding the size cap.
type FileChange struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// PullRequestEnrichment is the detail the webhook lacks but the metrics need:
// authoritative size counts, the commit list (PR↔commit correlation, P1.E), and
// per-file churn (review-health hotspots; patch text feeds Phase-3 AI-11).
type PullRequestEnrichment struct {
	Additions    int          `json:"additions"`
	Deletions    int          `json:"deletions"`
	ChangedFiles int          `json:"changed_files"`
	CommitOIDs   []string     `json:"commit_oids"`
	Files        []FileChange `json:"files"`
}

// prEnrichQuery fetches PR size, commit OIDs, and per-file churn in one batched
// call. It selects rateLimit so the budget refreshes from the real cost.
const prEnrichQuery = `query($owner:String!,$name:String!,$number:Int!){
  repository(owner:$owner,name:$name){
    pullRequest(number:$number){
      additions deletions changedFiles
      commits(first:100){nodes{commit{oid}}}
      files(first:100){nodes{path additions deletions}}
    }
  }
  rateLimit{cost remaining resetAt}
}`

// EnrichPullRequest fetches the batched GraphQL detail for one PR. repo is
// "owner/name". The GraphQL API caps connections at 100 nodes/page; for the deep
// history of very large PRs, pagination is a later concern (backfill, P1.G).
func (c *Client) EnrichPullRequest(ctx context.Context, installationID int64, repo string, number int) (PullRequestEnrichment, error) {
	owner, name, ok := splitRepo(repo)
	if !ok {
		return PullRequestEnrichment{}, fmt.Errorf("githubapp: enrich: bad repo %q (want owner/name)", repo)
	}

	data, err := c.GraphQL(ctx, installationID, prEnrichQuery,
		map[string]any{"owner": owner, "name": name, "number": number}, 1)
	if err != nil {
		return PullRequestEnrichment{}, err
	}

	var d struct {
		Repository struct {
			PullRequest struct {
				Additions    int `json:"additions"`
				Deletions    int `json:"deletions"`
				ChangedFiles int `json:"changedFiles"`
				Commits      struct {
					Nodes []struct {
						Commit struct {
							OID string `json:"oid"`
						} `json:"commit"`
					} `json:"nodes"`
				} `json:"commits"`
				Files struct {
					Nodes []FileChange `json:"nodes"`
				} `json:"files"`
			} `json:"pullRequest"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return PullRequestEnrichment{}, fmt.Errorf("githubapp: decode enrichment: %w", err)
	}

	pr := d.Repository.PullRequest
	out := PullRequestEnrichment{
		Additions:    pr.Additions,
		Deletions:    pr.Deletions,
		ChangedFiles: pr.ChangedFiles,
		Files:        pr.Files.Nodes,
	}
	for _, n := range pr.Commits.Nodes {
		if n.Commit.OID != "" {
			out.CommitOIDs = append(out.CommitOIDs, n.Commit.OID)
		}
	}
	return out, nil
}

// FetchPullRequestPatches fills per-file patch text via the REST endpoint
// (GET /pulls/{n}/files) — GraphQL cannot return patches. Patches over maxBytes
// are dropped and marked Truncated so a giant diff can't bloat the event log.
// Off by default in the enricher (ENRICH_PATCHES); Phase-3 AI-11 turns it on.
func (c *Client) FetchPullRequestPatches(ctx context.Context, installationID int64, repo string, number, maxBytes int) ([]FileChange, error) {
	resp, err := c.get(ctx, installationID, fmt.Sprintf("/repos/%s/pulls/%d/files?per_page=100", repo, number))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("githubapp: pr files status %d: %s", resp.StatusCode, body)
	}
	var raw []struct {
		Filename  string `json:"filename"`
		Additions int    `json:"additions"`
		Deletions int    `json:"deletions"`
		Patch     string `json:"patch"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("githubapp: decode pr files: %w", err)
	}
	out := make([]FileChange, len(raw))
	for i, f := range raw {
		fc := FileChange{Path: f.Filename, Additions: f.Additions, Deletions: f.Deletions}
		if maxBytes > 0 && len(f.Patch) > maxBytes {
			fc.Truncated = true
		} else {
			fc.Patch = f.Patch
		}
		out[i] = fc
	}
	return out, nil
}

func splitRepo(repo string) (owner, name string, ok bool) {
	owner, name, ok = strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		return "", "", false
	}
	return owner, name, true
}

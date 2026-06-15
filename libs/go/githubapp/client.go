package githubapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// RateLimitedError is returned by Do when the installation's budget for the
// resource is exhausted; RetryAfter is how long until the window resets. Callers
// (backfill, enrichment) decide whether to wait or defer.
type RateLimitedError struct {
	Resource   Resource
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("githubapp: %s rate budget exhausted; retry in %s", e.Resource, e.RetryAfter)
}

// Client is the authenticated, rate-budgeted GitHub API client. Every call is
// scoped to an installation: it injects that installation's access token and
// debits/refreshes its rate budget. This is the single seam all GitHub API
// access (P1.C enrichment, P1.G backfill) goes through.
type Client struct {
	tokens  *TokenSource
	budgets *Registry
	http    *http.Client
	apiBase string
}

// NewClient wires a Client over a TokenSource and a budget Registry. It inherits
// the token source's API base + HTTP client so both halves talk to the same
// endpoint (real GitHub, GHES, or a test server).
func NewClient(tokens *TokenSource, budgets *Registry) *Client {
	return &Client{
		tokens:  tokens,
		budgets: budgets,
		http:    tokens.http,
		apiBase: tokens.apiBase,
	}
}

// Budget returns the installation's rate budget — for health metrics/alerts
// (FR-2.8) and for callers that want to check headroom before a batch.
func (c *Client) Budget(installationID int64) *Budget { return c.budgets.For(installationID) }

// Do issues an installation-scoped REST request after reserving rate budget. It
// injects the token, sends the request, and feeds the response's rate headers
// back into the budget. cost is the REST point cost (1 for a normal call).
func (c *Client) Do(ctx context.Context, installationID int64, req *http.Request, cost int) (*http.Response, error) {
	budget := c.budgets.For(installationID)
	if wait, ok := budget.Reserve(REST, cost); !ok {
		return nil, &RateLimitedError{Resource: REST, RetryAfter: wait}
	}

	tok, err := c.tokens.Token(ctx, installationID)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+tok.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	budget.Observe(resp.Header) // refresh the budget from authoritative headers
	return resp, nil
}

// getJSON does a GET against the installation and decodes a JSON array length /
// body. Returns the parsed response and the HTTP status.
func (c *Client) get(ctx context.Context, installationID int64, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+path, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(ctx, installationID, req, 1)
}

// Capabilities is the per-repo presence of capability-gated signals (FR-2.10).
type Capabilities struct {
	Deployments bool
	Releases    bool
}

// DetectRepoCapabilities probes whether a repo actually emits deployments and
// releases — the capability-gated signals that gate DORA/deploy metrics
// (GITHUB-APP.md §3). "Capable" means at least one such object exists; an empty
// list means the signal is absent for this repo, so the dependent metric stays
// dark (never shown as a shaky zero). repo is "owner/name".
func (c *Client) DetectRepoCapabilities(ctx context.Context, installationID int64, repo string) (Capabilities, error) {
	deployments, err := c.hasAny(ctx, installationID, "/repos/"+repo+"/deployments?per_page=1")
	if err != nil {
		return Capabilities{}, fmt.Errorf("detect deployments for %s: %w", repo, err)
	}
	releases, err := c.hasAny(ctx, installationID, "/repos/"+repo+"/releases?per_page=1")
	if err != nil {
		return Capabilities{}, fmt.Errorf("detect releases for %s: %w", repo, err)
	}
	return Capabilities{Deployments: deployments, Releases: releases}, nil
}

// hasAny reports whether a list endpoint returns at least one element.
func (c *Client) hasAny(ctx context.Context, installationID int64, path string) (bool, error) {
	resp, err := c.get(ctx, installationID, path)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(body, &items); err != nil {
		return false, fmt.Errorf("decode list: %w", err)
	}
	return len(items) > 0, nil
}

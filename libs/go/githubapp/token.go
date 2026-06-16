package githubapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// defaultAPIBase is GitHub's REST root; overridable for GHES/tests.
const defaultAPIBase = "https://api.github.com"

// expirySkew refreshes a token this long before it actually expires, so an
// in-flight request never races the expiry boundary.
const expirySkew = 60 * time.Second

// InstallationToken is a short-lived access token scoped to one installation.
type InstallationToken struct {
	Token       string
	ExpiresAt   time.Time
	Permissions map[string]string // granted permission → access level (read)
}

func (t InstallationToken) valid(now time.Time) bool {
	return t.Token != "" && now.Before(t.ExpiresAt.Add(-expirySkew))
}

// TokenSource mints and caches installation access tokens. One per process;
// safe for concurrent use. It holds the app credentials and exchanges the app
// JWT for per-installation tokens on demand, caching until near expiry.
type TokenSource struct {
	creds   *AppCredentials
	http    *http.Client
	apiBase string
	now     func() time.Time // injectable clock for tests

	mu     sync.Mutex
	cache  map[int64]InstallationToken
	single map[int64]*sync.Mutex // per-installation lock to coalesce refreshes
}

// Option configures a TokenSource.
type Option func(*TokenSource)

// WithHTTPClient overrides the HTTP client (timeouts, test transport).
func WithHTTPClient(c *http.Client) Option { return func(ts *TokenSource) { ts.http = c } }

// WithAPIBase overrides the REST root (GitHub Enterprise Server or a test server).
func WithAPIBase(base string) Option { return func(ts *TokenSource) { ts.apiBase = base } }

// WithClock overrides the clock (tests).
func WithClock(now func() time.Time) Option { return func(ts *TokenSource) { ts.now = now } }

// NewTokenSource builds a TokenSource for the given app credentials.
func NewTokenSource(creds *AppCredentials, opts ...Option) *TokenSource {
	ts := &TokenSource{
		creds:   creds,
		http:    &http.Client{Timeout: 15 * time.Second},
		apiBase: defaultAPIBase,
		now:     time.Now,
		cache:   make(map[int64]InstallationToken),
		single:  make(map[int64]*sync.Mutex),
	}
	for _, o := range opts {
		o(ts)
	}
	return ts
}

// Token returns a valid installation token for installationID, minting (and
// caching) a fresh one if none is cached or the cached one is near expiry.
// Concurrent callers for the same installation coalesce onto one refresh.
func (ts *TokenSource) Token(ctx context.Context, installationID int64) (InstallationToken, error) {
	if tok, ok := ts.cached(installationID); ok {
		return tok, nil
	}

	// Serialize refreshes per installation so a burst mints one token, not N.
	lock := ts.installLock(installationID)
	lock.Lock()
	defer lock.Unlock()

	// Re-check: another goroutine may have refreshed while we waited.
	if tok, ok := ts.cached(installationID); ok {
		return tok, nil
	}

	tok, err := ts.mint(ctx, installationID)
	if err != nil {
		return InstallationToken{}, err
	}
	ts.mu.Lock()
	ts.cache[installationID] = tok
	ts.mu.Unlock()
	return tok, nil
}

func (ts *TokenSource) cached(installationID int64) (InstallationToken, bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	tok, ok := ts.cache[installationID]
	if ok && tok.valid(ts.now()) {
		return tok, true
	}
	return InstallationToken{}, false
}

func (ts *TokenSource) installLock(installationID int64) *sync.Mutex {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	lock, ok := ts.single[installationID]
	if !ok {
		lock = &sync.Mutex{}
		ts.single[installationID] = lock
	}
	return lock
}

// Installation identifies where the App is installed (account + installation id).
type Installation struct {
	ID      int64
	Account string
}

// ListInstallations returns every installation of this App, via
// GET /app/installations (an app-JWT call — no installation token needed). It's
// how a freshly registered App discovers its installation ids.
func (ts *TokenSource) ListInstallations(ctx context.Context) ([]Installation, error) {
	jwt, err := ts.creds.AppJWT(ts.now(), 10*time.Minute)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.apiBase+"/app/installations?per_page=100", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := ts.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("githubapp: list installations: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("githubapp: list installations: status %d: %s", resp.StatusCode, body)
	}
	var raw []struct {
		ID      int64 `json:"id"`
		Account struct {
			Login string `json:"login"`
		} `json:"account"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("githubapp: decode installations: %w", err)
	}
	out := make([]Installation, len(raw))
	for i, r := range raw {
		out[i] = Installation{ID: r.ID, Account: r.Account.Login}
	}
	return out, nil
}

// mint exchanges a fresh app JWT for an installation token via
// POST /app/installations/{id}/access_tokens.
func (ts *TokenSource) mint(ctx context.Context, installationID int64) (InstallationToken, error) {
	jwt, err := ts.creds.AppJWT(ts.now(), 10*time.Minute)
	if err != nil {
		return InstallationToken{}, err
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", ts.apiBase, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return InstallationToken{}, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := ts.http.Do(req)
	if err != nil {
		return InstallationToken{}, fmt.Errorf("githubapp: mint token: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != http.StatusCreated {
		return InstallationToken{}, fmt.Errorf("githubapp: mint token: status %d: %s", resp.StatusCode, body)
	}

	var out struct {
		Token       string            `json:"token"`
		ExpiresAt   time.Time         `json:"expires_at"`
		Permissions map[string]string `json:"permissions"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return InstallationToken{}, fmt.Errorf("githubapp: decode token response: %w", err)
	}
	if out.Token == "" {
		return InstallationToken{}, fmt.Errorf("githubapp: empty token in response")
	}
	return InstallationToken{
		Token:       out.Token,
		ExpiresAt:   out.ExpiresAt,
		Permissions: out.Permissions,
	}, nil
}

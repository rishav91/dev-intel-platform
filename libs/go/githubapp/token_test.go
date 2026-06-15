package githubapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// tokenServer returns a stub that mints installation tokens, counting calls.
func tokenServer(t *testing.T, mints *int32, expiresIn time.Duration) *httptest.Server {
	t.Helper()
	now := time.Now()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/app/installations/42/access_tokens" {
			n := atomic.AddInt32(mints, 1)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token":      fmt.Sprintf("v1.token-%d", n),
				"expires_at": now.Add(expiresIn).Format(time.RFC3339),
				"permissions": map[string]string{
					"pull_requests": "read", "checks": "read",
				},
			})
			return
		}
		http.Error(w, "unexpected", http.StatusBadRequest)
	}))
}

func TestTokenCachingAndRefresh(t *testing.T) {
	_, pemBytes := testKeyPEM(t)
	creds, _ := NewAppCredentials(7, pemBytes)

	var mints int32
	srv := tokenServer(t, &mints, time.Hour)
	defer srv.Close()

	clock := time.Now()
	ts := NewTokenSource(creds,
		WithAPIBase(srv.URL),
		WithHTTPClient(srv.Client()),
		WithClock(func() time.Time { return clock }),
	)
	ctx := context.Background()

	// First two calls share one mint (cache hit on the second).
	t1, err := ts.Token(ctx, 42)
	if err != nil {
		t.Fatalf("token 1: %v", err)
	}
	t2, _ := ts.Token(ctx, 42)
	if t1.Token != t2.Token {
		t.Errorf("expected cached token, got %q then %q", t1.Token, t2.Token)
	}
	if got := atomic.LoadInt32(&mints); got != 1 {
		t.Fatalf("expected 1 mint, got %d", got)
	}
	if t1.Permissions["pull_requests"] != "read" {
		t.Errorf("permissions not parsed: %v", t1.Permissions)
	}

	// Advance past expiry (minus skew) → a refresh mints a new token.
	clock = clock.Add(time.Hour) // > 1h-60s skew
	t3, err := ts.Token(ctx, 42)
	if err != nil {
		t.Fatalf("token 3: %v", err)
	}
	if t3.Token == t1.Token {
		t.Errorf("expected refreshed token, still %q", t3.Token)
	}
	if got := atomic.LoadInt32(&mints); got != 2 {
		t.Fatalf("expected 2 mints after expiry, got %d", got)
	}
}

func TestListInstallations(t *testing.T) {
	_, pemBytes := testKeyPEM(t)
	creds, _ := NewAppCredentials(7, pemBytes)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations" {
			http.Error(w, "unexpected", http.StatusNotFound)
			return
		}
		if auth := r.Header.Get("Authorization"); auth == "" || auth[:7] != "Bearer " {
			t.Errorf("expected app JWT bearer auth, got %q", auth)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": 4242, "account": map[string]any{"login": "acme"}},
			{"id": 8484, "account": map[string]any{"login": "globex"}},
		})
	}))
	defer srv.Close()

	ts := NewTokenSource(creds, WithAPIBase(srv.URL), WithHTTPClient(srv.Client()))
	installs, err := ts.ListInstallations(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(installs) != 2 || installs[0].ID != 4242 || installs[0].Account != "acme" {
		t.Fatalf("unexpected installations: %+v", installs)
	}
}

func TestTokenDistinctInstallations(t *testing.T) {
	_, pemBytes := testKeyPEM(t)
	creds, _ := NewAppCredentials(7, pemBytes)

	now := time.Now()
	var mints int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&mints, 1)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "v1." + r.URL.Path,
			"expires_at": now.Add(time.Hour).Format(time.RFC3339),
		})
	}))
	defer srv.Close()

	ts := NewTokenSource(creds, WithAPIBase(srv.URL), WithHTTPClient(srv.Client()))
	ctx := context.Background()

	a, _ := ts.Token(ctx, 1)
	b, _ := ts.Token(ctx, 2)
	if a.Token == b.Token {
		t.Error("different installations must get different tokens")
	}
	if got := atomic.LoadInt32(&mints); got != 2 {
		t.Errorf("expected 2 mints for 2 installations, got %d", got)
	}
}

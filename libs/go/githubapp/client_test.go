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

// apiServer stubs token minting + repo list endpoints with rate-limit headers.
func apiServer(t *testing.T, deployments, releases []json.RawMessage) *httptest.Server {
	t.Helper()
	now := time.Now()
	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations/42/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token": "v1.tok", "expires_at": now.Add(time.Hour).Format(time.RFC3339),
		})
	})
	list := func(items []json.RawMessage) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-RateLimit-Remaining", "4999")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(now.Add(time.Hour).Unix(), 10))
			_ = json.NewEncoder(w).Encode(items)
		}
	}
	mux.HandleFunc("/repos/acme/app/deployments", list(deployments))
	mux.HandleFunc("/repos/acme/app/releases", list(releases))
	return httptest.NewServer(mux)
}

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	_, pemBytes := testKeyPEM(t)
	creds, _ := NewAppCredentials(7, pemBytes)
	ts := NewTokenSource(creds, WithAPIBase(srv.URL), WithHTTPClient(srv.Client()))
	return NewClient(ts, NewRegistry())
}

func TestDetectRepoCapabilities(t *testing.T) {
	cases := []struct {
		name        string
		deployments []json.RawMessage
		releases    []json.RawMessage
		wantDeploy  bool
		wantRelease bool
	}{
		{"both present", []json.RawMessage{[]byte(`{}`)}, []json.RawMessage{[]byte(`{}`)}, true, true},
		{"neither", []json.RawMessage{}, []json.RawMessage{}, false, false},
		{"only deployments", []json.RawMessage{[]byte(`{}`)}, []json.RawMessage{}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := apiServer(t, tc.deployments, tc.releases)
			defer srv.Close()
			c := newTestClient(t, srv)

			caps, err := c.DetectRepoCapabilities(context.Background(), 42, "acme/app")
			if err != nil {
				t.Fatalf("detect: %v", err)
			}
			if caps.Deployments != tc.wantDeploy || caps.Releases != tc.wantRelease {
				t.Errorf("got %+v, want deploy=%v release=%v", caps, tc.wantDeploy, tc.wantRelease)
			}
		})
	}
}

func TestClientObservesBudget(t *testing.T) {
	srv := apiServer(t, []json.RawMessage{[]byte(`{}`)}, []json.RawMessage{})
	defer srv.Close()

	_, pemBytes := testKeyPEM(t)
	creds, _ := NewAppCredentials(7, pemBytes)
	ts := NewTokenSource(creds, WithAPIBase(srv.URL), WithHTTPClient(srv.Client()))
	reg := NewRegistry()
	c := NewClient(ts, reg)

	if _, err := c.DetectRepoCapabilities(context.Background(), 42, "acme/app"); err != nil {
		t.Fatalf("detect: %v", err)
	}
	// The response's rate headers must have been fed back into the budget.
	if rem, known := reg.For(42).Remaining(REST); !known || rem != 4999 {
		t.Errorf("budget not observed from headers: rem=%d known=%v", rem, known)
	}
}

func TestClientRateLimited(t *testing.T) {
	srv := apiServer(t, []json.RawMessage{}, []json.RawMessage{})
	defer srv.Close()

	_, pemBytes := testKeyPEM(t)
	creds, _ := NewAppCredentials(7, pemBytes)
	ts := NewTokenSource(creds, WithAPIBase(srv.URL), WithHTTPClient(srv.Client()))
	reg := NewRegistry()
	c := NewClient(ts, reg)

	// Pre-exhaust the installation's REST budget.
	b := reg.For(42)
	h := http.Header{}
	h.Set("X-RateLimit-Remaining", "1")
	h.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
	b.Observe(h)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/repos/acme/app/releases", nil)
	_, err := c.Do(context.Background(), 42, req, 1)
	var rl *RateLimitedError
	if !errors.As(err, &rl) {
		t.Fatalf("expected RateLimitedError, got %v", err)
	}
	if rl.Resource != REST {
		t.Errorf("wrong resource: %v", rl.Resource)
	}
}

//go:build integration

// Live integration test against the real GitHub API. Opt-in: it runs only under
// the `integration` build tag AND when real credentials are present, so the
// hermetic unit tests (jwt/token/budget/client *_test.go) remain the default
// gate. Run with:
//
//	GITHUB_APP_ID=... GITHUB_APP_PRIVATE_KEY_PATH=... \
//	GITHUB_TEST_INSTALLATION_ID=... GITHUB_TEST_REPO=owner/name \
//	go test -tags=integration ./libs/go/githubapp/ -run Live -v
package githubapp

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestLiveInstallationAuthAndCapabilities(t *testing.T) {
	instStr := os.Getenv("GITHUB_TEST_INSTALLATION_ID")
	repo := os.Getenv("GITHUB_TEST_REPO")
	if instStr == "" || repo == "" || os.Getenv("GITHUB_APP_ID") == "" {
		t.Skip("set GITHUB_APP_ID, GITHUB_APP_PRIVATE_KEY_PATH, GITHUB_TEST_INSTALLATION_ID, GITHUB_TEST_REPO")
	}
	installation, err := strconv.ParseInt(instStr, 10, 64)
	if err != nil {
		t.Fatalf("GITHUB_TEST_INSTALLATION_ID: %v", err)
	}

	tokens, client, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Real installation token: must be non-empty, future-dated, with permissions.
	tok, err := tokens.Token(ctx, installation)
	if err != nil {
		t.Fatalf("mint installation token: %v", err)
	}
	if tok.Token == "" {
		t.Fatal("empty token")
	}
	if !tok.ExpiresAt.After(time.Now()) {
		t.Fatalf("token already expired at %s", tok.ExpiresAt)
	}
	t.Logf("token ok, expires %s, permissions=%v", tok.ExpiresAt.Format(time.RFC3339), tok.Permissions)

	// A second call must be served from cache (no new mint) — same token value.
	tok2, _ := tokens.Token(ctx, installation)
	if tok2.Token != tok.Token {
		t.Error("expected cached token on second call")
	}

	// Real capability probe against a real repo: must not error, and the response
	// headers must have populated the rate budget.
	caps, err := client.DetectRepoCapabilities(ctx, installation, repo)
	if err != nil {
		t.Fatalf("detect capabilities: %v", err)
	}
	t.Logf("capabilities for %s: deployments=%v releases=%v", repo, caps.Deployments, caps.Releases)

	if rem, known := client.Budget(installation).Remaining(REST); !known || rem <= 0 {
		t.Errorf("expected REST budget observed from live headers, got rem=%d known=%v", rem, known)
	}
}

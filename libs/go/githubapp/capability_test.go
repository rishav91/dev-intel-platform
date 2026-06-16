package githubapp

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dev-intel/platform/libs/go/tenancy"
)

// Exercises PersistRepoCapability against Postgres (RLS upsert). Skipped without
// POSTGRES_DSN, like the red-team test. Uses the seeded tenant A.
func TestPersistRepoCapability(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set; skipping integration test")
	}
	const tenantA = "11111111-1111-1111-1111-111111111111"
	repo := "acme/cap-" + time.Now().Format("150405.000000")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	store, err := tenancy.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer store.Close()

	// First detection: deployments only.
	if err := PersistRepoCapability(ctx, store, tenantA, repo, Capabilities{Deployments: true}); err != nil {
		t.Fatalf("persist 1: %v", err)
	}
	if d, r := readCap(t, ctx, store, tenantA, repo); !d || r {
		t.Fatalf("after first persist: deploy=%v release=%v, want true/false", d, r)
	}

	// Re-detection upserts (no duplicate row), now releases too.
	if err := PersistRepoCapability(ctx, store, tenantA, repo, Capabilities{Deployments: true, Releases: true}); err != nil {
		t.Fatalf("persist 2: %v", err)
	}
	if d, r := readCap(t, ctx, store, tenantA, repo); !d || !r {
		t.Fatalf("after upsert: deploy=%v release=%v, want true/true", d, r)
	}
	if n := countCap(t, ctx, store, tenantA, repo); n != 1 {
		t.Fatalf("expected exactly 1 row after upsert, got %d", n)
	}
}

func readCap(t *testing.T, ctx context.Context, store *tenancy.Store, tenant, repo string) (deploy, release bool) {
	t.Helper()
	_ = store.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT deployments, releases FROM repo_capability WHERE repo = $1`, repo).Scan(&deploy, &release)
	})
	return
}

func countCap(t *testing.T, ctx context.Context, store *tenancy.Store, tenant, repo string) int {
	t.Helper()
	var n int
	_ = store.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM repo_capability WHERE repo = $1`, repo).Scan(&n)
	})
	return n
}

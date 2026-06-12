// Red-team: tenant isolation must hold at the database layer (RLS), independent
// of any application filter. This test is the gate that locks isolation in early
// (REPO-LAYOUT suggested command #3). It is skipped unless POSTGRES_DSN is set.
//
//	POSTGRES_DSN=postgres://devintel:devintel@localhost:5432/devintel go test ./tests/red-team/...
package redteam

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dev-intel/platform/libs/go/tenancy"
)

const (
	tenantA = "11111111-1111-1111-1111-111111111111"
	tenantB = "22222222-2222-2222-2222-222222222222"
)

func TestTenantIsolation(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	store, err := tenancy.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer store.Close()

	// Insert one work item per tenant, each within its own RLS scope.
	insert(t, ctx, store, tenantA, "acme/app", "PR_A")
	insert(t, ctx, store, tenantB, "globex/app", "PR_B")

	// 1) Within tenant A's scope, tenant B's row must be invisible.
	if got := countNode(t, ctx, store, tenantA, "PR_B"); got != 0 {
		t.Fatalf("LEAK: tenant A can see tenant B's row (count=%d)", got)
	}
	// ...and A's own row visible.
	if got := countNode(t, ctx, store, tenantA, "PR_A"); got != 1 {
		t.Fatalf("tenant A cannot see its own row (count=%d)", got)
	}

	// 2) WITH CHECK must block writing another tenant's id while scoped to A.
	err = store.WithTenant(ctx, tenantA, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
INSERT INTO work_item (tenant_id, type, repo, node_id, created_at)
VALUES ($1, 'pr', 'evil/repo', 'PR_EVIL', now())`, tenantB)
		return e
	})
	if err == nil {
		t.Fatalf("LEAK: writing tenant B's row while scoped to tenant A was allowed")
	}
}

func insert(t *testing.T, ctx context.Context, store *tenancy.Store, tenant, repo, node string) {
	t.Helper()
	err := store.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
INSERT INTO work_item (tenant_id, type, repo, node_id, title, status, current_stage, created_at)
VALUES ($1,'pr',$2,$3,'t','open','open', now())
ON CONFLICT (tenant_id, repo, type, node_id) DO NOTHING`, tenant, repo, node)
		return e
	})
	if err != nil {
		t.Fatalf("insert %s/%s: %v", repo, node, err)
	}
}

func countNode(t *testing.T, ctx context.Context, store *tenancy.Store, tenant, node string) int {
	t.Helper()
	var n int
	err := store.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM work_item WHERE node_id = $1`, node).Scan(&n)
	})
	if err != nil {
		t.Fatalf("count %s: %v", node, err)
	}
	return n
}

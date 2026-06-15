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

	"github.com/google/uuid"
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

// TestTenantIsolationWriteModel runs the same RLS red-team checks across every
// P1.A write-model table (FR-3.1): a row written under tenant A must be invisible
// to tenant B, and WITH CHECK must block writing tenant B's id while scoped to A.
// Each case supplies a minimal insert ($1=tenant_id, $2=marker) and a count by
// marker, so adding a table here is one line.
func TestTenantIsolationWriteModel(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	store, err := tenancy.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer store.Close()

	cases := []struct {
		table  string
		insert string // $1=tenant_id, $2=marker
		count  string // $1=marker
	}{
		{"review",
			`INSERT INTO review (tenant_id, work_item_id, reviewer_id, source_id, state, submitted_at)
			 VALUES ($1, $2::uuid, gen_random_uuid(), $2, 'approved', now())`,
			`SELECT count(*) FROM review WHERE source_id = $1`},
		{"check_run",
			`INSERT INTO check_run (tenant_id, source_id, head_sha, name)
			 VALUES ($1, $2, 'deadbeef', 'build')`,
			`SELECT count(*) FROM check_run WHERE source_id = $1`},
		{"contributor",
			`INSERT INTO contributor (tenant_id, display_name) VALUES ($1, $2)`,
			`SELECT count(*) FROM contributor WHERE display_name = $1`},
		{"identity_link",
			`INSERT INTO identity_link (tenant_id, contributor_id, identifier_kind, identifier_value_hash, confidence)
			 VALUES ($1, gen_random_uuid(), 'github_login', $2, 1.0)`,
			`SELECT count(*) FROM identity_link WHERE identifier_value_hash = $1`},
		{"state_transition",
			`INSERT INTO state_transition (tenant_id, work_item_id, to_stage, occurred_at)
			 VALUES ($1, $2::uuid, 'open', now())`,
			`SELECT count(*) FROM state_transition WHERE work_item_id = $1::uuid`},
		{"entity_edge",
			`INSERT INTO entity_edge (tenant_id, src_id, dst_kind, dst_id, relation, confidence)
			 VALUES ($1, $2::uuid, 'work_item', gen_random_uuid(), 'closes', 1.0)`,
			`SELECT count(*) FROM entity_edge WHERE src_id = $1::uuid`},
		{"repo_capability",
			`INSERT INTO repo_capability (tenant_id, repo, deployments) VALUES ($1, $2, true)`,
			`SELECT count(*) FROM repo_capability WHERE repo = $1`},
	}

	for _, tc := range cases {
		t.Run(tc.table, func(t *testing.T) {
			marker := uuid.NewString() // unique per run; valid for uuid or text columns

			// Write the row inside tenant A's scope.
			if err := store.WithTenant(ctx, tenantA, func(ctx context.Context, tx pgx.Tx) error {
				_, e := tx.Exec(ctx, tc.insert, tenantA, marker)
				return e
			}); err != nil {
				t.Fatalf("%s: insert under tenant A: %v", tc.table, err)
			}

			// Tenant B must not see it.
			if got := countMarker(t, ctx, store, tenantB, tc.count, marker); got != 0 {
				t.Fatalf("%s LEAK: tenant B sees tenant A's row (count=%d)", tc.table, got)
			}
			// Tenant A must see its own.
			if got := countMarker(t, ctx, store, tenantA, tc.count, marker); got != 1 {
				t.Fatalf("%s: tenant A cannot see its own row (count=%d)", tc.table, got)
			}

			// WITH CHECK: writing tenant B's id while scoped to A must fail.
			err := store.WithTenant(ctx, tenantA, func(ctx context.Context, tx pgx.Tx) error {
				_, e := tx.Exec(ctx, tc.insert, tenantB, uuid.NewString())
				return e
			})
			if err == nil {
				t.Fatalf("%s LEAK: WITH CHECK allowed writing tenant B's row while scoped to A", tc.table)
			}
		})
	}
}

func countMarker(t *testing.T, ctx context.Context, store *tenancy.Store, tenant, countSQL, marker string) int {
	t.Helper()
	var n int
	err := store.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, countSQL, marker).Scan(&n)
	})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
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

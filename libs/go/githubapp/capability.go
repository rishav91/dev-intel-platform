package githubapp

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/dev-intel/platform/libs/go/tenancy"
)

// PersistRepoCapability upserts a repo's detected capabilities under the tenant's
// RLS scope (FR-2.10). Idempotent: re-detecting a repo updates the flags and the
// detection timestamp rather than inserting a duplicate. These flags gate
// DORA/deploy metrics downstream (GITHUB-APP.md §3).
func PersistRepoCapability(ctx context.Context, store *tenancy.Store, tenantID, repo string, caps Capabilities) error {
	return store.WithTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
INSERT INTO repo_capability (tenant_id, repo, deployments, releases, detected_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (tenant_id, repo) DO UPDATE SET
  deployments = EXCLUDED.deployments,
  releases    = EXCLUDED.releases,
  detected_at = now()`,
			tenantID, repo, caps.Deployments, caps.Releases)
		return err
	})
}

// Package tenancy centralizes tenant resolution and tenant-scoped DB access.
// Every tenant-scoped write/read MUST go through WithTenant so the Postgres
// session variable app.tenant_id is set and RLS applies. This makes "forgetting
// the tenant filter" structurally impossible — the point of doing it in one lib.
package tenancy

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrUnknownInstallation is returned when no tenant maps to an installation id.
var ErrUnknownInstallation = errors.New("tenancy: unknown github installation")

// Store wraps a pgx pool and provides tenant-aware access.
type Store struct{ pool *pgxpool.Pool }

// New connects a Store. dsn example:
//
//	postgres://devintel:devintel@localhost:5432/devintel
func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("tenancy: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("tenancy: ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

// ResolveTenant maps a GitHub App installation id to a tenant id.
// Control-plane lookup — github_installation has no RLS.
func (s *Store) ResolveTenant(ctx context.Context, installationID int64) (string, error) {
	var tenantID string
	err := s.pool.QueryRow(ctx,
		`SELECT tenant_id FROM github_installation WHERE installation_id = $1`,
		installationID,
	).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUnknownInstallation
	}
	if err != nil {
		return "", fmt.Errorf("tenancy: resolve tenant: %w", err)
	}
	return tenantID, nil
}

// WithTenant runs fn inside a transaction with app.tenant_id set to tenantID,
// so RLS scopes every statement fn issues. The setting is transaction-local
// (set_config(..., is_local=true)), so it cannot leak across pooled connections.
func (s *Store) WithTenant(ctx context.Context, tenantID string, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("tenancy: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID); err != nil {
		return fmt.Errorf("tenancy: set tenant: %w", err)
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("tenancy: commit: %w", err)
	}
	return nil
}

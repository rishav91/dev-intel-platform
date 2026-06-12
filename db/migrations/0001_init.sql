-- Phase 0 schema: control-plane (tenant, github_installation) + write model (work_item) with RLS.
-- Applied automatically by the citus image on first boot (docker-entrypoint-initdb.d).

-- gen_random_uuid() is built-in on PG13+; pgcrypto kept for portability.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ---------------------------------------------------------------------------
-- Control-plane tables (no RLS: services read these to resolve tenant context).
-- ---------------------------------------------------------------------------
CREATE TABLE tenant (
  tenant_id   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name        text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE github_installation (
  installation_id bigint PRIMARY KEY,            -- GitHub App installation id
  tenant_id       uuid   NOT NULL REFERENCES tenant(tenant_id),
  created_at      timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Write model: canonical work item (Phase 0 subset; full schema in DATA-MODEL.md).
-- In Phase 2 this becomes a Citus distributed table sharded by tenant_id.
-- ---------------------------------------------------------------------------
CREATE TABLE work_item (
  tenant_id     uuid NOT NULL,
  work_item_id  uuid NOT NULL DEFAULT gen_random_uuid(),
  type          text NOT NULL CHECK (type IN ('pr','issue','commit')),
  repo          text NOT NULL,
  node_id       text NOT NULL,
  number        int,
  title         text,
  status        text,                            -- open | closed | merged
  current_stage text,                            -- open | in_review | merged | closed
  author_login  text,
  changed_files int,
  additions     int,
  deletions     int,
  created_at    timestamptz NOT NULL,
  merged_at     timestamptz,
  closed_at     timestamptz,
  updated_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, work_item_id),
  UNIQUE (tenant_id, repo, type, node_id)        -- idempotency / dedup
);

-- ---------------------------------------------------------------------------
-- Row-level security: the tenant-isolation backstop.
-- FORCE so even the table owner is constrained. NOTE: FORCE still does NOT apply to
-- SUPERUSER or BYPASSRLS roles — `devintel` (the Citus image's POSTGRES_USER) is a
-- superuser, so the runtime/tests must connect as the least-privilege `devintel_app`
-- role (0003_app_role.sql), not as `devintel`, or RLS is bypassed. The app sets
-- `app.tenant_id` per transaction; if unset, the policy evaluates against NULL and
-- returns zero rows (default-deny).
-- ---------------------------------------------------------------------------
ALTER TABLE work_item ENABLE ROW LEVEL SECURITY;
ALTER TABLE work_item FORCE  ROW LEVEL SECURITY;

CREATE POLICY work_item_tenant_isolation ON work_item
  USING      (tenant_id = current_setting('app.tenant_id', true)::uuid)
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

CREATE INDEX work_item_stage_idx ON work_item (tenant_id, type, current_stage);

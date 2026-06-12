-- Phase 0 isolation fix: a dedicated, least-privilege APPLICATION role.
--
-- Why this exists: RLS (incl. FORCE) is *bypassed* by superusers and by any role
-- with BYPASSRLS. The POSTGRES_USER (`devintel`) created by the Citus image is a
-- SUPERUSER, so connecting the app/tests as `devintel` makes the RLS backstop a
-- no-op and tenant rows leak across scopes. The fix (ADR-004): the runtime and the
-- isolation test connect as `devintel_app`, which is NOSUPERUSER + NOBYPASSRLS, so
-- the per-row policies on work_item actually engage. `devintel` remains the
-- migration/admin owner only.
--
-- Applied by the citus image on first boot (runs after 0001/0002, alphabetical).

CREATE ROLE devintel_app
  LOGIN PASSWORD 'devintel_app'
  NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOINHERIT;

GRANT CONNECT ON DATABASE devintel TO devintel_app;
GRANT USAGE  ON SCHEMA   public    TO devintel_app;

-- Control-plane tables (no RLS): the app reads these to resolve tenant context.
GRANT SELECT ON tenant, github_installation TO devintel_app;

-- Write model (RLS enforced): full DML, but every row is gated by the
-- work_item_tenant_isolation policy keyed on current_setting('app.tenant_id').
GRANT SELECT, INSERT, UPDATE, DELETE ON work_item TO devintel_app;

-- Future Phase-1 tables created by `devintel` inherit the same grants automatically,
-- so each new migration doesn't have to remember to grant the app role.
ALTER DEFAULT PRIVILEGES FOR ROLE devintel IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO devintel_app;
ALTER DEFAULT PRIVILEGES FOR ROLE devintel IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO devintel_app;

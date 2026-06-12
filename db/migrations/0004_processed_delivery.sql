-- M0.5 hardening: explicit webhook idempotency (FR-2.2, ADR-010).
--
-- The work_item upsert already absorbs redeliveries, but "absorbed by an upsert"
-- is not the same as a *provable* no-op: a redelivery would still re-run the
-- write and re-emit a canonical event. This table records each delivery the
-- normalizer has processed so a redelivery short-circuits to a true no-op (no
-- re-emit). The row is written in the SAME transaction as the work_item, so the
-- dedup mark and the effect commit atomically (exactly-once effect under
-- at-least-once delivery).
--
-- Tenant-scoped with RLS (FORCE), like work_item, so the same default-deny
-- backstop applies. Inserted only inside tenancy.WithTenant (app.tenant_id set).

CREATE TABLE processed_delivery (
  tenant_id    uuid NOT NULL,
  delivery_id  text NOT NULL,            -- X-GitHub-Delivery (globally unique UUID)
  event_type   text,                     -- X-GitHub-Event, for audit/debug
  processed_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, delivery_id)
);

ALTER TABLE processed_delivery ENABLE ROW LEVEL SECURITY;
ALTER TABLE processed_delivery FORCE  ROW LEVEL SECURITY;

CREATE POLICY processed_delivery_tenant_isolation ON processed_delivery
  USING      (tenant_id = current_setting('app.tenant_id', true)::uuid)
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

-- DML grant for the app role is covered by ALTER DEFAULT PRIVILEGES in 0003.

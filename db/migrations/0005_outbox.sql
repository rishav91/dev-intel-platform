-- M0.5 hardening: transactional outbox for canonical event emission (ADR-012).
--
-- Problem it solves: the normalizer used to write the work_item (committed) and
-- THEN publish the canonical event to Kafka — two side effects that aren't atomic.
-- A crash in between, combined with delivery-id dedup, could lose the emit (the
-- redelivery sees the delivery already processed and skips it). The outbox closes
-- that window: the canonical event is INSERTed into this table in the SAME
-- transaction as the work_item + the processed_delivery mark, so all three commit
-- together. A separate relay (services/outbox-relay) publishes unpublished rows to
-- Kafka at-least-once and stamps published_at. Downstream is keyed/upserting, so a
-- relay republish (publish-then-crash-before-mark) is absorbed.

CREATE TABLE outbox (
  outbox_id    bigint GENERATED ALWAYS AS IDENTITY,
  tenant_id    uuid        NOT NULL,
  event_id     uuid        NOT NULL,        -- canonical event_id (for tracing)
  topic        text        NOT NULL,        -- destination Kafka topic
  kafka_key    text        NOT NULL,        -- partition key (tenant_id → per-tenant order)
  trace_id     text,                        -- propagated to the published message header
  payload      bytea       NOT NULL,        -- exact canonical-event JSON bytes to publish
  created_at   timestamptz NOT NULL DEFAULT now(),
  published_at timestamptz,                 -- NULL = not yet published
  PRIMARY KEY (tenant_id, outbox_id)
);

-- Relay scan: pending rows in insertion order. Partial index keeps it tiny — it
-- only holds rows still awaiting publication.
CREATE INDEX outbox_unpublished_idx ON outbox (outbox_id) WHERE published_at IS NULL;

-- ---------------------------------------------------------------------------
-- RLS. The WRITE path (normalizer as devintel_app) is tenant-scoped exactly like
-- work_item, so it structurally cannot enqueue another tenant's event. The RELAY
-- is trusted infrastructure that must read across all tenants to drain the topic,
-- so it gets a dedicated least-privilege role with a read/update-all policy scoped
-- to this one table (not a blanket BYPASSRLS). See ADR-012.
-- ---------------------------------------------------------------------------
ALTER TABLE outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbox FORCE  ROW LEVEL SECURITY;

-- App (writer): only its own tenant's rows. DML grant comes from 0003's
-- ALTER DEFAULT PRIVILEGES; this policy is the row-level gate.
CREATE POLICY outbox_tenant_isolation ON outbox
  TO devintel_app
  USING      (tenant_id = current_setting('app.tenant_id', true)::uuid)
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

-- Relay: a dedicated NOSUPERUSER/NOBYPASSRLS role that may read+update every
-- pending row (it never inserts). Its reach is limited to the outbox table by the
-- explicit GRANT below + this policy — it cannot touch work_item etc.
CREATE ROLE devintel_relay
  LOGIN PASSWORD 'devintel_relay'
  NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOINHERIT;

GRANT CONNECT ON DATABASE devintel TO devintel_relay;
GRANT USAGE  ON SCHEMA   public    TO devintel_relay;
GRANT SELECT, UPDATE ON outbox TO devintel_relay;

CREATE POLICY outbox_relay_all ON outbox
  TO devintel_relay
  USING (true) WITH CHECK (true);

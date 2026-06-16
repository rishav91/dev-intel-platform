-- Observability (Tier 2, OpenTelemetry): carry W3C trace context across the
-- outbox DB hop.
--
-- The normalizer processes a delivery inside one trace and stages canonical
-- events in the outbox in the SAME transaction. The relay drains those rows in a
-- DIFFERENT process, asynchronously — so without persisting the trace context the
-- relay's publish would start an orphan trace and the end-to-end waterfall would
-- break exactly at the normalizer→relay boundary.
--
-- We already keep trace_id (human-readable, for logs). traceparent is the full
-- W3C value (version-traceid-spanid-flags); the relay re-extracts it to resume
-- the trace and publishes the canonical event as a child span. See
-- libs/go/observability and ADR-012 (outbox).
--
-- Nullable: rows written before this column existed (or by a path without an
-- active span) simply start a fresh trace at the relay, which is harmless.
ALTER TABLE outbox ADD COLUMN traceparent text;

-- No grant change: GRANT SELECT ON outbox (0005) is table-level and so already
-- covers this new column for the devintel_relay role.

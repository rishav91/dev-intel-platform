-- P1.A: the rest of the canonical write model (FR-3.1). Schemas per DATA-MODEL.md §2.
-- Every table is tenant-scoped with RLS FORCE — the same default-deny backstop as
-- work_item. DML for devintel_app comes from 0003's ALTER DEFAULT PRIVILEGES (these
-- tables are created by devintel), so no per-table GRANT is needed.
--
-- No cross-table FK constraints: matching work_item/DATA-MODEL.md, these colocate by
-- tenant_id under Citus (Phase 2) where cross-shard FKs don't hold; integrity is
-- enforced by the deterministic, replayable derivation jobs (ADR-010), not by FKs.

-- ---------------------------------------------------------------------------
-- review — PR reviews (pillar 2: review health). DATA-MODEL.md §2.
-- ---------------------------------------------------------------------------
CREATE TABLE review (
  tenant_id     uuid NOT NULL,
  review_id     uuid NOT NULL DEFAULT gen_random_uuid(),
  work_item_id  uuid NOT NULL,
  reviewer_id   uuid NOT NULL,                 -- resolved contributor
  source_id     text NOT NULL,                 -- GitHub review node id (idempotency)
  state         text NOT NULL,                 -- approved|changes_requested|commented|dismissed
  comment_count int  NOT NULL DEFAULT 0,       -- review depth signal
  submitted_at  timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, review_id),
  UNIQUE (tenant_id, source_id)
);
CREATE INDEX review_work_item_idx ON review (tenant_id, work_item_id);

-- ---------------------------------------------------------------------------
-- check_run — CI checks (pillar 3: reliability, flake). DATA-MODEL.md §2/§6.
-- ---------------------------------------------------------------------------
CREATE TABLE check_run (
  tenant_id     uuid NOT NULL,
  check_run_id  uuid NOT NULL DEFAULT gen_random_uuid(),
  work_item_id  uuid,                           -- associated PR (via head SHA), nullable until correlated
  source_id     text NOT NULL,                  -- GitHub check_run id (idempotency)
  head_sha      text NOT NULL,
  name          text NOT NULL,
  conclusion    text,                           -- success|failure|cancelled|timed_out|null(in-progress)
  was_retried   bool NOT NULL DEFAULT false,    -- flaky-CI signal
  started_at    timestamptz,
  completed_at  timestamptz,
  PRIMARY KEY (tenant_id, check_run_id),
  UNIQUE (tenant_id, source_id)
);
CREATE INDEX check_run_flake_idx    ON check_run (tenant_id, name, conclusion, started_at);
CREATE INDEX check_run_head_sha_idx ON check_run (tenant_id, head_sha);

-- ---------------------------------------------------------------------------
-- contributor — unified human/bot identity (pillar 5). DATA-MODEL.md §2.
-- ---------------------------------------------------------------------------
CREATE TABLE contributor (
  tenant_id      uuid NOT NULL,
  contributor_id uuid NOT NULL DEFAULT gen_random_uuid(),
  display_name   text,
  is_bot         bool NOT NULL DEFAULT false,
  resolution_confidence float,
  PRIMARY KEY (tenant_id, contributor_id)
);

-- ---------------------------------------------------------------------------
-- identity_link — raw identifier → contributor map (FR-3.3). DATA-MODEL.md §2/§4.
-- identifier_value_hash: PII is hashed, never stored raw (METRICS-ETHICS.md).
-- ---------------------------------------------------------------------------
CREATE TABLE identity_link (
  tenant_id        uuid NOT NULL,
  link_id          uuid NOT NULL DEFAULT gen_random_uuid(),
  contributor_id   uuid NOT NULL,
  identifier_kind  text NOT NULL,               -- commit_email|github_login|noreply
  identifier_value_hash text NOT NULL,
  confidence       float NOT NULL,
  PRIMARY KEY (tenant_id, link_id),
  UNIQUE (tenant_id, identifier_kind, identifier_value_hash)
);

-- ---------------------------------------------------------------------------
-- state_transition — work-item FSM timeline (STATE-MACHINE.md; FR-4.1 inputs).
-- ---------------------------------------------------------------------------
CREATE TABLE state_transition (
  tenant_id     uuid NOT NULL,
  transition_id uuid NOT NULL DEFAULT gen_random_uuid(),
  work_item_id  uuid NOT NULL,
  from_stage    text,
  to_stage      text NOT NULL,
  occurred_at   timestamptz NOT NULL,
  idle_before   interval,                       -- computed by the derivation job
  PRIMARY KEY (tenant_id, transition_id),
  -- one transition per (work_item, to_stage, occurred_at): idempotent replay (ADR-010).
  UNIQUE (tenant_id, work_item_id, to_stage, occurred_at)
);
CREATE INDEX state_transition_timeline_idx ON state_transition (tenant_id, work_item_id, occurred_at);

-- ---------------------------------------------------------------------------
-- entity_edge — intra-GitHub graph, confidence-scored (FR-3.2/3.4). DATA-MODEL.md §2/§6.
-- ---------------------------------------------------------------------------
CREATE TABLE entity_edge (
  tenant_id  uuid NOT NULL,
  edge_id    uuid NOT NULL DEFAULT gen_random_uuid(),
  src_id     uuid NOT NULL,
  dst_kind   text NOT NULL,                     -- work_item|check_run
  dst_id     uuid NOT NULL,
  relation   text NOT NULL,                     -- contains_commit|closes|references|validated_by
  confidence float NOT NULL,
  method     text,                              -- pr_commit_list|closes_kw|trailer|head_sha|timeline_xref
  PRIMARY KEY (tenant_id, edge_id),
  -- deterministic edges: same (src,dst,relation) recomputes identically on replay.
  UNIQUE (tenant_id, src_id, dst_kind, dst_id, relation)
);
CREATE INDEX entity_edge_src_idx ON entity_edge (tenant_id, src_id);
CREATE INDEX entity_edge_dst_idx ON entity_edge (tenant_id, dst_id);

-- ---------------------------------------------------------------------------
-- repo_capability — per-repo presence of capability-gated signals (FR-2.10/2.11).
-- Gates DORA/deploy metrics: a metric lights up only if its signal is detected.
-- Written by capability detection on connect/first sync (libs/go/githubapp).
-- ---------------------------------------------------------------------------
CREATE TABLE repo_capability (
  tenant_id    uuid NOT NULL,
  repo         text NOT NULL,                   -- owner/name
  deployments  bool NOT NULL DEFAULT false,     -- repo emits deployments/environments
  releases     bool NOT NULL DEFAULT false,     -- repo publishes releases
  detected_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, repo)
);

-- ---------------------------------------------------------------------------
-- RLS: enable + FORCE on every table above; tenant-isolation policy keyed on
-- app.tenant_id (default-deny when unset). Identical backstop to work_item.
-- ---------------------------------------------------------------------------
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY[
    'review','check_run','contributor','identity_link',
    'state_transition','entity_edge','repo_capability'
  ] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
    EXECUTE format($f$
      CREATE POLICY %1$s_tenant_isolation ON %1$I
        USING      (tenant_id = current_setting('app.tenant_id', true)::uuid)
        WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid)
    $f$, t);
  END LOOP;
END $$;

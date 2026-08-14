-- Additive Graph projection and orchestration facts. Business revisions and
-- prior jobs remain immutable; only graph_sync_states is CAS-mutable.
UPDATE project_meta SET db_schema_version=5;

CREATE TABLE projection_summaries (
  revision_id TEXT NOT NULL REFERENCES config_revisions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  projection_schema_version TEXT NOT NULL,
  projector_version TEXT NOT NULL,
  config_hash TEXT NOT NULL CHECK(length(config_hash)=64),
  graph_manifest_hash TEXT NOT NULL CHECK(length(graph_manifest_hash)=64),
  node_count INTEGER NOT NULL CHECK(node_count>=0),
  edge_count INTEGER NOT NULL CHECK(edge_count>=0),
  evidence TEXT NOT NULL,
  cache_identity TEXT,
  created_at TEXT NOT NULL,
  PRIMARY KEY(revision_id,projection_schema_version,projector_version)
);

CREATE TABLE graph_sync_states (
  revision_id TEXT PRIMARY KEY REFERENCES config_revisions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  pipeline_state TEXT NOT NULL,
  latest_job_id TEXT REFERENCES jobs(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  external_task_id TEXT,
  generation INTEGER NOT NULL DEFAULT 0 CHECK(generation>=0),
  safe_error TEXT,
  warnings TEXT NOT NULL DEFAULT '[]',
  updated_at TEXT NOT NULL
);

CREATE TABLE graph_impact_handoffs (
  revision_id TEXT NOT NULL REFERENCES config_revisions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  graph_manifest_hash TEXT NOT NULL CHECK(length(graph_manifest_hash)=64),
  stage TEXT NOT NULL,
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY(revision_id,stage,graph_manifest_hash)
);

CREATE INDEX projection_summaries_graph_hash_lookup ON projection_summaries(graph_manifest_hash);
CREATE INDEX graph_sync_states_recovery_lookup ON graph_sync_states(pipeline_state,updated_at);
CREATE TRIGGER projection_summaries_immutable_update BEFORE UPDATE ON projection_summaries BEGIN SELECT RAISE(ABORT, 'projection summary is immutable'); END;
CREATE TRIGGER projection_summaries_immutable_delete BEFORE DELETE ON projection_summaries BEGIN SELECT RAISE(ABORT, 'projection summary is immutable'); END;
CREATE TRIGGER graph_sync_states_generation_monotonic BEFORE UPDATE ON graph_sync_states
WHEN NEW.revision_id != OLD.revision_id OR NEW.generation <= OLD.generation
BEGIN SELECT RAISE(ABORT, 'graph sync state transition is invalid'); END;
INSERT INTO schema_migration_steps(step_id,schema_version,committed_at) VALUES('graph-sync-v5',5,strftime('%Y-%m-%dT%H:%M:%fZ','now'));

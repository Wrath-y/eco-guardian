-- Additive versioning facts. This migration intentionally does not alter
-- entity blobs, config hashes, revision manifests, or validation history.
ALTER TABLE project_meta RENAME TO project_meta_v2;
CREATE TABLE project_meta (
  id TEXT PRIMARY KEY NOT NULL,
  db_schema_version INTEGER NOT NULL CHECK (db_schema_version = 3),
  created_at TEXT NOT NULL
);
INSERT INTO project_meta(id, db_schema_version, created_at)
  SELECT id, 3, created_at FROM project_meta_v2;
DROP TABLE project_meta_v2;

CREATE TABLE schema_migration_steps (
  step_id TEXT PRIMARY KEY NOT NULL,
  schema_version INTEGER NOT NULL UNIQUE,
  committed_at TEXT NOT NULL
);

CREATE TABLE revision_metadata (
  revision_id TEXT PRIMARY KEY NOT NULL REFERENCES config_revisions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  name TEXT,
  description TEXT,
  parent_revision_id TEXT REFERENCES config_revisions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  source_revision_id TEXT REFERENCES config_revisions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  source_release_id TEXT REFERENCES releases(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  version_manifest TEXT NOT NULL,
  version_manifest_hash TEXT NOT NULL CHECK (length(version_manifest_hash) = 64),
  created_at TEXT NOT NULL
);

CREATE TABLE release_policies (
  id TEXT PRIMARY KEY NOT NULL,
  display_version INTEGER NOT NULL CHECK (display_version > 0),
  canonical_body TEXT NOT NULL,
  canonical_hash TEXT NOT NULL CHECK (length(canonical_hash) = 64),
  created_at TEXT NOT NULL
);

CREATE TABLE jobs (
  id TEXT PRIMARY KEY NOT NULL,
  project_uuid TEXT NOT NULL REFERENCES project_meta(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  kind TEXT NOT NULL,
  revision_id TEXT REFERENCES config_revisions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  input_hash TEXT NOT NULL CHECK (length(input_hash) = 64),
  idempotency_key TEXT NOT NULL,
  request_hash TEXT NOT NULL CHECK (length(request_hash) = 64),
  status TEXT NOT NULL CHECK (status IN ('queued','running','succeeded','failed','canceled','interrupted')),
  result_type TEXT,
  result_id TEXT,
  result_url TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE job_events (
  job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  event_ordinal INTEGER NOT NULL CHECK (event_ordinal > 0),
  phase TEXT NOT NULL,
  progress INTEGER NOT NULL CHECK (progress >= 0 AND progress <= 100),
  warning TEXT,
  error TEXT,
  result_type TEXT,
  result_id TEXT,
  result_url TEXT,
  created_at TEXT NOT NULL,
  PRIMARY KEY (job_id, event_ordinal)
);

CREATE TABLE release_intents (
  id TEXT PRIMARY KEY NOT NULL,
  job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  candidate_revision_id TEXT NOT NULL REFERENCES config_revisions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  baseline_release_id TEXT REFERENCES releases(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  policy_id TEXT NOT NULL REFERENCES release_policies(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  gate_manifest TEXT NOT NULL,
  gate_manifest_hash TEXT NOT NULL CHECK (length(gate_manifest_hash) = 64),
  confirmations TEXT NOT NULL,
  backup_evidence TEXT,
  previous_graph_identity TEXT,
  previous_release_id TEXT REFERENCES releases(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  request_hash TEXT NOT NULL CHECK (length(request_hash) = 64),
  idempotency_key TEXT NOT NULL,
  external_task_id TEXT,
  phase TEXT NOT NULL,
  error_details TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE releases (
  id TEXT PRIMARY KEY NOT NULL,
  revision_id TEXT NOT NULL REFERENCES config_revisions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  policy_id TEXT NOT NULL REFERENCES release_policies(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  baseline_release_id TEXT REFERENCES releases(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  intent_id TEXT NOT NULL REFERENCES release_intents(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  notes TEXT NOT NULL,
  gate_evidence TEXT NOT NULL,
  confirmations TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE active_release_pointer (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  active_release_id TEXT REFERENCES releases(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  generation INTEGER NOT NULL CHECK (generation >= 0)
);
INSERT INTO active_release_pointer(singleton, active_release_id, generation) VALUES(1, NULL, 0);

CREATE INDEX revision_metadata_manifest_hash_lookup ON revision_metadata(version_manifest_hash);
CREATE UNIQUE INDEX release_policies_display_version_unique ON release_policies(display_version);
CREATE INDEX release_policies_canonical_hash_lookup ON release_policies(canonical_hash);
CREATE UNIQUE INDEX jobs_project_idempotency_unique ON jobs(project_uuid, idempotency_key);
CREATE INDEX jobs_revision_history_lookup ON jobs(project_uuid, revision_id, created_at, id);
CREATE INDEX job_events_job_ordinal_lookup ON job_events(job_id, event_ordinal);
CREATE UNIQUE INDEX release_intents_job_unique ON release_intents(job_id);
CREATE UNIQUE INDEX releases_intent_unique ON releases(intent_id);
CREATE INDEX releases_revision_history_lookup ON releases(revision_id, created_at, id);
CREATE INDEX releases_policy_history_lookup ON releases(policy_id, created_at, id);
CREATE INDEX releases_baseline_history_lookup ON releases(baseline_release_id, created_at, id);
CREATE UNIQUE INDEX active_release_pointer_generation_unique ON active_release_pointer(generation);

CREATE TRIGGER revision_metadata_immutable_update BEFORE UPDATE ON revision_metadata BEGIN SELECT RAISE(ABORT, 'revision metadata is immutable'); END;
CREATE TRIGGER revision_metadata_immutable_delete BEFORE DELETE ON revision_metadata BEGIN SELECT RAISE(ABORT, 'revision metadata is immutable'); END;
CREATE TRIGGER release_policies_immutable_update BEFORE UPDATE ON release_policies BEGIN SELECT RAISE(ABORT, 'release policies are immutable'); END;
CREATE TRIGGER release_policies_immutable_delete BEFORE DELETE ON release_policies BEGIN SELECT RAISE(ABORT, 'release policies are immutable'); END;
CREATE TRIGGER releases_immutable_update BEFORE UPDATE ON releases BEGIN SELECT RAISE(ABORT, 'releases are immutable'); END;
CREATE TRIGGER releases_immutable_delete BEFORE DELETE ON releases BEGIN SELECT RAISE(ABORT, 'releases are immutable'); END;

-- Jobs, intents, and the singleton pointer are the only mutable records. Their
-- immutable identity/input columns cannot change; repositories perform state
-- transitions with an expected status/phase/generation predicate.
CREATE TRIGGER jobs_identity_immutable BEFORE UPDATE ON jobs
WHEN NEW.id != OLD.id OR NEW.project_uuid != OLD.project_uuid OR NEW.kind != OLD.kind OR
     NEW.revision_id IS NOT OLD.revision_id OR NEW.input_hash != OLD.input_hash OR
     NEW.idempotency_key != OLD.idempotency_key OR NEW.request_hash != OLD.request_hash OR
     NEW.created_at != OLD.created_at
BEGIN SELECT RAISE(ABORT, 'job identity is immutable'); END;
CREATE TRIGGER release_intents_identity_immutable BEFORE UPDATE ON release_intents
WHEN NEW.id != OLD.id OR NEW.job_id != OLD.job_id OR NEW.candidate_revision_id != OLD.candidate_revision_id OR
     NEW.baseline_release_id IS NOT OLD.baseline_release_id OR NEW.policy_id != OLD.policy_id OR
     NEW.gate_manifest != OLD.gate_manifest OR NEW.gate_manifest_hash != OLD.gate_manifest_hash OR
     NEW.confirmations != OLD.confirmations OR NEW.request_hash != OLD.request_hash OR
     NEW.idempotency_key != OLD.idempotency_key OR NEW.created_at != OLD.created_at
BEGIN SELECT RAISE(ABORT, 'release intent identity is immutable'); END;
CREATE TRIGGER active_release_pointer_singleton_update BEFORE UPDATE ON active_release_pointer
WHEN NEW.singleton != 1 OR NEW.generation < OLD.generation
BEGIN SELECT RAISE(ABORT, 'active release pointer transition is invalid'); END;

-- v3 can only run after v2. Both identities become visible atomically with the
-- schema-version row, so restart/replay cannot observe a half-completed step.
INSERT INTO schema_migration_steps(step_id, schema_version, committed_at) VALUES
  ('validation-v2', 2, strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  ('versioning-v3', 3, strftime('%Y-%m-%dT%H:%M:%fZ','now'));

-- Persist the exact canonical input captured before a simulation Job starts.
-- This enables restart recovery without reconstructing from mutable state.
UPDATE project_meta SET db_schema_version=13;
CREATE TABLE simulation_job_materializations (
  job_id TEXT PRIMARY KEY NOT NULL REFERENCES jobs(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  project_uuid TEXT NOT NULL REFERENCES project_meta(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  revision_id TEXT NOT NULL REFERENCES config_revisions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  scenario_definition_id TEXT NOT NULL REFERENCES scenario_definitions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  canonical_input BLOB NOT NULL,
  input_hash TEXT NOT NULL CHECK(length(input_hash)=64),
  fingerprint_hash TEXT NOT NULL CHECK(length(fingerprint_hash)=64),
  cancel_generation INTEGER NOT NULL CHECK(cancel_generation >= 0),
  created_at TEXT NOT NULL
);
CREATE INDEX simulation_materializations_recovery_lookup ON simulation_job_materializations(project_uuid,revision_id,created_at,job_id);
CREATE TRIGGER simulation_job_materializations_immutable_update BEFORE UPDATE ON simulation_job_materializations BEGIN SELECT RAISE(ABORT,'simulation job materialization is immutable'); END;
CREATE TRIGGER simulation_job_materializations_immutable_delete BEFORE DELETE ON simulation_job_materializations BEGIN SELECT RAISE(ABORT,'simulation job materialization is immutable'); END;
INSERT INTO schema_migration_steps(step_id,schema_version,committed_at)
  VALUES('simulation-job-materializations-v13',13,strftime('%Y-%m-%dT%H:%M:%fZ','now'));

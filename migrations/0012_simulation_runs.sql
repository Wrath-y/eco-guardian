-- Durable simulation staging and immutable result facts. Checkpoints remain
-- mutable recovery state; runs, metric rows, and verifications are insert-only.
UPDATE project_meta SET db_schema_version=12;
CREATE TABLE simulation_runs (
  id TEXT PRIMARY KEY NOT NULL,
  job_id TEXT NOT NULL UNIQUE REFERENCES jobs(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  project_uuid TEXT NOT NULL REFERENCES project_meta(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  revision_id TEXT NOT NULL REFERENCES config_revisions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  scenario_definition_id TEXT NOT NULL REFERENCES scenario_definitions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  input_hash TEXT NOT NULL CHECK(length(input_hash)=64),
  fingerprint_hash TEXT NOT NULL CHECK(length(fingerprint_hash)=64),
  result_hash TEXT NOT NULL CHECK(length(result_hash)=64),
  canonical_result TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE simulation_metric_results (
  run_id TEXT NOT NULL REFERENCES simulation_runs(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  metric_id TEXT NOT NULL,
  metric_version TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('available','unavailable')),
  canonical_result TEXT NOT NULL,
  PRIMARY KEY(run_id,metric_id)
);
CREATE TABLE simulation_verifications (
  id TEXT PRIMARY KEY NOT NULL,
  source_run_id TEXT NOT NULL REFERENCES simulation_runs(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  reproduction_run_id TEXT NOT NULL UNIQUE REFERENCES simulation_runs(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  input_hash TEXT NOT NULL CHECK(length(input_hash)=64),
  fingerprint_hash TEXT NOT NULL CHECK(length(fingerprint_hash)=64),
  source_result_hash TEXT NOT NULL CHECK(length(source_result_hash)=64),
  reproduction_result_hash TEXT NOT NULL CHECK(length(reproduction_result_hash)=64),
  status TEXT NOT NULL CHECK(status IN ('verified','mismatch')),
  created_at TEXT NOT NULL
);
CREATE TABLE simulation_job_checkpoints (
  job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  sample_ordinal INTEGER NOT NULL CHECK(sample_ordinal >= 0),
  input_hash TEXT NOT NULL CHECK(length(input_hash)=64),
  fingerprint_hash TEXT NOT NULL CHECK(length(fingerprint_hash)=64),
  cancel_generation INTEGER NOT NULL CHECK(cancel_generation >= 0),
  accumulator TEXT NOT NULL,
  accumulator_hash TEXT NOT NULL CHECK(length(accumulator_hash)=64),
  completed_at TEXT NOT NULL,
  PRIMARY KEY(job_id,sample_ordinal)
);
CREATE INDEX simulation_runs_project_history_lookup ON simulation_runs(project_uuid,created_at,id);
CREATE INDEX simulation_runs_revision_history_lookup ON simulation_runs(project_uuid,revision_id,created_at,id);
CREATE INDEX simulation_metric_results_lookup ON simulation_metric_results(run_id,metric_id);
CREATE INDEX simulation_checkpoints_recovery_lookup ON simulation_job_checkpoints(job_id,cancel_generation,sample_ordinal);
CREATE TRIGGER simulation_runs_immutable_update BEFORE UPDATE ON simulation_runs BEGIN SELECT RAISE(ABORT,'simulation run is immutable'); END;
CREATE TRIGGER simulation_runs_immutable_delete BEFORE DELETE ON simulation_runs BEGIN SELECT RAISE(ABORT,'simulation run is immutable'); END;
CREATE TRIGGER simulation_metric_results_immutable_update BEFORE UPDATE ON simulation_metric_results BEGIN SELECT RAISE(ABORT,'simulation metric result is immutable'); END;
CREATE TRIGGER simulation_metric_results_immutable_delete BEFORE DELETE ON simulation_metric_results BEGIN SELECT RAISE(ABORT,'simulation metric result is immutable'); END;
CREATE TRIGGER simulation_verifications_immutable_update BEFORE UPDATE ON simulation_verifications BEGIN SELECT RAISE(ABORT,'simulation verification is immutable'); END;
CREATE TRIGGER simulation_verifications_immutable_delete BEFORE DELETE ON simulation_verifications BEGIN SELECT RAISE(ABORT,'simulation verification is immutable'); END;
INSERT INTO schema_migration_steps(step_id,schema_version,committed_at)
  VALUES('simulation-runs-v12',12,strftime('%Y-%m-%dT%H:%M:%fZ','now'));

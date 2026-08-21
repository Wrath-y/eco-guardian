-- Preserve the exact implementation descriptors used by each immutable run.
-- Older runs intentionally remain readable without backfilling current code.
UPDATE project_meta SET db_schema_version=14;
CREATE TABLE simulation_run_implementations (
  run_id TEXT NOT NULL REFERENCES simulation_runs(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  descriptor_id TEXT NOT NULL,
  descriptor_version TEXT NOT NULL,
  descriptor_hash TEXT NOT NULL CHECK(length(descriptor_hash)=64),
  dependencies_json TEXT NOT NULL,
  PRIMARY KEY(run_id,descriptor_id)
);
CREATE INDEX simulation_run_implementations_lookup ON simulation_run_implementations(run_id,descriptor_id);
CREATE TRIGGER simulation_run_implementations_immutable_update BEFORE UPDATE ON simulation_run_implementations BEGIN SELECT RAISE(ABORT,'simulation run implementation is immutable'); END;
CREATE TRIGGER simulation_run_implementations_immutable_delete BEFORE DELETE ON simulation_run_implementations BEGIN SELECT RAISE(ABORT,'simulation run implementation is immutable'); END;
INSERT INTO schema_migration_steps(step_id,schema_version,committed_at)
  VALUES('simulation-run-implementations-v14',14,strftime('%Y-%m-%dT%H:%M:%fZ','now'));

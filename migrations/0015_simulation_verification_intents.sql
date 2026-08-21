-- Bind an optional reproduction Job to its immutable source run before work
-- starts; sealing writes the comparison in the same transaction as the run.
UPDATE project_meta SET db_schema_version=15;
ALTER TABLE simulation_job_materializations ADD COLUMN verify_source_run_id TEXT REFERENCES simulation_runs(id) ON DELETE RESTRICT ON UPDATE RESTRICT;
CREATE INDEX simulation_materializations_verify_source ON simulation_job_materializations(verify_source_run_id,job_id);
INSERT INTO schema_migration_steps(step_id,schema_version,committed_at)
  VALUES('simulation-verification-intents-v15',15,strftime('%Y-%m-%dT%H:%M:%fZ','now'));

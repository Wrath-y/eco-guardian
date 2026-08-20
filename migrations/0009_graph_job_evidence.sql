-- Bind automatic Graph jobs to the exact FULL-validation identity used for
-- admission. Evidence is insert-only for graph jobs and is never provider data.
UPDATE project_meta SET db_schema_version=9;
ALTER TABLE jobs ADD COLUMN graph_evidence TEXT;
CREATE TRIGGER graph_job_evidence_immutable BEFORE UPDATE ON jobs
WHEN OLD.kind='graph_sync' AND NEW.graph_evidence IS NOT OLD.graph_evidence
BEGIN SELECT RAISE(ABORT, 'graph job evidence is immutable'); END;
INSERT INTO schema_migration_steps(step_id,schema_version,committed_at)
  VALUES('graph-job-evidence-v9',9,strftime('%Y-%m-%dT%H:%M:%fZ','now'));

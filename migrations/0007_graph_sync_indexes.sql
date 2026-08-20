-- Harden Graph recovery identities while continuing to reuse the #7 jobs
-- table. All additions are nullable or indexes, so historical facts remain
-- readable without a backfill.
UPDATE project_meta SET db_schema_version=7;

ALTER TABLE jobs ADD COLUMN retry_of_job_id TEXT REFERENCES jobs(id) ON DELETE RESTRICT ON UPDATE RESTRICT;

CREATE INDEX jobs_retry_of_lookup ON jobs(project_uuid,retry_of_job_id,created_at,id)
  WHERE retry_of_job_id IS NOT NULL;
CREATE INDEX jobs_graph_sync_recovery_lookup ON jobs(status,updated_at,id)
  WHERE kind='graph_sync' AND status IN ('queued','running','interrupted');
CREATE INDEX graph_sync_states_provider_request_lookup ON graph_sync_states(provider_request_id)
  WHERE provider_request_id IS NOT NULL;
CREATE UNIQUE INDEX graph_sync_states_provider_task_unique ON graph_sync_states(provider_task_id)
  WHERE provider_task_id IS NOT NULL;

INSERT INTO schema_migration_steps(step_id,schema_version,committed_at)
  VALUES('graph-sync-indexes-v7',7,strftime('%Y-%m-%dT%H:%M:%fZ','now'));

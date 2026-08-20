-- Add provider-effect checkpoints without changing immutable revision facts.
UPDATE project_meta SET db_schema_version=6;
ALTER TABLE graph_sync_states ADD COLUMN provider_request_id TEXT;
ALTER TABLE graph_sync_states ADD COLUMN provider_task_id TEXT;
INSERT INTO schema_migration_steps(step_id,schema_version,committed_at) VALUES('graph-checkpoints-v6',6,strftime('%Y-%m-%dT%H:%M:%fZ','now'));

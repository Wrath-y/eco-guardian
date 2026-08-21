-- Persist cooperative cancellation independently of terminal Job state. The
-- defaults retain all v9 Job records exactly as not-yet-canceled work.
UPDATE project_meta SET db_schema_version=10;
ALTER TABLE jobs ADD COLUMN cancel_generation INTEGER NOT NULL DEFAULT 0 CHECK (cancel_generation >= 0);
ALTER TABLE jobs ADD COLUMN cancel_requested_at TEXT;
CREATE INDEX jobs_project_kind_status_lookup ON jobs(project_uuid,kind,status,created_at,id);
CREATE INDEX jobs_revision_kind_history_lookup ON jobs(project_uuid,revision_id,kind,created_at,id);
INSERT INTO schema_migration_steps(step_id,schema_version,committed_at)
  VALUES('shared-job-cancellation-v10',10,strftime('%Y-%m-%dT%H:%M:%fZ','now'));

-- Durable backup admission facts and minimal restore reconciliation envelopes.
-- Backup bytes and manifests remain filesystem-derived; project.db stores only
-- command/audit identities required for idempotency and recovery.
UPDATE project_meta SET db_schema_version=22;

CREATE TABLE backup_commands (
  job_id TEXT PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
  project_uuid TEXT NOT NULL,
  command_hash TEXT NOT NULL CHECK(length(command_hash)=64),
  command_json BLOB NOT NULL CHECK(length(command_json) BETWEEN 2 AND 8192),
  publication_started_at TEXT,
  created_at TEXT NOT NULL
);
CREATE INDEX backup_commands_project_lookup
  ON backup_commands(project_uuid,created_at,job_id);

CREATE TABLE backup_artifact_audit (
  backup_id TEXT PRIMARY KEY,
  project_uuid TEXT NOT NULL,
  job_id TEXT REFERENCES jobs(id) ON DELETE SET NULL,
  backup_type TEXT NOT NULL CHECK(backup_type IN ('manual','daily','migration','restore-pre','release')),
  manifest_hash TEXT NOT NULL CHECK(length(manifest_hash)=64),
  database_hash TEXT NOT NULL CHECK(length(database_hash)=64),
  result_json BLOB NOT NULL CHECK(length(result_json) BETWEEN 2 AND 16384),
  published_at TEXT NOT NULL
);
CREATE INDEX backup_artifact_audit_project_lookup
  ON backup_artifact_audit(project_uuid,published_at,backup_id);

CREATE TABLE daily_backup_admission (
  project_uuid TEXT NOT NULL,
  local_date TEXT NOT NULL CHECK(length(local_date)=10),
  job_id TEXT NOT NULL REFERENCES jobs(id),
  command_hash TEXT NOT NULL CHECK(length(command_hash)=64),
  created_at TEXT NOT NULL,
  PRIMARY KEY(project_uuid,local_date)
);

CREATE TABLE daily_backup_waivers (
  project_uuid TEXT NOT NULL,
  local_date TEXT NOT NULL CHECK(length(local_date)=10),
  failed_job_id TEXT NOT NULL REFERENCES jobs(id),
  waiver_json BLOB NOT NULL CHECK(length(waiver_json) BETWEEN 2 AND 8192),
  confirmed_at TEXT NOT NULL,
  PRIMARY KEY(project_uuid,local_date)
);

CREATE TABLE restore_reconciliation (
  job_id TEXT PRIMARY KEY,
  project_uuid TEXT NOT NULL,
  request_hash TEXT NOT NULL CHECK(length(request_hash)=64),
  idempotency_key TEXT NOT NULL,
  last_event_ordinal INTEGER NOT NULL DEFAULT 0 CHECK(last_event_ordinal>=0),
  journal_generation INTEGER NOT NULL CHECK(journal_generation>=1),
  reconciled_at TEXT NOT NULL
);

CREATE TABLE restore_graph_invalidations (
  job_id TEXT PRIMARY KEY REFERENCES restore_reconciliation(job_id) ON DELETE CASCADE,
  project_uuid TEXT NOT NULL,
  request_hash TEXT NOT NULL CHECK(length(request_hash)=64),
  restore_generation INTEGER NOT NULL CHECK(restore_generation>=1),
  invalidated_at TEXT NOT NULL
);

INSERT INTO schema_migration_steps(step_id,schema_version,committed_at)
  VALUES('backup-restore-v22',22,strftime('%Y-%m-%dT%H:%M:%fZ','now'));

-- Record the exact migration content accepted by this project database. The
-- runner fills every checksum in this same transaction after this schema
-- change, including this migration's own content hash.
UPDATE project_meta SET db_schema_version=8;
ALTER TABLE schema_migration_steps ADD COLUMN checksum TEXT;
INSERT INTO schema_migration_steps(step_id,schema_version,committed_at)
  VALUES('graph-migration-checksums-v8',8,strftime('%Y-%m-%dT%H:%M:%fZ','now'));

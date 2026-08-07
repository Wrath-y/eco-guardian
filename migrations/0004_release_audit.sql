PRAGMA ignore_check_constraints=ON;
UPDATE project_meta SET db_schema_version=4;
PRAGMA ignore_check_constraints=OFF;
-- v3 fixed the version to 3. Keep the table identity (jobs references it),
-- but widen that legacy check so later applications can report newer schemas.
PRAGMA writable_schema=ON;
UPDATE sqlite_master
  SET sql=replace(sql, 'CHECK (db_schema_version = 3)', 'CHECK (db_schema_version >= 3)')
  WHERE type='table' AND name='project_meta';
PRAGMA writable_schema=OFF;
ALTER TABLE release_intents ADD COLUMN notes TEXT NOT NULL DEFAULT '';
ALTER TABLE release_intents ADD COLUMN override_audit TEXT;
ALTER TABLE releases ADD COLUMN override_audit TEXT;
INSERT INTO schema_migration_steps(step_id,schema_version,committed_at) VALUES('release-audit-v4',4,strftime('%Y-%m-%dT%H:%M:%fZ','now'));

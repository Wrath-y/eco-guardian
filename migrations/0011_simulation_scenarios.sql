-- Store immutable scenario definitions per project. Built-ins are seeded by
-- the repository in the same migration transaction after this table exists.
UPDATE project_meta SET db_schema_version=11;
CREATE TABLE scenario_definitions (
  id TEXT PRIMARY KEY NOT NULL,
  project_uuid TEXT NOT NULL REFERENCES project_meta(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  scene_id TEXT NOT NULL,
  scene_version TEXT NOT NULL,
  origin TEXT NOT NULL CHECK (origin IN ('builtin','clone')),
  source_definition_id TEXT REFERENCES scenario_definitions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  canonical_body TEXT NOT NULL,
  canonical_hash TEXT NOT NULL CHECK (length(canonical_hash) = 64),
  created_at TEXT NOT NULL,
  UNIQUE(project_uuid, scene_id, scene_version),
  CHECK ((origin = 'builtin' AND source_definition_id IS NULL) OR (origin = 'clone' AND source_definition_id IS NOT NULL))
);
CREATE INDEX scenario_definitions_project_history_lookup ON scenario_definitions(project_uuid, created_at, id);
CREATE INDEX scenario_definitions_scene_lookup ON scenario_definitions(project_uuid, scene_id, scene_version);
CREATE TRIGGER scenario_definitions_immutable_update BEFORE UPDATE ON scenario_definitions BEGIN SELECT RAISE(ABORT, 'scenario definition is immutable'); END;
CREATE TRIGGER scenario_definitions_immutable_delete BEFORE DELETE ON scenario_definitions BEGIN SELECT RAISE(ABORT, 'scenario definition is immutable'); END;
INSERT INTO schema_migration_steps(step_id,schema_version,committed_at)
  VALUES('simulation-scenarios-v11',11,strftime('%Y-%m-%dT%H:%M:%fZ','now'));

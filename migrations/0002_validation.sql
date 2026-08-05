-- Additive, rebuildable validation data. Entity blobs and revision manifests
-- remain the only configuration facts.
ALTER TABLE project_meta RENAME TO project_meta_v1;
CREATE TABLE project_meta (
  id TEXT PRIMARY KEY NOT NULL,
  db_schema_version INTEGER NOT NULL CHECK (db_schema_version = 2),
  created_at TEXT NOT NULL
);
INSERT INTO project_meta(id, db_schema_version, created_at) SELECT id, 2, created_at FROM project_meta_v1;
DROP TABLE project_meta_v1;

CREATE TABLE compiled_ast (
  revision_id TEXT NOT NULL REFERENCES config_revisions(id) ON DELETE RESTRICT,
  entity_id TEXT NOT NULL,
  field_path TEXT NOT NULL,
  formula_hash TEXT NOT NULL CHECK (length(formula_hash) = 64),
  ast_version TEXT NOT NULL,
  dsl_version TEXT NOT NULL,
  registry_version TEXT NOT NULL,
  ast BLOB NOT NULL,
  ast_hash TEXT NOT NULL CHECK (length(ast_hash) = 64),
  PRIMARY KEY (revision_id, entity_id, field_path)
);
CREATE INDEX compiled_ast_revision_entity_path ON compiled_ast(revision_id, entity_id, field_path);

CREATE TABLE formula_index (
  revision_id TEXT NOT NULL REFERENCES config_revisions(id) ON DELETE RESTRICT,
  source_entity_id TEXT NOT NULL,
  field_path TEXT NOT NULL,
  read_ordinal INTEGER NOT NULL CHECK (read_ordinal >= 0),
  output_attribute_id TEXT NOT NULL,
  scope TEXT NOT NULL,
  symbol TEXT NOT NULL,
  span_start INTEGER NOT NULL CHECK (span_start >= 0),
  span_end INTEGER NOT NULL CHECK (span_end > span_start),
  value_type TEXT NOT NULL,
  unit TEXT NOT NULL,
  PRIMARY KEY (revision_id, source_entity_id, field_path, read_ordinal)
);
CREATE INDEX formula_index_revision_output ON formula_index(revision_id, output_attribute_id);

CREATE TABLE revision_references (
  revision_id TEXT NOT NULL REFERENCES config_revisions(id) ON DELETE RESTRICT,
  source_entity_id TEXT NOT NULL,
  field_path TEXT NOT NULL,
  ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
  expected_kind TEXT NOT NULL,
  target_entity_id TEXT NOT NULL,
  PRIMARY KEY (revision_id, source_entity_id, field_path, ordinal)
);
CREATE INDEX revision_references_target ON revision_references(revision_id, target_entity_id);

CREATE TABLE validation_runs (
  id TEXT PRIMARY KEY NOT NULL,
  source_kind TEXT NOT NULL CHECK (source_kind IN ('working','revision')),
  source_revision_id TEXT REFERENCES config_revisions(id) ON DELETE RESTRICT,
  source_input_hash TEXT NOT NULL CHECK (length(source_input_hash) = 64),
  scope TEXT NOT NULL CHECK (scope IN ('BASE','LOCAL','FULL')),
  version_manifest_hash TEXT NOT NULL CHECK (length(version_manifest_hash) = 64),
  version_manifest TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status = 'completed'),
  error_count INTEGER NOT NULL CHECK (error_count >= 0),
  block_count INTEGER NOT NULL CHECK (block_count >= 0),
  warning_count INTEGER NOT NULL CHECK (warning_count >= 0),
  info_count INTEGER NOT NULL CHECK (info_count >= 0),
  result_hash TEXT NOT NULL CHECK (length(result_hash) = 64),
  created_at TEXT NOT NULL,
  CHECK ((source_kind = 'revision' AND source_revision_id IS NOT NULL) OR (source_kind = 'working' AND source_revision_id IS NULL))
);
CREATE INDEX validation_runs_revision_lookup ON validation_runs(source_revision_id, scope, version_manifest_hash);
CREATE INDEX validation_runs_result_hash ON validation_runs(result_hash);

CREATE TABLE validation_issues (
  run_id TEXT NOT NULL REFERENCES validation_runs(id) ON DELETE RESTRICT,
  ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
  fingerprint TEXT NOT NULL CHECK (length(fingerprint) = 64),
  severity TEXT NOT NULL CHECK (severity IN ('ERROR','BLOCK','WARNING','INFO')),
  code TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  field_path TEXT NOT NULL,
  span_start INTEGER,
  span_end INTEGER,
  issue_ordinal INTEGER,
  message_key TEXT NOT NULL,
  message_params TEXT NOT NULL,
  fix_hint_key TEXT,
  evidence TEXT NOT NULL,
  PRIMARY KEY (run_id, ordinal),
  CHECK ((span_start IS NULL AND span_end IS NULL) OR (span_start >= 0 AND span_end > span_start)),
  CHECK (issue_ordinal IS NULL OR issue_ordinal >= 0)
);
CREATE INDEX validation_issues_fingerprint ON validation_issues(fingerprint);

CREATE TRIGGER validation_runs_immutable_update BEFORE UPDATE ON validation_runs BEGIN SELECT RAISE(ABORT, 'validation runs are immutable'); END;
CREATE TRIGGER validation_runs_immutable_delete BEFORE DELETE ON validation_runs BEGIN SELECT RAISE(ABORT, 'validation runs are immutable'); END;
CREATE TRIGGER validation_issues_immutable_update BEFORE UPDATE ON validation_issues BEGIN SELECT RAISE(ABORT, 'validation issues are immutable'); END;
CREATE TRIGGER validation_issues_immutable_delete BEFORE DELETE ON validation_issues BEGIN SELECT RAISE(ABORT, 'validation issues are immutable'); END;

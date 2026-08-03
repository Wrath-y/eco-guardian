PRAGMA foreign_keys = ON;

CREATE TABLE project_meta (
  id TEXT PRIMARY KEY NOT NULL,
  db_schema_version INTEGER NOT NULL CHECK (db_schema_version = 1),
  created_at TEXT NOT NULL
);

CREATE TABLE entity_blobs (
  hash TEXT PRIMARY KEY NOT NULL CHECK (length(hash) = 64),
  json BLOB NOT NULL
);

CREATE TABLE working_entities (
  id TEXT PRIMARY KEY NOT NULL,
  kind TEXT NOT NULL,
  entity_key TEXT NOT NULL,
  schema_version INTEGER NOT NULL,
  entity_version INTEGER NOT NULL,
  blob_hash TEXT NOT NULL REFERENCES entity_blobs(hash),
  status TEXT NOT NULL CHECK (status IN ('active', 'archived')),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX working_entities_active_kind_key ON working_entities(kind, entity_key) WHERE status = 'active';
CREATE INDEX working_entities_list ON working_entities(kind, status, entity_key, id);

CREATE TABLE entity_references (
  source_entity_id TEXT NOT NULL REFERENCES working_entities(id),
  field_path TEXT NOT NULL,
  ordinal INTEGER NOT NULL,
  expected_kind TEXT NOT NULL,
  target_entity_id TEXT NOT NULL,
  PRIMARY KEY (source_entity_id, field_path, ordinal)
);
CREATE INDEX entity_references_reverse ON entity_references(target_entity_id, source_entity_id);

CREATE TABLE entity_tags (
  entity_id TEXT NOT NULL REFERENCES working_entities(id),
  tag_id TEXT NOT NULL,
  PRIMARY KEY (entity_id, tag_id)
);

CREATE TABLE config_revisions (
  id TEXT PRIMARY KEY NOT NULL,
  display_revision INTEGER NOT NULL UNIQUE,
  config_hash TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE revision_entities (
  revision_id TEXT NOT NULL REFERENCES config_revisions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  entity_id TEXT NOT NULL,
  entity_version INTEGER NOT NULL,
  status TEXT NOT NULL,
  blob_hash TEXT NOT NULL REFERENCES entity_blobs(hash),
  PRIMARY KEY (revision_id, entity_id)
);

CREATE TRIGGER config_revisions_immutable_update BEFORE UPDATE ON config_revisions BEGIN SELECT RAISE(ABORT, 'revision history is immutable'); END;
CREATE TRIGGER config_revisions_immutable_delete BEFORE DELETE ON config_revisions BEGIN SELECT RAISE(ABORT, 'revision history is immutable'); END;
CREATE TRIGGER revision_entities_immutable_update BEFORE UPDATE ON revision_entities BEGIN SELECT RAISE(ABORT, 'revision history is immutable'); END;
CREATE TRIGGER revision_entities_immutable_delete BEFORE DELETE ON revision_entities BEGIN SELECT RAISE(ABORT, 'revision history is immutable'); END;

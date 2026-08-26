-- Immutable impact reports and mutable pre-seal staging. Graph evidence is a
-- derived audit projection and cannot mutate revisions or projection tables.
UPDATE project_meta SET db_schema_version=21;

ALTER TABLE graph_impact_handoffs ADD COLUMN base_revision_id TEXT REFERENCES config_revisions(id) ON DELETE RESTRICT ON UPDATE RESTRICT;
ALTER TABLE graph_impact_handoffs ADD COLUMN job_id TEXT REFERENCES jobs(id) ON DELETE RESTRICT ON UPDATE RESTRICT;
ALTER TABLE graph_impact_handoffs ADD COLUMN report_id TEXT;
ALTER TABLE graph_impact_handoffs ADD COLUMN safe_reason TEXT;
ALTER TABLE graph_impact_handoffs ADD COLUMN updated_at TEXT;

CREATE TABLE impact_staging (
  id TEXT PRIMARY KEY NOT NULL,
  job_id TEXT NOT NULL UNIQUE REFERENCES jobs(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  project_uuid TEXT NOT NULL REFERENCES project_meta(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  input_hash TEXT NOT NULL CHECK(length(input_hash)=64),
  input_json TEXT NOT NULL,
  phase TEXT NOT NULL,
  changed_json TEXT,
  affected_json TEXT,
  suspected_state TEXT,
  suspected_json TEXT,
  truncation_json TEXT NOT NULL DEFAULT '[]',
  warnings_json TEXT NOT NULL DEFAULT '[]',
  updated_at TEXT NOT NULL,
  UNIQUE(project_uuid,input_hash)
);

CREATE TABLE impact_reports (
  id TEXT PRIMARY KEY NOT NULL,
  project_uuid TEXT NOT NULL REFERENCES project_meta(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  base_revision_id TEXT NOT NULL REFERENCES config_revisions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  target_revision_id TEXT NOT NULL REFERENCES config_revisions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  input_hash TEXT NOT NULL CHECK(length(input_hash)=64),
  result_hash TEXT NOT NULL CHECK(length(result_hash)=64),
  analysis_contract_version TEXT NOT NULL,
  base_graph_hash TEXT NOT NULL CHECK(length(base_graph_hash)=64),
  target_graph_hash TEXT NOT NULL CHECK(length(target_graph_hash)=64),
  report_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(project_uuid,input_hash),
  UNIQUE(project_uuid,result_hash)
);

CREATE TABLE impact_changed_entities (
  report_id TEXT NOT NULL REFERENCES impact_reports(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  ordinal INTEGER NOT NULL CHECK(ordinal>=0),
  entity_id TEXT NOT NULL,
  entity_kind TEXT NOT NULL,
  change_kind TEXT NOT NULL,
  record_json TEXT NOT NULL,
  PRIMARY KEY(report_id,ordinal),
  UNIQUE(report_id,entity_id)
);

CREATE TABLE impact_affected_entities (
  report_id TEXT NOT NULL REFERENCES impact_reports(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  ordinal INTEGER NOT NULL CHECK(ordinal>=0),
  node_id TEXT NOT NULL,
  minimum_depth INTEGER NOT NULL CHECK(minimum_depth>0),
  record_json TEXT NOT NULL,
  PRIMARY KEY(report_id,ordinal),
  UNIQUE(report_id,node_id)
);

CREATE TABLE impact_paths (
  report_id TEXT NOT NULL REFERENCES impact_reports(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  expansion_hash TEXT NOT NULL,
  target_node_id TEXT NOT NULL,
  path_ordinal INTEGER NOT NULL CHECK(path_ordinal>=0),
  path_json TEXT NOT NULL,
  PRIMARY KEY(report_id,expansion_hash,target_node_id,path_ordinal)
);

CREATE TABLE impact_path_expansions (
  report_id TEXT NOT NULL REFERENCES impact_reports(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  expansion_hash TEXT NOT NULL CHECK(length(expansion_hash)=64),
  target_node_id TEXT NOT NULL,
  path_count INTEGER NOT NULL CHECK(path_count>=0),
  created_at TEXT NOT NULL,
  PRIMARY KEY(report_id,expansion_hash),
  UNIQUE(report_id,target_node_id,expansion_hash)
);

CREATE TABLE impact_suspected_evidence (
  report_id TEXT NOT NULL REFERENCES impact_reports(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  rank INTEGER NOT NULL CHECK(rank>0),
  node_id TEXT NOT NULL,
  evidence_json TEXT NOT NULL,
  PRIMARY KEY(report_id,rank),
  UNIQUE(report_id,node_id,rank)
);

CREATE TABLE impact_explanations (
  id TEXT PRIMARY KEY NOT NULL,
  report_id TEXT NOT NULL REFERENCES impact_reports(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  input_hash TEXT NOT NULL CHECK(length(input_hash)=64),
  status TEXT NOT NULL,
  explanation_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(report_id,input_hash)
);

CREATE INDEX impact_reports_history_lookup ON impact_reports(project_uuid,target_revision_id,created_at,id);
CREATE INDEX impact_reports_pair_lookup ON impact_reports(project_uuid,base_revision_id,target_revision_id,created_at,id);
CREATE INDEX impact_changed_page_lookup ON impact_changed_entities(report_id,ordinal);
CREATE INDEX impact_affected_page_lookup ON impact_affected_entities(report_id,ordinal);
CREATE INDEX impact_paths_target_lookup ON impact_paths(report_id,target_node_id,expansion_hash,path_ordinal);
CREATE INDEX impact_path_expansions_target_lookup ON impact_path_expansions(report_id,target_node_id,created_at,expansion_hash);

CREATE TRIGGER impact_reports_immutable_update BEFORE UPDATE ON impact_reports BEGIN SELECT RAISE(ABORT, 'impact report is immutable'); END;
CREATE TRIGGER impact_reports_immutable_delete BEFORE DELETE ON impact_reports BEGIN SELECT RAISE(ABORT, 'impact report is immutable'); END;
CREATE TRIGGER impact_changed_immutable_update BEFORE UPDATE ON impact_changed_entities BEGIN SELECT RAISE(ABORT, 'impact changed evidence is immutable'); END;
CREATE TRIGGER impact_changed_immutable_delete BEFORE DELETE ON impact_changed_entities BEGIN SELECT RAISE(ABORT, 'impact changed evidence is immutable'); END;
CREATE TRIGGER impact_affected_immutable_update BEFORE UPDATE ON impact_affected_entities BEGIN SELECT RAISE(ABORT, 'impact affected evidence is immutable'); END;
CREATE TRIGGER impact_affected_immutable_delete BEFORE DELETE ON impact_affected_entities BEGIN SELECT RAISE(ABORT, 'impact affected evidence is immutable'); END;
CREATE TRIGGER impact_paths_immutable_update BEFORE UPDATE ON impact_paths BEGIN SELECT RAISE(ABORT, 'impact path evidence is immutable'); END;
CREATE TRIGGER impact_paths_immutable_delete BEFORE DELETE ON impact_paths BEGIN SELECT RAISE(ABORT, 'impact path evidence is immutable'); END;
CREATE TRIGGER impact_path_expansions_immutable_update BEFORE UPDATE ON impact_path_expansions BEGIN SELECT RAISE(ABORT, 'impact path expansion is immutable'); END;
CREATE TRIGGER impact_path_expansions_immutable_delete BEFORE DELETE ON impact_path_expansions BEGIN SELECT RAISE(ABORT, 'impact path expansion is immutable'); END;
CREATE TRIGGER impact_suspected_immutable_update BEFORE UPDATE ON impact_suspected_evidence BEGIN SELECT RAISE(ABORT, 'impact suspected evidence is immutable'); END;
CREATE TRIGGER impact_suspected_immutable_delete BEFORE DELETE ON impact_suspected_evidence BEGIN SELECT RAISE(ABORT, 'impact suspected evidence is immutable'); END;

INSERT INTO schema_migration_steps(step_id,schema_version,committed_at)
VALUES('dependency-impact-v21',21,strftime('%Y-%m-%dT%H:%M:%fZ','now'));

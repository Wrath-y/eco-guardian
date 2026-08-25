-- Immutable balance-risk configuration and report facts. Mutable execution
-- state continues to live exclusively in the shared jobs/job_events tables.
UPDATE project_meta SET db_schema_version=16;

CREATE TABLE threshold_versions (
  id TEXT PRIMARY KEY NOT NULL,
  project_uuid TEXT NOT NULL REFERENCES project_meta(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  display_version INTEGER NOT NULL CHECK(display_version >= 0),
  origin TEXT NOT NULL CHECK(origin IN ('starter_template','starter','modified_starter')),
  enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
  schema_version TEXT NOT NULL,
  canonical_body TEXT NOT NULL,
  body_hash TEXT NOT NULL CHECK(length(body_hash)=64),
  created_by TEXT NOT NULL,
  created_at TEXT NOT NULL,
  activation_key TEXT,
  activation_request_hash TEXT CHECK(activation_request_hash IS NULL OR length(activation_request_hash)=64),
  CHECK((activation_key IS NULL) = (activation_request_hash IS NULL)),
  CHECK(origin != 'starter_template' OR (display_version=0 AND enabled=0 AND activation_key IS NULL))
);

CREATE TABLE risk_reviews (
  id TEXT PRIMARY KEY NOT NULL,
  job_id TEXT NOT NULL UNIQUE REFERENCES jobs(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  project_uuid TEXT NOT NULL REFERENCES project_meta(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  report_kind TEXT NOT NULL CHECK(report_kind IN ('calculation','decision')),
  source_report_id TEXT REFERENCES risk_reviews(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  input_hash TEXT NOT NULL CHECK(length(input_hash)=64),
  calculation_hash TEXT NOT NULL CHECK(length(calculation_hash)=64),
  evidence_hash TEXT NOT NULL CHECK(length(evidence_hash)=64),
  report_hash TEXT NOT NULL CHECK(length(report_hash)=64),
  candidate_revision_id TEXT NOT NULL REFERENCES config_revisions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  candidate_config_hash TEXT NOT NULL CHECK(length(candidate_config_hash)=64),
  candidate_manifest_hash TEXT NOT NULL CHECK(length(candidate_manifest_hash)=64),
  baseline_kind TEXT NOT NULL CHECK(baseline_kind IN ('BASELINE','NO_BASELINE')),
  baseline_release_id TEXT REFERENCES releases(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  baseline_revision_id TEXT REFERENCES config_revisions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  baseline_config_hash TEXT CHECK(baseline_config_hash IS NULL OR length(baseline_config_hash)=64),
  baseline_manifest_hash TEXT CHECK(baseline_manifest_hash IS NULL OR length(baseline_manifest_hash)=64),
  policy_id TEXT NOT NULL REFERENCES release_policies(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  policy_version TEXT NOT NULL,
  policy_hash TEXT NOT NULL CHECK(length(policy_hash)=64),
  threshold_id TEXT NOT NULL REFERENCES threshold_versions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  threshold_version TEXT NOT NULL,
  threshold_hash TEXT NOT NULL CHECK(length(threshold_hash)=64),
  validation_run_id TEXT NOT NULL REFERENCES validation_runs(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  validation_version TEXT NOT NULL,
  validation_hash TEXT NOT NULL CHECK(length(validation_hash)=64),
  implementation_refs TEXT NOT NULL,
  simulation_run_refs TEXT NOT NULL,
  canonical_report TEXT NOT NULL,
  decision_item_ids TEXT,
  decision_reason TEXT,
  status TEXT NOT NULL CHECK(status='SEALED'),
  created_at TEXT NOT NULL,
  CHECK(
    (baseline_kind='NO_BASELINE' AND baseline_release_id IS NULL AND baseline_revision_id IS NULL AND baseline_config_hash IS NULL AND baseline_manifest_hash IS NULL) OR
    (baseline_kind='BASELINE' AND baseline_release_id IS NOT NULL AND baseline_revision_id IS NOT NULL AND baseline_config_hash IS NOT NULL AND baseline_manifest_hash IS NOT NULL)
  ),
  CHECK(
    (report_kind='calculation' AND source_report_id IS NULL AND decision_item_ids IS NULL AND decision_reason IS NULL) OR
    (report_kind='decision' AND source_report_id IS NOT NULL AND decision_item_ids IS NOT NULL AND length(trim(decision_reason))>0)
  )
);

CREATE TABLE risk_items (
  report_id TEXT NOT NULL REFERENCES risk_reviews(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  ordinal INTEGER NOT NULL CHECK(ordinal >= 0),
  item_id TEXT NOT NULL,
  policy_role TEXT NOT NULL CHECK(policy_role IN ('required','optional')),
  comparison_status TEXT NOT NULL CHECK(comparison_status IN ('COMPARABLE','NOT_COMPARABLE','UNAVAILABLE','STALE')),
  severity TEXT CHECK(severity IS NULL OR severity IN ('BLOCK','WARNING','INFO')),
  rule_id TEXT NOT NULL,
  rule_version TEXT NOT NULL,
  rule_hash TEXT NOT NULL CHECK(length(rule_hash)=64),
  override_classification TEXT NOT NULL CHECK(override_classification IN ('NUMERIC_ELIGIBLE','NON_OVERRIDABLE')),
  evidence_hash TEXT NOT NULL CHECK(length(evidence_hash)=64),
  item_hash TEXT NOT NULL CHECK(length(item_hash)=64),
  canonical_item TEXT NOT NULL,
  PRIMARY KEY(report_id,ordinal),
  UNIQUE(report_id,item_id),
  CHECK(
    (comparison_status='COMPARABLE' AND severity IS NOT NULL) OR
    (comparison_status!='COMPARABLE' AND severity IS NULL AND override_classification='NON_OVERRIDABLE')
  )
);

CREATE TABLE risk_job_materializations (
  job_id TEXT PRIMARY KEY NOT NULL REFERENCES jobs(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  project_uuid TEXT NOT NULL REFERENCES project_meta(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  candidate_revision_id TEXT NOT NULL REFERENCES config_revisions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  canonical_input TEXT NOT NULL,
  input_hash TEXT NOT NULL CHECK(length(input_hash)=64),
  canonical_evidence_refs TEXT NOT NULL,
  evidence_hash TEXT NOT NULL CHECK(length(evidence_hash)=64),
  request_hash TEXT NOT NULL CHECK(length(request_hash)=64),
  cancel_generation INTEGER NOT NULL CHECK(cancel_generation >= 0),
  created_at TEXT NOT NULL
);

CREATE UNIQUE INDEX threshold_versions_project_display_unique ON threshold_versions(project_uuid,display_version);
CREATE UNIQUE INDEX threshold_versions_project_identity_unique ON threshold_versions(project_uuid,id,body_hash);
CREATE UNIQUE INDEX threshold_versions_activation_unique ON threshold_versions(project_uuid,activation_key) WHERE activation_key IS NOT NULL;
CREATE INDEX threshold_versions_body_history_lookup ON threshold_versions(project_uuid,body_hash,created_at,id);
CREATE INDEX threshold_versions_enabled_history_lookup ON threshold_versions(project_uuid,enabled,display_version,id);
CREATE INDEX risk_reviews_project_hash_lookup ON risk_reviews(project_uuid,report_hash);
CREATE INDEX risk_reviews_candidate_history_lookup ON risk_reviews(project_uuid,candidate_revision_id,created_at,id);
CREATE INDEX risk_reviews_baseline_history_lookup ON risk_reviews(project_uuid,baseline_release_id,created_at,id);
CREATE INDEX risk_reviews_policy_history_lookup ON risk_reviews(project_uuid,policy_id,policy_hash,created_at,id);
CREATE INDEX risk_reviews_threshold_history_lookup ON risk_reviews(project_uuid,threshold_id,threshold_hash,created_at,id);
CREATE INDEX risk_reviews_current_context_lookup ON risk_reviews(project_uuid,candidate_revision_id,baseline_release_id,policy_id,threshold_id,created_at,id);
CREATE INDEX risk_reviews_source_lookup ON risk_reviews(source_report_id,created_at,id);
CREATE INDEX risk_items_identity_lookup ON risk_items(report_id,item_id);
CREATE INDEX risk_materializations_recovery_lookup ON risk_job_materializations(project_uuid,candidate_revision_id,created_at,job_id);

CREATE TRIGGER threshold_versions_immutable_update BEFORE UPDATE ON threshold_versions BEGIN SELECT RAISE(ABORT,'threshold version is immutable'); END;
CREATE TRIGGER threshold_versions_immutable_delete BEFORE DELETE ON threshold_versions BEGIN SELECT RAISE(ABORT,'threshold version is immutable'); END;
CREATE TRIGGER risk_reviews_immutable_update BEFORE UPDATE ON risk_reviews BEGIN SELECT RAISE(ABORT,'risk review is immutable'); END;
CREATE TRIGGER risk_reviews_immutable_delete BEFORE DELETE ON risk_reviews BEGIN SELECT RAISE(ABORT,'risk review is immutable'); END;
CREATE TRIGGER risk_items_immutable_update BEFORE UPDATE ON risk_items BEGIN SELECT RAISE(ABORT,'risk item is immutable'); END;
CREATE TRIGGER risk_items_immutable_delete BEFORE DELETE ON risk_items BEGIN SELECT RAISE(ABORT,'risk item is immutable'); END;
CREATE TRIGGER risk_job_materializations_immutable_update BEFORE UPDATE ON risk_job_materializations BEGIN SELECT RAISE(ABORT,'risk job materialization is immutable'); END;
CREATE TRIGGER risk_job_materializations_immutable_delete BEFORE DELETE ON risk_job_materializations BEGIN SELECT RAISE(ABORT,'risk job materialization is immutable'); END;

INSERT INTO schema_migration_steps(step_id,schema_version,committed_at)
  VALUES('balance-risk-assessment-v16',16,strftime('%Y-%m-%dT%H:%M:%fZ','now'));

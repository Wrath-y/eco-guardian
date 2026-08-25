-- AI design durable facts. Shared jobs/job_events remain the sole work and
-- timeline protocol; these tables add bounded AI-specific identities and
-- immutable generation children around that shared root.
UPDATE project_meta SET db_schema_version=17;

CREATE TABLE ai_design_runs (
  job_id TEXT PRIMARY KEY NOT NULL REFERENCES jobs(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  project_uuid TEXT NOT NULL REFERENCES project_meta(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  base_revision_id TEXT NOT NULL REFERENCES config_revisions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  input_hash TEXT NOT NULL CHECK(length(input_hash)=64),
  canonical_input BLOB NOT NULL CHECK(length(canonical_input)>0 AND length(canonical_input)<=262144),
  phase TEXT NOT NULL CHECK(phase IN ('input_pinned','evidence_pinned','provider/tool_loop','deterministic_preview','patch_sealed')),
  owner TEXT CHECK(owner IS NULL OR (length(owner)>0 AND length(owner)<=128)),
  checkpoint_phase TEXT CHECK(checkpoint_phase IS NULL OR checkpoint_phase IN ('input_pinned','evidence_pinned','deterministic_preview','patch_sealed')),
  checkpoint_input_hash TEXT CHECK(checkpoint_input_hash IS NULL OR length(checkpoint_input_hash)=64),
  checkpoint_evidence_hash TEXT CHECK(checkpoint_evidence_hash IS NULL OR length(checkpoint_evidence_hash)=64),
  checkpoint_dependency_hash TEXT CHECK(checkpoint_dependency_hash IS NULL OR length(checkpoint_dependency_hash)=64),
  checkpoint_output_hash TEXT CHECK(checkpoint_output_hash IS NULL OR length(checkpoint_output_hash)=64),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  CHECK(
    (checkpoint_phase IS NULL AND checkpoint_input_hash IS NULL AND checkpoint_evidence_hash IS NULL AND checkpoint_dependency_hash IS NULL AND checkpoint_output_hash IS NULL) OR
    (checkpoint_phase IS NOT NULL AND checkpoint_input_hash IS NOT NULL AND checkpoint_evidence_hash IS NOT NULL AND checkpoint_dependency_hash IS NOT NULL AND checkpoint_output_hash IS NOT NULL)
  )
);

CREATE TABLE ai_attempts (
  attempt_id TEXT PRIMARY KEY NOT NULL,
  job_id TEXT NOT NULL REFERENCES ai_design_runs(job_id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  ordinal INTEGER NOT NULL CHECK(ordinal>0),
  attempt_kind TEXT NOT NULL CHECK(attempt_kind IN ('initial','repair','explicit_retry')),
  repair_round INTEGER NOT NULL CHECK(repair_round>=0 AND repair_round<=3),
  parent_job_id TEXT REFERENCES ai_design_runs(job_id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  parent_attempt_id TEXT REFERENCES ai_attempts(attempt_id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  canonical_manifest BLOB NOT NULL CHECK(length(canonical_manifest)>0 AND length(canonical_manifest)<=131072),
  manifest_hash TEXT NOT NULL CHECK(length(manifest_hash)=64),
  created_at TEXT NOT NULL,
  UNIQUE(job_id,ordinal),
  UNIQUE(job_id,manifest_hash),
  CHECK(
    (attempt_kind='initial' AND ordinal=1 AND repair_round=0 AND parent_job_id IS NULL AND parent_attempt_id IS NULL) OR
    (attempt_kind='repair' AND ordinal>1 AND repair_round>0 AND parent_job_id IS NULL AND parent_attempt_id IS NOT NULL) OR
    (attempt_kind='explicit_retry' AND ordinal=1 AND repair_round=0 AND parent_job_id IS NOT NULL AND parent_attempt_id IS NOT NULL)
  )
);

CREATE TABLE ai_attempt_responses (
  attempt_id TEXT PRIMARY KEY NOT NULL REFERENCES ai_attempts(attempt_id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  schema_id TEXT NOT NULL,
  schema_version TEXT NOT NULL,
  schema_hash TEXT NOT NULL CHECK(length(schema_hash)=64),
  original_body_hash TEXT NOT NULL CHECK(length(original_body_hash)=64),
  stored_body BLOB NOT NULL CHECK(length(stored_body)>0 AND length(stored_body)<=32768),
  stored_body_hash TEXT NOT NULL CHECK(length(stored_body_hash)=64),
  receipt_hash TEXT NOT NULL UNIQUE CHECK(length(receipt_hash)=64),
  created_at TEXT NOT NULL
);

CREATE TABLE ai_attempt_outcomes (
  attempt_id TEXT PRIMARY KEY NOT NULL REFERENCES ai_attempts(attempt_id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  outcome TEXT NOT NULL CHECK(outcome IN ('succeeded','failed','canceled','interrupted')),
  error_code TEXT CHECK(error_code IS NULL OR (length(error_code)>0 AND length(error_code)<=128)),
  outcome_hash TEXT NOT NULL UNIQUE CHECK(length(outcome_hash)=64),
  created_at TEXT NOT NULL,
  CHECK((outcome='succeeded' AND error_code IS NULL) OR (outcome!='succeeded' AND error_code IS NOT NULL))
);

CREATE TABLE ai_evidence_manifests (
  job_id TEXT PRIMARY KEY NOT NULL REFERENCES ai_design_runs(job_id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  attempt_id TEXT REFERENCES ai_attempts(attempt_id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  manifest_hash TEXT NOT NULL CHECK(length(manifest_hash)=64),
  request_hash TEXT NOT NULL CHECK(length(request_hash)=64),
  response_hash TEXT NOT NULL CHECK(length(response_hash)=64),
  canonical_manifest BLOB NOT NULL CHECK(length(canonical_manifest)>0 AND length(canonical_manifest)<=262144),
  created_at TEXT NOT NULL
);

CREATE TABLE ai_evidence_refs (
  job_id TEXT NOT NULL REFERENCES ai_design_runs(job_id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  ordinal INTEGER NOT NULL CHECK(ordinal>0 AND ordinal<=100),
  evidence_id TEXT NOT NULL CHECK(length(evidence_id)>0 AND length(evidence_id)<=256),
  attempt_id TEXT REFERENCES ai_attempts(attempt_id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  canonical_evidence BLOB NOT NULL CHECK(length(canonical_evidence)>0 AND length(canonical_evidence)<=32768),
  evidence_hash TEXT NOT NULL CHECK(length(evidence_hash)=64),
  created_at TEXT NOT NULL,
  PRIMARY KEY(job_id,ordinal),
  UNIQUE(job_id,evidence_id),
  UNIQUE(job_id,evidence_hash)
);

CREATE TABLE ai_blobs (
  content_hash TEXT PRIMARY KEY NOT NULL CHECK(length(content_hash)=64),
  media_type TEXT NOT NULL CHECK(length(media_type)>0 AND length(media_type)<=128),
  byte_size INTEGER NOT NULL CHECK(byte_size>0 AND byte_size<=1048576),
  body BLOB NOT NULL CHECK(length(body)=byte_size AND length(body)<=1048576),
  created_at TEXT NOT NULL
);

CREATE TABLE ai_draft_patches (
  id TEXT PRIMARY KEY NOT NULL,
  job_id TEXT NOT NULL UNIQUE REFERENCES ai_design_runs(job_id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  attempt_id TEXT NOT NULL UNIQUE REFERENCES ai_attempts(attempt_id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  base_revision_id TEXT NOT NULL REFERENCES config_revisions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  input_hash TEXT NOT NULL CHECK(length(input_hash)=64),
  patch_hash TEXT NOT NULL CHECK(length(patch_hash)=64),
  canonical_patch BLOB NOT NULL CHECK(length(canonical_patch)>0 AND length(canonical_patch)<=262144),
  raw_redacted_blob_hash TEXT REFERENCES ai_blobs(content_hash) ON DELETE RESTRICT ON UPDATE RESTRICT,
  diff_hash TEXT NOT NULL CHECK(length(diff_hash)=64),
  diff_blob_hash TEXT REFERENCES ai_blobs(content_hash) ON DELETE RESTRICT ON UPDATE RESTRICT,
  validation_hash TEXT NOT NULL CHECK(length(validation_hash)=64),
  preview_hash TEXT NOT NULL CHECK(length(preview_hash)=64),
  acceptability TEXT NOT NULL CHECK(acceptability IN ('pending','acceptable','failed')),
  created_at TEXT NOT NULL
);

CREATE TABLE ai_attempt_patch_seals (
  attempt_id TEXT PRIMARY KEY NOT NULL REFERENCES ai_attempts(attempt_id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  patch_id TEXT NOT NULL UNIQUE REFERENCES ai_draft_patches(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  patch_hash TEXT NOT NULL CHECK(length(patch_hash)=64),
  seal_hash TEXT NOT NULL UNIQUE CHECK(length(seal_hash)=64),
  created_at TEXT NOT NULL
);

CREATE TABLE ai_tool_calls (
  attempt_id TEXT NOT NULL REFERENCES ai_attempts(attempt_id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  ordinal INTEGER NOT NULL CHECK(ordinal>0 AND ordinal<=32),
  call_id TEXT NOT NULL,
  tool_id TEXT NOT NULL,
  tool_version TEXT NOT NULL,
  tool_hash TEXT NOT NULL CHECK(length(tool_hash)=64),
  input_hash TEXT NOT NULL CHECK(length(input_hash)=64),
  result_hash TEXT CHECK(result_hash IS NULL OR length(result_hash)=64),
  input_blob_hash TEXT REFERENCES ai_blobs(content_hash) ON DELETE RESTRICT ON UPDATE RESTRICT,
  result_blob_hash TEXT REFERENCES ai_blobs(content_hash) ON DELETE RESTRICT ON UPDATE RESTRICT,
  error_code TEXT CHECK(error_code IS NULL OR (length(error_code)>0 AND length(error_code)<=128)),
  created_at TEXT NOT NULL,
  completed_at TEXT,
  PRIMARY KEY(attempt_id,ordinal),
  UNIQUE(attempt_id,call_id),
  CHECK((result_hash IS NULL AND completed_at IS NULL) OR (result_hash IS NOT NULL AND completed_at IS NOT NULL))
);

CREATE TABLE ai_audit_events (
  attempt_id TEXT NOT NULL REFERENCES ai_attempts(attempt_id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  ordinal INTEGER NOT NULL CHECK(ordinal>0 AND ordinal<=512),
  event_kind TEXT NOT NULL CHECK(length(event_kind)>0 AND length(event_kind)<=64),
  canonical_event BLOB NOT NULL CHECK(length(canonical_event)>0 AND length(canonical_event)<=65536),
  event_hash TEXT NOT NULL CHECK(length(event_hash)=64),
  created_at TEXT NOT NULL,
  PRIMARY KEY(attempt_id,ordinal),
  UNIQUE(attempt_id,event_hash)
);

CREATE TABLE ai_job_event_details (
  job_id TEXT NOT NULL,
  event_ordinal INTEGER NOT NULL CHECK(event_ordinal>0),
  event_key TEXT NOT NULL CHECK(length(event_key)>0 AND length(event_key)<=128),
  event_kind TEXT NOT NULL CHECK(event_kind IN ('stage','progress','warning','tool','repair','ignored_late_result','terminal')),
  attempt_id TEXT REFERENCES ai_attempts(attempt_id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  warning_code TEXT CHECK(warning_code IS NULL OR (length(warning_code)>0 AND length(warning_code)<=128)),
  warning_ref TEXT CHECK(warning_ref IS NULL OR (length(warning_ref)>0 AND length(warning_ref)<=256)),
  tool_id TEXT,
  tool_version TEXT,
  tool_hash TEXT CHECK(tool_hash IS NULL OR length(tool_hash)=64),
  repair_count INTEGER CHECK(repair_count IS NULL OR (repair_count>=1 AND repair_count<=3)),
  outcome TEXT CHECK(outcome IS NULL OR outcome IN ('succeeded','failed','canceled','interrupted','ignored_late_result')),
  canonical_event BLOB NOT NULL CHECK(length(canonical_event)>0 AND length(canonical_event)<=8192),
  event_hash TEXT NOT NULL CHECK(length(event_hash)=64),
  PRIMARY KEY(job_id,event_ordinal),
  UNIQUE(job_id,event_key),
  FOREIGN KEY(job_id,event_ordinal) REFERENCES job_events(job_id,event_ordinal) ON DELETE RESTRICT ON UPDATE RESTRICT,
  CHECK((tool_id IS NULL AND tool_version IS NULL AND tool_hash IS NULL) OR (tool_id IS NOT NULL AND tool_version IS NOT NULL AND tool_hash IS NOT NULL))
);

CREATE TABLE ai_patch_decisions (
  id TEXT PRIMARY KEY NOT NULL,
  patch_id TEXT NOT NULL UNIQUE REFERENCES ai_draft_patches(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  project_uuid TEXT NOT NULL REFERENCES project_meta(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  decision TEXT NOT NULL CHECK(decision IN ('accepted','discarded')),
  idempotency_key TEXT NOT NULL CHECK(length(idempotency_key)>0 AND length(idempotency_key)<=256),
  request_hash TEXT NOT NULL CHECK(length(request_hash)=64),
  patch_hash TEXT NOT NULL CHECK(length(patch_hash)=64),
  expected_versions_hash TEXT NOT NULL CHECK(length(expected_versions_hash)=64),
  accepted_revision_id TEXT REFERENCES config_revisions(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  decision_hash TEXT NOT NULL UNIQUE CHECK(length(decision_hash)=64),
  created_at TEXT NOT NULL,
  UNIQUE(project_uuid,idempotency_key),
  CHECK((decision='accepted' AND accepted_revision_id IS NOT NULL) OR (decision='discarded' AND accepted_revision_id IS NULL))
);

CREATE INDEX ai_runs_recovery_lookup ON ai_design_runs(project_uuid,phase,updated_at,job_id);
CREATE INDEX ai_attempts_lineage_lookup ON ai_attempts(parent_job_id,parent_attempt_id,attempt_id);
CREATE INDEX ai_evidence_attempt_lookup ON ai_evidence_refs(attempt_id,ordinal);
CREATE INDEX ai_evidence_manifest_hash_lookup ON ai_evidence_manifests(manifest_hash,job_id);
CREATE INDEX ai_patches_history_lookup ON ai_draft_patches(base_revision_id,created_at,id);
CREATE INDEX ai_tools_identity_lookup ON ai_tool_calls(tool_id,tool_version,tool_hash);
CREATE INDEX ai_audit_kind_lookup ON ai_audit_events(attempt_id,event_kind,ordinal);
CREATE INDEX ai_decisions_revision_lookup ON ai_patch_decisions(accepted_revision_id,created_at,id);

INSERT INTO schema_migration_steps(step_id,schema_version,committed_at)
  VALUES('ai-design-persistence-v17',17,strftime('%Y-%m-%dT%H:%M:%fZ','now'));

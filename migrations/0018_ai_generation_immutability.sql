-- Seal AI generation facts while preserving the deliberately mutable run and
-- in-flight tool checkpoint lanes.
UPDATE project_meta SET db_schema_version=18;

CREATE TRIGGER ai_design_runs_identity_immutable BEFORE UPDATE ON ai_design_runs
WHEN NEW.job_id!=OLD.job_id OR NEW.project_uuid!=OLD.project_uuid OR NEW.base_revision_id!=OLD.base_revision_id OR
     NEW.input_hash!=OLD.input_hash OR NEW.canonical_input!=OLD.canonical_input OR NEW.created_at!=OLD.created_at OR
     (CASE NEW.phase WHEN 'input_pinned' THEN 0 WHEN 'evidence_pinned' THEN 1 WHEN 'provider/tool_loop' THEN 2 WHEN 'deterministic_preview' THEN 3 WHEN 'patch_sealed' THEN 4 END) <
     (CASE OLD.phase WHEN 'input_pinned' THEN 0 WHEN 'evidence_pinned' THEN 1 WHEN 'provider/tool_loop' THEN 2 WHEN 'deterministic_preview' THEN 3 WHEN 'patch_sealed' THEN 4 END)
BEGIN SELECT RAISE(ABORT,'AI design run identity or phase is immutable'); END;

CREATE TRIGGER ai_design_runs_delete_restrict BEFORE DELETE ON ai_design_runs
BEGIN SELECT RAISE(ABORT,'AI design run cannot be deleted'); END;

CREATE TRIGGER ai_tool_calls_transition_guard BEFORE UPDATE ON ai_tool_calls
WHEN NEW.attempt_id!=OLD.attempt_id OR NEW.ordinal!=OLD.ordinal OR NEW.call_id!=OLD.call_id OR NEW.tool_id!=OLD.tool_id OR
     NEW.tool_version!=OLD.tool_version OR NEW.tool_hash!=OLD.tool_hash OR NEW.input_hash!=OLD.input_hash OR
     NEW.input_blob_hash IS NOT OLD.input_blob_hash OR NEW.created_at!=OLD.created_at OR
     OLD.completed_at IS NOT NULL OR NEW.completed_at IS NULL OR NEW.result_hash IS NULL
BEGIN SELECT RAISE(ABORT,'AI tool call transition is invalid'); END;

CREATE TRIGGER ai_tool_calls_delete_restrict BEFORE DELETE ON ai_tool_calls
BEGIN SELECT RAISE(ABORT,'AI tool call cannot be deleted'); END;

CREATE TRIGGER ai_attempts_immutable_update BEFORE UPDATE ON ai_attempts BEGIN SELECT RAISE(ABORT,'AI attempt is immutable'); END;
CREATE TRIGGER ai_attempts_immutable_delete BEFORE DELETE ON ai_attempts BEGIN SELECT RAISE(ABORT,'AI attempt is immutable'); END;
CREATE TRIGGER ai_attempt_responses_immutable_update BEFORE UPDATE ON ai_attempt_responses BEGIN SELECT RAISE(ABORT,'AI attempt response is immutable'); END;
CREATE TRIGGER ai_attempt_responses_immutable_delete BEFORE DELETE ON ai_attempt_responses BEGIN SELECT RAISE(ABORT,'AI attempt response is immutable'); END;
CREATE TRIGGER ai_attempt_outcomes_immutable_update BEFORE UPDATE ON ai_attempt_outcomes BEGIN SELECT RAISE(ABORT,'AI attempt outcome is immutable'); END;
CREATE TRIGGER ai_attempt_outcomes_immutable_delete BEFORE DELETE ON ai_attempt_outcomes BEGIN SELECT RAISE(ABORT,'AI attempt outcome is immutable'); END;
CREATE TRIGGER ai_evidence_manifests_immutable_update BEFORE UPDATE ON ai_evidence_manifests BEGIN SELECT RAISE(ABORT,'AI evidence manifest is immutable'); END;
CREATE TRIGGER ai_evidence_manifests_immutable_delete BEFORE DELETE ON ai_evidence_manifests BEGIN SELECT RAISE(ABORT,'AI evidence manifest is immutable'); END;
CREATE TRIGGER ai_evidence_refs_immutable_update BEFORE UPDATE ON ai_evidence_refs BEGIN SELECT RAISE(ABORT,'AI evidence ref is immutable'); END;
CREATE TRIGGER ai_evidence_refs_immutable_delete BEFORE DELETE ON ai_evidence_refs BEGIN SELECT RAISE(ABORT,'AI evidence ref is immutable'); END;
CREATE TRIGGER ai_blobs_immutable_update BEFORE UPDATE ON ai_blobs BEGIN SELECT RAISE(ABORT,'AI blob is immutable'); END;
CREATE TRIGGER ai_blobs_immutable_delete BEFORE DELETE ON ai_blobs BEGIN SELECT RAISE(ABORT,'AI blob is immutable'); END;
CREATE TRIGGER ai_draft_patches_immutable_update BEFORE UPDATE ON ai_draft_patches BEGIN SELECT RAISE(ABORT,'AI DraftPatch is immutable'); END;
CREATE TRIGGER ai_draft_patches_immutable_delete BEFORE DELETE ON ai_draft_patches BEGIN SELECT RAISE(ABORT,'AI DraftPatch is immutable'); END;
CREATE TRIGGER ai_attempt_patch_seals_immutable_update BEFORE UPDATE ON ai_attempt_patch_seals BEGIN SELECT RAISE(ABORT,'AI Patch seal is immutable'); END;
CREATE TRIGGER ai_attempt_patch_seals_immutable_delete BEFORE DELETE ON ai_attempt_patch_seals BEGIN SELECT RAISE(ABORT,'AI Patch seal is immutable'); END;
CREATE TRIGGER ai_audit_events_immutable_update BEFORE UPDATE ON ai_audit_events BEGIN SELECT RAISE(ABORT,'AI audit event is immutable'); END;
CREATE TRIGGER ai_audit_events_immutable_delete BEFORE DELETE ON ai_audit_events BEGIN SELECT RAISE(ABORT,'AI audit event is immutable'); END;
CREATE TRIGGER ai_job_event_details_immutable_update BEFORE UPDATE ON ai_job_event_details BEGIN SELECT RAISE(ABORT,'AI Job event detail is immutable'); END;
CREATE TRIGGER ai_job_event_details_immutable_delete BEFORE DELETE ON ai_job_event_details BEGIN SELECT RAISE(ABORT,'AI Job event detail is immutable'); END;
CREATE TRIGGER ai_patch_decisions_immutable_update BEFORE UPDATE ON ai_patch_decisions BEGIN SELECT RAISE(ABORT,'AI Patch decision is immutable'); END;
CREATE TRIGGER ai_patch_decisions_immutable_delete BEFORE DELETE ON ai_patch_decisions BEGIN SELECT RAISE(ABORT,'AI Patch decision is immutable'); END;

CREATE TRIGGER ai_shared_job_events_immutable_update BEFORE UPDATE ON job_events
WHEN EXISTS(SELECT 1 FROM ai_design_runs WHERE job_id=OLD.job_id)
BEGIN SELECT RAISE(ABORT,'AI shared Job event is immutable'); END;
CREATE TRIGGER ai_shared_job_events_immutable_delete BEFORE DELETE ON job_events
WHEN EXISTS(SELECT 1 FROM ai_design_runs WHERE job_id=OLD.job_id)
BEGIN SELECT RAISE(ABORT,'AI shared Job event is immutable'); END;

INSERT INTO schema_migration_steps(step_id,schema_version,committed_at)
  VALUES('ai-generation-immutability-v18',18,strftime('%Y-%m-%dT%H:%M:%fZ','now'));

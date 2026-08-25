UPDATE project_meta SET db_schema_version=19;

ALTER TABLE ai_patch_decisions ADD COLUMN actor TEXT NOT NULL DEFAULT 'local-user'
  CHECK(length(actor)>0 AND length(actor)<=128);
ALTER TABLE ai_patch_decisions ADD COLUMN reason TEXT
  CHECK(reason IS NULL OR (length(trim(reason))>0 AND length(reason)<=2000));

CREATE TRIGGER ai_patch_decisions_shape_insert BEFORE INSERT ON ai_patch_decisions
WHEN (NEW.decision='accepted' AND NEW.reason IS NOT NULL)
  OR (NEW.decision='discarded' AND NEW.accepted_revision_id IS NOT NULL)
BEGIN
  SELECT RAISE(ABORT,'invalid AI Patch decision shape');
END;

INSERT INTO schema_migration_steps(step_id,schema_version,committed_at)
  VALUES('ai-patch-decisions-v19',19,strftime('%Y-%m-%dT%H:%M:%fZ','now'));

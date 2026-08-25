UPDATE project_meta SET db_schema_version=20;

ALTER TABLE ai_draft_patches ADD COLUMN canonical_preview BLOB
  CHECK(canonical_preview IS NULL OR (length(canonical_preview)>0 AND length(canonical_preview)<=262144));
ALTER TABLE ai_draft_patches ADD COLUMN preview_issues BLOB NOT NULL DEFAULT '[]'
  CHECK(json_valid(preview_issues) AND json_type(preview_issues)='array' AND length(preview_issues)<=262144);

CREATE TRIGGER ai_draft_patch_preview_required_insert BEFORE INSERT ON ai_draft_patches
WHEN NEW.canonical_preview IS NULL
BEGIN
  SELECT RAISE(ABORT,'AI DraftPatch preview facts are required');
END;

INSERT INTO schema_migration_steps(step_id,schema_version,committed_at)
  VALUES('ai-preview-facts-v20',20,strftime('%Y-%m-%dT%H:%M:%fZ','now'));

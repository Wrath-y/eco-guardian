package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
)

var ErrReleaseNotFound = errors.New("release not found")

// RestoreRelease creates a forward rollback revision from a readable release.
// It never changes the source release/revision and always emits a new LOCAL
// result against the restored working state.
func (s *Store) RestoreRelease(ctx context.Context, currentWorkingRevision, sourceReleaseID domain.ID) (domain.RevisionSummary, error) {
	if !currentWorkingRevision.Valid() || !sourceReleaseID.Valid() {
		return domain.RevisionSummary{}, ErrRevisionConflict
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.RevisionSummary{}, err
	}
	defer tx.Rollback()
	var latest, sourceRevisionID domain.ID
	if err = tx.QueryRowContext(ctx, `SELECT id FROM config_revisions ORDER BY display_revision DESC,id DESC LIMIT 1`).Scan(&latest); errors.Is(err, sql.ErrNoRows) || latest != currentWorkingRevision {
		return domain.RevisionSummary{}, ErrRevisionConflict
	} else if err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT revision_id FROM releases WHERE id=?`, sourceReleaseID).Scan(&sourceRevisionID); errors.Is(err, sql.ErrNoRows) {
		return domain.RevisionSummary{}, ErrReleaseNotFound
	} else if err != nil {
		return domain.RevisionSummary{}, err
	}
	entities, err := materializeRevisionEntitiesTx(ctx, tx, sourceRevisionID)
	if err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = replaceWorkingEntitiesTx(ctx, tx, entities); err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = s.inject("restore_working"); err != nil {
		return domain.RevisionSummary{}, err
	}
	versions, err := currentValidationVersionManifest()
	if err != nil {
		return domain.RevisionSummary{}, err
	}
	revision, err := s.writeRevision(ctx, tx)
	if err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = s.inject("restore_revision"); err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = s.writeRevisionMetadata(ctx, tx, revision, versions, revisionMetadataFields{parentRevisionID: currentWorkingRevision, sourceRevisionID: sourceRevisionID, sourceReleaseID: sourceReleaseID}); err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = s.inject("restore_metadata"); err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = s.persistRevisionDerived(ctx, tx, revision.ID); err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = s.inject("restore_derived"); err != nil {
		return domain.RevisionSummary{}, err
	}
	summary, err := s.persistLocalValidationForEntities(ctx, tx, entities, revision, versions)
	if err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = s.inject("restore_validation"); err != nil {
		return domain.RevisionSummary{}, err
	}
	revision.Validation = &summary
	if err = tx.Commit(); err != nil {
		return domain.RevisionSummary{}, err
	}
	return revision, nil
}

func replaceWorkingEntitiesTx(ctx context.Context, tx *sql.Tx, entities []domain.Entity) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM entity_references`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM entity_tags`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM working_entities`); err != nil {
		return err
	}
	for _, entity := range entities {
		hash, _, err := domain.BlobHash(entity)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO working_entities(id,kind,entity_key,schema_version,entity_version,blob_hash,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, entity.ID, entity.Kind, entity.Key, entity.SchemaVersion, entity.EntityVersion, hash, entity.Status, entity.CreatedAt.UTC().Format(time.RFC3339Nano), entity.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
		refs, tags := domain.ExtractIndexes(entity)
		for _, reference := range refs {
			if _, err = tx.ExecContext(ctx, `INSERT INTO entity_references(source_entity_id,field_path,ordinal,expected_kind,target_entity_id) VALUES(?,?,?,?,?)`, reference.SourceID, reference.FieldPath, reference.Ordinal, reference.ExpectedKind, reference.TargetID); err != nil {
				return err
			}
		}
		for _, tag := range tags {
			if _, err = tx.ExecContext(ctx, `INSERT INTO entity_tags(entity_id,tag_id) VALUES(?,?)`, tag.EntityID, tag.TagID); err != nil {
				return err
			}
		}
	}
	return nil
}

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
)

var ErrGraphProjectionRevisionInvalid = errors.New("graph projection revision is invalid or unreadable")

// ReadProjectionRevision is the SQLite adapter for projector.RevisionReader.
// It reads only immutable revision rows in one read transaction and returns
// detached values, including archived entities and revision-scoped references.
func (s *Store) ReadProjectionRevision(ctx context.Context, revisionID domain.ID) (projector.Revision, error) {
	if !revisionID.Valid() || !s.projectID.Valid() {
		return projector.Revision{}, ErrGraphProjectionRevisionInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return projector.Revision{}, err
	}
	defer tx.Rollback()

	var configHash string
	if err = tx.QueryRowContext(ctx, `SELECT config_hash FROM config_revisions WHERE id=?`, revisionID).Scan(&configHash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return projector.Revision{}, ErrGraphProjectionRevisionInvalid
		}
		return projector.Revision{}, err
	}
	result := projector.Revision{ProjectID: s.projectID, RevisionID: revisionID, ConfigHash: configHash}

	rows, err := tx.QueryContext(ctx, `SELECT r.entity_id,r.entity_version,r.status,b.json FROM revision_entities r JOIN entity_blobs b ON b.hash=r.blob_hash WHERE r.revision_id=? ORDER BY r.entity_id`, revisionID)
	if err != nil {
		return projector.Revision{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id domain.ID
		var version int64
		var status domain.EntityStatus
		var raw []byte
		if err = rows.Scan(&id, &version, &status, &raw); err != nil {
			return projector.Revision{}, err
		}
		var entity domain.Entity
		if err = json.Unmarshal(raw, &entity); err != nil {
			return projector.Revision{}, fmt.Errorf("decode immutable graph entity: %w", err)
		}
		if entity.ID != id || entity.EntityVersion != version || entity.Status != status || !entity.Kind.Valid() || entity.SchemaVersion < 1 {
			return projector.Revision{}, ErrGraphProjectionRevisionInvalid
		}
		result.Entities = append(result.Entities, entity)
	}
	if err = rows.Err(); err != nil {
		return projector.Revision{}, err
	}

	refs, err := tx.QueryContext(ctx, `SELECT source_entity_id,field_path,ordinal,expected_kind,target_entity_id FROM revision_references WHERE revision_id=? ORDER BY source_entity_id,field_path,ordinal`, revisionID)
	if err != nil {
		return projector.Revision{}, err
	}
	defer refs.Close()
	for refs.Next() {
		var reference projector.Reference
		if err = refs.Scan(&reference.SourceID, &reference.FieldPath, &reference.Ordinal, &reference.TargetKind, &reference.TargetID); err != nil {
			return projector.Revision{}, err
		}
		if !reference.SourceID.Valid() || !reference.TargetID.Valid() || !reference.TargetKind.Valid() || reference.FieldPath == "" || reference.Ordinal < 0 {
			return projector.Revision{}, ErrGraphProjectionRevisionInvalid
		}
		result.References = append(result.References, reference)
	}
	if err = refs.Err(); err != nil {
		return projector.Revision{}, err
	}
	if err = tx.Commit(); err != nil {
		return projector.Revision{}, err
	}
	return result, nil
}

var _ projector.RevisionReader = (*Store)(nil)

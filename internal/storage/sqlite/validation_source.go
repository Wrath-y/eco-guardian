package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/validation"
)

// MaterializeValidationSource reads all entity blobs within one SQLite read
// transaction, then returns detached values. Later writes therefore cannot
// change the input of the already materialized validation run.
func (s *Store) MaterializeValidationSource(ctx context.Context, kind validation.SourceKind, revisionID domain.ID) (validation.MaterializedSource, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return validation.MaterializedSource{}, err
	}
	defer tx.Rollback()
	var rows *sql.Rows
	var source validation.Source
	if kind == validation.SourceWorking {
		rows, err = tx.QueryContext(ctx, `SELECT w.id,w.entity_version,w.status,w.blob_hash,b.json FROM working_entities w JOIN entity_blobs b ON b.hash=w.blob_hash ORDER BY w.id`)
	} else if kind == validation.SourceRevision {
		if !revisionID.Valid() {
			return validation.MaterializedSource{}, fmt.Errorf("invalid revision id")
		}
		var configHash string
		if err = tx.QueryRowContext(ctx, `SELECT config_hash FROM config_revisions WHERE id=?`, revisionID).Scan(&configHash); err != nil {
			if err == sql.ErrNoRows {
				return validation.MaterializedSource{}, ErrNotFound
			}
			return validation.MaterializedSource{}, err
		}
		source, err = validation.NewSource(validation.SourceRevision, revisionID, configHash)
		if err != nil {
			return validation.MaterializedSource{}, err
		}
		rows, err = tx.QueryContext(ctx, `SELECT r.entity_id,r.entity_version,r.status,r.blob_hash,b.json FROM revision_entities r JOIN entity_blobs b ON b.hash=r.blob_hash WHERE r.revision_id=? ORDER BY r.entity_id`, revisionID)
	} else {
		return validation.MaterializedSource{}, fmt.Errorf("invalid source kind")
	}
	if err != nil {
		return validation.MaterializedSource{}, err
	}
	defer rows.Close()
	entities := []domain.Entity{}
	manifest := strings.Builder{}
	for rows.Next() {
		var id domain.ID
		var version int64
		var status domain.EntityStatus
		var hash string
		var raw []byte
		if err = rows.Scan(&id, &version, &status, &hash, &raw); err != nil {
			return validation.MaterializedSource{}, err
		}
		var entity domain.Entity
		if err = json.Unmarshal(raw, &entity); err != nil {
			return validation.MaterializedSource{}, err
		}
		entities = append(entities, entity)
		fmt.Fprintf(&manifest, "%s:%d:%s:%s\n", id, version, status, hash)
	}
	if err = rows.Err(); err != nil {
		return validation.MaterializedSource{}, err
	}
	if kind == validation.SourceWorking {
		sum := sha256.Sum256([]byte(manifest.String()))
		source, err = validation.NewSource(validation.SourceWorking, "", fmt.Sprintf("%x", sum))
		if err != nil {
			return validation.MaterializedSource{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return validation.MaterializedSource{}, err
	}
	return validation.MaterializedSource{Source: source, Entities: entities}, nil
}

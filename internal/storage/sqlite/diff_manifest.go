package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioningdiff "github.com/zouyi/eco-guardian/internal/versioning/diff"
)

var ErrDiffRevisionInvalid = errors.New("diff revision is invalid or unreadable")

// ActiveBaseline implements diff.ActiveBaselineReader. It follows only the
// active release pointer; the absence of that pointer is a first-release
// condition, not permission to use the newest or working revision.
func (s *Store) ActiveBaseline(ctx context.Context) (domain.ID, bool, error) {
	var revisionID domain.ID
	err := s.db.QueryRowContext(ctx, `SELECT r.revision_id FROM active_release_pointer p JOIN releases r ON r.id=p.active_release_id WHERE p.singleton=1 AND p.active_release_id IS NOT NULL`).Scan(&revisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !revisionID.Valid() {
		return "", false, ErrDiffRevisionInvalid
	}
	return revisionID, true, nil
}

// Materialize implements diff.ManifestReader using only an immutable revision
// manifest. It never substitutes working state or another revision when an ID
// is absent or unreadable.
func (s *Store) Materialize(ctx context.Context, revisionID domain.ID) ([]versioningdiff.EntityBlob, error) {
	return s.MaterializeSegment(ctx, revisionID, "", 100_000)
}

// MaterializeSegment implements diff.ManifestSegmentReader with a bounded,
// sorted read from immutable manifest rows. It neither takes the serialized
// save lock nor reads working_entities.
func (s *Store) MaterializeSegment(ctx context.Context, revisionID, afterEntityID domain.ID, limit int) ([]versioningdiff.EntityBlob, error) {
	if !revisionID.Valid() {
		return nil, ErrDiffRevisionInvalid
	}
	if afterEntityID != "" && !afterEntityID.Valid() || limit < 1 {
		return nil, ErrDiffRevisionInvalid
	}
	if _, err := s.GetRevisionRecord(ctx, revisionID); err != nil {
		if errors.Is(err, ErrRevisionNotFound) {
			return nil, ErrDiffRevisionInvalid
		}
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.entity_id,r.status,b.json FROM revision_entities r JOIN entity_blobs b ON b.hash=r.blob_hash WHERE r.revision_id=? AND r.entity_id>? ORDER BY r.entity_id LIMIT ?`, revisionID, afterEntityID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entities := make([]versioningdiff.EntityBlob, 0)
	for rows.Next() {
		var id domain.ID
		var status domain.EntityStatus
		var raw []byte
		if err := rows.Scan(&id, &status, &raw); err != nil {
			return nil, err
		}
		var entity domain.Entity
		if err := json.Unmarshal(raw, &entity); err != nil {
			return nil, fmt.Errorf("decode immutable revision entity: %w", err)
		}
		if entity.ID != id || entity.Status != status || !entity.Kind.Valid() {
			return nil, ErrDiffRevisionInvalid
		}
		entities = append(entities, versioningdiff.EntityBlob{EntityID: id, Kind: entity.Kind, Status: status, JSON: append(json.RawMessage(nil), raw...)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entities, nil
}

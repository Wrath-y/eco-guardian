package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioningrelease "github.com/zouyi/eco-guardian/internal/versioning/release"
)

var _ versioningrelease.CommandSources = (*Store)(nil)

// GetRelease is an immutable release lookup used by release-command
// validation. It is intentionally read-only and does not infer a baseline
// from the active pointer.
func (s *Store) GetRelease(ctx context.Context, releaseID domain.ID) (versioningrelease.Release, error) {
	if !releaseID.Valid() {
		return versioningrelease.Release{}, ErrReleaseNotFound
	}
	var id, revisionID, policyID, intentID, createdRaw string
	err := s.db.QueryRowContext(ctx, `SELECT id,revision_id,policy_id,intent_id,created_at FROM releases WHERE id=?`, releaseID).Scan(&id, &revisionID, &policyID, &intentID, &createdRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return versioningrelease.Release{}, ErrReleaseNotFound
	}
	if err != nil {
		return versioningrelease.Release{}, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdRaw)
	if err != nil {
		return versioningrelease.Release{}, err
	}
	release := versioningrelease.Release{ID: domain.ID(id), RevisionID: domain.ID(revisionID), PolicyID: domain.ID(policyID), IntentID: domain.ID(intentID), CreatedAt: createdAt}
	if !release.Valid() {
		return versioningrelease.Release{}, ErrReleaseNotFound
	}
	return release, nil
}

// GetActivePointer reads the singleton pointer without mutating its
// generation. A missing row is an adapter failure, never a first release.
func (s *Store) GetActivePointer(ctx context.Context) (versioningrelease.ActivePointer, error) {
	var releaseID sql.NullString
	var generation int64
	err := s.db.QueryRowContext(ctx, `SELECT active_release_id,generation FROM active_release_pointer WHERE singleton=1`).Scan(&releaseID, &generation)
	if err != nil {
		return versioningrelease.ActivePointer{}, err
	}
	pointer := versioningrelease.ActivePointer{Generation: generation}
	if releaseID.Valid {
		pointer.ReleaseID = domain.ID(releaseID.String)
	}
	if !pointer.Valid() {
		return versioningrelease.ActivePointer{}, errors.New("invalid active release pointer")
	}
	return pointer, nil
}

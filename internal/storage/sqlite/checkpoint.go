package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
)

var ErrInvalidCheckpoint = errors.New("invalid checkpoint")

// CreateCheckpoint creates a new immutable revision for the exact current
// working manifest. It intentionally writes a fresh LOCAL run, rather than
// reusing a potentially stale result or labelling it FULL.
func (s *Store) CreateCheckpoint(ctx context.Context, currentWorkingRevision domain.ID, name, description string) (domain.RevisionSummary, error) {
	if !currentWorkingRevision.Valid() {
		return domain.RevisionSummary{}, ErrRevisionConflict
	}
	name = strings.TrimSpace(name)
	if len(name) > 200 || len(description) > 10000 {
		return domain.RevisionSummary{}, ErrInvalidCheckpoint
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.RevisionSummary{}, err
	}
	defer tx.Rollback()
	var latest domain.ID
	if err = tx.QueryRowContext(ctx, `SELECT id FROM config_revisions ORDER BY display_revision DESC,id DESC LIMIT 1`).Scan(&latest); errors.Is(err, sql.ErrNoRows) || latest != currentWorkingRevision {
		return domain.RevisionSummary{}, ErrRevisionConflict
	} else if err != nil {
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
	if err = s.inject("checkpoint_revision"); err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = writeRevisionMetadata(ctx, tx, revision, versions, revisionMetadataFields{name: name, description: description, parentRevisionID: currentWorkingRevision}); err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = s.inject("metadata"); err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = s.inject("checkpoint_metadata"); err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = s.persistRevisionDerived(ctx, tx, revision.ID); err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = s.inject("derived"); err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = s.inject("checkpoint_derived"); err != nil {
		return domain.RevisionSummary{}, err
	}
	entities, err := materializeRevisionEntitiesTx(ctx, tx, revision.ID)
	if err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = s.inject("checkpoint_validation"); err != nil {
		return domain.RevisionSummary{}, err
	}
	summary, err := s.persistLocalValidationForEntities(ctx, tx, entities, revision, versions)
	if err != nil {
		return domain.RevisionSummary{}, err
	}
	revision.Validation = &summary
	if err = tx.Commit(); err != nil {
		return domain.RevisionSummary{}, err
	}
	return revision, nil
}

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioningrelease "github.com/zouyi/eco-guardian/internal/versioning/release"
)

var ErrReleaseCommitConflict = errors.New("release commit preconditions changed")

// CommitActivatedIntent is the short final transaction after Graph activation.
// It rechecks immutable intent/Job inputs and the pointer generation, then
// writes the release, pointer and terminal intent state together.
func (s *Store) CommitActivatedIntent(ctx context.Context, intentID domain.ID, expectedGeneration int64) (versioningrelease.Release, versioningrelease.ActivePointer, error) {
	if !intentID.Valid() || expectedGeneration < 0 {
		return versioningrelease.Release{}, versioningrelease.ActivePointer{}, ErrReleaseCommitConflict
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return versioningrelease.Release{}, versioningrelease.ActivePointer{}, err
	}
	defer tx.Rollback()
	var jobID, candidate, policy, phase, jobStatus string
	var baseline sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT i.job_id,i.candidate_revision_id,i.policy_id,i.baseline_release_id,i.phase,j.status FROM release_intents i JOIN jobs j ON j.id=i.job_id WHERE i.id=? AND j.project_uuid=?`, intentID, s.projectID).Scan(&jobID, &candidate, &policy, &baseline, &phase, &jobStatus)
	if err != nil || (phase != string(versioningrelease.IntentGraphActivated) && phase != string(versioningrelease.IntentPointerCommitting)) || (jobStatus != string(versioningrelease.JobRunning) && jobStatus != string(versioningrelease.JobInterrupted)) {
		return versioningrelease.Release{}, versioningrelease.ActivePointer{}, ErrReleaseCommitConflict
	}
	var active sql.NullString
	var generation int64
	if err = tx.QueryRowContext(ctx, `SELECT active_release_id,generation FROM active_release_pointer WHERE singleton=1`).Scan(&active, &generation); err != nil || generation != expectedGeneration || active.String != baseline.String {
		return versioningrelease.Release{}, versioningrelease.ActivePointer{}, ErrReleaseCommitConflict
	}
	id, err := domain.NewID()
	if err != nil {
		return versioningrelease.Release{}, versioningrelease.ActivePointer{}, err
	}
	now := s.now().UTC()
	var gate, confirms, notes string
	var override sql.NullString
	if err = tx.QueryRowContext(ctx, `SELECT gate_manifest,confirmations,notes,override_audit FROM release_intents WHERE id=?`, intentID).Scan(&gate, &confirms, &notes, &override); err != nil {
		return versioningrelease.Release{}, versioningrelease.ActivePointer{}, err
	}
	if err = s.inject("release_commit_before_insert"); err != nil {
		return versioningrelease.Release{}, versioningrelease.ActivePointer{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO releases(id,revision_id,policy_id,baseline_release_id,intent_id,notes,gate_evidence,confirmations,created_at,override_audit) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, candidate, policy, nullID(domain.ID(baseline.String)), intentID, notes, gate, confirms, now.Format(time.RFC3339Nano), nullString(override.String)); err != nil {
		return versioningrelease.Release{}, versioningrelease.ActivePointer{}, err
	}
	if err = s.inject("release_commit_after_release_insert"); err != nil {
		return versioningrelease.Release{}, versioningrelease.ActivePointer{}, err
	}
	if result, updateErr := tx.ExecContext(ctx, `UPDATE active_release_pointer SET active_release_id=?,generation=? WHERE singleton=1 AND generation=? AND active_release_id IS ?`, id, generation+1, generation, nullID(domain.ID(baseline.String))); updateErr != nil {
		return versioningrelease.Release{}, versioningrelease.ActivePointer{}, updateErr
	} else if n, _ := result.RowsAffected(); n != 1 {
		return versioningrelease.Release{}, versioningrelease.ActivePointer{}, ErrReleaseCommitConflict
	}
	if err = s.inject("release_commit_after_pointer_update"); err != nil {
		return versioningrelease.Release{}, versioningrelease.ActivePointer{}, err
	}
	if result, updateErr := tx.ExecContext(ctx, `UPDATE release_intents SET phase=?,updated_at=? WHERE id=? AND phase IN (?,?)`, versioningrelease.IntentSucceeded, now.Format(time.RFC3339Nano), intentID, versioningrelease.IntentGraphActivated, versioningrelease.IntentPointerCommitting); updateErr != nil {
		return versioningrelease.Release{}, versioningrelease.ActivePointer{}, updateErr
	} else if n, _ := result.RowsAffected(); n != 1 {
		return versioningrelease.Release{}, versioningrelease.ActivePointer{}, ErrReleaseCommitConflict
	}
	if err = s.inject("release_commit_after_intent_commit"); err != nil {
		return versioningrelease.Release{}, versioningrelease.ActivePointer{}, err
	}
	if err = tx.Commit(); err != nil {
		return versioningrelease.Release{}, versioningrelease.ActivePointer{}, err
	}
	release := versioningrelease.Release{ID: id, RevisionID: domain.ID(candidate), PolicyID: domain.ID(policy), IntentID: intentID, CreatedAt: now}
	return release, versioningrelease.ActivePointer{ReleaseID: id, Generation: generation + 1}, nil
}

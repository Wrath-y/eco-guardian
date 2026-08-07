package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioning "github.com/zouyi/eco-guardian/internal/versioning"
	versioningrelease "github.com/zouyi/eco-guardian/internal/versioning/release"
)

var (
	ErrReleaseIntentInvalid    = errors.New("release intent is invalid")
	ErrReleaseIntentNotFound   = errors.New("release intent not found")
	ErrReleaseIntentTransition = errors.New("release intent transition is invalid")
)

var _ versioningrelease.IntentRepository = (*Store)(nil)
var _ versioningrelease.IntentRecoveryRepository = (*Store)(nil)

func (s *Store) CreateIntent(ctx context.Context, intent versioningrelease.Intent) (versioningrelease.Intent, bool, error) {
	if !intent.Valid() || versioning.SHA256(intent.GateManifest) != intent.GateManifestHash || intent.Phase != versioningrelease.IntentRecorded {
		return versioningrelease.Intent{}, false, ErrReleaseIntentInvalid
	}
	confirmations, err := versioning.CanonicalJSON(intent.Confirmations)
	if err != nil {
		return versioningrelease.Intent{}, false, err
	}
	backup, err := intent.Backup.CanonicalJSON()
	if err != nil {
		return versioningrelease.Intent{}, false, err
	}
	override, err := versioning.CanonicalJSON(intent.Override)
	if err != nil {
		return versioningrelease.Intent{}, false, err
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return versioningrelease.Intent{}, false, err
	}
	defer tx.Rollback()
	if existing, err := scanReleaseIntent(tx.QueryRowContext(ctx, intentSelect+` WHERE job_id=?`, intent.JobID)); err == nil {
		return existing, true, tx.Commit()
	} else if !errors.Is(err, sql.ErrNoRows) {
		return versioningrelease.Intent{}, false, err
	}
	if err = s.inject("release_intent_before_insert"); err != nil {
		return versioningrelease.Intent{}, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO release_intents(id,job_id,candidate_revision_id,baseline_release_id,policy_id,gate_manifest,gate_manifest_hash,confirmations,backup_evidence,previous_graph_identity,previous_release_id,request_hash,idempotency_key,external_task_id,phase,error_details,created_at,updated_at,notes,override_audit) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, intent.ID, intent.JobID, intent.CandidateRevisionID, nullID(intent.BaselineReleaseID), intent.PolicyID, string(intent.GateManifest), intent.GateManifestHash, string(confirmations), string(backup), nullString(intent.PreviousGraphIdentity), nullID(intent.PreviousReleaseID), intent.RequestHash, intent.IdempotencyKey, nullString(intent.ExternalTaskID), intent.Phase, nullString(intent.ErrorDetails), intent.CreatedAt.UTC().Format(time.RFC3339Nano), intent.UpdatedAt.UTC().Format(time.RFC3339Nano), intent.Notes, nullString(string(override)))
	if err != nil {
		return versioningrelease.Intent{}, false, err
	}
	if err = s.inject("release_intent_after_insert"); err != nil {
		return versioningrelease.Intent{}, false, err
	}
	return intent, false, tx.Commit()
}

func (s *Store) GetIntent(ctx context.Context, id domain.ID) (versioningrelease.Intent, error) {
	if !id.Valid() {
		return versioningrelease.Intent{}, ErrReleaseIntentNotFound
	}
	intent, err := scanReleaseIntent(s.db.QueryRowContext(ctx, intentSelect+` WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return versioningrelease.Intent{}, ErrReleaseIntentNotFound
	}
	return intent, err
}

// ListNonterminalReleaseIntents is the project-open recovery scan. It also
// returns an already committed intent whose Job was not terminally completed
// before a crash, so the durable result can be repaired without replaying any
// external effect.
func (s *Store) ListNonterminalReleaseIntents(ctx context.Context) ([]versioningrelease.Intent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+intentColumns+` FROM release_intents i JOIN jobs j ON j.id=i.job_id WHERE i.phase NOT IN (?,?) OR j.status IN (?,?,?) ORDER BY i.created_at,i.id`, versioningrelease.IntentSucceeded, versioningrelease.IntentFailed, versioningrelease.JobQueued, versioningrelease.JobRunning, versioningrelease.JobInterrupted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	intents := make([]versioningrelease.Intent, 0)
	for rows.Next() {
		intent, scanErr := scanReleaseIntent(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		intents = append(intents, intent)
	}
	return intents, rows.Err()
}

func (s *Store) TransitionIntent(ctx context.Context, id domain.ID, expected, next versioningrelease.IntentPhase, externalTaskID, errorDetails string) (versioningrelease.Intent, bool, error) {
	if !id.Valid() || !expected.Valid() || !next.Valid() {
		return versioningrelease.Intent{}, false, ErrReleaseIntentTransition
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	result, err := s.db.ExecContext(ctx, `UPDATE release_intents SET phase=?,external_task_id=?,error_details=?,updated_at=? WHERE id=? AND phase=?`, next, nullString(externalTaskID), nullString(errorDetails), s.now().UTC().Format(time.RFC3339Nano), id, expected)
	if err != nil {
		return versioningrelease.Intent{}, false, err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return versioningrelease.Intent{}, false, err
	}
	intent, err := s.GetIntent(ctx, id)
	return intent, true, err
}

const intentColumns = `i.id,i.job_id,i.candidate_revision_id,i.baseline_release_id,i.policy_id,i.gate_manifest,i.gate_manifest_hash,i.confirmations,i.backup_evidence,i.previous_graph_identity,i.previous_release_id,i.request_hash,i.idempotency_key,i.external_task_id,i.phase,i.error_details,i.created_at,i.updated_at,i.notes,i.override_audit`
const intentSelect = `SELECT ` + intentColumns + ` FROM release_intents i`

type intentScanner interface{ Scan(...any) error }

func scanReleaseIntent(s intentScanner) (versioningrelease.Intent, error) {
	var id, job, candidate, policy, manifest, manifestHash, rawConfirm, rawBackup, request, key, phase, created, updated string
	var baseline, previousGraph, previousRelease, external, details, override sql.NullString
	var notes string
	if err := s.Scan(&id, &job, &candidate, &baseline, &policy, &manifest, &manifestHash, &rawConfirm, &rawBackup, &previousGraph, &previousRelease, &request, &key, &external, &phase, &details, &created, &updated, &notes, &override); err != nil {
		return versioningrelease.Intent{}, err
	}
	var confirmations []versioningrelease.Confirmation
	var backup versioningrelease.BackupEvidence
	if json.Unmarshal([]byte(rawConfirm), &confirmations) != nil || json.Unmarshal([]byte(rawBackup), &backup) != nil {
		return versioningrelease.Intent{}, ErrReleaseIntentInvalid
	}
	createdAt, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return versioningrelease.Intent{}, err
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return versioningrelease.Intent{}, err
	}
	var audit *versioningrelease.OverrideAudit
	if override.Valid && json.Unmarshal([]byte(override.String), &audit) != nil {
		return versioningrelease.Intent{}, ErrReleaseIntentInvalid
	}
	intent := versioningrelease.Intent{ID: domain.ID(id), JobID: domain.ID(job), CandidateRevisionID: domain.ID(candidate), BaselineReleaseID: domain.ID(baseline.String), PolicyID: domain.ID(policy), GateManifest: []byte(manifest), GateManifestHash: manifestHash, Confirmations: confirmations, Backup: backup, PreviousGraphIdentity: previousGraph.String, PreviousReleaseID: domain.ID(previousRelease.String), RequestHash: request, IdempotencyKey: key, Notes: notes, Override: audit, ExternalTaskID: external.String, Phase: versioningrelease.IntentPhase(phase), ErrorDetails: details.String, CreatedAt: createdAt, UpdatedAt: updatedAt}
	if !intent.Valid() || versioning.SHA256(intent.GateManifest) != intent.GateManifestHash {
		return versioningrelease.Intent{}, ErrReleaseIntentInvalid
	}
	return intent, nil
}

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
)

var ErrGraphSyncStateInvalid = errors.New("graph sync state is invalid")

func (s *Store) GetGraphSyncState(ctx context.Context, revisionID domain.ID) (graphsync.SyncState, bool, error) {
	if !revisionID.Valid() {
		return graphsync.SyncState{}, false, ErrGraphSyncStateInvalid
	}
	var state graphsync.SyncState
	var warnings string
	err := s.db.QueryRowContext(ctx, `SELECT pipeline_state,COALESCE(latest_job_id,''),COALESCE(external_task_id,''),COALESCE(provider_request_id,''),COALESCE(provider_task_id,''),generation,COALESCE(safe_error,''),warnings FROM graph_sync_states WHERE revision_id=?`, revisionID).Scan(&state.Pipeline, &state.LatestJobID, &state.ExternalTaskID, &state.ProviderRequestID, &state.ProviderTaskID, &state.Generation, &state.SafeError, &warnings)
	if errors.Is(err, sql.ErrNoRows) {
		return graphsync.SyncState{}, false, nil
	}
	if err != nil {
		return graphsync.SyncState{}, false, err
	}
	state.RevisionID = string(revisionID)
	if err = json.Unmarshal([]byte(warnings), &state.Warnings); err != nil || !state.Valid() {
		return graphsync.SyncState{}, false, ErrGraphSyncStateInvalid
	}
	return state, true, nil
}

func (s *Store) CreateGraphSyncState(ctx context.Context, state graphsync.SyncState) error {
	revisionID := domain.ID(state.RevisionID)
	if !revisionID.Valid() || !state.Valid() || state.Generation != 0 {
		return ErrGraphSyncStateInvalid
	}
	warnings, err := json.Marshal(state.Warnings)
	if err != nil {
		return ErrGraphSyncStateInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	_, err = s.db.ExecContext(ctx, `INSERT INTO graph_sync_states(revision_id,pipeline_state,latest_job_id,external_task_id,provider_request_id,provider_task_id,generation,safe_error,warnings,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, revisionID, state.Pipeline, nullString(state.LatestJobID), nullString(state.ExternalTaskID), nullString(state.ProviderRequestID), nullString(state.ProviderTaskID), 0, nullString(state.SafeError), string(warnings), s.now().UTC().Format(time.RFC3339Nano))
	return err
}

// CompareAndSwapGraphSyncState applies one monotonic-generation transition.
func (s *Store) CompareAndSwapGraphSyncState(ctx context.Context, expected graphsync.SyncState, next graphsync.SyncState) (graphsync.SyncState, bool, error) {
	if !expected.Valid() || !next.Valid() || expected.RevisionID != next.RevisionID || !expected.Pipeline.CanTransitionTo(next.Pipeline) || next.Generation != expected.Generation+1 {
		return graphsync.SyncState{}, false, ErrGraphSyncStateInvalid
	}
	warnings, err := json.Marshal(next.Warnings)
	if err != nil {
		return graphsync.SyncState{}, false, ErrGraphSyncStateInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	write, err := s.db.ExecContext(ctx, `UPDATE graph_sync_states SET pipeline_state=?,latest_job_id=?,external_task_id=?,provider_request_id=?,provider_task_id=?,generation=?,safe_error=?,warnings=?,updated_at=? WHERE revision_id=? AND generation=?`, next.Pipeline, nullString(next.LatestJobID), nullString(next.ExternalTaskID), nullString(next.ProviderRequestID), nullString(next.ProviderTaskID), next.Generation, nullString(next.SafeError), string(warnings), s.now().UTC().Format(time.RFC3339Nano), next.RevisionID, expected.Generation)
	if err != nil {
		return graphsync.SyncState{}, false, err
	}
	n, err := write.RowsAffected()
	if err != nil {
		return graphsync.SyncState{}, false, err
	}
	if n == 0 {
		return graphsync.SyncState{}, false, nil
	}
	return next, true, nil
}

// MarkGraphReady atomically advances the state and records the one durable
// downstream handoff. Callers may replay the same expected generation safely.
func (s *Store) MarkGraphReady(ctx context.Context, expected graphsync.SyncState, graphHash string) (graphsync.SyncState, bool, error) {
	return s.commitGraphReady(ctx, expected, graphHash, nil, "")
}

func (s *Store) CommitGraphReady(ctx context.Context, expected graphsync.SyncState, summary projector.Summary, evidence string) (graphsync.SyncState, bool, error) {
	if !summary.Valid() || summary.ProjectID != string(s.projectID) || summary.RevisionID != expected.RevisionID || evidence == "" {
		return graphsync.SyncState{}, false, ErrGraphSyncStateInvalid
	}
	return s.commitGraphReady(ctx, expected, summary.ManifestHash, &summary, evidence)
}

// CommitGraphFailure changes only derived Graph state and the linked Job in
// one transaction. It retains the accepted provider identity for audit and
// explicit retry; it never performs cleanup or creates replacement work.
func (s *Store) CommitGraphFailure(ctx context.Context, expected graphsync.SyncState, safeCode string) (graphsync.SyncState, bool, error) {
	if !expected.Valid() || (expected.Pipeline != graphsync.StateQueued && expected.Pipeline != graphsync.StateBuilding) || safeCode == "" {
		return graphsync.SyncState{}, false, ErrGraphSyncStateInvalid
	}
	next := expected
	next.Pipeline, next.Generation, next.SafeError = graphsync.StateFailed, expected.Generation+1, safeCode
	if !next.Valid() {
		return graphsync.SyncState{}, false, ErrGraphSyncStateInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return graphsync.SyncState{}, false, err
	}
	defer tx.Rollback()
	write, err := tx.ExecContext(ctx, `UPDATE graph_sync_states SET pipeline_state=?,generation=?,safe_error=?,updated_at=? WHERE revision_id=? AND generation=?`, next.Pipeline, next.Generation, next.SafeError, s.now().UTC().Format(time.RFC3339Nano), next.RevisionID, expected.Generation)
	if err != nil {
		return graphsync.SyncState{}, false, err
	}
	rows, err := write.RowsAffected()
	if err != nil || rows != 1 {
		if err != nil {
			return graphsync.SyncState{}, false, err
		}
		return graphsync.SyncState{}, false, ErrGraphSyncStateInvalid
	}
	if next.LatestJobID == "" || !domain.ID(next.LatestJobID).Valid() {
		return graphsync.SyncState{}, false, ErrGraphSyncStateInvalid
	}
	jobWrite, err := tx.ExecContext(ctx, `UPDATE jobs SET status='failed',updated_at=? WHERE id=? AND project_uuid=? AND kind='graph_sync' AND status IN ('queued','running','interrupted')`, s.now().UTC().Format(time.RFC3339Nano), next.LatestJobID, s.projectID)
	if err != nil {
		return graphsync.SyncState{}, false, err
	}
	if rows, err = jobWrite.RowsAffected(); err != nil || rows != 1 {
		if err != nil {
			return graphsync.SyncState{}, false, err
		}
		return graphsync.SyncState{}, false, ErrGraphJobTransition
	}
	if err = tx.Commit(); err != nil {
		return graphsync.SyncState{}, false, err
	}
	return next, false, nil
}

func (s *Store) commitGraphReady(ctx context.Context, expected graphsync.SyncState, graphHash string, summary *projector.Summary, evidence string) (graphsync.SyncState, bool, error) {
	next := expected
	next.Pipeline, next.Generation = graphsync.StateReady, expected.Generation+1
	if !expected.Valid() || expected.Pipeline != graphsync.StateBuilding || !hash64(graphHash) {
		return graphsync.SyncState{}, false, ErrGraphSyncStateInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return graphsync.SyncState{}, false, err
	}
	defer tx.Rollback()
	warnings, err := json.Marshal(next.Warnings)
	if err != nil {
		return graphsync.SyncState{}, false, ErrGraphSyncStateInvalid
	}
	write, err := tx.ExecContext(ctx, `UPDATE graph_sync_states SET pipeline_state=?,generation=?,warnings=?,updated_at=? WHERE revision_id=? AND generation=?`, next.Pipeline, next.Generation, string(warnings), s.now().UTC().Format(time.RFC3339Nano), next.RevisionID, expected.Generation)
	if err != nil {
		return graphsync.SyncState{}, false, err
	}
	n, err := write.RowsAffected()
	if err != nil || n == 0 {
		return graphsync.SyncState{}, false, err
	}
	if summary != nil {
		if _, err = s.insertProjectionSummaryTx(ctx, tx, *summary, evidence); err != nil {
			return graphsync.SyncState{}, false, err
		}
	}
	if next.LatestJobID != "" {
		if !domain.ID(next.LatestJobID).Valid() {
			return graphsync.SyncState{}, false, ErrGraphSyncStateInvalid
		}
		resultURL := "/api/v1/revisions/" + next.RevisionID + "/graph-status"
		jobWrite, jobErr := tx.ExecContext(ctx, `UPDATE jobs SET status='succeeded',result_type='graph_sync',result_id=?,result_url=?,updated_at=? WHERE id=? AND project_uuid=? AND kind='graph_sync' AND status IN ('running','interrupted')`, next.RevisionID, resultURL, s.now().UTC().Format(time.RFC3339Nano), next.LatestJobID, s.projectID)
		if jobErr != nil {
			return graphsync.SyncState{}, false, jobErr
		}
		if rows, rowsErr := jobWrite.RowsAffected(); rowsErr != nil || rows != 1 {
			if rowsErr != nil {
				return graphsync.SyncState{}, false, rowsErr
			}
			return graphsync.SyncState{}, false, ErrGraphJobTransition
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO graph_impact_handoffs(revision_id,graph_manifest_hash,stage,status,created_at) VALUES(?,?, 'impact','queued',?) ON CONFLICT(revision_id,stage,graph_manifest_hash) DO NOTHING`, next.RevisionID, graphHash, s.now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return graphsync.SyncState{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return graphsync.SyncState{}, false, err
	}
	return next, true, nil
}

// ListRecoverableGraphSyncStates returns only nonterminal work in durable
// update order; recovery never discovers work from mutable business state.
func (s *Store) ListRecoverableGraphSyncStates(ctx context.Context, limit int) ([]graphsync.SyncState, error) {
	if limit < 1 || limit > 1000 {
		return nil, ErrGraphSyncStateInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT revision_id,pipeline_state,COALESCE(latest_job_id,''),COALESCE(external_task_id,''),COALESCE(provider_request_id,''),COALESCE(provider_task_id,''),generation,COALESCE(safe_error,''),warnings FROM graph_sync_states WHERE pipeline_state IN ('graph_queued','graph_building') ORDER BY updated_at,revision_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []graphsync.SyncState{}
	for rows.Next() {
		var state graphsync.SyncState
		var warnings string
		if err = rows.Scan(&state.RevisionID, &state.Pipeline, &state.LatestJobID, &state.ExternalTaskID, &state.ProviderRequestID, &state.ProviderTaskID, &state.Generation, &state.SafeError, &warnings); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(warnings), &state.Warnings) != nil || !state.Valid() {
			return nil, ErrGraphSyncStateInvalid
		}
		out = append(out, state)
	}
	return out, rows.Err()
}

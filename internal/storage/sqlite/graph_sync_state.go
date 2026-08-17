package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
)

var ErrGraphSyncStateInvalid = errors.New("graph sync state is invalid")

func (s *Store) GetGraphSyncState(ctx context.Context, revisionID domain.ID) (graphsync.SyncState, bool, error) {
	if !revisionID.Valid() {
		return graphsync.SyncState{}, false, ErrGraphSyncStateInvalid
	}
	var state graphsync.SyncState
	var warnings string
	err := s.db.QueryRowContext(ctx, `SELECT pipeline_state,COALESCE(latest_job_id,''),COALESCE(external_task_id,''),generation,COALESCE(safe_error,''),warnings FROM graph_sync_states WHERE revision_id=?`, revisionID).Scan(&state.Pipeline, &state.LatestJobID, &state.ExternalTaskID, &state.Generation, &state.SafeError, &warnings)
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
	_, err = s.db.ExecContext(ctx, `INSERT INTO graph_sync_states(revision_id,pipeline_state,latest_job_id,external_task_id,generation,safe_error,warnings,updated_at) VALUES(?,?,?,?,?,?,?,?)`, revisionID, state.Pipeline, nullString(state.LatestJobID), nullString(state.ExternalTaskID), 0, nullString(state.SafeError), string(warnings), s.now().UTC().Format(time.RFC3339Nano))
	return err
}

// CompareAndSwapGraphSyncState applies one monotonic-generation transition.
func (s *Store) CompareAndSwapGraphSyncState(ctx context.Context, expected graphsync.SyncState, next graphsync.SyncState) (graphsync.SyncState, bool, error) {
	if !expected.Valid() || !next.Valid() || expected.RevisionID != next.RevisionID || next.Generation != expected.Generation+1 {
		return graphsync.SyncState{}, false, ErrGraphSyncStateInvalid
	}
	warnings, err := json.Marshal(next.Warnings)
	if err != nil {
		return graphsync.SyncState{}, false, ErrGraphSyncStateInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	write, err := s.db.ExecContext(ctx, `UPDATE graph_sync_states SET pipeline_state=?,latest_job_id=?,external_task_id=?,generation=?,safe_error=?,warnings=?,updated_at=? WHERE revision_id=? AND generation=?`, next.Pipeline, nullString(next.LatestJobID), nullString(next.ExternalTaskID), next.Generation, nullString(next.SafeError), string(warnings), s.now().UTC().Format(time.RFC3339Nano), next.RevisionID, expected.Generation)
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

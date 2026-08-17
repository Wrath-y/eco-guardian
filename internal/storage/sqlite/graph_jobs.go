package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"github.com/zouyi/eco-guardian/internal/domain"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	"time"
)

var ErrGraphJobConflict = errors.New("graph job idempotency conflict")

func (s *Store) CreateOrGetGraphJob(ctx context.Context, request graphsync.GraphJobRequest) (graphsync.GraphJob, bool, error) {
	if !request.Valid() || request.ProjectID != s.projectID {
		return graphsync.GraphJob{}, false, ErrGraphSyncStateInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return graphsync.GraphJob{}, false, err
	}
	defer tx.Rollback()
	var id, project, revision, input, requestHash string
	err = tx.QueryRowContext(ctx, `SELECT id,project_uuid,revision_id,input_hash,request_hash FROM jobs WHERE project_uuid=? AND idempotency_key=?`, request.ProjectID, request.IdempotencyKey).Scan(&id, &project, &revision, &input, &requestHash)
	if err == nil {
		if requestHash != request.RequestHash {
			return graphsync.GraphJob{}, false, ErrGraphJobConflict
		}
		return graphsync.GraphJob{ID: domain.ID(id), ProjectID: domain.ID(project), RevisionID: domain.ID(revision), InputHash: input}, true, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return graphsync.GraphJob{}, false, err
	}
	idValue, err := domain.NewID()
	if err != nil {
		return graphsync.GraphJob{}, false, err
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `INSERT INTO jobs(id,project_uuid,kind,revision_id,input_hash,idempotency_key,request_hash,status,created_at,updated_at) VALUES(?,?, 'graph_sync',?,?,?,?,'queued',?,?)`, idValue, request.ProjectID, request.RevisionID, request.InputHash, request.IdempotencyKey, request.RequestHash, now, now)
	if err != nil {
		return graphsync.GraphJob{}, false, err
	}
	return graphsync.GraphJob{ID: idValue, ProjectID: request.ProjectID, RevisionID: request.RevisionID, InputHash: request.InputHash}, false, tx.Commit()
}

var _ graphsync.JobAdmission = (*Store)(nil)

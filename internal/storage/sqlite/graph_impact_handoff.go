package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
)

var ErrGraphImpactHandoffInvalid = errors.New("graph impact handoff is invalid")

// CreateGraphImpactHandoff records the durable boundary to the optional
// impact scheduler. Its unique key makes ready/recovery replays idempotent.
func (s *Store) CreateGraphImpactHandoff(ctx context.Context, revisionID domain.ID, graphHash, stage string) (bool, error) {
	if !revisionID.Valid() || !hash64(graphHash) || strings.TrimSpace(stage) == "" {
		return false, ErrGraphImpactHandoffInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	result, err := s.db.ExecContext(ctx, `INSERT INTO graph_impact_handoffs(revision_id,graph_manifest_hash,stage,status,created_at) VALUES(?,?,?,'queued',?) ON CONFLICT(revision_id,stage,graph_manifest_hash) DO NOTHING`, revisionID, graphHash, stage, s.now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// GraphImpactHandoffStatus reads the durable handoff for one exact revision
// and Graph hash. It never selects an older projection or mutates scheduler
// work merely to render UI status.
func (s *Store) GraphImpactHandoffStatus(ctx context.Context, revisionID domain.ID, graphHash string) (string, bool, error) {
	if !revisionID.Valid() || !hash64(graphHash) {
		return "", false, ErrGraphImpactHandoffInvalid
	}
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT status FROM graph_impact_handoffs WHERE revision_id=? AND graph_manifest_hash=? AND stage='impact'`, revisionID, graphHash).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil || (status != "queued" && status != "consumed" && status != "failed") {
		return "", false, err
	}
	return status, true, nil
}

func (s *Store) ListQueuedImpactHandoffs(ctx context.Context, limit int) ([]graphsync.ImpactHandoff, error) {
	if limit < 1 || limit > 1000 {
		return nil, ErrGraphImpactHandoffInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT revision_id,graph_manifest_hash,stage FROM graph_impact_handoffs WHERE status IN ('queued','waiting') ORDER BY created_at,revision_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]graphsync.ImpactHandoff, 0)
	for rows.Next() {
		var handoff graphsync.ImpactHandoff
		if err = rows.Scan(&handoff.RevisionID, &handoff.GraphHash, &handoff.Stage); err != nil || !handoff.Valid() {
			if err != nil {
				return nil, err
			}
			return nil, ErrGraphImpactHandoffInvalid
		}
		result = append(result, handoff)
	}
	return result, rows.Err()
}

// SetImpactHandoffState records only consumer orchestration state for the
// exact target revision/hash. It never changes Graph readiness or projection.
func (s *Store) SetImpactHandoffState(ctx context.Context, targetID domain.ID, graphHash, status string, baseID, jobID, reportID domain.ID, safeReason string) error {
	if !targetID.Valid() || !validGraphHash(graphHash) || (status != "waiting" && status != "claimed" && status != "consumed" && status != "failed") || (baseID != "" && !baseID.Valid()) || (jobID != "" && !jobID.Valid()) || (reportID != "" && !reportID.Valid()) || len(safeReason) > 512 {
		return ErrGraphImpactHandoffInvalid
	}
	result, err := s.db.ExecContext(ctx, `UPDATE graph_impact_handoffs SET status=?,base_revision_id=?,job_id=?,report_id=?,safe_reason=?,updated_at=? WHERE revision_id=? AND graph_manifest_hash=? AND stage='impact'`, status, nullID(baseID), nullID(jobID), nullID(reportID), nullString(safeReason), s.now().UTC().Format(time.RFC3339Nano), targetID, graphHash)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return ErrGraphImpactHandoffInvalid
	}
	return nil
}

func validGraphHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	return strings.Trim(value, "0123456789abcdef") == ""
}

func hash64(value string) bool {
	if len(value) != 64 {
		return false
	}
	return strings.Trim(value, "0123456789abcdef") == ""
}

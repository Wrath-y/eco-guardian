package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
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

func hash64(value string) bool {
	if len(value) != 64 {
		return false
	}
	return strings.Trim(value, "0123456789abcdef") == ""
}

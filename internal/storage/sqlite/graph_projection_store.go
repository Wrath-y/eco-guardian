package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
)

var (
	ErrProjectionSummaryInvalid  = errors.New("projection summary is invalid")
	ErrProjectionSummaryConflict = errors.New("projection summary conflicts with immutable record")
)

// InsertProjectionSummary records immutable projection evidence once. Replays
// of identical evidence are accepted; conflicting evidence is never updated.
func (s *Store) InsertProjectionSummary(ctx context.Context, summary projector.Summary, evidence string) (bool, error) {
	if !summary.Valid() || summary.ProjectID != string(s.projectID) || evidence == "" {
		return false, ErrProjectionSummaryInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO projection_summaries(revision_id,projection_schema_version,projector_version,config_hash,graph_manifest_hash,node_count,edge_count,evidence,cache_identity,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, summary.RevisionID, summary.SchemaVersion, summary.ProjectorVersion, summary.ConfigHash, summary.ManifestHash, summary.NodeCount, summary.EdgeCount, evidence, nullString(summary.CacheIdentity), s.now().UTC().Format(time.RFC3339Nano))
	if err == nil {
		return true, tx.Commit()
	}
	var existingHash, existingConfig string
	var nodes, edges int
	readErr := tx.QueryRowContext(ctx, `SELECT config_hash,graph_manifest_hash,node_count,edge_count FROM projection_summaries WHERE revision_id=? AND projection_schema_version=? AND projector_version=?`, summary.RevisionID, summary.SchemaVersion, summary.ProjectorVersion).Scan(&existingConfig, &existingHash, &nodes, &edges)
	if readErr == nil && existingConfig == summary.ConfigHash && existingHash == summary.ManifestHash && nodes == summary.NodeCount && edges == summary.EdgeCount {
		return false, tx.Commit()
	}
	if readErr == sql.ErrNoRows {
		return false, err
	}
	if readErr != nil {
		return false, readErr
	}
	return false, ErrProjectionSummaryConflict
}

func (s *Store) GetProjectionSummary(ctx context.Context, revisionID domain.ID, schema projector.ProjectionSchemaVersion, version projector.ProjectorVersion) (projector.Summary, bool, error) {
	var out projector.Summary
	err := s.db.QueryRowContext(ctx, `SELECT config_hash,graph_manifest_hash,node_count,edge_count,COALESCE(cache_identity,'') FROM projection_summaries WHERE revision_id=? AND projection_schema_version=? AND projector_version=?`, revisionID, schema, version).Scan(&out.ConfigHash, &out.ManifestHash, &out.NodeCount, &out.EdgeCount, &out.CacheIdentity)
	if errors.Is(err, sql.ErrNoRows) {
		return projector.Summary{}, false, nil
	}
	if err != nil {
		return projector.Summary{}, false, err
	}
	out.ProjectID, out.RevisionID, out.SchemaVersion, out.ProjectorVersion = string(s.projectID), string(revisionID), schema, version
	if !out.Valid() {
		return projector.Summary{}, false, ErrProjectionSummaryInvalid
	}
	return out, true, nil
}

package sqlite

import (
	"context"
	"crypto/sha256"
	"fmt"
)

// BusinessGeneration excludes Job/event/audit/system writes. Restore target
// freshness is therefore invalidated by a committed working revision, not by
// the restore Job recording its own preflight.
func (s *Store) BusinessGeneration(ctx context.Context) (string, error) {
	var revisionID, configHash string
	var display int
	if err := s.db.QueryRowContext(ctx, `SELECT id,config_hash,display_revision FROM config_revisions ORDER BY display_revision DESC,id DESC LIMIT 1`).Scan(&revisionID, &configHash, &display); err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("business-generation:%s:%s:%s:%d", s.projectID, revisionID, configHash, display)))
	return fmt.Sprintf("%x", digest), nil
}

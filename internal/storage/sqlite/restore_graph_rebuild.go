package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	"github.com/zouyi/eco-guardian/internal/validation"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

var _ ports.DerivedStateInvalidator = (*Store)(nil)

// EnqueueFullRebuild admits restore-scoped Graph work for every retained
// revision that already had a projection state at the selected timepoint.
// Admission is entirely local and durable; provider availability is handled
// later by the ordinary Graph worker, so it cannot roll back a valid DB
// restore. Missing historical interpretation identities remain pending.
func (s *Store) EnqueueFullRebuild(ctx context.Context, projectID, restoreJobID domain.ID, restoreRequestHash string) error {
	if s == nil || projectID != s.projectID || !restoreJobID.Valid() || !hash64(restoreRequestHash) {
		return ErrRestoreDerivedInvalid
	}
	var invalidationCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM restore_graph_invalidations WHERE job_id=? AND project_uuid=? AND request_hash=?`, restoreJobID, projectID, restoreRequestHash).Scan(&invalidationCount); err != nil || invalidationCount != 1 {
		return errors.Join(ErrRestoreDerivedInvalid, err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT revision_id FROM graph_sync_states ORDER BY revision_id`)
	if err != nil {
		return err
	}
	var revisionIDs []domain.ID
	for rows.Next() {
		var revisionID domain.ID
		if err = rows.Scan(&revisionID); err != nil || !revisionID.Valid() {
			_ = rows.Close()
			return errors.Join(ErrRestoreDerivedInvalid, err)
		}
		revisionIDs = append(revisionIDs, revisionID)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, revisionID := range revisionIDs {
		record, recordErr := s.GetRevisionRecord(ctx, revisionID)
		if recordErr != nil {
			return recordErr
		}
		versions, available := restoreValidationVersions(record.Metadata.Manifest)
		if !available {
			continue
		}
		request := graphsync.ValidationPipelineRequest{ProjectID: projectID, RevisionID: revisionID, ConfigHash: record.Metadata.ConfigHash, Versions: versions}
		pipeline := graphsync.ValidationPipeline{
			States: s, Validation: validation.NewValidationGate(s), Runner: s, Evidence: s, Jobs: s,
			JobRequest: func(value graphsync.ValidationPipelineRequest, warnings []string) graphsync.GraphJobRequest {
				return graphsync.RestoreGraphJobRequest(value, warnings, restoreJobID, restoreRequestHash, record.Metadata.ManifestHash)
			},
		}
		if _, err = pipeline.Start(ctx, request); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}

func restoreValidationVersions(manifest versioningrevision.VersionManifest) (validation.VersionManifest, bool) {
	values := map[string]string{}
	for _, entry := range manifest.Entries {
		if entry.State == versioningrevision.Registered {
			values[entry.CapabilityID] = entry.ImplementationVersion
		}
	}
	versions := validation.VersionManifest{Schema: values["schema"], DSL: values["dsl"], Registry: values["validator-registry"], NumericPolicy: values["numeric-policy"]}
	return versions, versions.Valid() && values["graph-projector"] != ""
}

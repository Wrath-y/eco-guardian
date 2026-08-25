package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	aiapplication "github.com/zouyi/eco-guardian/internal/ai/application"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	"github.com/zouyi/eco-guardian/internal/domain"
)

var _ aiapplication.ProjectionRepository = (*Store)(nil)

func (s *Store) ReadStoredPatchProjection(ctx context.Context, patchID aicontract.PatchID) (aiapplication.StoredPatchProjection, error) {
	if ctx == nil || !patchID.Valid() {
		return aiapplication.StoredPatchProjection{}, aiapplication.ErrProjectionInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return aiapplication.StoredPatchProjection{}, err
	}
	defer tx.Rollback()
	var jobID, baseRevision domain.ID
	var canonical []byte
	var accepted sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT p.job_id,p.base_revision_id,p.canonical_patch,d.accepted_revision_id
		FROM ai_draft_patches p JOIN ai_design_runs r ON r.job_id=p.job_id
		LEFT JOIN ai_patch_decisions d ON d.patch_id=p.id
		WHERE p.id=? AND r.project_uuid=?`, patchID, s.projectID).Scan(&jobID, &baseRevision, &canonical, &accepted)
	if errors.Is(err, sql.ErrNoRows) {
		return aiapplication.StoredPatchProjection{}, aiapplication.ErrProjectionNotFound
	}
	if err != nil {
		return aiapplication.StoredPatchProjection{}, err
	}
	var patch aicontract.DraftPatchV1
	if err = json.Unmarshal(canonical, &patch); err != nil || !patch.Valid() || patch.ID != patchID || domain.ID(patch.Base.ConfigRevisionID) != baseRevision {
		return aiapplication.StoredPatchProjection{}, aiapplication.ErrProjectionInvalid
	}
	conflicts, err := readAIPatchFreshness(ctx, tx, patch)
	if err != nil {
		return aiapplication.StoredPatchProjection{}, err
	}
	freshness := aicontract.Freshness{State: aicontract.FreshnessFresh}
	if len(conflicts) != 0 {
		freshness.State, freshness.ConflictingTarget = aicontract.FreshnessStale, conflicts
	}
	projection := aiapplication.StoredPatchProjection{
		PatchID: patchID, JobID: jobID, BaseRevisionID: baseRevision, AcceptedRevisionID: domain.ID(accepted.String), Freshness: freshness,
	}
	if projection.AcceptedRevisionID.Valid() {
		projection.Formal, err = readAIFormalLinks(ctx, tx, s.projectID, projection.AcceptedRevisionID)
		if err != nil {
			return aiapplication.StoredPatchProjection{}, err
		}
	}
	if !projection.Valid() {
		return aiapplication.StoredPatchProjection{}, aiapplication.ErrProjectionInvalid
	}
	if err = tx.Commit(); err != nil {
		return aiapplication.StoredPatchProjection{}, err
	}
	return projection, nil
}

func readAIPatchFreshness(ctx context.Context, tx *sql.Tx, patch aicontract.DraftPatchV1) ([]aicontract.EntityID, error) {
	var currentRevision domain.ID
	if err := tx.QueryRowContext(ctx, `SELECT id FROM config_revisions ORDER BY display_revision DESC LIMIT 1`).Scan(&currentRevision); err != nil {
		return nil, err
	}
	conflicts := []aicontract.EntityID{}
	baseIsCurrent := currentRevision == domain.ID(patch.Base.ConfigRevisionID)
	for _, target := range patch.Targets {
		var version int64
		err := tx.QueryRowContext(ctx, `SELECT entity_version FROM working_entities WHERE id=?`, target.EntityID).Scan(&version)
		if !baseIsCurrent || errors.Is(err, sql.ErrNoRows) || err == nil && version != target.ExpectedEntityVersion {
			conflicts = append(conflicts, target.EntityID)
			continue
		}
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(conflicts, func(left, right int) bool { return conflicts[left] < conflicts[right] })
	return conflicts, nil
}

func readAIFormalLinks(ctx context.Context, tx *sql.Tx, projectID, revisionID domain.ID) (aiapplication.FormalAnalysisLinks, error) {
	links := aiapplication.FormalAnalysisLinks{}
	if id, found, err := readOptionalAIID(ctx, tx, `SELECT id FROM validation_runs WHERE source_kind='revision' AND source_revision_id=? AND scope='FULL' ORDER BY created_at DESC,id DESC LIMIT 1`, revisionID); err != nil {
		return links, err
	} else if found {
		links.Validation = "/api/v1/validation/runs/" + string(id)
	}
	var graphFacts int
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM graph_sync_states WHERE revision_id=?)+(SELECT count(*) FROM projection_summaries WHERE revision_id=?)`, revisionID, revisionID).Scan(&graphFacts); err != nil {
		return links, err
	} else if graphFacts > 0 {
		links.Graph = "/api/v1/revisions/" + string(revisionID) + "/graph-status"
	}
	if id, found, err := readOptionalAIID(ctx, tx, `SELECT id FROM simulation_runs WHERE project_uuid=? AND revision_id=? ORDER BY created_at DESC,id DESC LIMIT 1`, projectID, revisionID); err != nil {
		return links, err
	} else if found {
		links.Simulation = "/api/v1/simulation-runs/" + string(id)
	}
	if id, found, err := readOptionalAIID(ctx, tx, `SELECT id FROM risk_reviews WHERE project_uuid=? AND candidate_revision_id=? ORDER BY created_at DESC,id DESC LIMIT 1`, projectID, revisionID); err != nil {
		return links, err
	} else if found {
		links.Risk = "/api/v1/risk-reviews/" + string(id)
	}
	return links, nil
}

func readOptionalAIID(ctx context.Context, tx *sql.Tx, query string, args ...any) (domain.ID, bool, error) {
	var id domain.ID
	err := tx.QueryRowContext(ctx, query, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return id, err == nil, err
}

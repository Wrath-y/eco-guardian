package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	aiapplication "github.com/zouyi/eco-guardian/internal/ai/application"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aipatch "github.com/zouyi/eco-guardian/internal/ai/patch"
	aipersistence "github.com/zouyi/eco-guardian/internal/ai/persistence"
	aipreview "github.com/zouyi/eco-guardian/internal/ai/preview"
	"github.com/zouyi/eco-guardian/internal/domain"
)

var _ aiapplication.DecisionRepository = (*Store)(nil)

type aiPatchDecisionFacts struct {
	jobID            domain.ID
	patchHash        aicontract.Hash
	baseRevisionID   domain.ID
	inputHash        aicontract.Hash
	canonicalPatch   []byte
	diffHash         aicontract.Hash
	canonicalDiff    []byte
	acceptability    aipersistence.PatchAcceptability
	canonicalInput   []byte
	jobStatus        string
	cancelGeneration int64
}

func (s *Store) AcceptDraftPatch(ctx context.Context, command aiapplication.AcceptCommand) (aiapplication.PatchDecision, bool, error) {
	if ctx == nil || !command.Valid() {
		return aiapplication.PatchDecision{}, false, aiapplication.ErrDecisionInvalid
	}
	s.writes.Lock()
	decision, replay, revision, err := s.acceptDraftPatchLocked(ctx, command)
	s.writes.Unlock()
	if err == nil && !replay {
		s.notifyRevisionCommitted(ctx, revision)
	}
	return decision, replay, err
}

func (s *Store) acceptDraftPatchLocked(ctx context.Context, command aiapplication.AcceptCommand) (aiapplication.PatchDecision, bool, domain.RevisionSummary, error) {
	requestHash, _ := command.RequestHash()
	expectedVersionsHash, _ := command.ExpectedVersionsHash()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return aiapplication.PatchDecision{}, false, domain.RevisionSummary{}, err
	}
	defer tx.Rollback()
	if existing, found, loadErr := loadAIDecisionByKey(ctx, tx, s.projectID, command.IdempotencyKey); loadErr != nil {
		return aiapplication.PatchDecision{}, false, domain.RevisionSummary{}, loadErr
	} else if found {
		if existing.PatchID == command.PatchID && existing.Kind == aicontract.DecisionAccepted && existing.RequestHash == requestHash {
			return existing, true, domain.RevisionSummary{}, tx.Commit()
		}
		return aiapplication.PatchDecision{}, false, domain.RevisionSummary{}, aiapplication.ErrDecisionConflict
	}
	facts, err := loadAIPatchDecisionFacts(ctx, tx, s.projectID, command.PatchID)
	if err != nil {
		return aiapplication.PatchDecision{}, false, domain.RevisionSummary{}, err
	}
	if _, found, loadErr := loadAIDecisionByPatch(ctx, tx, s.projectID, command.PatchID); loadErr != nil {
		return aiapplication.PatchDecision{}, false, domain.RevisionSummary{}, loadErr
	} else if found {
		return aiapplication.PatchDecision{}, false, domain.RevisionSummary{}, aiapplication.ErrDecisionConflict
	}
	if command.PatchHash != facts.patchHash {
		return aiapplication.PatchDecision{}, false, domain.RevisionSummary{}, aiapplication.ErrDecisionConflict
	}
	if command.BaseRevisionID != facts.baseRevisionID {
		return aiapplication.PatchDecision{}, false, domain.RevisionSummary{}, aiapplication.ErrDecisionStale
	}
	if facts.cancelGeneration != 0 || facts.jobStatus == "canceled" {
		return aiapplication.PatchDecision{}, false, domain.RevisionSummary{}, aiapplication.ErrDecisionCanceled
	}
	if facts.jobStatus != "succeeded" || facts.acceptability != aipersistence.PatchAcceptable {
		return aiapplication.PatchDecision{}, false, domain.RevisionSummary{}, aiapplication.ErrDecisionNotAcceptable
	}
	patch, diff, input, err := decodeAIDecisionArtifacts(facts)
	if err != nil || !patchWithinInputScope(patch, input) {
		return aiapplication.PatchDecision{}, false, domain.RevisionSummary{}, aiapplication.ErrDecisionValidation
	}
	if !acceptCommandMatchesPatch(command, patch) {
		return aiapplication.PatchDecision{}, false, domain.RevisionSummary{}, aiapplication.ErrDecisionStale
	}
	if err = requireFreshAIPatchTx(ctx, tx, patch); err != nil {
		return aiapplication.PatchDecision{}, false, domain.RevisionSummary{}, err
	}
	materialization, err := aipreview.BuildProposalMaterializationV1(ctx, aiProposalTxReader{tx: tx}, patch, diff)
	if err != nil {
		return aiapplication.PatchDecision{}, false, domain.RevisionSummary{}, aiapplication.ErrDecisionValidation
	}
	changed, err := s.acceptedAIEntities(materialization, patch)
	if err != nil {
		return aiapplication.PatchDecision{}, false, domain.RevisionSummary{}, err
	}
	revision, err := s.writeAIAcceptedEntities(ctx, tx, changed)
	if err != nil {
		return aiapplication.PatchDecision{}, false, domain.RevisionSummary{}, err
	}
	if err = s.inject("ai-accept-revision"); err != nil {
		return aiapplication.PatchDecision{}, false, domain.RevisionSummary{}, err
	}
	decision, err := s.insertAIDecision(ctx, tx, command.PatchID, aicontract.DecisionAccepted, command.Actor, command.IdempotencyKey, requestHash, command.PatchHash, expectedVersionsHash, revision.ID, "")
	if err != nil {
		return aiapplication.PatchDecision{}, false, domain.RevisionSummary{}, err
	}
	if err = s.inject("ai-accept-decision"); err != nil {
		return aiapplication.PatchDecision{}, false, domain.RevisionSummary{}, err
	}
	if err = s.inject("ai-accept-before-commit"); err != nil {
		return aiapplication.PatchDecision{}, false, domain.RevisionSummary{}, err
	}
	if err = tx.Commit(); err != nil {
		return aiapplication.PatchDecision{}, false, domain.RevisionSummary{}, err
	}
	return decision, false, revision, nil
}

func (s *Store) DiscardDraftPatch(ctx context.Context, command aiapplication.DiscardCommand) (aiapplication.PatchDecision, bool, error) {
	if ctx == nil || !command.Valid() {
		return aiapplication.PatchDecision{}, false, aiapplication.ErrDecisionInvalid
	}
	requestHash, _ := command.RequestHash()
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return aiapplication.PatchDecision{}, false, err
	}
	defer tx.Rollback()
	if existing, found, loadErr := loadAIDecisionByKey(ctx, tx, s.projectID, command.IdempotencyKey); loadErr != nil {
		return aiapplication.PatchDecision{}, false, loadErr
	} else if found {
		if existing.PatchID == command.PatchID && existing.Kind == aicontract.DecisionDiscarded && existing.RequestHash == requestHash {
			return existing, true, tx.Commit()
		}
		return aiapplication.PatchDecision{}, false, aiapplication.ErrDecisionConflict
	}
	facts, err := loadAIPatchDecisionFacts(ctx, tx, s.projectID, command.PatchID)
	if err != nil {
		return aiapplication.PatchDecision{}, false, err
	}
	if facts.patchHash != command.PatchHash {
		return aiapplication.PatchDecision{}, false, aiapplication.ErrDecisionConflict
	}
	if _, found, loadErr := loadAIDecisionByPatch(ctx, tx, s.projectID, command.PatchID); loadErr != nil {
		return aiapplication.PatchDecision{}, false, loadErr
	} else if found {
		return aiapplication.PatchDecision{}, false, aiapplication.ErrDecisionConflict
	}
	decision, err := s.insertAIDecision(ctx, tx, command.PatchID, aicontract.DecisionDiscarded, command.Actor, command.IdempotencyKey, requestHash, command.PatchHash, aiapplication.EmptyExpectedVersionsHash(), "", command.Reason)
	if err != nil {
		return aiapplication.PatchDecision{}, false, err
	}
	return decision, false, tx.Commit()
}

func loadAIPatchDecisionFacts(ctx context.Context, tx *sql.Tx, projectID domain.ID, patchID aicontract.PatchID) (aiPatchDecisionFacts, error) {
	var value aiPatchDecisionFacts
	err := tx.QueryRowContext(ctx, `SELECT p.job_id,p.patch_hash,p.base_revision_id,p.input_hash,p.canonical_patch,p.diff_hash,b.body,p.acceptability,r.canonical_input,j.status,j.cancel_generation
		FROM ai_draft_patches p JOIN ai_design_runs r ON r.job_id=p.job_id JOIN jobs j ON j.id=p.job_id
		LEFT JOIN ai_blobs b ON b.content_hash=p.diff_blob_hash WHERE p.id=? AND r.project_uuid=?`, patchID, projectID).
		Scan(&value.jobID, &value.patchHash, &value.baseRevisionID, &value.inputHash, &value.canonicalPatch, &value.diffHash, &value.canonicalDiff, &value.acceptability, &value.canonicalInput, &value.jobStatus, &value.cancelGeneration)
	if errors.Is(err, sql.ErrNoRows) {
		return value, aiapplication.ErrDecisionNotFound
	}
	return value, err
}

func decodeAIDecisionArtifacts(facts aiPatchDecisionFacts) (aicontract.DraftPatchV1, aipatch.DiffResult, aicontract.AIDesignInputV1, error) {
	var patch aicontract.DraftPatchV1
	var diffValue aicontract.DraftDiff
	var input aicontract.AIDesignInputV1
	if json.Unmarshal(facts.canonicalPatch, &patch) != nil || json.Unmarshal(facts.canonicalDiff, &diffValue) != nil || json.Unmarshal(facts.canonicalInput, &input) != nil || !patch.Valid() || !input.Valid() {
		return patch, aipatch.DiffResult{}, input, aiapplication.ErrDecisionValidation
	}
	inputHash, err := aicontract.HashAIDesignInputV1(input)
	if err != nil || inputHash != facts.inputHash {
		return patch, aipatch.DiffResult{}, input, aiapplication.ErrDecisionValidation
	}
	patchCanonical, err := aicontract.CanonicalDraftPatch(patch)
	if err != nil || !bytes.Equal(patchCanonical, facts.canonicalPatch) || patch.Hash != facts.patchHash || patch.Base.ConfigRevisionID != aicontract.RevisionID(facts.baseRevisionID) || patch.Base != input.Base {
		return patch, aipatch.DiffResult{}, input, aiapplication.ErrDecisionValidation
	}
	diffCanonical, err := aicontract.CanonicalDraftDiff(diffValue)
	if err != nil || !bytes.Equal(diffCanonical, facts.canonicalDiff) || diffValue.PatchHash != patch.Hash {
		return patch, aipatch.DiffResult{}, input, aiapplication.ErrDecisionValidation
	}
	diffHash, err := aicontract.HashDraftDiff(diffValue)
	if err != nil || diffHash != facts.diffHash {
		return patch, aipatch.DiffResult{}, input, aiapplication.ErrDecisionValidation
	}
	return patch, aipatch.DiffResult{Diff: diffValue, Canonical: diffCanonical, Hash: diffHash}, input, nil
}

func acceptCommandMatchesPatch(command aiapplication.AcceptCommand, patch aicontract.DraftPatchV1) bool {
	if len(command.Targets) != len(patch.Targets) {
		return false
	}
	for index, target := range patch.Targets {
		if command.Targets[index].EntityID != target.EntityID || command.Targets[index].ExpectedEntityVersion != target.ExpectedEntityVersion {
			return false
		}
	}
	return true
}

func patchWithinInputScope(patch aicontract.DraftPatchV1, input aicontract.AIDesignInputV1) bool {
	allowed := make(map[aicontract.EntityID]aicontract.AllowedTarget, len(input.AllowedTargets))
	for _, target := range input.AllowedTargets {
		allowed[target.EntityID] = target
	}
	for _, target := range patch.Targets {
		scope, found := allowed[target.EntityID]
		if !found || scope.Kind != target.Kind || scope.ExpectedEntityVersion != target.ExpectedEntityVersion {
			return false
		}
		paths := make(map[aicontract.FieldPath]map[aicontract.PatchOperationKind]struct{}, len(scope.Paths))
		for _, path := range scope.Paths {
			operations := make(map[aicontract.PatchOperationKind]struct{}, len(path.Operations))
			for _, operation := range path.Operations {
				operations[operation] = struct{}{}
			}
			paths[path.Path] = operations
		}
		for _, operation := range target.Operations {
			if _, found = paths[operation.Path][operation.Kind]; !found {
				return false
			}
		}
	}
	return true
}

func requireFreshAIPatchTx(ctx context.Context, tx *sql.Tx, patch aicontract.DraftPatchV1) error {
	var latest domain.ID
	var configHash string
	if err := tx.QueryRowContext(ctx, `SELECT id,config_hash FROM config_revisions ORDER BY display_revision DESC LIMIT 1`).Scan(&latest, &configHash); err != nil {
		return err
	}
	if latest != domain.ID(patch.Base.ConfigRevisionID) || configHash != string(patch.Base.ConfigHash) {
		return aiapplication.ErrDecisionStale
	}
	for _, target := range patch.Targets {
		var version int64
		var kind domain.EntityKind
		if err := tx.QueryRowContext(ctx, `SELECT entity_version,kind FROM working_entities WHERE id=?`, target.EntityID).Scan(&version, &kind); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return aiapplication.ErrDecisionStale
			}
			return err
		}
		if version != target.ExpectedEntityVersion || string(kind) != target.Kind {
			return aiapplication.ErrDecisionStale
		}
	}
	return nil
}

type aiProposalTxReader struct{ tx *sql.Tx }

func (reader aiProposalTxReader) ReadProposalBase(ctx context.Context, base aicontract.FrozenBaseIdentity) (aipreview.ProposalBaseSnapshot, error) {
	entities, err := materializeRevisionEntitiesTx(ctx, reader.tx, domain.ID(base.ConfigRevisionID))
	if err != nil {
		return aipreview.ProposalBaseSnapshot{}, err
	}
	return aipreview.ProposalBaseSnapshot{Base: base, Entities: entities}, nil
}

func (s *Store) acceptedAIEntities(materialization aipreview.ProposalMaterializationV1, patch aicontract.DraftPatchV1) ([]domain.Entity, error) {
	targets := make(map[domain.ID]struct{}, len(patch.Targets))
	for _, target := range patch.Targets {
		targets[domain.ID(target.EntityID)] = struct{}{}
	}
	changed := make([]domain.Entity, 0, len(targets))
	for _, entity := range materialization.Entities {
		if _, found := targets[entity.ID]; !found {
			continue
		}
		entity.UpdatedAt = s.now().UTC()
		normalized, err := s.validateProspective(entity)
		if err != nil {
			return nil, errors.Join(aiapplication.ErrDecisionValidation, err)
		}
		changed = append(changed, normalized)
	}
	if len(changed) != len(targets) {
		return nil, aiapplication.ErrDecisionValidation
	}
	return changed, nil
}

func (s *Store) writeAIAcceptedEntities(ctx context.Context, tx *sql.Tx, entities []domain.Entity) (domain.RevisionSummary, error) {
	versions, err := currentValidationVersionManifest()
	if err != nil {
		return domain.RevisionSummary{}, err
	}
	for _, entity := range entities {
		if err = s.recheckActiveKeyTx(ctx, tx, entity); err != nil {
			return domain.RevisionSummary{}, err
		}
		hash, blob, hashErr := domain.BlobHash(entity)
		if hashErr != nil {
			return domain.RevisionSummary{}, hashErr
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO entity_blobs(hash,json) VALUES(?,?)`, hash, blob); err != nil {
			return domain.RevisionSummary{}, err
		}
		write, writeErr := tx.ExecContext(ctx, `UPDATE working_entities SET entity_key=?,schema_version=?,entity_version=?,blob_hash=?,status=?,updated_at=? WHERE id=? AND kind=? AND entity_version=?`, entity.Key, entity.SchemaVersion, entity.EntityVersion, hash, entity.Status, entity.UpdatedAt.Format(time.RFC3339Nano), entity.ID, entity.Kind, entity.EntityVersion-1)
		if writeErr != nil {
			return domain.RevisionSummary{}, writeErr
		}
		if rows, rowsErr := write.RowsAffected(); rowsErr != nil || rows != 1 {
			return domain.RevisionSummary{}, aiapplication.ErrDecisionStale
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM entity_references WHERE source_entity_id=?`, entity.ID); err != nil {
			return domain.RevisionSummary{}, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM entity_tags WHERE entity_id=?`, entity.ID); err != nil {
			return domain.RevisionSummary{}, err
		}
		refs, tags := domain.ExtractIndexes(entity)
		for _, ref := range refs {
			if _, err = tx.ExecContext(ctx, `INSERT INTO entity_references(source_entity_id,field_path,ordinal,expected_kind,target_entity_id) VALUES(?,?,?,?,?)`, ref.SourceID, ref.FieldPath, ref.Ordinal, ref.ExpectedKind, ref.TargetID); err != nil {
				return domain.RevisionSummary{}, err
			}
		}
		for _, tag := range tags {
			if _, err = tx.ExecContext(ctx, `INSERT INTO entity_tags(entity_id,tag_id) VALUES(?,?)`, tag.EntityID, tag.TagID); err != nil {
				return domain.RevisionSummary{}, err
			}
		}
	}
	if err = s.inject("ai-accept-working"); err != nil {
		return domain.RevisionSummary{}, err
	}
	revision, err := s.writeRevision(ctx, tx)
	if err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = s.writeRevisionMetadata(ctx, tx, revision, versions, revisionMetadataFields{}); err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = s.persistRevisionDerived(ctx, tx, revision.ID); err != nil {
		return domain.RevisionSummary{}, err
	}
	summary, err := s.persistLocalValidationForEntities(ctx, tx, entities, revision, versions)
	if err != nil {
		return domain.RevisionSummary{}, err
	}
	revision.Validation = &summary
	return revision, nil
}

func (s *Store) insertAIDecision(ctx context.Context, tx *sql.Tx, patchID aicontract.PatchID, kind aicontract.HumanDecisionKind, actor, key string, requestHash, patchHash, expectedVersionsHash aicontract.Hash, revisionID domain.ID, reason string) (aiapplication.PatchDecision, error) {
	id, err := domain.NewID()
	if err != nil {
		return aiapplication.PatchDecision{}, err
	}
	decision, err := aiapplication.NewPatchDecision(aicontract.DecisionID(id), patchID, kind, actor, requestHash, revisionID, reason, s.now().UTC())
	if err != nil {
		return aiapplication.PatchDecision{}, err
	}
	var accepted any
	if revisionID.Valid() {
		accepted = revisionID
	}
	var discardReason any
	if strings.TrimSpace(reason) != "" {
		discardReason = reason
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ai_patch_decisions(id,patch_id,project_uuid,decision,idempotency_key,request_hash,patch_hash,expected_versions_hash,accepted_revision_id,decision_hash,created_at,actor,reason) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, decision.ID, patchID, s.projectID, kind, key, requestHash, patchHash, expectedVersionsHash, accepted, decision.ResultHash, decision.CreatedAt.Format(time.RFC3339Nano), actor, discardReason)
	return decision, err
}

func loadAIDecisionByKey(ctx context.Context, tx *sql.Tx, projectID domain.ID, key string) (aiapplication.PatchDecision, bool, error) {
	return scanAIDecision(tx.QueryRowContext(ctx, `SELECT id,patch_id,decision,actor,request_hash,decision_hash,accepted_revision_id,reason,created_at FROM ai_patch_decisions WHERE project_uuid=? AND idempotency_key=?`, projectID, key))
}

func loadAIDecisionByPatch(ctx context.Context, tx *sql.Tx, projectID domain.ID, patchID aicontract.PatchID) (aiapplication.PatchDecision, bool, error) {
	return scanAIDecision(tx.QueryRowContext(ctx, `SELECT id,patch_id,decision,actor,request_hash,decision_hash,accepted_revision_id,reason,created_at FROM ai_patch_decisions WHERE project_uuid=? AND patch_id=?`, projectID, patchID))
}

func scanAIDecision(row *sql.Row) (aiapplication.PatchDecision, bool, error) {
	var value aiapplication.PatchDecision
	var revision, reason sql.NullString
	var createdAt string
	err := row.Scan(&value.ID, &value.PatchID, &value.Kind, &value.Actor, &value.RequestHash, &value.ResultHash, &revision, &reason, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return value, false, nil
	}
	if err != nil {
		return value, false, err
	}
	value.AcceptedRevisionID = domain.ID(revision.String)
	value.Reason = reason.String
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil || !value.Valid() {
		return aiapplication.PatchDecision{}, false, aiapplication.ErrDecisionConflict
	}
	return value, true, nil
}

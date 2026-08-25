package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiorchestration "github.com/zouyi/eco-guardian/internal/ai/orchestration"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/rules/materialization"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

var ErrAIAdmissionSnapshotInvalid = errors.New("AI admission snapshot is invalid or unavailable")

// ResolveAIAdmissionSnapshot is the SQLite atomic snapshot adapter. Revision,
// FULL-validation/materialization, targets, graph identity and active baseline
// are read through one read transaction and detached before commit.
func (s *Store) ResolveAIAdmissionSnapshot(ctx context.Context, selection aiorchestration.AdmissionSelection) (aiorchestration.AdmissionSnapshot, error) {
	if selection.ProjectID != aicontract.ProjectID(s.projectID) || !selection.BaseRevisionID.Valid() || len(selection.TargetIDs) == 0 {
		return aiorchestration.AdmissionSnapshot{}, ErrAIAdmissionSnapshotInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return aiorchestration.AdmissionSnapshot{}, err
	}
	defer tx.Rollback()

	revisionID := domain.ID(selection.BaseRevisionID)
	record, err := scanRevisionRecord(tx.QueryRowContext(ctx, revisionMetadataSelect+` WHERE r.id=?`, revisionID))
	if err != nil {
		return aiorchestration.AdmissionSnapshot{}, fmt.Errorf("%w: revision: %v", ErrAIAdmissionSnapshotInvalid, err)
	}
	validationVersions, err := currentValidationVersionManifest()
	if err != nil {
		return aiorchestration.AdmissionSnapshot{}, fmt.Errorf("%w: validation registry: %v", ErrAIAdmissionSnapshotInvalid, err)
	}
	validationHash, err := validationVersions.Hash()
	if err != nil {
		return aiorchestration.AdmissionSnapshot{}, fmt.Errorf("%w: validation identity: %v", ErrAIAdmissionSnapshotInvalid, err)
	}
	var validationRunID domain.ID
	var validationResultHash string
	err = tx.QueryRowContext(ctx, `SELECT id,result_hash FROM validation_runs
		WHERE source_kind='revision' AND source_revision_id=? AND source_input_hash=? AND scope='FULL'
		AND version_manifest_hash=? AND status='completed' AND error_count=0 AND block_count=0
		ORDER BY created_at DESC,id DESC LIMIT 1`, revisionID, record.Metadata.ConfigHash, validationHash).Scan(&validationRunID, &validationResultHash)
	if err != nil {
		return aiorchestration.AdmissionSnapshot{}, fmt.Errorf("%w: FULL validation: %v", ErrAIAdmissionSnapshotInvalid, err)
	}
	entities, err := ruleMaterializationEntitiesFrom(ctx, tx, revisionID)
	if err != nil {
		return aiorchestration.AdmissionSnapshot{}, fmt.Errorf("%w: entities: %v", ErrAIAdmissionSnapshotInvalid, err)
	}
	formulas, err := ruleMaterializationFormulaIndexesFrom(ctx, tx, revisionID)
	if err != nil {
		return aiorchestration.AdmissionSnapshot{}, fmt.Errorf("%w: formula indexes: %v", ErrAIAdmissionSnapshotInvalid, err)
	}
	rules, err := materialization.Build(materialization.Source{
		ProjectID: s.projectID, RevisionID: revisionID, ConfigHash: record.Metadata.ConfigHash,
		Certification: materialization.Certification{RunID: validationRunID, ResultHash: validationResultHash, Versions: validationVersions},
		Entities:      entities, Formulas: formulas,
	})
	if err != nil {
		return aiorchestration.AdmissionSnapshot{}, fmt.Errorf("%w: materialization: %v", ErrAIAdmissionSnapshotInvalid, err)
	}

	graphSchema, graphProjector, ok := graphVersions(record.Metadata.Manifest)
	if !ok {
		return aiorchestration.AdmissionSnapshot{}, ErrAIAdmissionSnapshotInvalid
	}
	var graphConfigHash, graphContentHash string
	err = tx.QueryRowContext(ctx, `SELECT p.config_hash,p.graph_manifest_hash
		FROM projection_summaries p JOIN graph_sync_states g ON g.revision_id=p.revision_id
		WHERE p.revision_id=? AND p.projection_schema_version=? AND p.projector_version=? AND g.pipeline_state='graph_ready'`,
		revisionID, graphSchema, graphProjector).Scan(&graphConfigHash, &graphContentHash)
	if err != nil || graphConfigHash != record.Metadata.ConfigHash {
		return aiorchestration.AdmissionSnapshot{}, fmt.Errorf("%w: graph identity: %v", ErrAIAdmissionSnapshotInvalid, err)
	}

	baseline, err := resolveAIAdmissionBaseline(ctx, tx)
	if err != nil {
		return aiorchestration.AdmissionSnapshot{}, fmt.Errorf("%w: baseline: %v", ErrAIAdmissionSnapshotInvalid, err)
	}
	byID := make(map[domain.ID]struct {
		kind    string
		version int64
	}, len(entities))
	for _, entity := range entities {
		byID[entity.ID] = struct {
			kind    string
			version int64
		}{kind: string(entity.Kind), version: entity.EntityVersion}
	}
	targets := make([]aiorchestration.ResolvedTarget, 0, len(selection.TargetIDs))
	for _, targetID := range selection.TargetIDs {
		fact, found := byID[domain.ID(targetID)]
		if !found {
			return aiorchestration.AdmissionSnapshot{}, ErrAIAdmissionSnapshotInvalid
		}
		targets = append(targets, aiorchestration.ResolvedTarget{EntityID: targetID, Kind: fact.kind, EntityVersion: fact.version})
	}
	versions, ok := aiVersionIdentities(record.Metadata.Manifest)
	if !ok {
		return aiorchestration.AdmissionSnapshot{}, ErrAIAdmissionSnapshotInvalid
	}
	snapshot := aiorchestration.AdmissionSnapshot{
		Base: aicontract.FrozenBaseIdentity{
			ProjectID: selection.ProjectID, ConfigRevisionID: selection.BaseRevisionID,
			ConfigHash: aicontract.Hash(record.Metadata.ConfigHash), VersionManifestHash: aicontract.Hash(record.Metadata.ManifestHash),
			MaterializationHash: aicontract.Hash(rules.MaterializationHash), GraphNamespace: string(selection.ProjectID),
			GraphSnapshot: string(selection.BaseRevisionID), GraphContentHash: aicontract.Hash(graphContentHash),
		},
		Baseline: baseline, Targets: targets, RequiredVersions: versions,
	}
	if !snapshot.Valid() {
		return aiorchestration.AdmissionSnapshot{}, ErrAIAdmissionSnapshotInvalid
	}
	if err = tx.Commit(); err != nil {
		return aiorchestration.AdmissionSnapshot{}, err
	}
	return snapshot, nil
}

func resolveAIAdmissionBaseline(ctx context.Context, tx *sql.Tx) (aicontract.BaselineIdentity, error) {
	var releaseID, revisionID, configHash sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT p.active_release_id,r.revision_id,c.config_hash
		FROM active_release_pointer p LEFT JOIN releases r ON r.id=p.active_release_id
		LEFT JOIN config_revisions c ON c.id=r.revision_id WHERE p.singleton=1`).Scan(&releaseID, &revisionID, &configHash)
	if err != nil {
		return aicontract.BaselineIdentity{}, err
	}
	if !releaseID.Valid {
		return aicontract.BaselineIdentity{Kind: aicontract.BaselineNone}, nil
	}
	baseline := aicontract.BaselineIdentity{Kind: aicontract.BaselineCurrent, ReleaseID: aicontract.ReleaseID(releaseID.String), ConfigRevisionID: aicontract.RevisionID(revisionID.String), ConfigHash: aicontract.Hash(configHash.String)}
	if !baseline.Valid() {
		return aicontract.BaselineIdentity{}, ErrAIAdmissionSnapshotInvalid
	}
	return baseline, nil
}

func graphVersions(manifest versioningrevision.VersionManifest) (string, string, bool) {
	for _, entry := range manifest.Entries {
		if entry.CapabilityID != "graph-projector" || entry.State != versioningrevision.Registered {
			continue
		}
		schema, projector, ok := strings.Cut(entry.ImplementationVersion, "/")
		return schema, projector, ok && schema != "" && projector != ""
	}
	return "", "", false
}

func aiVersionIdentities(manifest versioningrevision.VersionManifest) ([]aicontract.VersionIdentity, bool) {
	versions := make([]aicontract.VersionIdentity, 0, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		if entry.State != versioningrevision.Registered {
			return nil, false
		}
		sum := sha256.Sum256([]byte("eco-guardian.revision-capability/v1\x00" + entry.CapabilityID + "\x00" + entry.ContractVersion + "\x00" + entry.ImplementationVersion))
		versions = append(versions, aicontract.VersionIdentity{ID: entry.CapabilityID, Version: entry.ImplementationVersion, Hash: aicontract.Hash(hex.EncodeToString(sum[:]))})
	}
	return versions, true
}

var _ aiorchestration.AdmissionSnapshotSource = (*Store)(nil)

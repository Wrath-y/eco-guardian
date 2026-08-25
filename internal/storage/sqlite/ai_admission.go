package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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
	if selection.ProjectID != aicontract.ProjectID(s.projectID) || !selection.BaseRevisionID.Valid() || len(selection.Targets) == 0 {
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
	entityByID := make(map[domain.ID]domain.Entity, len(entities))
	for _, entity := range entities {
		entityByID[entity.ID] = entity
	}
	targets := make([]aiorchestration.ResolvedTarget, 0, len(selection.Targets))
	for _, selected := range selection.Targets {
		fact, found := byID[domain.ID(selected.EntityID)]
		if !found {
			return aiorchestration.AdmissionSnapshot{}, ErrAIAdmissionSnapshotInvalid
		}
		entity := entityByID[domain.ID(selected.EntityID)]
		if entity.Status != domain.StatusActive || !validAIAllowedPaths(s.registry, entity, selected.Paths) {
			return aiorchestration.AdmissionSnapshot{}, aiorchestration.ErrAdmissionScopeInvalid
		}
		targets = append(targets, aiorchestration.ResolvedTarget{EntityID: selected.EntityID, Kind: fact.kind, EntityVersion: fact.version, Paths: cloneAIAllowedPaths(selected.Paths)})
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

func cloneAIAllowedPaths(values []aicontract.AllowedPath) []aicontract.AllowedPath {
	result := append([]aicontract.AllowedPath(nil), values...)
	for index := range result {
		result[index].Operations = append([]aicontract.PatchOperationKind(nil), result[index].Operations...)
	}
	return result
}

func validAIAllowedPaths(registry *domain.Registry, entity domain.Entity, paths []aicontract.AllowedPath) bool {
	if registry == nil || len(paths) == 0 {
		return false
	}
	seen := map[aicontract.FieldPath]struct{}{}
	var document any
	body, err := json.Marshal(entity)
	if err != nil || json.Unmarshal(body, &document) != nil {
		return false
	}
	for _, allowed := range paths {
		if !allowed.Valid() || len(allowed.Path) > maxAIAdmissionPathBytes {
			return false
		}
		if _, duplicate := seen[allowed.Path]; duplicate {
			return false
		}
		seen[allowed.Path] = struct{}{}
		tokens, ok := aiPointerTokens(string(allowed.Path))
		if !ok || !aiPathExists(document, tokens) || !aiPathInRegisteredSchema(registry, entity.Kind, tokens) {
			return false
		}
	}
	return true
}

const maxAIAdmissionPathBytes = 512

func aiPointerTokens(pointer string) ([]string, bool) {
	if pointer == "" || pointer[0] != '/' {
		return nil, false
	}
	raw := strings.Split(pointer[1:], "/")
	for index, token := range raw {
		var builder strings.Builder
		for cursor := 0; cursor < len(token); cursor++ {
			if token[cursor] != '~' {
				builder.WriteByte(token[cursor])
				continue
			}
			if cursor+1 >= len(token) || (token[cursor+1] != '0' && token[cursor+1] != '1') {
				return nil, false
			}
			cursor++
			if token[cursor] == '0' {
				builder.WriteByte('~')
			} else {
				builder.WriteByte('/')
			}
		}
		raw[index] = builder.String()
	}
	return raw, true
}

func aiPathExists(value any, tokens []string) bool {
	current := value
	for _, token := range tokens {
		switch typed := current.(type) {
		case map[string]any:
			var found bool
			current, found = typed[token]
			if !found {
				return false
			}
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(typed) {
				return false
			}
			current = typed[index]
		default:
			return false
		}
	}
	return true
}

func aiPathInRegisteredSchema(registry *domain.Registry, kind domain.EntityKind, tokens []string) bool {
	if len(tokens) == 0 {
		return false
	}
	switch tokens[0] {
	case "description", "balance_group":
		return len(tokens) == 1
	case "tag_ids":
		return len(tokens) == 1 || (len(tokens) == 2 && aiArrayIndex(tokens[1]))
	case "payload":
		if len(tokens) < 2 {
			return false
		}
		schema, found := registry.Schema(kind)
		if !found {
			return false
		}
		return aiSchemaContainsPath(registry, schema.Raw, tokens[1:])
	default:
		return false
	}
}

func aiSchemaContainsPath(registry *domain.Registry, raw json.RawMessage, tokens []string) bool {
	var schema map[string]any
	if json.Unmarshal(raw, &schema) != nil {
		return false
	}
	for len(tokens) > 0 {
		if reference, ok := schema["$ref"].(string); ok {
			resolved, found := registry.SchemaByID(reference)
			if !found || json.Unmarshal(resolved.Raw, &schema) != nil {
				return false
			}
			continue
		}
		if properties, ok := schema["properties"].(map[string]any); ok {
			next, found := properties[tokens[0]].(map[string]any)
			if !found {
				return false
			}
			schema, tokens = next, tokens[1:]
			continue
		}
		if items, ok := schema["items"].(map[string]any); ok && aiArrayIndex(tokens[0]) {
			schema, tokens = items, tokens[1:]
			continue
		}
		return false
	}
	return true
}

func aiArrayIndex(value string) bool {
	if value == "-" {
		return true
	}
	index, err := strconv.Atoi(value)
	return err == nil && index >= 0 && strconv.Itoa(index) == value
}

var _ aiorchestration.AdmissionSnapshotSource = (*Store)(nil)

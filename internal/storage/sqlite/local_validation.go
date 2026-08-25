package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
	"github.com/zouyi/eco-guardian/internal/validation"
)

// localPreflight is intentionally small: it captures only the immutable
// working-state identity and direct targets that a prospective LOCAL check
// depends on. Formula parsing/type checking is run while it is prepared; the
// eventual report is still produced from the rechecked write transaction.
type localPreflight struct {
	workingHash  string
	dependencies []localDependency
}

type localDependency struct {
	id      domain.ID
	present bool
	version int64
	kind    domain.EntityKind
	status  domain.EntityStatus
}

func (s *Store) preflightLocal(ctx context.Context, entity domain.Entity) (localPreflight, error) {
	snapshot, err := s.MaterializeValidationSource(ctx, validation.SourceWorking, "")
	if err != nil {
		return localPreflight{}, err
	}
	references, bindings := validation.WalkKnownSchema([]domain.Entity{entity})
	registry, err := formula.V1Registry()
	if err != nil {
		return localPreflight{}, err
	}
	// This is deliberately non-authoritative feedback. It ensures LOCAL parser,
	// type, and unit checks happen before acquiring the serialized write lock;
	// diagnostics are recomputed after the dependency identity is rechecked.
	_, _ = validation.CompileFormulaBindings(bindings, registry, formula.SymbolTable{})
	byID := make(map[domain.ID]domain.Entity, len(snapshot.Entities))
	for _, current := range snapshot.Entities {
		byID[current.ID] = current
	}
	deps := make([]localDependency, 0, len(references))
	seen := map[domain.ID]struct{}{}
	for _, reference := range references {
		if _, ok := seen[reference.TargetID]; ok {
			continue
		}
		seen[reference.TargetID] = struct{}{}
		dependency := localDependency{id: reference.TargetID}
		if target, ok := byID[reference.TargetID]; ok {
			dependency.present = true
			dependency.version = target.EntityVersion
			dependency.kind = target.Kind
			dependency.status = target.Status
		}
		deps = append(deps, dependency)
	}
	return localPreflight{workingHash: snapshot.Source.InputHash, dependencies: deps}, nil
}

// recheckLocalPreflightTx closes the read/write race before any save writes.
// The manifest hash detects all working changes; direct target versions make
// the dependency contract explicit and give callers a focused check to audit.
func (s *Store) recheckLocalPreflightTx(ctx context.Context, tx *sql.Tx, preflight localPreflight) (bool, error) {
	hash, err := workingManifestHashTx(ctx, tx)
	if err != nil || hash != preflight.workingHash {
		return false, err
	}
	for _, dependency := range preflight.dependencies {
		var version int64
		var kind domain.EntityKind
		var status domain.EntityStatus
		err = tx.QueryRowContext(ctx, `SELECT entity_version,kind,status FROM working_entities WHERE id=?`, dependency.id).Scan(&version, &kind, &status)
		if err == sql.ErrNoRows {
			if dependency.present {
				return false, nil
			}
			continue
		}
		if err != nil {
			return false, err
		}
		if !dependency.present || dependency.version != version || dependency.kind != kind || dependency.status != status {
			return false, nil
		}
	}
	return true, nil
}

func workingManifestHashTx(ctx context.Context, tx *sql.Tx) (string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,entity_version,status,blob_hash FROM working_entities ORDER BY id`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	manifest := make([]byte, 0)
	for rows.Next() {
		var id domain.ID
		var version int64
		var status domain.EntityStatus
		var hash string
		if err = rows.Scan(&id, &version, &status, &hash); err != nil {
			return "", err
		}
		manifest = append(manifest, []byte(fmt.Sprintf("%s:%d:%s:%s\n", id, version, status, hash))...)
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	sum := sha256.Sum256(manifest)
	return fmt.Sprintf("%x", sum), nil
}

// currentValidationVersionManifest is the single source of the #6
// interpretation identities used by both LOCAL validation and the immutable
// revision metadata written by a save. Keeping it shared prevents a revision
// from recording metadata that disagrees with its LOCAL validation result.
func currentValidationVersionManifest() (validation.VersionManifest, error) {
	registry, err := formula.V1Registry()
	if err != nil {
		return validation.VersionManifest{}, err
	}
	return validation.V1VersionManifest(registry)
}

// persistLocalValidation records only direct references and the changed
// entity's formula syntax/type checks. Global graph validators deliberately
// remain FULL-only.
func (s *Store) persistLocalValidation(ctx context.Context, tx *sql.Tx, entity domain.Entity, revision domain.RevisionSummary, versions validation.VersionManifest) (domain.LocalValidationSummary, error) {
	return s.persistLocalValidationForEntities(ctx, tx, []domain.Entity{entity}, revision, versions)
}

// persistLocalValidationForEntities remains LOCAL-only: it checks direct
// references and formula syntax/types without invoking graph-wide validators.
// Checkpoints use it with their materialized immutable manifest so their run
// is never mislabeled FULL.
func (s *Store) persistLocalValidationForEntities(ctx context.Context, tx *sql.Tx, entities []domain.Entity, revision domain.RevisionSummary, versions validation.VersionManifest) (domain.LocalValidationSummary, error) {
	registry, err := formula.V1Registry()
	if err != nil {
		return domain.LocalValidationSummary{}, err
	}
	references, bindings := validation.WalkKnownSchema(entities)
	issues := make([]validation.Issue, 0)
	for _, reference := range references {
		var kind domain.EntityKind
		var status domain.EntityStatus
		err = tx.QueryRowContext(ctx, `SELECT kind,status FROM working_entities WHERE id=?`, reference.TargetID).Scan(&kind, &status)
		code := ""
		if err == sql.ErrNoRows {
			code = "REFERENCE_NOT_FOUND"
		} else if err != nil {
			return domain.LocalValidationSummary{}, err
		} else if status != domain.StatusActive {
			code = "REFERENCE_TARGET_INACTIVE"
		} else if kind != reference.ExpectedKind {
			code = "REFERENCE_KIND_MISMATCH"
		}
		if code != "" {
			issue, issueErr := validation.NewIssue(validation.SeverityBlock, code, reference.SourceID, reference.FieldPath, nil, &reference.Ordinal, nil, map[string]string{"target": string(reference.TargetID)}, versions.Registry)
			if issueErr != nil {
				return domain.LocalValidationSummary{}, issueErr
			}
			issues = append(issues, issue)
		}
	}
	_, diagnostics := validation.CompileFormulaBindings(bindings, registry, formula.SymbolTable{})
	for _, diagnostic := range diagnostics {
		span := validation.FormulaSpan{StartByte: diagnostic.Span.StartByte, EndByte: diagnostic.Span.EndByte}
		var issueSpan *validation.FormulaSpan
		if span.EndByte > span.StartByte {
			issueSpan = &span
		}
		issue, issueErr := validation.NewIssue(validation.SeverityBlock, diagnostic.Code, diagnostic.SourceID, diagnostic.FieldPath, issueSpan, nil, nil, nil, versions.Registry)
		if issueErr != nil {
			return domain.LocalValidationSummary{}, issueErr
		}
		issues = append(issues, issue)
	}
	source, err := validation.NewSource(validation.SourceRevision, revision.ID, revision.ConfigHash)
	if err != nil {
		return domain.LocalValidationSummary{}, err
	}
	runID, err := domain.NewID()
	if err != nil {
		return domain.LocalValidationSummary{}, err
	}
	run, err := validation.NewCompletedRun(runID, source, validation.ScopeLocal, versions, issues, s.now())
	if err != nil {
		return domain.LocalValidationSummary{}, err
	}
	manifestHash, err := run.Versions.Hash()
	if err != nil {
		return domain.LocalValidationSummary{}, err
	}
	manifest, err := json.Marshal(run.Versions)
	if err != nil {
		return domain.LocalValidationSummary{}, err
	}
	if err = s.insertCompletedValidationRunTx(ctx, tx, run, issues, manifestHash, manifest); err != nil {
		return domain.LocalValidationSummary{}, err
	}
	return domain.LocalValidationSummary{RunID: run.ID, Scope: string(validation.ScopeLocal), Error: run.Summary.Error, Block: run.Summary.Block, Warning: run.Summary.Warning, Info: run.Summary.Info}, nil
}

// persistRevisionDerived compiles the complete immutable manifest after it
// has been written. A LOCAL save may contain semantic BLOCK diagnostics, so a
// malformed binding deliberately produces no AST rows while valid bindings
// and every known-schema reference remain rebuildable from that revision.
func (s *Store) persistRevisionDerived(ctx context.Context, tx *sql.Tx, revisionID domain.ID) error {
	registry, err := formula.V1Registry()
	if err != nil {
		return err
	}
	entities, err := materializeRevisionEntitiesTx(ctx, tx, revisionID)
	if err != nil {
		return err
	}
	references, bindings := validation.WalkKnownSchema(entities)
	formulas, _ := validation.CompileFormulaBindings(bindings, registry, formula.SymbolTable{})
	return s.replaceRevisionDerivedTx(ctx, tx, revisionID, formulas, references)
}

func materializeRevisionEntitiesTx(ctx context.Context, tx *sql.Tx, revisionID domain.ID) ([]domain.Entity, error) {
	rows, err := tx.QueryContext(ctx, `SELECT b.json FROM revision_entities r JOIN entity_blobs b ON b.hash=r.blob_hash WHERE r.revision_id=? ORDER BY r.entity_id`, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entities := []domain.Entity{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var entity domain.Entity
		if err = json.Unmarshal(raw, &entity); err != nil {
			return nil, err
		}
		entities = append(entities, entity)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return entities, nil
}

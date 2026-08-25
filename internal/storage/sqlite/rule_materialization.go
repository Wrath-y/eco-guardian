package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
	"github.com/zouyi/eco-guardian/internal/rules/materialization"
	"github.com/zouyi/eco-guardian/internal/validation"
)

// ReadRuleSource implements materialization.Reader. It resolves only the
// requested immutable revision and returns no source until the exact FULL
// validation result certifies the same config and semantic versions.
func (s *Store) ReadRuleSource(ctx context.Context, revisionID domain.ID) (materialization.Source, error) {
	if !revisionID.Valid() {
		return materialization.Source{}, materialization.Diagnostic{Code: materialization.DiagnosticValidationRequired}
	}
	record, err := s.GetRevisionRecord(ctx, revisionID)
	if err != nil {
		return materialization.Source{}, fmt.Errorf("%w: %w", materialization.ErrUnavailable, err)
	}
	registry, err := formula.V1Registry()
	if err != nil {
		return materialization.Source{}, fmt.Errorf("%w: %w", materialization.ErrUnavailable, err)
	}
	versions := validation.VersionManifest{Schema: "schema-v1", DSL: formula.DSLVersion, Registry: registry.ManifestHash(), NumericPolicy: formula.NumericPolicyV1.Version}
	run, found, err := s.FindMatchingFullRun(ctx, revisionID, record.Metadata.ConfigHash, versions)
	if err != nil {
		return materialization.Source{}, fmt.Errorf("%w: %w", materialization.ErrUnavailable, err)
	}
	if !found || run.Scope != validation.ScopeFull || run.Status != validation.RunCompleted || run.Summary.Error != 0 || run.Summary.Block != 0 {
		return materialization.Source{}, materialization.Diagnostic{Code: materialization.DiagnosticValidationRequired}
	}
	entities, err := s.ruleMaterializationEntities(ctx, revisionID)
	if err != nil {
		return materialization.Source{}, fmt.Errorf("%w: %w", materialization.ErrUnavailable, err)
	}
	formulas, err := s.ruleMaterializationFormulaIndexes(ctx, revisionID)
	if err != nil {
		return materialization.Source{}, fmt.Errorf("%w: %w", materialization.ErrUnavailable, err)
	}
	return materialization.Source{ProjectID: s.projectID, RevisionID: revisionID, ConfigHash: record.Metadata.ConfigHash, Certification: materialization.Certification{RunID: run.ID, ResultHash: run.ResultHash, Versions: versions}, Entities: entities, Formulas: formulas}, nil
}

func (s *Store) ruleMaterializationEntities(ctx context.Context, revisionID domain.ID) ([]domain.Entity, error) {
	return ruleMaterializationEntitiesFrom(ctx, s.db, revisionID)
}

type ruleMaterializationQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func ruleMaterializationEntitiesFrom(ctx context.Context, queryer ruleMaterializationQueryer, revisionID domain.ID) ([]domain.Entity, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT b.json FROM revision_entities r JOIN entity_blobs b ON b.hash=r.blob_hash WHERE r.revision_id=? ORDER BY r.entity_id`, revisionID)
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
		if err = json.Unmarshal(raw, &entity); err != nil || !entity.ID.Valid() || !entity.Kind.Valid() {
			return nil, errors.New("invalid immutable rule entity")
		}
		entities = append(entities, entity)
	}
	return entities, rows.Err()
}

func (s *Store) ruleMaterializationFormulaIndexes(ctx context.Context, revisionID domain.ID) ([]validation.FormulaIndexRecord, error) {
	return ruleMaterializationFormulaIndexesFrom(ctx, s.db, revisionID)
}

func ruleMaterializationFormulaIndexesFrom(ctx context.Context, queryer ruleMaterializationQueryer, revisionID domain.ID) ([]validation.FormulaIndexRecord, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT entity_id,field_path,formula_hash,ast_version,dsl_version,registry_version,ast,ast_hash FROM compiled_ast WHERE revision_id=? ORDER BY entity_id,field_path`, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []validation.FormulaIndexRecord{}
	for rows.Next() {
		var record validation.FormulaIndexRecord
		if err = rows.Scan(&record.SourceID, &record.FieldPath, &record.FormulaHash, &record.ASTVersion, &record.DSLVersion, &record.RegistryVersion, &record.AST, &record.ASTHash); err != nil {
			return nil, err
		}
		readRows, readErr := queryer.QueryContext(ctx, `SELECT output_attribute_id,scope,symbol,span_start,span_end FROM formula_index WHERE revision_id=? AND source_entity_id=? AND field_path=? ORDER BY read_ordinal`, revisionID, record.SourceID, record.FieldPath)
		if readErr != nil {
			return nil, readErr
		}
		for readRows.Next() {
			var read validation.FormulaRead
			if err = readRows.Scan(&record.OutputAttributeID, &read.Scope, &read.Symbol, &read.Span.StartByte, &read.Span.EndByte); err != nil {
				readRows.Close()
				return nil, err
			}
			record.Reads = append(record.Reads, read)
		}
		if err = readRows.Err(); err != nil {
			readRows.Close()
			return nil, err
		}
		readRows.Close()
		// A formula without selector reads has no formula_index row; recover its
		// output attribute from the immutable typed payload during Build.
		result = append(result, record)
	}
	return result, rows.Err()
}

var _ materialization.Reader = (*Store)(nil)

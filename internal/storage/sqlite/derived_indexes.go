package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/validation"
)

// ReplaceRevisionDerived atomically rebuilds only the selected revision's
// disposable AST/formula/reference rows. It cannot write entity blobs or a
// config revision.
func (s *Store) ReplaceRevisionDerived(ctx context.Context, revisionID domain.ID, formulas []validation.FormulaIndexRecord, references []validation.ReferenceTuple) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.replaceRevisionDerivedTx(ctx, tx, revisionID, formulas, references); err != nil {
		return err
	}
	return tx.Commit()
}

// replaceRevisionDerivedTx is the transaction-aware form used by both a
// rebuild and the save hook. Keeping it here makes the disposable derived
// rows follow the immutable revision atomically without granting either path
// permission to change entity payloads or manifests.
func (s *Store) replaceRevisionDerivedTx(ctx context.Context, tx *sql.Tx, revisionID domain.ID, formulas []validation.FormulaIndexRecord, references []validation.ReferenceTuple) error {
	if err := deleteDerived(ctx, tx, revisionID); err != nil {
		return err
	}
	for _, formula := range formulas {
		if _, err := tx.ExecContext(ctx, `INSERT INTO compiled_ast(revision_id,entity_id,field_path,formula_hash,ast_version,dsl_version,registry_version,ast,ast_hash) VALUES(?,?,?,?,?,?,?,?,?)`, revisionID, formula.SourceID, formula.FieldPath, formula.FormulaHash, formula.ASTVersion, formula.DSLVersion, formula.RegistryVersion, formula.AST, formula.ASTHash); err != nil {
			return err
		}
		for ordinal, read := range formula.Reads {
			if _, err := tx.ExecContext(ctx, `INSERT INTO formula_index(revision_id,source_entity_id,field_path,read_ordinal,output_attribute_id,scope,symbol,span_start,span_end,value_type,unit) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, revisionID, formula.SourceID, formula.FieldPath, ordinal, formula.OutputAttributeID, read.Scope, read.Symbol, read.Span.StartByte, read.Span.EndByte, "", ""); err != nil {
				return err
			}
		}
	}
	for _, reference := range references {
		if _, err := tx.ExecContext(ctx, `INSERT INTO revision_references(revision_id,source_entity_id,field_path,ordinal,expected_kind,target_entity_id) VALUES(?,?,?,?,?,?)`, revisionID, reference.SourceID, reference.FieldPath, reference.Ordinal, reference.ExpectedKind, reference.TargetID); err != nil {
			return err
		}
	}
	return nil
}
func deleteDerived(ctx context.Context, tx *sql.Tx, revisionID domain.ID) error {
	for _, table := range []string{"compiled_ast", "formula_index", "revision_references"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE revision_id=?", revisionID); err != nil {
			return fmt.Errorf("delete %s: %w", table, err)
		}
	}
	return nil
}

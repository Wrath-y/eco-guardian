package sqlite

import (
	"context"
	"strings"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
	"github.com/zouyi/eco-guardian/internal/validation"
)

// RunValidation materializes a source before analysis and keeps SQLite write
// time to the final immutable report insertion.
func (s *Store) RunValidation(ctx context.Context, kind validation.SourceKind, revisionID domain.ID, scope validation.Scope) (ValidationReport, error) {
	snapshot, err := s.MaterializeValidationSource(ctx, kind, revisionID)
	if err != nil {
		return ValidationReport{}, err
	}
	registry, err := formula.V1Registry()
	if err != nil {
		return ValidationReport{}, err
	}
	versions := validation.VersionManifest{Schema: "schema-v1", DSL: formula.DSLVersion, Registry: registry.ManifestHash(), NumericPolicy: formula.NumericPolicyV1.Version}
	issues := []validation.Issue{}
	for _, entity := range snapshot.Entities {
		for _, field := range s.registry.Validate(entity) {
			issue, issueErr := validation.NewIssue(validation.SeverityError, "SCHEMA_INVALID", entity.ID, toPointer(field.Path), nil, nil, map[string]string{"message": field.Message}, nil, versions.Registry)
			if issueErr == nil {
				issues = append(issues, issue)
			}
		}
	}
	references, bindings := validation.WalkKnownSchema(snapshot.Entities)
	for _, finding := range validation.ValidateReferences(snapshot.Entities, references) {
		issue, issueErr := validation.NewIssue(validation.SeverityBlock, finding.Code, finding.Tuple.SourceID, finding.Tuple.FieldPath, nil, &finding.Tuple.Ordinal, nil, map[string]string{"target": string(finding.Tuple.TargetID)}, versions.Registry)
		if issueErr == nil {
			issues = append(issues, issue)
		}
	}
	_, diagnostics := validation.CompileFormulaBindings(bindings, registry, formula.SymbolTable{})
	for _, diagnostic := range diagnostics {
		span := validation.FormulaSpan{StartByte: diagnostic.Span.StartByte, EndByte: diagnostic.Span.EndByte}
		if span.EndByte <= span.StartByte {
			span = nilSpan()
		}
		var issueSpan *validation.FormulaSpan
		if span.EndByte > span.StartByte {
			issueSpan = &span
		}
		issue, issueErr := validation.NewIssue(validation.SeverityBlock, diagnostic.Code, diagnostic.SourceID, diagnostic.FieldPath, issueSpan, nil, nil, nil, versions.Registry)
		if issueErr == nil {
			issues = append(issues, issue)
		}
	}
	if scope == validation.ScopeFull {
		records, _ := validation.CompileFormulaBindings(bindings, registry, formula.SymbolTable{})
		nodes := []validation.FormulaNode{}
		producer := map[string]validation.FormulaNode{}
		for _, record := range records {
			node := validation.FormulaNode{EntityID: record.SourceID, FieldPath: record.FieldPath, OutputAttributeID: record.OutputAttributeID}
			nodes = append(nodes, node)
			producer[string(record.SourceID)+"|"+string(record.OutputAttributeID)] = node
		}
		edges := []validation.FormulaEdge{}
		for _, record := range records {
			from := validation.FormulaNode{EntityID: record.SourceID, FieldPath: record.FieldPath, OutputAttributeID: record.OutputAttributeID}
			for _, read := range record.Reads {
				if read.Scope == "self" {
					if to, ok := producer[string(record.SourceID)+"|"+read.Symbol]; ok {
						edges = append(edges, validation.FormulaEdge{From: from, To: to})
					}
				}
			}
		}
		for _, cycle := range validation.StaticFormulaCycles(nodes, edges) {
			issue, issueErr := validation.NewIssue(validation.SeverityBlock, "STATIC_FORMULA_CYCLE", cycle.Node.EntityID, cycle.Node.FieldPath, nil, nil, nil, map[string]string{"cycle_hash": cycle.CycleHash}, versions.Registry)
			if issueErr == nil {
				issues = append(issues, issue)
			}
		}
		triggerRecords, stackRecords := validation.ExtractRuleSafety(snapshot.Entities)
		triggerEdges := make([]validation.TriggerEdge, len(triggerRecords))
		for i, record := range triggerRecords {
			triggerEdges[i] = record.Edge
		}
		for _, loop := range validation.UnboundedEventLoops(triggerEdges) {
			for _, record := range triggerRecords {
				for _, edge := range loop.Edges {
					if record.Edge == edge {
						issue, issueErr := validation.NewIssue(validation.SeverityBlock, "EVENT_LOOP_UNBOUNDED", record.EntityID, record.FieldPath, nil, nil, nil, map[string]string{"cycle_hash": loop.EvidenceHash}, versions.Registry)
						if issueErr == nil {
							issues = append(issues, issue)
						}
						break
					}
				}
			}
		}
		for _, record := range stackRecords {
			for _, code := range validation.ValidateStackRule(record.Rule) {
				issue, issueErr := validation.NewIssue(validation.SeverityBlock, code, record.EntityID, record.FieldPath, nil, nil, nil, nil, versions.Registry)
				if issueErr == nil {
					issues = append(issues, issue)
				}
			}
		}
	}
	id, err := domain.NewID()
	if err != nil {
		return ValidationReport{}, err
	}
	run, err := validation.NewCompletedRun(id, snapshot.Source, scope, versions, issues, time.Now())
	if err != nil {
		return ValidationReport{}, err
	}
	if err = s.InsertCompletedValidationRun(ctx, run, issues); err != nil {
		return ValidationReport{}, err
	}
	return ValidationReport{Run: run, Issues: validation.SortIssues(issues)}, nil
}

// RunFullValidation is the narrow Graph orchestration adapter over the
// existing revision-scoped #6 validation runner.
func (s *Store) RunFullValidation(ctx context.Context, revisionID domain.ID) error {
	_, err := s.RunValidation(ctx, validation.SourceRevision, revisionID, validation.ScopeFull)
	return err
}
func toPointer(path string) string {
	if path == "" {
		return "/"
	}
	return "/" + strings.ReplaceAll(strings.ReplaceAll(path, "[", "/"), "]", "")
}
func nilSpan() validation.FormulaSpan { return validation.FormulaSpan{} }

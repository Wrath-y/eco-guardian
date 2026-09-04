package validation

import (
	"context"
	"fmt"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
)

const SchemaVersionV1 = "schema-v1"

// V1VersionManifest binds analysis to the exact schema, DSL, registry and
// decimal/unit policy implementations used by the retained v1 validators.
func V1VersionManifest(registry *formula.Registry) (VersionManifest, error) {
	if registry == nil {
		return VersionManifest{}, fmt.Errorf("formula registry is required")
	}
	return VersionManifest{
		Schema:        SchemaVersionV1,
		DSL:           formula.DSLVersion,
		Registry:      registry.ManifestHash(),
		NumericPolicy: formula.NumericPolicyV1.Version,
	}, nil
}

// AnalyzeV1 runs the same transport-neutral validators for persisted sources
// and isolated proposal materializations. It never creates a validation run,
// writes a report, or evaluates a release Gate.
func AnalyzeV1(ctx context.Context, schemas *domain.Registry, registry *formula.Registry, versions VersionManifest, entities []domain.Entity, scope Scope) ([]Issue, error) {
	if ctx == nil || schemas == nil || registry == nil || !scope.Valid() {
		return nil, fmt.Errorf("invalid validation analysis input")
	}
	expected, err := V1VersionManifest(registry)
	if err != nil || versions != expected {
		return nil, fmt.Errorf("validation implementation/version mismatch")
	}
	issues := make([]Issue, 0)
	appendIssue := func(issue Issue, issueErr error) error {
		if issueErr != nil {
			return issueErr
		}
		issues = append(issues, issue)
		return nil
	}
	for _, entity := range entities {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, field := range schemas.Validate(entity) {
			if err := appendIssue(NewIssue(SeverityError, "SCHEMA_INVALID", entity.ID, fieldPathPointer(field.Path), nil, nil, map[string]string{"message": field.Message}, nil, versions.Registry)); err != nil {
				return nil, err
			}
		}
	}
	if scope == ScopeBase {
		return CloneIssues(issues), nil
	}
	references, bindings := WalkKnownSchema(entities)
	for _, finding := range ValidateReferences(entities, references) {
		if err := appendIssue(NewIssue(SeverityBlock, finding.Code, finding.Tuple.SourceID, finding.Tuple.FieldPath, nil, &finding.Tuple.Ordinal, nil, map[string]string{"target": string(finding.Tuple.TargetID)}, versions.Registry)); err != nil {
			return nil, err
		}
	}
	records, diagnostics := CompileFormulaBindings(bindings, registry, AttributeSymbols(entities, registry, nil))
	for _, diagnostic := range diagnostics {
		var span *FormulaSpan
		if diagnostic.Span.EndByte > diagnostic.Span.StartByte {
			value := FormulaSpan{StartByte: diagnostic.Span.StartByte, EndByte: diagnostic.Span.EndByte}
			span = &value
		}
		if err := appendIssue(NewIssue(SeverityBlock, diagnostic.Code, diagnostic.SourceID, diagnostic.FieldPath, span, nil, nil, nil, versions.Registry)); err != nil {
			return nil, err
		}
	}
	if scope != ScopeFull {
		return CloneIssues(issues), nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	nodes := make([]FormulaNode, 0, len(records))
	producer := make(map[string]FormulaNode, len(records))
	attributeIDs := make(map[string]domain.ID)
	for _, entity := range entities {
		if entity.Kind == domain.KindAttribute && entity.Status == domain.StatusActive {
			attributeIDs[entity.Key] = entity.ID
			attributeIDs[string(entity.ID)] = entity.ID
		}
	}
	for _, record := range records {
		node := FormulaNode{EntityID: record.SourceID, FieldPath: record.FieldPath, OutputAttributeID: record.OutputAttributeID}
		nodes = append(nodes, node)
		producer[string(record.SourceID)+"|"+string(record.OutputAttributeID)] = node
	}
	edges := make([]FormulaEdge, 0)
	for _, record := range records {
		from := FormulaNode{EntityID: record.SourceID, FieldPath: record.FieldPath, OutputAttributeID: record.OutputAttributeID}
		for _, read := range record.Reads {
			if read.Scope == "self" {
				target := attributeIDs[read.Symbol]
				if to, ok := producer[string(record.SourceID)+"|"+string(target)]; ok {
					edges = append(edges, FormulaEdge{From: from, To: to})
				}
			}
		}
	}
	for _, cycle := range StaticFormulaCycles(nodes, edges) {
		if err := appendIssue(NewIssue(SeverityBlock, "STATIC_FORMULA_CYCLE", cycle.Node.EntityID, cycle.Node.FieldPath, nil, nil, nil, map[string]string{"cycle_hash": cycle.CycleHash}, versions.Registry)); err != nil {
			return nil, err
		}
	}
	triggerRecords, stackRecords := ExtractRuleSafety(entities)
	triggerEdges := make([]TriggerEdge, len(triggerRecords))
	for index, record := range triggerRecords {
		triggerEdges[index] = record.Edge
	}
	for _, loop := range UnboundedEventLoops(triggerEdges) {
		for _, record := range triggerRecords {
			for _, edge := range loop.Edges {
				if record.Edge == edge {
					if err := appendIssue(NewIssue(SeverityBlock, "EVENT_LOOP_UNBOUNDED", record.EntityID, record.FieldPath, nil, nil, nil, map[string]string{"cycle_hash": loop.EvidenceHash}, versions.Registry)); err != nil {
						return nil, err
					}
					break
				}
			}
		}
	}
	for _, record := range stackRecords {
		for _, code := range ValidateStackRule(record.Rule) {
			if err := appendIssue(NewIssue(SeverityBlock, code, record.EntityID, record.FieldPath, nil, nil, nil, nil, versions.Registry)); err != nil {
				return nil, err
			}
		}
	}
	return CloneIssues(issues), nil
}

func fieldPathPointer(path string) string {
	if path == "" {
		return "/"
	}
	return "/" + strings.ReplaceAll(strings.ReplaceAll(path, "[", "/"), "]", "")
}

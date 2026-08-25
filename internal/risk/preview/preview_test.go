package preview

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
	riskcomparison "github.com/zouyi/eco-guardian/internal/risk/comparison"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
	riskstructure "github.com/zouyi/eco-guardian/internal/risk/structure"
	"github.com/zouyi/eco-guardian/internal/risk/threshold"
	"github.com/zouyi/eco-guardian/internal/validation"
	versioningdiff "github.com/zouyi/eco-guardian/internal/versioning/diff"
)

const previewHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func previewRequest(t *testing.T, candidateValue string) (Service, Request) {
	t.Helper()
	registries, err := riskcontract.V1Registries()
	if err != nil {
		t.Fatal(err)
	}
	body := threshold.StarterFixtureV1().Body
	bodyHash, err := body.Hash()
	if err != nil {
		t.Fatal(err)
	}
	comparison := MetricComparison{
		ID:           "dps-preview",
		Role:         riskcontract.Required,
		SceneID:      "single-target-30s",
		SceneVersion: "v1",
		Subject: riskcontract.Subject{
			Kind:       riskcontract.SingletonSubject,
			EntityKind: "character",
			StableID:   "01948c1e-0000-7000-8000-000000000011",
			Members:    []domain.ID{"01948c1e-0000-7000-8000-000000000011"},
		},
		Candidate:         previewMetric("metric-dps", "points_per_second", riskcontract.HigherIsRisk, candidateValue),
		Baseline:          previewMetric("metric-dps", "points_per_second", riskcontract.HigherIsRisk, "100"),
		CandidateEvidence: EvidenceRef{Kind: AdvisorySimulation, ID: "proposal-simulation", InputHash: previewHash, FingerprintHash: strings.Repeat("b", 64), ResultHash: strings.Repeat("c", 64)},
		BaselineEvidence:  EvidenceRef{Kind: FormalSimulation, ID: "baseline-run", InputHash: strings.Repeat("d", 64), FingerprintHash: strings.Repeat("e", 64), ResultHash: strings.Repeat("f", 64)},
	}
	request := Request{
		SchemaVersion:               SchemaVersionV1,
		ProjectID:                   "01948c1e-0000-7000-8000-000000000010",
		ProposalMaterializationHash: previewHash,
		ThresholdIdentity:           riskcontract.Identity{ID: "threshold-v1", Version: "v1", Hash: bodyHash},
		ThresholdBody:               body,
		Comparisons:                 []MetricComparison{comparison},
		Structural: StructuralInput{Versions: riskstructure.IndexContract{
			ASTVersion: formula.ASTSchemaVersion, DSLVersion: formula.DSLVersion, RegistryVersion: "registry-v1",
		}},
	}
	return Service{Registries: registries}, request
}

func previewMetric(id, unit string, direction riskcontract.MetricDirection, value string) riskcontract.MetricEvidence {
	low, high := value, value
	return riskcontract.MetricEvidence{MetricID: id, MetricVersion: "v1", Status: riskcontract.MetricAvailable, Unit: unit, Direction: direction, Value: &value, ConfidenceLow: &low, ConfidenceHigh: &high, SampleCount: 1000, Assumptions: []string{"sealed proposal", "fixed scene"}, CanonicalHash: previewHash}
}

func TestServiceUsesFormalComparisonSemanticsWithoutFormalCandidateRun(t *testing.T) {
	service, request := previewRequest(t, "125")
	result, err := service.Evaluate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Advisory || result.Acceptable || result.ProposalMaterializationHash != previewHash || len(result.Metrics) != 1 || len(result.ResultHash) != 64 {
		t.Fatalf("result=%#v", result)
	}
	preview := result.Metrics[0]
	if preview.CandidateEvidence.Kind != AdvisorySimulation || preview.Assessment.Status != riskcontract.Comparable || preview.Assessment.Severity == nil || *preview.Assessment.Severity != riskcontract.Block || preview.Assessment.Override != riskcontract.NumericOverrideEligible {
		t.Fatalf("metric preview=%#v", preview)
	}
	formal := riskcomparison.Compare(riskcomparison.Request{
		ID: request.Comparisons[0].ID, Role: request.Comparisons[0].Role, Candidate: request.Comparisons[0].Candidate, Baseline: request.Comparisons[0].Baseline,
		Threshold: preview.Threshold.Entry, Rule: preview.Rule, Subject: request.Comparisons[0].Subject,
		CandidateRun: riskcomparison.RunRef{ID: "01948c1e-0000-7000-8000-000000000012", ResultHash: request.Comparisons[0].CandidateEvidence.ResultHash},
		BaselineRun:  riskcomparison.RunRef{ID: "01948c1e-0000-7000-8000-000000000013", ResultHash: request.Comparisons[0].BaselineEvidence.ResultHash},
	})
	if formal.Status != preview.Assessment.Status || !reflect.DeepEqual(formal.Severity, preview.Assessment.Severity) || !reflect.DeepEqual(formal.PolicyEffect, preview.Assessment.PolicyEffect) || formal.RiskDelta == nil || preview.Assessment.RiskDelta == nil || *formal.RiskDelta != *preview.Assessment.RiskDelta {
		t.Fatalf("formal=%#v preview=%#v", formal, preview.Assessment)
	}
}

func TestServiceCanonicalizesComparisonAndAssumptionOrder(t *testing.T) {
	service, request := previewRequest(t, "110")
	second := request.Comparisons[0]
	second.ID = "a-healing-preview"
	second.Candidate = previewMetric("metric-healing", "points_per_second", riskcontract.HigherIsRisk, "105")
	second.Baseline = previewMetric("metric-healing", "points_per_second", riskcontract.HigherIsRisk, "100")
	request.Comparisons = append(request.Comparisons, second)
	first, err := service.Evaluate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	shuffled := request
	shuffled.Comparisons = append([]MetricComparison(nil), request.Comparisons...)
	shuffled.Comparisons[0], shuffled.Comparisons[1] = shuffled.Comparisons[1], shuffled.Comparisons[0]
	for index := range shuffled.Comparisons {
		shuffled.Comparisons[index].Candidate.Assumptions = append([]string(nil), shuffled.Comparisons[index].Candidate.Assumptions...)
		shuffled.Comparisons[index].Candidate.Assumptions[0], shuffled.Comparisons[index].Candidate.Assumptions[1] = shuffled.Comparisons[index].Candidate.Assumptions[1], shuffled.Comparisons[index].Candidate.Assumptions[0]
	}
	secondResult, err := service.Evaluate(context.Background(), shuffled)
	if err != nil || first.InputHash != secondResult.InputHash || first.ResultHash != secondResult.ResultHash || first.Metrics[0].ID != "a-healing-preview" {
		t.Fatalf("first=%#v second=%#v err=%v", first, secondResult, err)
	}
	if !first.Acceptable || first.Metrics[1].Assessment.Severity == nil || *first.Metrics[1].Assessment.Severity != riskcontract.Warning {
		t.Fatalf("warning preview=%#v", first)
	}
}

func TestServiceReusesStructuralRulesAndPreservesBlock(t *testing.T) {
	service, request := previewRequest(t, "100")
	entity := domain.ID("01948c1e-0000-7000-8000-000000000021")
	output := domain.ID("01948c1e-0000-7000-8000-000000000022")
	path := "/payload/formula"
	request.Structural.Changes = []versioningdiff.FieldChange{{EntityID: entity, EntityKind: domain.KindCharacter, Path: path, Kind: versioningdiff.Modify, OldValue: json.RawMessage(`"old"`), NewValue: json.RawMessage(`"new"`)}}
	request.Structural.BaselineIndexes = []validation.FormulaIndexRecord{previewFormulaRecord(t, entity, output, path, previewSelector("self", "power"))}
	request.Structural.CandidateIndexes = []validation.FormulaIndexRecord{previewFormulaRecord(t, entity, output, path, previewBinary("*", previewSelector("self", "power"), previewSelector("self", "scale")))}
	result, err := service.Evaluate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Acceptable || len(result.Structural) != 1 || result.Structural[0].Finding.Severity != riskcontract.Block || result.Structural[0].Finding.Override != riskcontract.NonOverridable || result.Structural[0].Finding.Evidence == nil || result.Structural[0].Finding.Evidence.Kind != riskcontract.NewMultiplierIssue {
		t.Fatalf("structural=%#v", result.Structural)
	}
}

func TestServiceRejectsNonAdvisoryCandidateTamperingAndCancellation(t *testing.T) {
	service, request := previewRequest(t, "110")
	request.Comparisons[0].CandidateEvidence.Kind = FormalSimulation
	if _, err := service.Evaluate(context.Background(), request); !errors.Is(err, ErrInvalid) {
		t.Fatalf("formal candidate error=%v", err)
	}

	service, request = previewRequest(t, "110")
	request.ThresholdIdentity.Hash = strings.Repeat("f", 64)
	if _, err := service.Evaluate(context.Background(), request); !errors.Is(err, ErrInvalid) {
		t.Fatalf("threshold tamper error=%v", err)
	}

	service, request = previewRequest(t, "110")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Evaluate(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
}

func previewFormulaRecord(t *testing.T, source, output domain.ID, path string, root formula.Node) validation.FormulaIndexRecord {
	t.Helper()
	ast := formula.NewAST(root)
	body, err := ast.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := ast.Hash()
	if err != nil {
		t.Fatal(err)
	}
	return validation.FormulaIndexRecord{SourceID: source, OutputAttributeID: output, FieldPath: path, FormulaHash: previewHash, ASTHash: hash, AST: body, ASTVersion: formula.ASTSchemaVersion, DSLVersion: formula.DSLVersion, RegistryVersion: "registry-v1"}
}

func previewSelector(scope, symbol string) formula.Node {
	return formula.Node{Kind: formula.NodeSelector, Scope: scope, Symbol: symbol, Span: formula.Span{StartByte: 0, EndByte: 1}}
}

func previewBinary(operator string, left, right formula.Node) formula.Node {
	return formula.Node{Kind: formula.NodeBinary, Operator: operator, Args: []formula.Node{left, right}, Span: formula.Span{StartByte: 0, EndByte: 1}}
}

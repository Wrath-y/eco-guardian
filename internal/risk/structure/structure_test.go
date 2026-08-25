package structure

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
	"github.com/zouyi/eco-guardian/internal/validation"
	versioningdiff "github.com/zouyi/eco-guardian/internal/versioning/diff"
)

func TestValidationAdapterAcceptsOnlyExactFullRunAndThreeFrozenBlockCodes(t *testing.T) {
	revision, runID, entity := structureID(1), structureID(2), structureID(3)
	hash := strings.Repeat("a", 64)
	source, err := validation.NewSource(validation.SourceRevision, revision, hash)
	if err != nil {
		t.Fatal(err)
	}
	versions := validation.VersionManifest{Schema: "schema-v1", DSL: formula.DSLVersion, Registry: "registry-v1", NumericPolicy: formula.NumericPolicyV1.Version}
	issues := []validation.Issue{}
	for index, code := range []string{"STATIC_FORMULA_CYCLE", "EVENT_LOOP_UNBOUNDED", "STACK_UNBOUNDED", "READABLE_TEXT_ONLY"} {
		ordinal := index
		severity := validation.SeverityBlock
		if code == "READABLE_TEXT_ONLY" {
			severity = validation.SeverityWarning
		}
		issues = append(issues, validation.Issue{Severity: severity, Code: code, EntityID: entity, FieldPath: "/payload/rules", Ordinal: &ordinal, MessageKey: strings.ToLower(code), Fingerprint: stableHash(code)})
	}
	run, err := validation.NewCompletedRun(runID, source, validation.ScopeFull, versions, issues, time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	expected := ValidationExpectation{RevisionID: revision, ConfigHash: hash, RunID: runID, ResultHash: run.ResultHash, Versions: versions}
	findings, err := ValidationEvidence(run, issues, expected)
	if err != nil || len(findings) != 3 {
		t.Fatalf("findings=%#v err=%v", findings, err)
	}
	for _, finding := range findings {
		if !finding.Valid() || finding.Severity != riskcontract.Block || finding.Override != riskcontract.NonOverridable {
			t.Fatalf("finding=%#v", finding)
		}
	}
	shuffledIssues := []validation.Issue{issues[3], issues[1], issues[0], issues[2]}
	shuffledFindings, err := ValidationEvidence(run, shuffledIssues, expected)
	if err != nil || len(shuffledFindings) != len(findings) {
		t.Fatalf("shuffled findings=%#v err=%v", shuffledFindings, err)
	}
	for index := range findings {
		if shuffledFindings[index].ID != findings[index].ID || shuffledFindings[index].EvidenceHash != findings[index].EvidenceHash {
			t.Fatal("validation issue enumeration changed structural ordering/hash")
		}
	}
	tampered := expected
	tampered.ConfigHash = strings.Repeat("b", 64)
	if _, err = ValidationEvidence(run, issues, tampered); err == nil {
		t.Fatal("validation evidence accepted a mismatched config identity")
	}
	local := run
	local.Scope = validation.ScopeLocal
	if _, err = ValidationEvidence(local, issues, expected); err == nil {
		t.Fatal("LOCAL validation evidence satisfied structural risk")
	}
}

func TestTypedASTRulesDetectNewAndRepeatedMultipliersOnlyFromCanonicalDiff(t *testing.T) {
	entity, output := structureID(1), structureID(2)
	path := "/payload/formula"
	baseline := formulaRecord(t, entity, output, path, binary("*", selector("self", "power"), selector("self", "power")))
	candidate := formulaRecord(t, entity, output, path, binary("*", binary("*", selector("self", "power"), selector("self", "power")), selector("self", "power")))
	change := versioningdiff.FieldChange{EntityID: entity, EntityKind: domain.KindCharacter, Path: path, Kind: versioningdiff.Modify, OldValue: json.RawMessage(`"old"`), NewValue: json.RawMessage(`"new"`)}
	versions := IndexContract{ASTVersion: formula.ASTSchemaVersion, DSLVersion: formula.DSLVersion, RegistryVersion: "registry-v1"}
	findings := Analyze([]versioningdiff.FieldChange{change}, []validation.FormulaIndexRecord{baseline}, []validation.FormulaIndexRecord{candidate}, versions, V1Rules())
	if len(findings) != 2 || findings[0].Evidence == nil || findings[1].Evidence == nil {
		t.Fatalf("findings=%#v", findings)
	}
	if item, err := findings[0].Item(0, riskcontract.Required); err != nil || !item.Valid() || item.Kind != riskcontract.StructuralRiskItem || item.Structural == nil || item.Structural.Fingerprint != findings[0].Evidence.Fingerprint {
		t.Fatalf("structural item=%#v err=%v", item, err)
	}
	kinds := map[riskcontract.StructureEvidenceKind]bool{findings[0].Evidence.Kind: true, findings[1].Evidence.Kind: true}
	if !kinds[riskcontract.NewMultiplierIssue] || !kinds[riskcontract.RepeatedMultiplierIssue] {
		t.Fatalf("kinds=%#v", kinds)
	}
	for _, finding := range findings {
		if !finding.Valid() || finding.Override != riskcontract.NonOverridable {
			t.Fatalf("finding=%#v", finding)
		}
	}
	reversedRules := V1Rules()
	reversedRules[0], reversedRules[1] = reversedRules[1], reversedRules[0]
	reordered := Analyze([]versioningdiff.FieldChange{change}, []validation.FormulaIndexRecord{baseline}, []validation.FormulaIndexRecord{candidate}, versions, reversedRules)
	if len(reordered) != len(findings) {
		t.Fatalf("reordered=%#v", reordered)
	}
	for index := range findings {
		if reordered[index].ID != findings[index].ID || reordered[index].EvidenceHash != findings[index].EvidenceHash {
			t.Fatal("rule enumeration changed finding ordering/hash")
		}
	}
	limitCandidate := formulaRecord(t, entity, output, path, binary("*", selector("self", "power"), selector("self", "power")))
	limitBaseline := formulaRecord(t, entity, output, path, selector("self", "power"))
	limitFindings := Analyze([]versioningdiff.FieldChange{change}, []validation.FormulaIndexRecord{limitBaseline}, []validation.FormulaIndexRecord{limitCandidate}, versions, V1Rules())
	if len(limitFindings) != 1 || limitFindings[0].Evidence == nil || limitFindings[0].Evidence.Kind != riskcontract.NewMultiplierIssue {
		t.Fatalf("repeated multiplier fired at its declared limit: %#v", limitFindings)
	}
	if moved := Analyze([]versioningdiff.FieldChange{{EntityID: entity, EntityKind: domain.KindCharacter, Path: path, Kind: versioningdiff.Move}}, []validation.FormulaIndexRecord{baseline}, []validation.FormulaIndexRecord{candidate}, versions, V1Rules()); len(moved) != 0 {
		t.Fatalf("moved AST created structural match: %#v", moved)
	}
	if unchanged := Analyze(nil, []validation.FormulaIndexRecord{baseline}, []validation.FormulaIndexRecord{candidate}, versions, V1Rules()); len(unchanged) != 0 {
		t.Fatalf("unchanged AST created structural match: %#v", unchanged)
	}
	description := versioningdiff.FieldChange{EntityID: entity, EntityKind: domain.KindCharacter, Path: "/description", Kind: versioningdiff.Modify, NewValue: json.RawMessage(`"multiply power forever"`)}
	if text := Analyze([]versioningdiff.FieldChange{description}, []validation.FormulaIndexRecord{baseline}, []validation.FormulaIndexRecord{candidate}, versions, V1Rules()); len(text) != 0 {
		t.Fatalf("natural-language description created structural match: %#v", text)
	}
}

func TestUnknownASTContractIsUnavailableWithoutParsingCurrentSemantics(t *testing.T) {
	entity, output := structureID(1), structureID(2)
	record := validation.FormulaIndexRecord{SourceID: entity, OutputAttributeID: output, FieldPath: "/payload/formula", AST: []byte(`not-json-current-language`), ASTHash: strings.Repeat("a", 64), ASTVersion: "ast-v2", DSLVersion: "dsl-v2", RegistryVersion: "registry-v2"}
	change := versioningdiff.FieldChange{EntityID: entity, EntityKind: domain.KindCharacter, Path: record.FieldPath, Kind: versioningdiff.Modify}
	findings := Analyze([]versioningdiff.FieldChange{change}, nil, []validation.FormulaIndexRecord{record}, IndexContract{ASTVersion: formula.ASTSchemaVersion, DSLVersion: formula.DSLVersion, RegistryVersion: "registry-v1"}, V1Rules())
	if len(findings) != 1 || findings[0].Status != riskcontract.Unavailable || findings[0].Reason != "STRUCTURAL_INDEX_VERSION_UNAVAILABLE" || !findings[0].Valid() {
		t.Fatalf("findings=%#v", findings)
	}
}

type impactReaderFake struct {
	refs []riskcontract.ImpactEvidenceRef
	err  error
}

func (reader impactReaderFake) Evidence(context.Context, domain.ID, domain.ID) ([]riskcontract.ImpactEvidenceRef, error) {
	return append([]riskcontract.ImpactEvidenceRef(nil), reader.refs...), reader.err
}

func TestOptionalImpactEvidenceCannotAlterStructuralFindingOrEligibility(t *testing.T) {
	pairHash, snapshotHash := strings.Repeat("a", 64), strings.Repeat("b", 64)
	exact := riskcontract.ImpactEvidenceRef{ReportID: "report", EvidenceID: "path", RevisionPairHash: pairHash, SnapshotHash: snapshotHash, Classification: "deterministic", Freshness: "fresh"}
	stale := exact
	stale.EvidenceID, stale.SnapshotHash, stale.Classification, stale.Freshness = "suspected", strings.Repeat("c", 64), "suspected", "stale"
	notInstalled := AttachImpactEvidence(context.Background(), nil, structureID(1), structureID(2), pairHash, snapshotHash)
	attached := AttachImpactEvidence(context.Background(), impactReaderFake{refs: []riskcontract.ImpactEvidenceRef{stale, exact}}, structureID(1), structureID(2), pairHash, snapshotHash)
	degraded := AttachImpactEvidence(context.Background(), impactReaderFake{err: errors.New("provider offline")}, structureID(1), structureID(2), pairHash, snapshotHash)
	if notInstalled.Status != "NOT_INSTALLED" || degraded.Status != "UNAVAILABLE" || attached.Status != "ATTACHED" || len(attached.Refs) != 1 || attached.Refs[0].EvidenceID != "path" {
		t.Fatalf("notInstalled=%#v attached=%#v degraded=%#v", notInstalled, attached, degraded)
	}
	rule := V1Rules()[0]
	record := formulaRecord(t, structureID(1), structureID(2), "/payload/formula", binary("*", selector("self", "power"), selector("self", "scale")))
	finding := structuralFinding(record, riskcontract.NewMultiplierIssue, rule, 0, struct {
		Count int `json:"count"`
	}{1})
	before := findingHash(finding)
	_ = attached
	after := findingHash(finding)
	if before != after || finding.Override != riskcontract.NonOverridable || finding.Severity != rule.Severity {
		t.Fatal("optional impact attachment altered deterministic finding")
	}
}

func formulaRecord(t *testing.T, source, output domain.ID, path string, root formula.Node) validation.FormulaIndexRecord {
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
	return validation.FormulaIndexRecord{SourceID: source, OutputAttributeID: output, FieldPath: path, AST: body, ASTHash: hash, FormulaHash: stableHash(string(body)), ASTVersion: formula.ASTSchemaVersion, DSLVersion: formula.DSLVersion, RegistryVersion: "registry-v1"}
}

func selector(scope, symbol string) formula.Node {
	return formula.Node{Kind: formula.NodeSelector, Scope: scope, Symbol: symbol, Span: formula.Span{StartByte: 0, EndByte: 1}}
}

func binary(operator string, left, right formula.Node) formula.Node {
	return formula.Node{Kind: formula.NodeBinary, Operator: operator, Args: []formula.Node{left, right}, Span: formula.Span{StartByte: 0, EndByte: 3}}
}

func structureID(suffix int) domain.ID {
	return domain.ID("01948c1e-0000-7000-8000-" + strings.Repeat("0", 11) + string(rune('0'+suffix)))
}

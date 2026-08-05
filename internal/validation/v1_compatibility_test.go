package validation

import (
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
)

// TestV1CompatibilityGoldens freezes retained-v1 parser, numeric, unit,
// catalog, cycle, fingerprint, and result identities. New versions may add a
// separate suite but must never silently reinterpret these values.
func TestV1CompatibilityGoldens(t *testing.T) {
	registry, err := formula.V1Registry()
	if err != nil {
		t.Fatal(err)
	}
	parsed := formula.Parse("if(false, 1[ratio], 0[ratio])", registry)
	astHash, err := parsed.AST.Hash()
	if err != nil || len(parsed.Errors) != 0 || astHash != "37dd92ec1082714e509c472a277ac59ab59a3bdebc5b2faf038cbb5155cb761c" {
		t.Fatalf("parse v1: %v %#v", err, parsed.Errors)
	}
	decimal, err := formula.ParseDecimal("1.2345678901234567890123456789012345")
	if err != nil || decimal.String() != "1.234567890123456789012345678901234" {
		t.Fatalf("decimal v1: %v %q", err, decimal.String())
	}
	percentage, err := formula.ParsePercentageDisplay("100")
	if err != nil || percentage.String() != "1" {
		t.Fatalf("percentage v1: %v %q", err, percentage.String())
	}
	duration, err := formula.ParseDuration("1000")
	if err != nil || duration.String() != "1000" {
		t.Fatalf("duration v1: %v %q", err, duration.String())
	}
	entity := domain.ID("01948c1e-0000-7000-8000-000000000000")
	issue, err := NewIssue(SeverityBlock, "REFERENCE_NOT_FOUND", entity, "/payload/effect_ids/0", nil, nil, nil, map[string]string{"target": "x"}, registry.ManifestHash())
	if err != nil {
		t.Fatal(err)
	}
	source, err := NewSource(SourceWorking, "", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	versions := VersionManifest{Schema: "schema-v1", DSL: formula.DSLVersion, Registry: registry.ManifestHash(), NumericPolicy: formula.NumericPolicyV1.Version}
	result, err := ResultHash(source, ScopeFull, versions, []Issue{issue})
	if err != nil || registry.ManifestHash() != "f19067585963197109c484e1e02f7a46cbf3ec508ac81391756b27b089bb93ab" || issue.Fingerprint != "75f1ceb5a6dc2dfe0f657ce20392845d5ae74c51472d887fd42110580a59b9d2" || result != "ca95221312a99a70df9dfc833da3f3e418b6ef1d45a8f8f1c1c8d9bd3b4aaece" {
		t.Fatal(err)
	}
	first := FormulaNode{EntityID: entity, FieldPath: "/payload/a", OutputAttributeID: entity}
	cycle := StaticFormulaCycles([]FormulaNode{first}, []FormulaEdge{{From: first, To: first}})
	if len(cycle) != 1 || cycle[0].CycleHash != "2056c614e6765189588955e5b37dee1e222e26db85cb84cf6e17b9de70526e84" {
		t.Fatalf("self cycle=%#v", cycle)
	}
}

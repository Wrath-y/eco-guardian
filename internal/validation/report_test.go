package validation

import (
	"context"
	"testing"
)

func TestCanonicalIssuesAndPhasedOrchestration(t *testing.T) {
	id := referenceID(t)
	issue, err := NewIssue(SeverityBlock, "REFERENCE_NOT_FOUND", id, "/payload/effect_ids/0", nil, nil, map[string]string{"b": "2", "a": "1"}, map[string]string{"target": "x", "kind": "effect"}, "registry-v1")
	if err != nil {
		t.Fatal(err)
	}
	again, err := NewIssue(SeverityBlock, "REFERENCE_NOT_FOUND", id, "/payload/effect_ids/0", nil, nil, map[string]string{"a": "1", "b": "2"}, map[string]string{"kind": "effect", "target": "x"}, "registry-v1")
	if err != nil || issue.Fingerprint != again.Fingerprint {
		t.Fatalf("%#v %#v %v", issue, again, err)
	}
	for _, code := range CodesV1 {
		if _, ok := CodeTextsV1[code.Name]; !ok {
			t.Fatalf("missing text %s", code.Name)
		}
	}
	orchestrator, err := NewOrchestrator([]Validator{{"base", ScopeBase, "SCHEMA_INVALID", 2, func(context.Context) ([]Issue, error) { return []Issue{issue}, nil }}, {"full", ScopeFull, "STATIC_FORMULA_CYCLE", 1, func(context.Context) ([]Issue, error) { t.Fatal("FULL validator ran locally"); return nil, nil }}})
	if err != nil {
		t.Fatal(err)
	}
	issues, err := orchestrator.Run(context.Background(), ScopeLocal)
	if err != nil || len(issues) != 0 {
		t.Fatalf("%#v %v", issues, err)
	}
	issues, err = orchestrator.Run(context.Background(), ScopeBase)
	if err != nil || len(issues) != 1 {
		t.Fatalf("%#v %v", issues, err)
	}
}

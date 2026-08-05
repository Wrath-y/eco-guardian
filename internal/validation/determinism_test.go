package validation

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
)

func TestDeterministicIssueAndGraphResultsAcrossInputOrder(t *testing.T) {
	entityA := domain.ID("01948c1e-0000-7000-8000-000000000000")
	entityB := domain.ID("01948c1e-0000-7000-8000-000000000001")
	issues := make([]Issue, 0, 2)
	for _, input := range []struct {
		id   domain.ID
		path string
		code string
	}{{entityB, "/payload/effect_ids/1", "REFERENCE_NOT_FOUND"}, {entityA, "/payload/effect_ids/0", "REFERENCE_KIND_MISMATCH"}} {
		issue, err := NewIssue(SeverityBlock, input.code, input.id, input.path, nil, nil, nil, map[string]string{"target": "x", "kind": "effect"}, "registry-v1")
		if err != nil {
			t.Fatal(err)
		}
		issues = append(issues, issue)
	}
	source, err := NewSource(SourceWorking, "", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	versions := VersionManifest{Schema: "schema-v1", DSL: "dsl-v1", Registry: "registry-v1", NumericPolicy: "decimal128-v1"}
	baseline, err := ResultHash(source, ScopeFull, versions, issues)
	if err != nil {
		t.Fatal(err)
	}
	for _, permuted := range [][]Issue{issues, {issues[1], issues[0]}, {issues[0], issues[1]}} {
		hash, err := ResultHash(source, ScopeFull, versions, permuted)
		if err != nil || hash != baseline || !reflect.DeepEqual(SortIssues(permuted), SortIssues(issues)) {
			t.Fatalf("hash=%q baseline=%q sorted=%#v err=%v", hash, baseline, SortIssues(permuted), err)
		}
	}
	var concurrent sync.WaitGroup
	hashes := make(chan string, 32)
	for index := 0; index < cap(hashes); index++ {
		concurrent.Add(1)
		go func(reverse bool) {
			defer concurrent.Done()
			input := issues
			if reverse {
				input = []Issue{issues[1], issues[0]}
			}
			hash, runErr := ResultHash(source, ScopeFull, versions, input)
			if runErr != nil {
				t.Errorf("concurrent result hash: %v", runErr)
				return
			}
			hashes <- hash
		}(index%2 == 1)
	}
	concurrent.Wait()
	close(hashes)
	for hash := range hashes {
		if hash != baseline {
			t.Fatalf("concurrent hash=%q baseline=%q", hash, baseline)
		}
	}
	first := FormulaNode{EntityID: entityA, FieldPath: "/payload/a", OutputAttributeID: entityA}
	second := FormulaNode{EntityID: entityB, FieldPath: "/payload/b", OutputAttributeID: entityB}
	forward := StaticFormulaCycles([]FormulaNode{first, second}, []FormulaEdge{{From: first, To: second}, {From: second, To: first}})
	reversed := StaticFormulaCycles([]FormulaNode{second, first}, []FormulaEdge{{From: second, To: first}, {From: first, To: second}})
	if !reflect.DeepEqual(forward, reversed) {
		t.Fatalf("cycle result changed with input order: %#v %#v", forward, reversed)
	}
	ordered, err := NewOrchestrator([]Validator{
		{Name: "later", Phase: ScopeFull, Code: "STATIC_FORMULA_CYCLE", Order: 2, Run: func(context.Context) ([]Issue, error) { return []Issue{issues[1]}, nil }},
		{Name: "first", Phase: ScopeFull, Code: "REFERENCE_NOT_FOUND", Order: 1, Run: func(context.Context) ([]Issue, error) { return []Issue{issues[0]}, nil }},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ordered.Run(context.Background(), ScopeFull)
	if err != nil || !reflect.DeepEqual(got, SortIssues(issues)) {
		t.Fatalf("validator order=%#v err=%v", got, err)
	}
	firstRun, err := NewCompletedRun(entityA, source, ScopeFull, versions, issues, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	secondRun, err := NewCompletedRun(entityB, source, ScopeFull, versions, []Issue{issues[1], issues[0]}, time.Unix(1, 0))
	if err != nil || firstRun.ResultHash != secondRun.ResultHash || firstRun.Summary != secondRun.Summary {
		t.Fatalf("run determinism=%#v %#v err=%v", firstRun, secondRun, err)
	}
}

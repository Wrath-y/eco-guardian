package validation

import (
	"context"
	"github.com/zouyi/eco-guardian/internal/domain"
	"testing"
)

type fakeFullRuns struct {
	run   ValidationRun
	found bool
	err   error
}

func (f fakeFullRuns) FindMatchingFullRun(context.Context, domain.ID, string, VersionManifest) (ValidationRun, bool, error) {
	return f.run, f.found, f.err
}
func TestValidationGateOnlyPassesMatchingFullWithoutBlocks(t *testing.T) {
	revision := referenceID(t)
	versions := VersionManifest{Schema: "v1", DSL: "dsl", Registry: "registry", NumericPolicy: "numeric"}
	run := ValidationRun{Source: Source{Kind: SourceRevision, RevisionID: revision, InputHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, Scope: ScopeFull, Versions: versions, Summary: SeveritySummary{}}
	gate := NewValidationGate(fakeFullRuns{run: run, found: true})
	result, err := gate.Check(context.Background(), revision, run.Source.InputHash, versions)
	if err != nil || result != GatePass {
		t.Fatalf("%s %v", result, err)
	}
	run.Summary.Block = 1
	result, _ = NewValidationGate(fakeFullRuns{run: run, found: true}).Check(context.Background(), revision, run.Source.InputHash, versions)
	if result != GateBlocked {
		t.Fatalf("%s", result)
	}
	run.Scope = ScopeLocal
	result, _ = NewValidationGate(fakeFullRuns{run: run, found: true}).Check(context.Background(), revision, run.Source.InputHash, versions)
	if result != GateRequiresValidation {
		t.Fatalf("%s", result)
	}
	for _, candidate := range []fakeFullRuns{{found: false}, {run: ValidationRun{Source: Source{Kind: SourceRevision, RevisionID: referenceID(t), InputHash: run.Source.InputHash}, Scope: ScopeFull, Versions: versions}, found: true}, {run: ValidationRun{Source: Source{Kind: SourceRevision, RevisionID: revision, InputHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}, Scope: ScopeFull, Versions: versions}, found: true}, {run: ValidationRun{Source: Source{Kind: SourceRevision, RevisionID: revision, InputHash: run.Source.InputHash}, Scope: ScopeFull, Versions: VersionManifest{Schema: "v2", DSL: "dsl", Registry: "registry", NumericPolicy: "numeric"}}, found: true}} {
		result, _ = NewValidationGate(candidate).Check(context.Background(), revision, run.Source.InputHash, versions)
		if result != GateRequiresValidation {
			t.Fatalf("%s", result)
		}
	}
	for _, summary := range []SeveritySummary{{Error: 1}, {Block: 1}} {
		blocked := run
		blocked.Scope = ScopeFull
		blocked.Summary = summary
		result, _ = NewValidationGate(fakeFullRuns{run: blocked, found: true}).Check(context.Background(), revision, run.Source.InputHash, versions)
		if result != GateBlocked {
			t.Fatalf("%s", result)
		}
	}
	warnings := run
	warnings.Scope = ScopeFull
	warnings.Summary = SeveritySummary{Warning: 1, Info: 1}
	result, _ = NewValidationGate(fakeFullRuns{run: warnings, found: true}).Check(context.Background(), revision, run.Source.InputHash, versions)
	if result != GatePass {
		t.Fatalf("%s", result)
	}
}

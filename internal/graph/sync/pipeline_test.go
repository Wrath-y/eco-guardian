package sync

import (
	"context"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/validation"
)

type pipelineStateFake struct {
	state SyncState
	found bool
}

func (f *pipelineStateFake) GetGraphSyncState(context.Context, domain.ID) (SyncState, bool, error) {
	return f.state, f.found, nil
}
func (f *pipelineStateFake) CreateGraphSyncState(_ context.Context, state SyncState) error {
	f.state, f.found = state, true
	return nil
}
func (f *pipelineStateFake) CompareAndSwapGraphSyncState(_ context.Context, expected, next SyncState) (SyncState, bool, error) {
	if f.state.RevisionID != expected.RevisionID || f.state.Generation != expected.Generation || f.state.Pipeline != expected.Pipeline {
		return SyncState{}, false, nil
	}
	f.state = next
	return next, true, nil
}

type fullRunnerFake struct{ calls int }

func (f *fullRunnerFake) RunFullValidation(context.Context, domain.ID) error { f.calls++; return nil }

type pipelineJobsFake struct {
	calls int
	job   GraphJob
}

func (f *pipelineJobsFake) CreateOrGetGraphJob(context.Context, GraphJobRequest) (GraphJob, bool, error) {
	f.calls++
	return f.job, false, nil
}

func TestValidationPipelineRequiresExactPassBeforeQueueing(t *testing.T) {
	id, _ := domain.NewID()
	jobID, _ := domain.NewID()
	versions := validation.VersionManifest{Schema: "s", DSL: "d", Registry: "r", NumericPolicy: "n"}
	for _, test := range []struct {
		name    string
		initial validation.GateResult
		final   validation.GateResult
		want    PipelineState
		calls   int
	}{
		{"reuse pass", validation.GatePass, validation.GatePass, StateQueued, 0},
		{"run then pass", validation.GateRequiresValidation, validation.GatePass, StateQueued, 1},
		{"blocked", validation.GateBlocked, validation.GateBlocked, StateBlockedValidation, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			states, runner := &pipelineStateFake{}, &fullRunnerFake{}
			jobs := &pipelineJobsFake{job: GraphJob{ID: jobID, ProjectID: id, RevisionID: id, InputHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", IdempotencyKey: "automatic", RequestHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Status: JobQueued}}
			gate := &pipelineGateFake{results: []validation.GateResult{test.initial, test.final}}
			pipeline := ValidationPipeline{States: states, Validation: gate, Runner: runner, Jobs: jobs}
			got, err := pipeline.Start(context.Background(), ValidationPipelineRequest{ProjectID: id, RevisionID: id, ConfigHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Versions: versions})
			wantJobs := 0
			if test.want == StateQueued {
				wantJobs = 1
			}
			if err != nil || got.Pipeline != test.want || runner.calls != test.calls || jobs.calls != wantJobs {
				t.Fatalf("state=%#v runner=%d jobs=%d err=%v", got, runner.calls, jobs.calls, err)
			}
			if got.Pipeline == StateBlockedValidation && got.SafeError != "VALIDATION_NOT_PASSED" {
				t.Fatalf("state=%#v", got)
			}
		})
	}
}

type pipelineGateFake struct{ results []validation.GateResult }

func (f *pipelineGateFake) Check(context.Context, domain.ID, string, validation.VersionManifest) (validation.GateResult, error) {
	result := f.results[0]
	if len(f.results) > 1 {
		f.results = f.results[1:]
	}
	return result, nil
}

package sync

import (
	"context"
	"encoding/json"
	"errors"
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
	calls   int
	job     GraphJob
	request GraphJobRequest
}

func (f *pipelineJobsFake) CreateOrGetGraphJob(_ context.Context, request GraphJobRequest) (GraphJob, bool, error) {
	f.calls++
	f.request = request
	return f.job, false, nil
}

type pipelineEvidenceFake struct {
	warnings []string
	err      error
}

func (f pipelineEvidenceFake) FullValidationWarningCodes(context.Context, domain.ID, string, validation.VersionManifest) ([]string, error) {
	return append([]string(nil), f.warnings...), f.err
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
			pipeline := ValidationPipeline{States: states, Validation: gate, Runner: runner, Evidence: pipelineEvidenceFake{warnings: []string{"VECTOR_DEGRADED"}}, Jobs: jobs}
			got, err := pipeline.Start(context.Background(), ValidationPipelineRequest{ProjectID: id, RevisionID: id, ConfigHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Versions: versions})
			wantJobs := 0
			if test.want == StateQueued {
				wantJobs = 1
			}
			if err != nil || got.Pipeline != test.want || runner.calls != test.calls || jobs.calls != wantJobs {
				t.Fatalf("state=%#v runner=%d jobs=%d err=%v", got, runner.calls, jobs.calls, err)
			}
			if wantJobs == 1 {
				replayed, replayErr := pipeline.Start(context.Background(), ValidationPipelineRequest{ProjectID: id, RevisionID: id, ConfigHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Versions: versions})
				if replayErr != nil || replayed.Pipeline != StateQueued || jobs.calls != 1 {
					t.Fatalf("duplicate state=%#v jobs=%d err=%v", replayed, jobs.calls, replayErr)
				}
			}
			if wantJobs == 1 {
				var evidence struct {
					Warnings []string `json:"validation_warning_codes"`
				}
				if err := json.Unmarshal([]byte(jobs.request.Evidence), &evidence); err != nil || len(evidence.Warnings) != 1 || evidence.Warnings[0] != "VECTOR_DEGRADED" {
					t.Fatalf("evidence=%q err=%v", jobs.request.Evidence, err)
				}
			}
			if got.Pipeline == StateBlockedValidation && got.SafeError != "VALIDATION_NOT_PASSED" {
				t.Fatalf("state=%#v", got)
			}
		})
	}
}

func TestValidationPipelineBlocksWhenWarningEvidenceIsUnavailable(t *testing.T) {
	id, _ := domain.NewID()
	jobID, _ := domain.NewID()
	versions := validation.VersionManifest{Schema: "s", DSL: "d", Registry: "r", NumericPolicy: "n"}
	states := &pipelineStateFake{}
	jobs := &pipelineJobsFake{job: GraphJob{ID: jobID, ProjectID: id, RevisionID: id, InputHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", IdempotencyKey: "automatic", RequestHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Status: JobQueued}}
	pipeline := ValidationPipeline{
		States: states, Validation: &pipelineGateFake{results: []validation.GateResult{validation.GatePass}}, Runner: &fullRunnerFake{},
		Evidence: pipelineEvidenceFake{err: errors.New("validation reports unavailable")}, Jobs: jobs,
	}
	got, err := pipeline.Start(context.Background(), ValidationPipelineRequest{ProjectID: id, RevisionID: id, ConfigHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Versions: versions})
	if err != nil || got.Pipeline != StateBlockedValidation || got.SafeError != "VALIDATION_EVIDENCE_UNAVAILABLE" || jobs.calls != 0 {
		t.Fatalf("state=%#v jobs=%d err=%v", got, jobs.calls, err)
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

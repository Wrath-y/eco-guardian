package search

import (
	"context"
	"errors"
	"strings"
	"testing"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
)

type evaluatorFake struct {
	descriptor EvaluatorDescriptor
	calls      []Candidate
	cancel     context.CancelFunc
	blockAfter int
	blocked    bool
}

func (fake *evaluatorFake) Descriptor() EvaluatorDescriptor { return fake.descriptor }

func (fake *evaluatorFake) EvaluateCandidate(ctx context.Context, candidate Candidate) (Evaluation, error) {
	fake.calls = append(fake.calls, candidate)
	if fake.blockAfter > 0 && len(fake.calls) > fake.blockAfter {
		<-ctx.Done()
		return Evaluation{}, ctx.Err()
	}
	if fake.cancel != nil && len(fake.calls) == 1 {
		fake.cancel()
	}
	hash := strings.Repeat("b", 64)
	if fake.blocked {
		return Evaluation{Status: EvaluationValidationBlocked, ValidationResultHash: hash}, nil
	}
	return Evaluation{
		Status: EvaluationPassed, ValidationResultHash: hash, SimulationResultHash: strings.Repeat("c", 64),
		Objectives:  []Objective{{ID: "z-score", Value: "2"}, {ID: "a-score", Value: "1"}},
		Constraints: []Constraint{{ID: "limit", Satisfied: true, EvidenceHash: strings.Repeat("d", 64)}},
	}, nil
}

func TestSearchUsesRegisteredNumericOrderingAndExactCandidateBudget(t *testing.T) {
	evaluator, request := searchFixture()
	request.Budget.MaxCandidates = 3
	result, err := (Service{Evaluator: evaluator, Registered: evaluator.descriptor}).Search(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() || result.StopReason != StopCandidateBudget || len(result.Records) != 3 || len(evaluator.calls) != 3 {
		t.Fatalf("result=%#v calls=%d", result, len(evaluator.calls))
	}
	want := [][2]string{{"1", "10"}, {"1", "20"}, {"2", "10"}}
	for index, record := range result.Records {
		if len(record.Candidate.Assignments) != 2 || record.Candidate.Assignments[0].Value != want[index][0] || record.Candidate.Assignments[1].Value != want[index][1] {
			t.Fatalf("candidate %d=%#v", index, record.Candidate.Assignments)
		}
		if record.Evaluation == nil || record.Evaluation.Objectives[0].ID != "a-score" || len(record.Candidate.InputHash) != 64 || len(record.ResultHash) != 64 {
			t.Fatalf("record %d=%#v", index, record)
		}
	}
}

func TestSearchCompletesAndRecordsValidationBlockedResultsWithoutSimulation(t *testing.T) {
	evaluator, request := searchFixture()
	evaluator.blocked = true
	request.Dimensions = request.Dimensions[:1]
	request.Budget.MaxCandidates = 4
	result, err := (Service{Evaluator: evaluator, Registered: evaluator.descriptor}).Search(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() || result.StopReason != StopCompleted || len(result.Records) != 2 {
		t.Fatalf("result=%#v", result)
	}
	for _, record := range result.Records {
		if record.Evaluation == nil || record.Evaluation.Status != EvaluationValidationBlocked || record.Evaluation.SimulationResultHash != "" {
			t.Fatalf("blocked record=%#v", record)
		}
	}
}

func TestSearchStopsExactlyOnCancelAndTimeBudget(t *testing.T) {
	evaluator, request := searchFixture()
	ctx, cancel := context.WithCancel(context.Background())
	evaluator.cancel = cancel
	result, err := (Service{Evaluator: evaluator, Registered: evaluator.descriptor}).Search(ctx, request)
	if !errors.Is(err, context.Canceled) || !result.Valid() || result.StopReason != StopCanceled || len(result.Records) != 1 || len(evaluator.calls) != 1 {
		t.Fatalf("cancel result=%#v err=%v calls=%d", result, err, len(evaluator.calls))
	}

	evaluator, request = searchFixture()
	evaluator.blockAfter = 1
	request.Budget.MaxDurationMillis = 5
	result, err = (Service{Evaluator: evaluator, Registered: evaluator.descriptor}).Search(context.Background(), request)
	if !errors.Is(err, context.DeadlineExceeded) || !result.Valid() || result.StopReason != StopTimeBudget || len(result.Records) != 2 || len(evaluator.calls) != 2 || result.Records[1].ErrorCode != errorTimeBudget {
		t.Fatalf("time result=%#v err=%v calls=%d", result, err, len(evaluator.calls))
	}
}

func TestSearchRejectsUnauthorizedPathsAndEvaluatorDrift(t *testing.T) {
	evaluator, request := searchFixture()
	request.Dimensions[0].Path = "/payload/not_allowed"
	if _, err := (Service{Evaluator: evaluator, Registered: evaluator.descriptor}).Search(context.Background(), request); !errors.Is(err, ErrInvalid) {
		t.Fatalf("path error=%v", err)
	}
	evaluator, request = searchFixture()
	registered := evaluator.descriptor
	registered.Simulation.Hash = aicontract.Hash(strings.Repeat("f", 64))
	if _, err := (Service{Evaluator: evaluator, Registered: registered}).Search(context.Background(), request); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("descriptor drift error=%v", err)
	}
}

func searchFixture() (*evaluatorFake, Request) {
	hash := aicontract.Hash(strings.Repeat("a", 64))
	descriptor := EvaluatorDescriptor{
		Identity:   aicontract.VersionIdentity{ID: "proposal-candidate-evaluator", Version: "v1", Hash: hash},
		Validation: aicontract.VersionIdentity{ID: "validation-evaluator", Version: "v1", Hash: hash},
		Simulation: aicontract.VersionIdentity{ID: "simulation-evaluator", Version: "v1", Hash: hash},
	}
	first := aicontract.EntityID("018f9e40-0000-7000-8000-000000000210")
	second := aicontract.EntityID("018f9e40-0000-7000-8000-000000000211")
	request := Request{
		Base: aicontract.FrozenBaseIdentity{
			ProjectID: "018f9e40-0000-7000-8000-000000000201", ConfigRevisionID: "018f9e40-0000-7000-8000-000000000202",
			ConfigHash: hash, VersionManifestHash: hash, MaterializationHash: hash,
			GraphNamespace: "018f9e40-0000-7000-8000-000000000201", GraphSnapshot: "018f9e40-0000-7000-8000-000000000202", GraphContentHash: hash,
		},
		MaterializationHash: hash,
		AllowedTargets: []aicontract.AllowedTarget{
			{EntityID: first, Kind: "skill", ExpectedEntityVersion: 1, Paths: []aicontract.AllowedPath{{Path: "/payload/cooldown", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}}}},
			{EntityID: second, Kind: "effect", ExpectedEntityVersion: 1, Paths: []aicontract.AllowedPath{{Path: "/payload/duration", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}}}},
		},
		// Deliberately reverse dimensions and candidate values. The registered
		// ordering normalizes both before Cartesian enumeration.
		Dimensions: []Dimension{
			{EntityID: second, Path: "/payload/duration", Kind: Duration, Values: []string{"20", "10"}},
			{EntityID: first, Path: "/payload/cooldown", Kind: Duration, Range: &Range{Min: "1", Max: "2", Step: "1"}},
		},
		Budget: Budget{MaxCandidates: 4, MaxDurationMillis: 1_000},
	}
	return &evaluatorFake{descriptor: descriptor}, request
}

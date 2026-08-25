package preview

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/formula"
	"github.com/zouyi/eco-guardian/internal/simulation/contract"
	"github.com/zouyi/eco-guardian/internal/simulation/engine"
	"github.com/zouyi/eco-guardian/internal/simulation/metric"
	"github.com/zouyi/eco-guardian/internal/simulation/scenario"
)

type eventAdapterFake struct {
	descriptor contract.Descriptor
	mutate     bool
}

func (adapter *eventAdapterFake) Descriptor() contract.Descriptor { return adapter.descriptor }

func (adapter *eventAdapterFake) Prepare(_ context.Context, request SampleRequest) (engine.Evaluator, ObservationReader, error) {
	if adapter.mutate {
		request.Materialization.Materialization[0] = 'x'
		request.Input.Actions[0].ID = "mutated"
		request.Scene.Body[0] = 'x'
	}
	executed := 0
	evaluate := func(engine.Event, *engine.Emitter) error {
		executed++
		return nil
	}
	read := func(stats engine.Stats) ([]metric.Observation, error) {
		if stats.EventsExecuted != executed || executed == 0 {
			return nil, ErrInvalid
		}
		damage, err := formula.ParseDecimal(string(rune('1' + request.Ordinal)))
		if err != nil {
			return nil, err
		}
		healing, err := formula.ParseDecimal(string(rune('3' + request.Ordinal)))
		if err != nil {
			return nil, err
		}
		return []metric.Observation{
			{ID: "damage_per_second", Value: damage, Unit: "points_per_second"},
			{ID: "healing_per_second", Value: healing, Unit: "points_per_second"},
		}, nil
	}
	return evaluate, read, nil
}

func previewFixture(t *testing.T) (Service, Request) {
	t.Helper()
	scenarios, err := scenario.NewBuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := metric.V1Registry()
	if err != nil {
		t.Fatal(err)
	}
	manifests, err := contract.NewManifestRegistry(contract.RequiredV1Descriptors, contract.V1Descriptors())
	if err != nil {
		t.Fatal(err)
	}
	descriptor, found := manifests.Descriptor(evaluatorDescriptorID)
	if !found {
		t.Fatal("missing evaluator descriptor")
	}
	base := contract.Revision{ID: "01948c1e-0000-7000-8000-000000000001", ProjectID: "01948c1e-0000-7000-8000-000000000002", ConfigHash: strings.Repeat("a", 64), ManifestHash: strings.Repeat("b", 64)}
	materialization, err := SealMaterialization(base, []byte(`{"entities":[{"id":"01948c1e-0000-7000-8000-000000000003"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		Materialization: materialization,
		SceneID:         "single-target-30s",
		SceneVersion:    "v1",
		SampleCount:     2,
		Metrics: []contract.MetricIdentity{
			{ID: "metric-healing", Version: "v1"},
			{ID: "metric-dps", Version: "v1"},
		},
		RevisionImplementations: []contract.RevisionImplementation{
			{CapabilityID: "validator-registry", ContractVersion: "v1", ImplementationVersion: "v1", State: "registered"},
			{CapabilityID: "schema", ContractVersion: "v1", ImplementationVersion: "v1", State: "registered"},
			{CapabilityID: "numeric-policy", ContractVersion: "v1", ImplementationVersion: "v1", State: "registered"},
			{CapabilityID: "dsl", ContractVersion: "v1", ImplementationVersion: "v1", State: "registered"},
		},
	}
	return Service{Scenarios: scenarios, Metrics: metrics, Manifests: manifests, Samples: EngineSampleEvaluator{Adapter: &eventAdapterFake{descriptor: descriptor}}}, request
}

func TestServiceEvaluatesSealedProposalWithoutFormalRun(t *testing.T) {
	service, request := previewFixture(t)
	result, err := service.Evaluate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != SchemaVersionV1 || !result.Advisory || result.MaterializationHash != request.Materialization.Hash || result.Input.RuleMaterializationHash != request.Materialization.Hash {
		t.Fatalf("preview identity=%#v", result)
	}
	if len(result.Result.Metrics) != 2 || result.Result.Metrics[0].ID != "metric-dps" || result.Result.Metrics[0].Value != "1.5" || result.Result.Metrics[1].ID != "metric-healing" || result.Result.Metrics[1].Value != "3.5" {
		t.Fatalf("metrics=%#v", result.Result.Metrics)
	}
	if len(result.InputHash) != 64 || len(result.FingerprintHash) != 64 || len(result.ResultHash) != 64 || result.Result.InputHash != result.InputHash || result.Result.FingerprintHash != result.FingerprintHash {
		t.Fatalf("hashes input=%q fingerprint=%q result=%q", result.InputHash, result.FingerprintHash, result.ResultHash)
	}

	reversed := request
	reversed.Metrics = append([]contract.MetricIdentity(nil), request.Metrics...)
	reversed.Metrics[0], reversed.Metrics[1] = reversed.Metrics[1], reversed.Metrics[0]
	reversed.RevisionImplementations = append([]contract.RevisionImplementation(nil), request.RevisionImplementations...)
	for left, right := 0, len(reversed.RevisionImplementations)-1; left < right; left, right = left+1, right-1 {
		reversed.RevisionImplementations[left], reversed.RevisionImplementations[right] = reversed.RevisionImplementations[right], reversed.RevisionImplementations[left]
	}
	again, err := service.Evaluate(context.Background(), reversed)
	if err != nil || again.InputHash != result.InputHash || again.FingerprintHash != result.FingerprintHash || again.ResultHash != result.ResultHash {
		t.Fatalf("order changed identity: %#v err=%v", again, err)
	}
}

func TestServiceBindsMaterializationAndDefensivelyCopiesInput(t *testing.T) {
	service, request := previewFixture(t)
	service.Samples = EngineSampleEvaluator{Adapter: &eventAdapterFake{descriptor: service.Samples.Descriptor(), mutate: true}}
	original := append([]byte(nil), request.Materialization.Materialization...)
	first, err := service.Evaluate(context.Background(), request)
	if err != nil || string(request.Materialization.Materialization) != string(original) {
		t.Fatalf("caller materialization mutated or evaluation failed: err=%v", err)
	}

	changed := request
	changed.Materialization, err = SealMaterialization(request.Materialization.BaseRevision, []byte(`{"entities":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Evaluate(context.Background(), changed)
	if err != nil {
		t.Fatal(err)
	}
	if first.MaterializationHash == second.MaterializationHash || first.InputHash == second.InputHash || first.ResultHash == second.ResultHash {
		t.Fatal("proposal materialization change did not change preview identities")
	}
}

func TestServiceRejectsDriftCancellationAndInvalidSeal(t *testing.T) {
	service, request := previewFixture(t)
	drift := service.Samples.Descriptor()
	drift.Hash = strings.Repeat("f", 64)
	service.Samples = EngineSampleEvaluator{Adapter: &eventAdapterFake{descriptor: drift}}
	if _, err := service.Evaluate(context.Background(), request); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("drift error=%v", err)
	}

	service, request = previewFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Evaluate(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}

	if _, err := SealMaterialization(contract.Revision{}, []byte(`{}`)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid base error=%v", err)
	}
	tampered := request
	tampered.Materialization.Materialization[0] = 'x'
	if _, err := service.Evaluate(context.Background(), tampered); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tampered materialization error=%v", err)
	}
}

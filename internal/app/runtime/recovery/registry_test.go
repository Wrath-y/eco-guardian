package recovery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

type recoveryStoreFake struct {
	records        []sharedjob.Record
	transitions    int
	wantGeneration int64
}

func (store *recoveryStoreFake) ListRecoverableJobs(context.Context, int) ([]sharedjob.Record, error) {
	return append([]sharedjob.Record(nil), store.records...), nil
}
func (*recoveryStoreFake) CreateOrGet(context.Context, sharedjob.Request) (sharedjob.Record, bool, error) {
	return sharedjob.Record{}, false, errors.New("unused")
}
func (store *recoveryStoreFake) GetJob(context.Context, domain.ID) (sharedjob.Record, error) {
	return store.records[0], nil
}
func (store *recoveryStoreFake) Transition(_ context.Context, id domain.ID, expected, next sharedjob.Status, result *sharedjob.Result, generation int64) (sharedjob.Record, bool, error) {
	if generation != store.wantGeneration {
		return sharedjob.Record{}, false, errors.New("stale generation")
	}
	store.transitions++
	value := store.records[0]
	value.Status, value.Result = next, result
	return value, true, nil
}
func (*recoveryStoreFake) RequestCancellation(context.Context, domain.ID) (sharedjob.Record, bool, error) {
	return sharedjob.Record{}, false, errors.New("unused")
}

func recoveryJob(status sharedjob.Status, generation int64) sharedjob.Record {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	var canceled *time.Time
	if generation > 0 {
		canceled = &now
	}
	return sharedjob.Record{
		ID: domain.ID("01991a39-e000-7000-8000-000000000001"), ProjectID: domain.ID("01991a39-e000-7000-8000-000000000002"),
		RevisionID: domain.ID("01991a39-e000-7000-8000-000000000003"), Kind: "simulation", InputHash: strings.Repeat("a", 64),
		IdempotencyKey: "recover-1", RequestHash: strings.Repeat("b", 64), Status: status,
		CancelGeneration: generation, CancelRequestedAt: canceled, CreatedAt: now, UpdatedAt: now,
	}
}

func exactDescriptor(record sharedjob.Record, planner Planner) Descriptor {
	return Descriptor{
		Kind: record.Kind, LegalSourceStates: []sharedjob.Status{sharedjob.Queued, sharedjob.Running, sharedjob.Interrupted},
		RequireImmutableInput: true, RequireFingerprint: true, RequiredCheckpoints: []string{"sample-1"}, AutomaticResume: true,
		Inspector: InspectorFunc(func(context.Context, sharedjob.Record) (Facts, error) {
			return Facts{Job: record, InputHash: record.InputHash, ImplementationFingerprint: strings.Repeat("c", 64), ExpectedCancelGeneration: record.CancelGeneration, Checkpoints: []EffectCheckpoint{{ID: "sample-1", InputHash: record.InputHash, Fingerprint: strings.Repeat("c", 64), Generation: 1, Sealed: true}}}, nil
		}), Planner: planner,
	}
}

func TestRegistryValidatesFactsThenUsesExpectedGenerationTransition(t *testing.T) {
	record := recoveryJob(sharedjob.Queued, 0)
	store := &recoveryStoreFake{records: []sharedjob.Record{record}}
	plannerCalls := 0
	descriptor := exactDescriptor(record, PlannerFunc(func(_ context.Context, facts Facts) (Decision, error) {
		plannerCalls++
		facts.Checkpoints[0].ID = "caller-mutation"
		return Decision{Disposition: DispositionResumed, NextStatus: sharedjob.Running}, nil
	}))
	registry, err := NewRegistry(store, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	results, err := registry.Recover(context.Background())
	if err != nil || len(results) != 1 || results[0].Status != sharedjob.Running || !results[0].Changed || plannerCalls != 1 || store.transitions != 1 {
		t.Fatalf("results=%#v planner=%d transitions=%d err=%v", results, plannerCalls, store.transitions, err)
	}
}

func TestRegistryRejectsMismatchedFactsAndUnregisteredKindsWithoutEffects(t *testing.T) {
	record := recoveryJob(sharedjob.Running, 1)
	store := &recoveryStoreFake{records: []sharedjob.Record{record}, wantGeneration: 1}
	plannerCalls := 0
	descriptor := exactDescriptor(record, PlannerFunc(func(context.Context, Facts) (Decision, error) { plannerCalls++; return Decision{}, nil }))
	descriptor.Inspector = InspectorFunc(func(context.Context, sharedjob.Record) (Facts, error) {
		return Facts{Job: record, InputHash: strings.Repeat("d", 64), ExpectedCancelGeneration: 0}, nil
	})
	registry, err := NewRegistry(store, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	results, err := registry.Recover(context.Background())
	if err != nil || results[0].ReasonCode != "RECOVERY_FACTS_MISMATCH" || plannerCalls != 0 || store.transitions != 0 {
		t.Fatalf("results=%#v calls=%d err=%v", results, plannerCalls, err)
	}

	registry, _ = NewRegistry(store)
	results, err = registry.Recover(context.Background())
	if err != nil || results[0].ReasonCode != "RECOVERY_POLICY_UNREGISTERED" || store.transitions != 0 {
		t.Fatalf("unregistered=%#v err=%v", results, err)
	}
}

func TestRegistryEnforcesAutomaticResumePolicyAndDescriptorUniqueness(t *testing.T) {
	record := recoveryJob(sharedjob.Queued, 0)
	store := &recoveryStoreFake{records: []sharedjob.Record{record}}
	descriptor := exactDescriptor(record, PlannerFunc(func(context.Context, Facts) (Decision, error) {
		return Decision{Disposition: DispositionResumed, NextStatus: sharedjob.Running}, nil
	}))
	descriptor.AutomaticResume = false
	registry, err := NewRegistry(store, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = registry.Recover(context.Background()); !errors.Is(err, ErrDecisionInvalid) {
		t.Fatalf("err=%v", err)
	}
	if _, err = NewRegistry(store, descriptor, descriptor); !errors.Is(err, ErrRegistryInvalid) {
		t.Fatalf("duplicate err=%v", err)
	}
}

var _ sharedjob.RecoverableStore = (*recoveryStoreFake)(nil)

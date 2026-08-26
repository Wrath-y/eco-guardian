// Package recovery coordinates process-level recovery without owning any
// feature's recovery rules or durable facts.
package recovery

import (
	"context"
	"errors"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

var (
	ErrRegistryInvalid = errors.New("recovery registry is invalid")
	ErrFactsInvalid    = errors.New("recovery facts are invalid")
	ErrDecisionInvalid = errors.New("recovery decision is invalid")
)

type Disposition string

const (
	DispositionResumed          Disposition = "resumed"
	DispositionReconciled       Disposition = "reconciled"
	DispositionRecoveryRequired Disposition = "recovery_required"
	DispositionUnchanged        Disposition = "unchanged"
)

type EffectCheckpoint struct {
	ID          string
	InputHash   string
	Fingerprint string
	Generation  int64
	Sealed      bool
}

type Facts struct {
	Job                       sharedjob.Record
	InputHash                 string
	ImplementationFingerprint string
	ExpectedCancelGeneration  int64
	Checkpoints               []EffectCheckpoint
}

type Inspector interface {
	InspectRecovery(context.Context, sharedjob.Record) (Facts, error)
}

type InspectorFunc func(context.Context, sharedjob.Record) (Facts, error)

func (function InspectorFunc) InspectRecovery(ctx context.Context, record sharedjob.Record) (Facts, error) {
	return function(ctx, record)
}

type Planner interface {
	PlanRecovery(context.Context, Facts) (Decision, error)
}

type PlannerFunc func(context.Context, Facts) (Decision, error)

func (function PlannerFunc) PlanRecovery(ctx context.Context, facts Facts) (Decision, error) {
	return function(ctx, facts)
}

type Descriptor struct {
	Kind                  sharedjob.Kind
	LegalSourceStates     []sharedjob.Status
	RequireImmutableInput bool
	RequireFingerprint    bool
	RequiredCheckpoints   []string
	AutomaticResume       bool
	Inspector             Inspector
	Planner               Planner
}

type Decision struct {
	Disposition Disposition
	NextStatus  sharedjob.Status
	Result      *sharedjob.Result
	ReasonCode  string
}

type Result struct {
	JobID       domain.ID
	Kind        sharedjob.Kind
	Disposition Disposition
	Status      sharedjob.Status
	ReasonCode  string
	Changed     bool
}

type Registry struct {
	repository  sharedjob.RecoverableStore
	descriptors map[sharedjob.Kind]Descriptor
	limit       int
}

func NewRegistry(repository sharedjob.RecoverableStore, descriptors ...Descriptor) (*Registry, error) {
	if repository == nil {
		return nil, ErrRegistryInvalid
	}
	registry := &Registry{repository: repository, descriptors: map[sharedjob.Kind]Descriptor{}, limit: 100}
	for _, descriptor := range descriptors {
		if !validDescriptor(descriptor) {
			return nil, ErrRegistryInvalid
		}
		if _, exists := registry.descriptors[descriptor.Kind]; exists {
			return nil, ErrRegistryInvalid
		}
		descriptor.LegalSourceStates = append([]sharedjob.Status(nil), descriptor.LegalSourceStates...)
		descriptor.RequiredCheckpoints = append([]string(nil), descriptor.RequiredCheckpoints...)
		registry.descriptors[descriptor.Kind] = descriptor
	}
	return registry, nil
}

func (registry *Registry) Recover(ctx context.Context) ([]Result, error) {
	if registry == nil || registry.repository == nil {
		return nil, ErrRegistryInvalid
	}
	records, err := registry.repository.ListRecoverableJobs(ctx, registry.limit)
	if err != nil {
		return nil, err
	}
	results := make([]Result, 0, len(records))
	for _, record := range records {
		result, recoverErr := registry.recoverOne(ctx, record)
		results = append(results, result)
		if recoverErr != nil {
			return results, recoverErr
		}
	}
	return results, nil
}

func (registry *Registry) recoverOne(ctx context.Context, record sharedjob.Record) (Result, error) {
	base := Result{JobID: record.ID, Kind: record.Kind, Status: record.Status}
	descriptor, registered := registry.descriptors[record.Kind]
	if !registered {
		base.Disposition, base.ReasonCode = DispositionRecoveryRequired, "RECOVERY_POLICY_UNREGISTERED"
		return base, nil
	}
	if !containsStatus(descriptor.LegalSourceStates, record.Status) {
		base.Disposition, base.ReasonCode = DispositionRecoveryRequired, "RECOVERY_SOURCE_STATE_ILLEGAL"
		return base, nil
	}
	facts, err := descriptor.Inspector.InspectRecovery(ctx, record)
	if err != nil {
		base.Disposition, base.ReasonCode = DispositionRecoveryRequired, "RECOVERY_FACTS_UNAVAILABLE"
		return base, nil
	}
	if !validFacts(record, descriptor, facts) {
		base.Disposition, base.ReasonCode = DispositionRecoveryRequired, "RECOVERY_FACTS_MISMATCH"
		return base, nil
	}
	decision, err := descriptor.Planner.PlanRecovery(ctx, cloneFacts(facts))
	if err != nil {
		return base, err
	}
	if !validDecision(record, descriptor, decision) {
		return base, ErrDecisionInvalid
	}
	base.Disposition, base.ReasonCode = decision.Disposition, decision.ReasonCode
	if decision.NextStatus == "" {
		return base, nil
	}
	updated, changed, err := registry.repository.Transition(ctx, record.ID, record.Status, decision.NextStatus, decision.Result, facts.ExpectedCancelGeneration)
	if err != nil {
		return base, err
	}
	base.Status, base.Changed = updated.Status, changed
	return base, nil
}

func validDescriptor(value Descriptor) bool {
	if !value.Kind.Valid() || value.Inspector == nil || value.Planner == nil || len(value.LegalSourceStates) == 0 {
		return false
	}
	seenStates, seenCheckpoints := map[sharedjob.Status]bool{}, map[string]bool{}
	for _, state := range value.LegalSourceStates {
		if (state != sharedjob.Queued && state != sharedjob.Running && state != sharedjob.Interrupted) || seenStates[state] {
			return false
		}
		seenStates[state] = true
	}
	for _, checkpoint := range value.RequiredCheckpoints {
		if !validName(checkpoint) || seenCheckpoints[checkpoint] {
			return false
		}
		seenCheckpoints[checkpoint] = true
	}
	return true
}

func validFacts(record sharedjob.Record, descriptor Descriptor, facts Facts) bool {
	if !record.Valid() || !sameJob(facts.Job, record) || facts.ExpectedCancelGeneration != record.CancelGeneration {
		return false
	}
	if descriptor.RequireImmutableInput && (facts.InputHash == "" || facts.InputHash != record.InputHash) {
		return false
	}
	if descriptor.RequireFingerprint && !validHash(facts.ImplementationFingerprint) {
		return false
	}
	checkpoints := map[string]EffectCheckpoint{}
	for _, checkpoint := range facts.Checkpoints {
		if !validName(checkpoint.ID) || checkpoints[checkpoint.ID].ID != "" || checkpoint.Generation < 1 || !checkpoint.Sealed || checkpoint.InputHash != facts.InputHash || (descriptor.RequireFingerprint && checkpoint.Fingerprint != facts.ImplementationFingerprint) {
			return false
		}
		checkpoints[checkpoint.ID] = checkpoint
	}
	for _, required := range descriptor.RequiredCheckpoints {
		if checkpoints[required].ID == "" {
			return false
		}
	}
	return true
}

func validDecision(record sharedjob.Record, descriptor Descriptor, decision Decision) bool {
	switch decision.Disposition {
	case DispositionResumed:
		if !descriptor.AutomaticResume {
			return false
		}
	case DispositionReconciled, DispositionRecoveryRequired, DispositionUnchanged:
	default:
		return false
	}
	if decision.NextStatus == "" {
		return decision.Result == nil
	}
	return record.Status.CanTransitionTo(decision.NextStatus) && (decision.NextStatus != sharedjob.Succeeded || decision.Result != nil) && (decision.Result == nil || decision.Result.Valid())
}

func cloneFacts(value Facts) Facts {
	value.Checkpoints = append([]EffectCheckpoint(nil), value.Checkpoints...)
	return value
}

func containsStatus(values []sharedjob.Status, target sharedjob.Status) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func sameJob(left, right sharedjob.Record) bool {
	if left.ID != right.ID || left.ProjectID != right.ProjectID || left.Kind != right.Kind || left.RevisionID != right.RevisionID || left.InputHash != right.InputHash || left.IdempotencyKey != right.IdempotencyKey || left.RequestHash != right.RequestHash || left.Status != right.Status || left.CancelGeneration != right.CancelGeneration || !left.CreatedAt.Equal(right.CreatedAt) || !left.UpdatedAt.Equal(right.UpdatedAt) {
		return false
	}
	if (left.CancelRequestedAt == nil) != (right.CancelRequestedAt == nil) || (left.Result == nil) != (right.Result == nil) {
		return false
	}
	if left.CancelRequestedAt != nil && !left.CancelRequestedAt.Equal(*right.CancelRequestedAt) {
		return false
	}
	return left.Result == nil || *left.Result == *right.Result
}

func validHash(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
}
func validName(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 64
}

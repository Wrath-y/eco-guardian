package orchestration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/simulation/contract"
)

var (
	ErrRecoveryInvalid     = errors.New("simulation recovery is invalid")
	ErrRecoveryUnavailable = errors.New("simulation recovery implementation is unavailable")
	ErrRecoveryCorrupt     = errors.New("simulation recovery checkpoint is corrupt")
)

type RecoveryMaterialization struct {
	JobID, ProjectID, RevisionID, ScenarioDefinitionID domain.ID
	CanonicalInput                                     []byte
	InputHash, FingerprintHash                         string
	CancelGeneration                                   int64
}
type RecoveryCheckpoint struct {
	Ordinal                      uint64
	InputHash, FingerprintHash   string
	CancelGeneration             int64
	Accumulator, AccumulatorHash string
}
type RecoveryFingerprintResolver interface {
	ResolveSimulationFingerprint(context.Context, []byte) (string, error)
}
type RecoveryRequest struct {
	Job             sharedjob.Record
	Materialization RecoveryMaterialization
	SampleCount     int
	Checkpoints     []RecoveryCheckpoint
}
type RecoveryPlan struct {
	ReusableOrdinals []uint64
	MissingOrdinals  []uint64
}
type RecoverySampleRunner func(context.Context, sharedjob.Record, RecoveryMaterialization, uint64) error

// PlanRecovery re-resolves the exact implementation before any checkpoint is
// reused. Missing ordinals must be recomputed under the original Job identity.
func PlanRecovery(ctx context.Context, resolver RecoveryFingerprintResolver, request RecoveryRequest) (RecoveryPlan, error) {
	if resolver == nil || request.SampleCount < 1 || !recoveryJobValid(request.Job) || !recoveryMaterializationMatches(request.Job, request.Materialization) {
		return RecoveryPlan{}, ErrRecoveryInvalid
	}
	fingerprint, err := resolver.ResolveSimulationFingerprint(ctx, append([]byte(nil), request.Materialization.CanonicalInput...))
	if err != nil || fingerprint != request.Materialization.FingerprintHash {
		return RecoveryPlan{}, fmt.Errorf("%w: fingerprint mismatch", ErrRecoveryUnavailable)
	}
	complete := make(map[uint64]struct{}, len(request.Checkpoints))
	for _, checkpoint := range request.Checkpoints {
		if checkpoint.Ordinal >= uint64(request.SampleCount) || checkpoint.InputHash != request.Materialization.InputHash || checkpoint.FingerprintHash != request.Materialization.FingerprintHash || checkpoint.CancelGeneration != request.Materialization.CancelGeneration || checkpoint.Accumulator == "" || checkpoint.AccumulatorHash != contract.CheckpointAccumulatorHash(checkpoint.Accumulator) {
			return RecoveryPlan{}, ErrRecoveryCorrupt
		}
		if _, duplicate := complete[checkpoint.Ordinal]; duplicate {
			return RecoveryPlan{}, ErrRecoveryCorrupt
		}
		complete[checkpoint.Ordinal] = struct{}{}
	}
	plan := RecoveryPlan{ReusableOrdinals: make([]uint64, 0, len(complete)), MissingOrdinals: make([]uint64, 0, request.SampleCount-len(complete))}
	for ordinal := 0; ordinal < request.SampleCount; ordinal++ {
		if _, found := complete[uint64(ordinal)]; found {
			plan.ReusableOrdinals = append(plan.ReusableOrdinals, uint64(ordinal))
		} else {
			plan.MissingOrdinals = append(plan.MissingOrdinals, uint64(ordinal))
		}
	}
	sort.Slice(plan.ReusableOrdinals, func(i, j int) bool { return plan.ReusableOrdinals[i] < plan.ReusableOrdinals[j] })
	return plan, nil
}

// Recover reuses verified complete ordinals and invokes the caller only for
// missing samples, in ascending ordinal order under the original Job facts.
func Recover(ctx context.Context, resolver RecoveryFingerprintResolver, request RecoveryRequest, run RecoverySampleRunner) (RecoveryPlan, error) {
	plan, err := PlanRecovery(ctx, resolver, request)
	if err != nil || len(plan.MissingOrdinals) == 0 {
		return plan, err
	}
	if run == nil {
		return RecoveryPlan{}, ErrRecoveryInvalid
	}
	for _, ordinal := range plan.MissingOrdinals {
		if err = run(ctx, request.Job, request.Materialization, ordinal); err != nil {
			return RecoveryPlan{}, err
		}
	}
	return plan, nil
}

func recoveryJobValid(job sharedjob.Record) bool {
	return job.Valid() && job.Kind == "simulation" && (job.Status == sharedjob.Queued || job.Status == sharedjob.Running || job.Status == sharedjob.Interrupted) && job.CancelGeneration == 0
}
func recoveryMaterializationMatches(job sharedjob.Record, materialization RecoveryMaterialization) bool {
	if materialization.JobID != job.ID || materialization.ProjectID != job.ProjectID || materialization.RevisionID != job.RevisionID || materialization.InputHash != job.InputHash || materialization.FingerprintHash == "" || materialization.CancelGeneration != job.CancelGeneration || len(materialization.CanonicalInput) == 0 {
		return false
	}
	digest := sha256.Sum256(materialization.CanonicalInput)
	return hex.EncodeToString(digest[:]) == materialization.InputHash
}

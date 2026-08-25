package orchestration

import (
	"bytes"
	"context"
	"errors"
	"sort"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/risk/contract"
)

type Rematerializer interface {
	RematerializeRiskInput(context.Context, contract.RiskInputV1) (MaterializedInput, error)
}

type RecoveryResult struct {
	JobID  domain.ID
	Status sharedjob.Status
	Error  string
}

type RecoveryManager struct {
	Worker       Worker
	Source       Rematerializer
	Jobs         JobStore
	Materialized MaterializationStore
}

// RecoverAll re-reads every immutable identity through Source before invoking
// the normal whole-calculation worker. It intentionally has no checkpoint
// branch: risk comparison is bounded and either reruns from the exact capture
// or terminates with a stable recovery failure.
func (r RecoveryManager) RecoverAll(ctx context.Context) ([]RecoveryResult, error) {
	if r.Source == nil || r.Jobs == nil || r.Materialized == nil {
		return nil, errors.New("risk recovery manager is invalid")
	}
	jobs, err := r.Materialized.ListRecoverableRiskJobs(ctx)
	if err != nil {
		return nil, err
	}
	results := make([]RecoveryResult, 0, len(jobs))
	for _, job := range jobs {
		result := RecoveryResult{JobID: job.ID, Status: job.Status}
		captured, loadErr := r.Materialized.GetRiskJobMaterialization(ctx, job.ID)
		if loadErr != nil {
			failed, _ := r.Worker.fail(ctx, job, "RECOVERY_MISMATCH", "captured risk input is unavailable")
			result.Status, result.Error = failed.Status, "RECOVERY_MISMATCH"
			results = append(results, result)
			continue
		}
		current, materializeErr := r.Source.RematerializeRiskInput(ctx, captured.Input)
		if materializeErr != nil {
			code := "RECOVERY_MISMATCH"
			if errors.Is(materializeErr, ErrRiskRecoveryUnavailable) {
				code = "RECOVERY_UNAVAILABLE"
			}
			failed, _ := r.Worker.fail(ctx, job, code, "captured risk implementations or identities are unavailable")
			result.Status, result.Error = failed.Status, code
			results = append(results, result)
			continue
		}
		if !sameRecoveredMaterialization(captured, current) {
			failed, _ := r.Worker.fail(ctx, job, "RECOVERY_MISMATCH", "captured baseline, threshold, run, or implementation changed")
			result.Status, result.Error = failed.Status, "RECOVERY_MISMATCH"
			results = append(results, result)
			continue
		}
		completed, runErr := r.Worker.Run(ctx, job.ID)
		result.Status = completed.Status
		if runErr != nil {
			result.Error = runErr.Error()
		}
		results = append(results, result)
	}
	return results, nil
}

func sameRecoveredMaterialization(captured Materialization, current MaterializedInput) bool {
	normalized, err := current.Input.Normalize()
	if err != nil {
		return false
	}
	canonical, err := domain.CanonicalJSON(normalized)
	if err != nil || !bytes.Equal(canonical, captured.CanonicalInput) {
		return false
	}
	inputHash, err := normalized.Hash()
	if err != nil || inputHash != captured.InputHash {
		return false
	}
	evidence := append([]contract.ImpactEvidenceRef(nil), current.ImpactEvidence...)
	sort.Slice(evidence, func(i, j int) bool {
		return evidence[i].ReportID+"\x00"+evidence[i].EvidenceID < evidence[j].ReportID+"\x00"+evidence[j].EvidenceID
	})
	evidenceHash, err := contract.ExplanationEvidenceManifestHash(evidence)
	return err == nil && evidenceHash == captured.EvidenceHash
}

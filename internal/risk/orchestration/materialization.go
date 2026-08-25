package orchestration

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/risk/contract"
)

type MaterializedInput struct {
	Input          contract.RiskInputV1
	ImpactEvidence []contract.ImpactEvidenceRef
}

// AdmissionSource performs the consistent immutable reads and domain-specific
// validation in the documented order: revision/policy/threshold materialize,
// then validation/run/cohort admission. Deterministic failures are returned
// before the shared Job store is called.
type AdmissionSource interface {
	MaterializeRiskInput(context.Context) (MaterializedInput, error)
}

type Materialization struct {
	JobID             domain.ID                    `json:"job_id"`
	ProjectID         domain.ID                    `json:"project_id"`
	CandidateRevision domain.ID                    `json:"candidate_revision_id"`
	Input             contract.RiskInputV1         `json:"input"`
	CanonicalInput    []byte                       `json:"canonical_input"`
	InputHash         string                       `json:"input_hash"`
	ImpactEvidence    []contract.ImpactEvidenceRef `json:"impact_evidence"`
	EvidenceHash      string                       `json:"evidence_hash"`
	RequestHash       string                       `json:"request_hash"`
	CancelGeneration  int64                        `json:"cancel_generation"`
	CreatedAt         time.Time                    `json:"created_at"`
}

func (m Materialization) Valid() bool {
	if !m.JobID.Valid() || !m.ProjectID.Valid() || !m.CandidateRevision.Valid() || m.CandidateRevision != m.Input.Candidate.RevisionID || !validHash(m.InputHash) || !validHash(m.EvidenceHash) || !validHash(m.RequestHash) || m.CancelGeneration < 0 || m.CreatedAt.IsZero() {
		return false
	}
	normalized, err := m.Input.Normalize()
	if err != nil {
		return false
	}
	canonical, err := domain.CanonicalJSON(normalized)
	if err != nil || !bytes.Equal(canonical, m.CanonicalInput) {
		return false
	}
	inputHash, err := normalized.Hash()
	if err != nil || inputHash != m.InputHash {
		return false
	}
	evidence := append([]contract.ImpactEvidenceRef(nil), m.ImpactEvidence...)
	sort.Slice(evidence, func(i, j int) bool {
		return evidence[i].ReportID+"\x00"+evidence[i].EvidenceID < evidence[j].ReportID+"\x00"+evidence[j].EvidenceID
	})
	for _, ref := range evidence {
		if !ref.Valid() {
			return false
		}
	}
	evidenceHash, err := contract.ExplanationEvidenceManifestHash(evidence)
	if err != nil || evidenceHash != m.EvidenceHash {
		return false
	}
	requestHash, err := RiskRequestHash(m.InputHash, m.EvidenceHash)
	return err == nil && requestHash == m.RequestHash
}

type MaterializationStore interface {
	SaveRiskJobMaterialization(context.Context, Materialization) error
	GetRiskJobMaterialization(context.Context, domain.ID) (Materialization, error)
	ListRecoverableRiskJobs(context.Context) ([]sharedjob.Record, error)
}

type AdmissionCommand struct {
	ProjectID      domain.ID
	IdempotencyKey string
}

type AdmissionResult struct {
	Job             sharedjob.Record
	Materialization Materialization
	Replayed        bool
}

func AdmitRiskReview(ctx context.Context, source AdmissionSource, jobs JobStore, materializations MaterializationStore, clock Clock, command AdmissionCommand) (AdmissionResult, error) {
	if source == nil || jobs == nil || materializations == nil || clock == nil || !command.ProjectID.Valid() || strings.TrimSpace(command.IdempotencyKey) == "" {
		return AdmissionResult{}, fmt.Errorf("risk admission is invalid")
	}
	materialized, err := source.MaterializeRiskInput(ctx)
	if err != nil {
		return AdmissionResult{}, err
	}
	normalized, err := materialized.Input.Normalize()
	if err != nil || normalized.ProjectID != command.ProjectID {
		return AdmissionResult{}, fmt.Errorf("risk admission input is invalid")
	}
	canonical, err := domain.CanonicalJSON(normalized)
	if err != nil {
		return AdmissionResult{}, err
	}
	inputHash, err := normalized.Hash()
	if err != nil {
		return AdmissionResult{}, err
	}
	evidence := append([]contract.ImpactEvidenceRef(nil), materialized.ImpactEvidence...)
	sort.Slice(evidence, func(i, j int) bool {
		return evidence[i].ReportID+"\x00"+evidence[i].EvidenceID < evidence[j].ReportID+"\x00"+evidence[j].EvidenceID
	})
	evidenceHash, err := contract.ExplanationEvidenceManifestHash(evidence)
	if err != nil {
		return AdmissionResult{}, err
	}
	requestHash, err := RiskRequestHash(inputHash, evidenceHash)
	if err != nil {
		return AdmissionResult{}, err
	}
	job, replayed, err := jobs.CreateOrGet(ctx, sharedjob.Request{ProjectID: command.ProjectID, Kind: RiskReviewJobKind, RevisionID: normalized.Candidate.RevisionID, InputHash: inputHash, IdempotencyKey: command.IdempotencyKey, RequestHash: requestHash})
	if err != nil {
		return AdmissionResult{}, err
	}
	if replayed {
		stored, loadErr := materializations.GetRiskJobMaterialization(ctx, job.ID)
		if loadErr != nil || stored.InputHash != inputHash || stored.EvidenceHash != evidenceHash || stored.RequestHash != requestHash {
			return AdmissionResult{}, fmt.Errorf("risk admission replay mismatch")
		}
		return AdmissionResult{Job: job, Materialization: stored, Replayed: true}, nil
	}
	value := Materialization{JobID: job.ID, ProjectID: command.ProjectID, CandidateRevision: normalized.Candidate.RevisionID, Input: normalized, CanonicalInput: canonical, InputHash: inputHash, ImpactEvidence: evidence, EvidenceHash: evidenceHash, RequestHash: requestHash, CancelGeneration: job.CancelGeneration, CreatedAt: clock.Now().UTC()}
	if !value.Valid() {
		return AdmissionResult{}, fmt.Errorf("risk materialization is invalid")
	}
	if err = materializations.SaveRiskJobMaterialization(ctx, value); err != nil {
		return AdmissionResult{}, err
	}
	return AdmissionResult{Job: job, Materialization: value}, nil
}

func RiskRequestHash(inputHash, evidenceHash string) (string, error) {
	if !validHash(inputHash) || !validHash(evidenceHash) {
		return "", fmt.Errorf("risk request hashes are invalid")
	}
	hash, _, err := contract.CanonicalHash("eco-guardian/risk-request/v1", struct {
		InputHash    string `json:"input_hash"`
		EvidenceHash string `json:"evidence_hash"`
	}{inputHash, evidenceHash})
	return hash, err
}

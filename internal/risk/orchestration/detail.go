package orchestration

import (
	"context"
	"errors"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/risk/contract"
	"github.com/zouyi/eco-guardian/internal/risk/threshold"
)

type ThresholdHistoryReader interface {
	GetThresholdVersion(context.Context, domain.ID, domain.ID) (threshold.Version, error)
}

type Detail struct {
	Report          Report                       `json:"report"`
	Input           contract.RiskInputV1         `json:"input"`
	Threshold       threshold.Version            `json:"threshold"`
	ImpactEvidence  []contract.ImpactEvidenceRef `json:"impact_evidence"`
	Projection      Projection                   `json:"projection"`
	ReleaseAuditURL string                       `json:"release_audit_url,omitempty"`
}

type DetailService struct {
	Reports          ReportRepository
	Materializations MaterializationStore
	Thresholds       ThresholdHistoryReader
}

func (s DetailService) Get(ctx context.Context, reportID domain.ID, current CurrentContext, releaseAuditURL string) (Detail, error) {
	if s.Reports == nil || s.Materializations == nil || s.Thresholds == nil || !reportID.Valid() {
		return Detail{}, errors.New("risk detail request is invalid")
	}
	report, err := s.Reports.GetRiskReport(ctx, reportID)
	if err != nil {
		return Detail{}, err
	}
	materialization, err := s.Materializations.GetRiskJobMaterialization(ctx, report.JobID)
	if err != nil || materialization.InputHash != report.InputHash || materialization.EvidenceHash != report.EvidenceHash {
		return Detail{}, errors.New("risk report materialization mismatch")
	}
	thresholdVersion, err := s.Thresholds.GetThresholdVersion(ctx, report.ProjectID, domain.ID(report.Threshold.ID))
	if err != nil || thresholdVersion.BodyHash != report.Threshold.Hash {
		return Detail{}, errors.New("risk report threshold mismatch")
	}
	projection := ProjectReport(report, current)
	projection.ReleaseAuditURL = releaseAuditURL
	for _, ref := range materialization.ImpactEvidence {
		state := "unavailable"
		if currentState := current.ImpactStates[ref.ReportID+"\x00"+ref.EvidenceID]; currentState != "" {
			state = currentState
		}
		projection.ImpactEvidence = append(projection.ImpactEvidence, ImpactProjection{ReportID: ref.ReportID, EvidenceID: ref.EvidenceID, Classification: ref.Classification, State: state})
	}
	return Detail{Report: report, Input: materialization.Input, Threshold: thresholdVersion, ImpactEvidence: append([]contract.ImpactEvidenceRef(nil), materialization.ImpactEvidence...), Projection: projection, ReleaseAuditURL: releaseAuditURL}, nil
}

package contract

import (
	"fmt"
	"sort"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
)

// ReportEnvelopeV1 is the canonical report-level fact. Calculation item
// bodies are stored separately in ordinal order; this envelope deliberately
// contains only immutable identities and hashes, never revision manifests,
// simulation samples/events, Graph path bodies, or release configuration.
type ReportEnvelopeV1 struct {
	SchemaVersion   string     `json:"schema_version"`
	Kind            ReportKind `json:"kind"`
	SourceReportID  domain.ID  `json:"source_report_id,omitempty"`
	InputHash       string     `json:"input_hash"`
	CalculationHash string     `json:"calculation_hash"`
	EvidenceHash    string     `json:"evidence_hash"`
	DecisionItemIDs []string   `json:"decision_item_ids,omitempty"`
	DecisionReason  string     `json:"decision_reason,omitempty"`
}

func (r ReportEnvelopeV1) Normalize() (ReportEnvelopeV1, error) {
	copy := r
	copy.DecisionItemIDs = append([]string(nil), r.DecisionItemIDs...)
	sort.Strings(copy.DecisionItemIDs)
	if copy.Kind == CalculationReport {
		copy.DecisionItemIDs = nil
	}
	if !copy.Valid() {
		return ReportEnvelopeV1{}, fmt.Errorf("invalid risk report envelope")
	}
	return copy, nil
}

func (r ReportEnvelopeV1) Valid() bool {
	if r.SchemaVersion != "v1" || !validHash(r.InputHash) || !validHash(r.CalculationHash) || !validHash(r.EvidenceHash) {
		return false
	}
	if r.Kind == CalculationReport {
		return r.SourceReportID == "" && len(r.DecisionItemIDs) == 0 && r.DecisionReason == ""
	}
	if r.Kind != DecisionReport || !r.SourceReportID.Valid() || len(r.DecisionItemIDs) == 0 || strings.TrimSpace(r.DecisionReason) == "" {
		return false
	}
	seen := make(map[string]struct{}, len(r.DecisionItemIDs))
	for _, id := range r.DecisionItemIDs {
		if strings.TrimSpace(id) == "" {
			return false
		}
		if _, duplicate := seen[id]; duplicate {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

func (r ReportEnvelopeV1) CanonicalJSON() ([]byte, error) {
	normalized, err := r.Normalize()
	if err != nil {
		return nil, err
	}
	return domain.CanonicalJSON(normalized)
}

func (r ReportEnvelopeV1) Hash() (string, error) {
	normalized, err := r.Normalize()
	if err != nil {
		return "", err
	}
	hash, _, err := CanonicalHash("eco-guardian/risk-report-envelope/v1", normalized)
	return hash, err
}

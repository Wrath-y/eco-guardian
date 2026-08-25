package structure

import (
	"fmt"
	"strings"

	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
)

type Finding struct {
	ID           string                          `json:"id"`
	Status       riskcontract.ComparisonStatus   `json:"status"`
	Severity     riskcontract.Severity           `json:"severity,omitempty"`
	Reason       string                          `json:"reason,omitempty"`
	Rule         riskcontract.Identity           `json:"rule"`
	Evidence     *riskcontract.StructureEvidence `json:"evidence,omitempty"`
	Override     riskcontract.OverrideClass      `json:"override_classification"`
	EvidenceHash string                          `json:"evidence_hash"`
}

func (finding Finding) Item(ordinal int, role riskcontract.PolicyRole) (riskcontract.RiskItemV1, error) {
	var severity *riskcontract.Severity
	if finding.Status == riskcontract.Comparable {
		value := finding.Severity
		severity = &value
	}
	item := riskcontract.RiskItemV1{ID: finding.ID, Ordinal: ordinal, Kind: riskcontract.StructuralRiskItem, Role: role, Status: finding.Status, Severity: severity, Reason: finding.Reason, Rule: finding.Rule, Override: finding.Override, EvidenceHash: finding.EvidenceHash, Structural: finding.Evidence}
	if !finding.Valid() || !item.Valid() {
		return riskcontract.RiskItemV1{}, fmt.Errorf("invalid structural risk item")
	}
	return item, nil
}

func (finding Finding) Valid() bool {
	if strings.TrimSpace(finding.ID) == "" || !finding.Rule.Valid() || finding.Override != riskcontract.NonOverridable || !validHash(finding.EvidenceHash) {
		return false
	}
	if finding.Status == riskcontract.Comparable {
		return (finding.Severity == riskcontract.Block || finding.Severity == riskcontract.Warning || finding.Severity == riskcontract.Info) && finding.Reason == "" && finding.Evidence != nil && finding.Evidence.Valid()
	}
	return finding.Status == riskcontract.Unavailable && finding.Severity == "" && strings.TrimSpace(finding.Reason) != "" && finding.Evidence == nil
}

package orchestration

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/risk/contract"
)

const RiskReviewJobKind = "risk_review"

type RevisionRef struct {
	RevisionID   domain.ID `json:"revision_id"`
	ConfigHash   string    `json:"config_hash"`
	ManifestHash string    `json:"version_manifest_hash"`
}

func (r RevisionRef) Valid() bool {
	return r.RevisionID.Valid() && validHash(r.ConfigHash) && validHash(r.ManifestHash)
}

type BaselineRef struct {
	Kind      contract.BaselineKind `json:"type"`
	ReleaseID domain.ID             `json:"release_id,omitempty"`
	Revision  *RevisionRef          `json:"revision,omitempty"`
}

func (b BaselineRef) Valid() bool {
	return (b.Kind == contract.NoBaseline && b.ReleaseID == "" && b.Revision == nil) ||
		(b.Kind == contract.BaselineCurrent && b.ReleaseID.Valid() && b.Revision != nil && b.Revision.Valid())
}

type SimulationRef struct {
	RunID           domain.ID `json:"run_id"`
	InputHash       string    `json:"input_hash"`
	FingerprintHash string    `json:"fingerprint_hash"`
	ResultHash      string    `json:"result_hash"`
}

func (r SimulationRef) Valid() bool {
	return r.RunID.Valid() && validHash(r.InputHash) && validHash(r.FingerprintHash) && validHash(r.ResultHash)
}

type StoredItem struct {
	Item     contract.RiskItemV1 `json:"item"`
	ItemHash string              `json:"item_hash"`
}

func (i StoredItem) Valid() bool {
	if !i.Item.Valid() || !validHash(i.ItemHash) {
		return false
	}
	hash, err := contract.CalculationItemHash(i.Item)
	return err == nil && hash == i.ItemHash
}

type Report struct {
	ID               domain.ID                 `json:"id"`
	JobID            domain.ID                 `json:"job_id"`
	ProjectID        domain.ID                 `json:"project_id"`
	Kind             contract.ReportKind       `json:"kind"`
	SourceReportID   domain.ID                 `json:"source_report_id,omitempty"`
	InputHash        string                    `json:"input_hash"`
	CalculationHash  string                    `json:"calculation_hash"`
	EvidenceHash     string                    `json:"evidence_hash"`
	ReportHash       string                    `json:"report_hash"`
	Candidate        RevisionRef               `json:"candidate"`
	Baseline         BaselineRef               `json:"baseline"`
	Policy           contract.Identity         `json:"policy"`
	Threshold        contract.Identity         `json:"threshold"`
	Validation       contract.Identity         `json:"validation"`
	Implementations  []contract.Identity       `json:"implementations"`
	SimulationRuns   []SimulationRef           `json:"simulation_runs"`
	Envelope         contract.ReportEnvelopeV1 `json:"envelope"`
	Items            []StoredItem              `json:"items"`
	CreatedAt        time.Time                 `json:"created_at"`
	CancelGeneration int64                     `json:"-"`
}

func (r Report) Valid() bool {
	if !r.ID.Valid() || !r.JobID.Valid() || !r.ProjectID.Valid() || !r.Candidate.Valid() || !r.Baseline.Valid() || !r.Policy.Valid() || !r.Threshold.Valid() || !r.Validation.Valid() || !identityHasDomainID(r.Policy) || !identityHasDomainID(r.Threshold) || !identityHasDomainID(r.Validation) || len(r.Implementations) == 0 || len(r.SimulationRuns) == 0 || r.CreatedAt.IsZero() || r.CancelGeneration < 0 || !validHash(r.InputHash) || !validHash(r.CalculationHash) || !validHash(r.EvidenceHash) || !validHash(r.ReportHash) {
		return false
	}
	if r.Envelope.Kind != r.Kind || r.Envelope.SourceReportID != r.SourceReportID || r.Envelope.InputHash != r.InputHash || r.Envelope.CalculationHash != r.CalculationHash || r.Envelope.EvidenceHash != r.EvidenceHash {
		return false
	}
	reportHash, err := r.Envelope.Hash()
	if err != nil || reportHash != r.ReportHash {
		return false
	}
	seenImplementations := map[string]struct{}{}
	for _, implementation := range r.Implementations {
		if !implementation.Valid() {
			return false
		}
		if _, duplicate := seenImplementations[implementation.ID]; duplicate {
			return false
		}
		seenImplementations[implementation.ID] = struct{}{}
	}
	seenRuns := map[domain.ID]struct{}{}
	runResults := map[domain.ID]string{}
	for _, run := range r.SimulationRuns {
		if !run.Valid() {
			return false
		}
		if _, duplicate := seenRuns[run.RunID]; duplicate {
			return false
		}
		seenRuns[run.RunID] = struct{}{}
		runResults[run.RunID] = run.ResultHash
	}
	if r.Kind == contract.DecisionReport {
		return r.SourceReportID.Valid() && len(r.Items) == 0
	}
	if r.Kind != contract.CalculationReport || r.SourceReportID != "" || (len(r.Items) == 0 && r.Baseline.Kind != contract.NoBaseline) {
		return false
	}
	items := make([]contract.RiskItemV1, len(r.Items))
	seenItemIDs := map[string]struct{}{}
	for index, item := range r.Items {
		if !item.Valid() || item.Item.Ordinal != index {
			return false
		}
		if item.Item.Metric != nil {
			metric := item.Item.Metric
			if runResults[metric.Candidate.RunID] != metric.Candidate.ResultHash || (metric.Baseline != nil && runResults[metric.Baseline.RunID] != metric.Baseline.ResultHash) {
				return false
			}
		}
		if _, duplicate := seenItemIDs[item.Item.ID]; duplicate {
			return false
		}
		seenItemIDs[item.Item.ID] = struct{}{}
		items[index] = item.Item
	}
	calculationHash, err := contract.ReportHash(r.InputHash, items)
	return err == nil && calculationHash == r.CalculationHash
}

type HistoryQuery struct {
	CandidateRevisionID domain.ID
	Before              time.Time
	Limit               int
}

type ReportRepository interface {
	SealRiskReport(context.Context, Report) error
	GetRiskReport(context.Context, domain.ID) (Report, error)
	ListRiskReports(context.Context, HistoryQuery) ([]Report, error)
}

type Freshness string
type Compatibility string

const (
	Fresh            Freshness     = "fresh"
	Stale            Freshness     = "stale"
	Compatible       Compatibility = "compatible"
	MissingImplement Compatibility = "missing_implementation"
	Incompatible     Compatibility = "incompatible"
)

type CurrentContext struct {
	ActiveBaselineReleaseID domain.ID
	Policy                  contract.Identity
	Threshold               contract.Identity
	Implementations         []contract.Identity
	SimulationResults       map[domain.ID]string
	ImpactStates            map[string]string
}

type ImpactProjection struct {
	ReportID       string `json:"report_id"`
	EvidenceID     string `json:"evidence_id"`
	Classification string `json:"classification"`
	State          string `json:"state"`
}

type Projection struct {
	Freshness       Freshness          `json:"freshness"`
	Compatibility   Compatibility      `json:"compatibility"`
	Reasons         []string           `json:"reasons"`
	ImpactEvidence  []ImpactProjection `json:"impact_evidence"`
	ReleaseAuditURL string             `json:"release_audit_url,omitempty"`
}

// ProjectReport compares current identities without rewriting stored facts.
// Missing implementations remain a read-time compatibility result so old
// reports continue to be readable after a plugin/build change.
func ProjectReport(report Report, current CurrentContext) Projection {
	projection := Projection{Freshness: Fresh, Compatibility: Compatible, Reasons: []string{}, ImpactEvidence: []ImpactProjection{}}
	if (report.Baseline.Kind == contract.NoBaseline && current.ActiveBaselineReleaseID != "") || (report.Baseline.Kind == contract.BaselineCurrent && report.Baseline.ReleaseID != current.ActiveBaselineReleaseID) {
		projection.Freshness = Stale
		projection.Reasons = append(projection.Reasons, "BASELINE_CHANGED")
	}
	if !sameIdentity(report.Policy, current.Policy) {
		projection.Freshness = Stale
		projection.Reasons = append(projection.Reasons, "POLICY_CHANGED")
	}
	if !sameIdentity(report.Threshold, current.Threshold) {
		projection.Freshness = Stale
		projection.Reasons = append(projection.Reasons, "THRESHOLD_CHANGED")
	}
	installed := make(map[string]contract.Identity, len(current.Implementations))
	for _, identity := range current.Implementations {
		installed[identity.ID] = identity
	}
	for _, required := range report.Implementations {
		got, found := installed[required.ID]
		if !found {
			projection.Compatibility = MissingImplement
			projection.Reasons = append(projection.Reasons, "IMPLEMENTATION_MISSING:"+required.ID)
		} else if !sameIdentity(required, got) {
			projection.Compatibility = Incompatible
			projection.Reasons = append(projection.Reasons, "IMPLEMENTATION_CHANGED:"+required.ID)
		}
	}
	for _, run := range report.SimulationRuns {
		resultHash, found := current.SimulationResults[run.RunID]
		if !found || resultHash != run.ResultHash {
			projection.Freshness = Stale
			projection.Reasons = append(projection.Reasons, "SIMULATION_CHANGED:"+string(run.RunID))
		}
	}
	sort.Strings(projection.Reasons)
	return projection
}

func identityHasDomainID(identity contract.Identity) bool { return domain.ID(identity.ID).Valid() }

func sameIdentity(left, right contract.Identity) bool {
	return left.ID == right.ID && left.Version == right.Version && left.Hash == right.Hash
}

func validHash(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	for _, r := range value {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

func NewCalculationReport(report Report, items []contract.RiskItemV1) (Report, error) {
	if len(items) == 0 && report.Baseline.Kind != contract.NoBaseline {
		return Report{}, fmt.Errorf("risk calculation requires items")
	}
	report.Kind = contract.CalculationReport
	report.SourceReportID = ""
	report.Items = make([]StoredItem, len(items))
	ordered := make([]contract.RiskItemV1, len(items))
	for index, item := range items {
		item.Ordinal = index
		hash, err := contract.CalculationItemHash(item)
		if err != nil {
			return Report{}, err
		}
		report.Items[index] = StoredItem{Item: item, ItemHash: hash}
		ordered[index] = item
	}
	calculationHash, err := contract.ReportHash(report.InputHash, ordered)
	if err != nil {
		return Report{}, err
	}
	report.CalculationHash = calculationHash
	report.Envelope = contract.ReportEnvelopeV1{SchemaVersion: "v1", Kind: contract.CalculationReport, InputHash: report.InputHash, CalculationHash: report.CalculationHash, EvidenceHash: report.EvidenceHash}
	report.ReportHash, err = report.Envelope.Hash()
	if err != nil || !report.Valid() {
		return Report{}, fmt.Errorf("invalid risk calculation report")
	}
	return report, nil
}

func NewDecisionReport(report Report, source Report, itemIDs []string, reason string) (Report, error) {
	if !source.Valid() || source.Kind != contract.CalculationReport || len(itemIDs) == 0 || strings.TrimSpace(reason) == "" {
		return Report{}, fmt.Errorf("invalid risk decision source")
	}
	eligible := make(map[string]struct{}, len(source.Items))
	for _, item := range source.Items {
		if item.Item.Status == contract.Comparable && item.Item.Severity != nil && *item.Item.Severity == contract.Block && item.Item.Override == contract.NumericOverrideEligible {
			eligible[item.Item.ID] = struct{}{}
		}
	}
	for _, id := range itemIDs {
		if _, found := eligible[id]; !found {
			return Report{}, fmt.Errorf("risk decision item is not eligible")
		}
	}
	report.Kind = contract.DecisionReport
	report.SourceReportID = source.ID
	report.CalculationHash = source.CalculationHash
	report.EvidenceHash = source.EvidenceHash
	report.Candidate = source.Candidate
	report.Baseline = source.Baseline
	report.Policy = source.Policy
	report.Threshold = source.Threshold
	report.Validation = source.Validation
	report.Implementations = append([]contract.Identity(nil), source.Implementations...)
	report.SimulationRuns = append([]SimulationRef(nil), source.SimulationRuns...)
	report.Items = nil
	report.Envelope = contract.ReportEnvelopeV1{SchemaVersion: "v1", Kind: contract.DecisionReport, SourceReportID: source.ID, InputHash: report.InputHash, CalculationHash: report.CalculationHash, EvidenceHash: report.EvidenceHash, DecisionItemIDs: append([]string(nil), itemIDs...), DecisionReason: strings.TrimSpace(reason)}
	var err error
	report.ReportHash, err = report.Envelope.Hash()
	if err != nil || !report.Valid() {
		return Report{}, fmt.Errorf("invalid risk decision report")
	}
	return report, nil
}

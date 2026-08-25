package contract

import (
	"encoding/hex"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

type BaselineKind string

const (
	BaselineCurrent BaselineKind = "BASELINE"
	NoBaseline      BaselineKind = "NO_BASELINE"
)

type PolicyRole string

const (
	Required PolicyRole = "required"
	Optional PolicyRole = "optional"
)

type ComparisonStatus string

const (
	Comparable    ComparisonStatus = "COMPARABLE"
	NotComparable ComparisonStatus = "NOT_COMPARABLE"
	Unavailable   ComparisonStatus = "UNAVAILABLE"
	Stale         ComparisonStatus = "STALE"
)

type Severity string

const (
	Block   Severity = "BLOCK"
	Warning Severity = "WARNING"
	Info    Severity = "INFO"
)

type MetricDirection string

const (
	HigherIsRisk MetricDirection = "higher_is_risk"
	LowerIsRisk  MetricDirection = "lower_is_risk"
	TargetRange  MetricDirection = "target_range"
)

type OverrideClass string

const (
	NumericOverrideEligible OverrideClass = "NUMERIC_ELIGIBLE"
	NonOverridable          OverrideClass = "NON_OVERRIDABLE"
)

type ReportKind string

const (
	CalculationReport ReportKind = "calculation"
	DecisionReport    ReportKind = "decision"
)

type RiskItemKind string

const (
	MetricRiskItem     RiskItemKind = "metric"
	StructuralRiskItem RiskItemKind = "structural"
)

type SubjectKind string

const (
	CohortSubject    SubjectKind = "cohort"
	SingletonSubject SubjectKind = "singleton"
)

type MetricAvailability string

const (
	MetricAvailable   MetricAvailability = "AVAILABLE"
	MetricUnavailable MetricAvailability = "UNAVAILABLE"
)

type ThresholdSelectionKind string

const (
	ExistingThreshold ThresholdSelectionKind = "EXISTING"
	StarterThreshold  ThresholdSelectionKind = "STARTER"
	ModifiedStarter   ThresholdSelectionKind = "MODIFIED_STARTER"
)

type StructureEvidenceKind string

const (
	ValidationStructureIssue StructureEvidenceKind = "VALIDATION_ISSUE"
	NewMultiplierIssue       StructureEvidenceKind = "NEW_MULTIPLIER"
	RepeatedMultiplierIssue  StructureEvidenceKind = "REPEATED_MULTIPLIER"
)

type GateState string

const (
	GatePass        GateState = "PASS"
	GateWarning     GateState = "WARNING"
	GateBlock       GateState = "BLOCK"
	GateUnavailable GateState = "UNAVAILABLE"
	GateStale       GateState = "STALE"
)

type Identity struct {
	ID      string `json:"id"`
	Hash    string `json:"hash"`
	Version string `json:"version"`
}

func (i Identity) Valid() bool {
	return strings.TrimSpace(i.ID) != "" && validHash(i.Hash) && strings.TrimSpace(i.Version) != ""
}

type RevisionIdentity struct {
	RevisionID   domain.ID                          `json:"revision_id"`
	ConfigHash   string                             `json:"config_hash"`
	Manifest     versioningrevision.VersionManifest `json:"version_manifest"`
	ManifestHash string                             `json:"version_manifest_hash"`
}

func (i RevisionIdentity) Valid() bool {
	if !i.RevisionID.Valid() || !validHash(i.ConfigHash) || !validHash(i.ManifestHash) || !i.Manifest.Valid() {
		return false
	}
	hash, err := i.Manifest.Hash()
	return err == nil && hash == i.ManifestHash
}

type Baseline struct {
	Kind      BaselineKind      `json:"type"`
	ReleaseID domain.ID         `json:"release_id,omitempty"`
	Revision  *RevisionIdentity `json:"revision,omitempty"`
}

func (b Baseline) Valid() bool {
	return (b.Kind == NoBaseline && b.ReleaseID == "" && b.Revision == nil) ||
		(b.Kind == BaselineCurrent && b.ReleaseID.Valid() && b.Revision != nil && b.Revision.Valid())
}

type TargetRangeValue struct {
	Lower  string `json:"lower"`
	Upper  string `json:"upper"`
	Bounds string `json:"bounds"`
}

func (r TargetRangeValue) Valid() bool {
	if r.Bounds != "inclusive" {
		return false
	}
	lower, err := formula.ParseDecimal(r.Lower)
	if err != nil || lower.String() != r.Lower {
		return false
	}
	upper, err := formula.ParseDecimal(r.Upper)
	return err == nil && upper.String() == r.Upper && lower.Compare(upper) <= 0
}

type UnavailableReason struct {
	Code    string   `json:"code"`
	Missing []string `json:"missing"`
	Message string   `json:"message"`
}

func (r UnavailableReason) Valid() bool {
	if strings.TrimSpace(r.Code) == "" || strings.TrimSpace(r.Message) == "" {
		return false
	}
	for _, missing := range r.Missing {
		if strings.TrimSpace(missing) == "" {
			return false
		}
	}
	return true
}

type MetricEvidence struct {
	MetricID          string             `json:"metric_id"`
	MetricVersion     string             `json:"metric_version"`
	Status            MetricAvailability `json:"status"`
	Unit              string             `json:"unit"`
	Direction         MetricDirection    `json:"direction"`
	TargetRange       *TargetRangeValue  `json:"target_range"`
	AbsoluteThreshold *string            `json:"absolute_threshold"`
	Value             *string            `json:"value"`
	ConfidenceLow     *string            `json:"confidence_low"`
	ConfidenceHigh    *string            `json:"confidence_high"`
	SampleCount       int                `json:"sample_count"`
	Assumptions       []string           `json:"assumptions"`
	Unavailable       *UnavailableReason `json:"unavailable"`
	CanonicalHash     string             `json:"canonical_hash"`
}

func (m MetricEvidence) Valid() bool {
	if strings.TrimSpace(m.MetricID) == "" || strings.TrimSpace(m.MetricVersion) == "" || strings.TrimSpace(m.Unit) == "" || m.SampleCount <= 0 || !validHash(m.CanonicalHash) {
		return false
	}
	if m.Direction != HigherIsRisk && m.Direction != LowerIsRisk && m.Direction != TargetRange {
		return false
	}
	if (m.Direction == TargetRange) != (m.TargetRange != nil) || (m.TargetRange != nil && !m.TargetRange.Valid()) {
		return false
	}
	if m.AbsoluteThreshold != nil && !canonicalDecimal(*m.AbsoluteThreshold) {
		return false
	}
	if m.Status == MetricUnavailable {
		return m.Value == nil && m.ConfidenceLow == nil && m.ConfidenceHigh == nil && m.Unavailable != nil && m.Unavailable.Valid()
	}
	if m.Status != MetricAvailable || m.Value == nil || m.ConfidenceLow == nil || m.ConfidenceHigh == nil || m.Unavailable != nil {
		return false
	}
	if !canonicalDecimal(*m.Value) || !canonicalDecimal(*m.ConfidenceLow) || !canonicalDecimal(*m.ConfidenceHigh) {
		return false
	}
	low, _ := formula.ParseDecimal(*m.ConfidenceLow)
	value, _ := formula.ParseDecimal(*m.Value)
	high, _ := formula.ParseDecimal(*m.ConfidenceHigh)
	return low.Compare(value) <= 0 && value.Compare(high) <= 0
}

type RunEvidence struct {
	RunID           domain.ID        `json:"run_id"`
	Revision        RevisionIdentity `json:"revision"`
	SceneID         string           `json:"scene_id"`
	SceneVersion    string           `json:"scene_version"`
	Participants    []domain.ID      `json:"participants"`
	SampleCount     int              `json:"sample_count"`
	Seed            uint64           `json:"seed"`
	InputHash       string           `json:"input_hash"`
	FingerprintHash string           `json:"fingerprint_hash"`
	ResultHash      string           `json:"result_hash"`
	Status          string           `json:"status"`
	Reproducible    bool             `json:"reproducible"`
	Implementations []Identity       `json:"implementations"`
	Metrics         []MetricEvidence `json:"metrics"`
}

func (r RunEvidence) Valid() bool {
	if !r.RunID.Valid() || !r.Revision.Valid() || strings.TrimSpace(r.SceneID) == "" || strings.TrimSpace(r.SceneVersion) == "" || len(r.Participants) == 0 || r.SampleCount <= 0 || !validHash(r.InputHash) || !validHash(r.FingerprintHash) || !validHash(r.ResultHash) || r.Status != "succeeded" || !r.Reproducible || len(r.Implementations) == 0 || len(r.Metrics) == 0 {
		return false
	}
	seenParticipants := make(map[domain.ID]struct{}, len(r.Participants))
	for _, participant := range r.Participants {
		if !participant.Valid() {
			return false
		}
		if _, duplicate := seenParticipants[participant]; duplicate {
			return false
		}
		seenParticipants[participant] = struct{}{}
	}
	seenImplementations := make(map[string]struct{}, len(r.Implementations))
	for _, implementation := range r.Implementations {
		if !implementation.Valid() {
			return false
		}
		if _, duplicate := seenImplementations[implementation.ID]; duplicate {
			return false
		}
		seenImplementations[implementation.ID] = struct{}{}
	}
	seenMetrics := make(map[string]struct{}, len(r.Metrics))
	for _, metric := range r.Metrics {
		key := metric.MetricID + "\x00" + metric.MetricVersion
		if !metric.Valid() || metric.SampleCount != r.SampleCount {
			return false
		}
		if _, duplicate := seenMetrics[key]; duplicate {
			return false
		}
		seenMetrics[key] = struct{}{}
	}
	return true
}

type Subject struct {
	Kind         SubjectKind `json:"type"`
	EntityKind   string      `json:"entity_kind"`
	BalanceGroup string      `json:"balance_group,omitempty"`
	StableID     domain.ID   `json:"stable_id,omitempty"`
	Members      []domain.ID `json:"members"`
}

func (s Subject) Key() string {
	if s.Kind == CohortSubject {
		return string(s.Kind) + "\x00" + s.EntityKind + "\x00" + s.BalanceGroup
	}
	return string(s.Kind) + "\x00" + s.EntityKind + "\x00" + string(s.StableID)
}

func (s Subject) Valid() bool {
	if strings.TrimSpace(s.EntityKind) == "" || len(s.Members) == 0 {
		return false
	}
	seen := make(map[domain.ID]struct{}, len(s.Members))
	for _, member := range s.Members {
		if !member.Valid() {
			return false
		}
		if _, duplicate := seen[member]; duplicate {
			return false
		}
		seen[member] = struct{}{}
	}
	switch s.Kind {
	case CohortSubject:
		return strings.TrimSpace(s.BalanceGroup) != "" && s.StableID == ""
	case SingletonSubject:
		return s.BalanceGroup == "" && s.StableID.Valid() && len(s.Members) == 1 && s.Members[0] == s.StableID
	default:
		return false
	}
}

type ThresholdSelection struct {
	Kind      ThresholdSelectionKind `json:"type"`
	Threshold *Identity              `json:"threshold,omitempty"`
	Confirmed bool                   `json:"confirmed"`
}

func (s ThresholdSelection) Valid() bool {
	if s.Kind == ExistingThreshold {
		return s.Threshold != nil && s.Threshold.Valid() && !s.Confirmed
	}
	return (s.Kind == StarterThreshold || s.Kind == ModifiedStarter) && s.Threshold == nil && s.Confirmed
}

type ThresholdScope struct {
	SceneID       string          `json:"scene_id"`
	SceneVersion  string          `json:"scene_version"`
	MetricID      string          `json:"metric_id"`
	MetricVersion string          `json:"metric_version"`
	BalanceGroup  *string         `json:"balance_group"`
	Unit          string          `json:"unit"`
	Direction     MetricDirection `json:"direction"`
}

func (s ThresholdScope) Valid() bool {
	if strings.TrimSpace(s.SceneID) == "" || strings.TrimSpace(s.SceneVersion) == "" || strings.TrimSpace(s.MetricID) == "" || strings.TrimSpace(s.MetricVersion) == "" || strings.TrimSpace(s.Unit) == "" {
		return false
	}
	if s.BalanceGroup != nil && strings.TrimSpace(*s.BalanceGroup) == "" {
		return false
	}
	return s.Direction == HigherIsRisk || s.Direction == LowerIsRisk || s.Direction == TargetRange
}

type PolicyRequirement struct {
	SceneID       string     `json:"scene_id"`
	SceneVersion  string     `json:"scene_version"`
	MetricID      string     `json:"metric_id"`
	MetricVersion string     `json:"metric_version"`
	Role          PolicyRole `json:"role"`
}

func (r PolicyRequirement) Valid() bool {
	return strings.TrimSpace(r.SceneID) != "" && strings.TrimSpace(r.SceneVersion) != "" && strings.TrimSpace(r.MetricID) != "" && strings.TrimSpace(r.MetricVersion) != "" && (r.Role == Required || r.Role == Optional)
}

type StructureEvidence struct {
	Kind        StructureEvidenceKind `json:"type"`
	Rule        Identity              `json:"rule"`
	EntityID    domain.ID             `json:"entity_id"`
	FieldPath   string                `json:"field_path"`
	Ordinal     int                   `json:"ordinal"`
	Fingerprint string                `json:"fingerprint"`
	Override    OverrideClass         `json:"override_classification"`
}

func (e StructureEvidence) Valid() bool {
	return (e.Kind == ValidationStructureIssue || e.Kind == NewMultiplierIssue || e.Kind == RepeatedMultiplierIssue) && e.Rule.Valid() && e.EntityID.Valid() && strings.HasPrefix(e.FieldPath, "/") && e.Ordinal >= 0 && validHash(e.Fingerprint) && e.Override == NonOverridable
}

type RiskInputV1 struct {
	SchemaVersion       string              `json:"schema_version"`
	ProjectID           domain.ID           `json:"project_id"`
	Candidate           RevisionIdentity    `json:"candidate"`
	Baseline            Baseline            `json:"baseline"`
	Policy              Identity            `json:"policy"`
	Threshold           Identity            `json:"threshold"`
	Validation          Identity            `json:"validation"`
	PolicyRequirements  []PolicyRequirement `json:"policy_requirements"`
	Implementations     []Identity          `json:"implementations"`
	Subjects            []Subject           `json:"subjects"`
	CandidateRuns       []RunEvidence       `json:"candidate_runs"`
	BaselineRuns        []RunEvidence       `json:"baseline_runs"`
	ComparisonVersion   string              `json:"comparison_version"`
	CohortVersion       string              `json:"cohort_version"`
	StructuralVersion   string              `json:"structural_version"`
	ReportSchemaVersion string              `json:"report_schema_version"`
}

type ReportMetricValue struct {
	Status         MetricAvailability `json:"status"`
	Value          *string            `json:"value"`
	ConfidenceLow  *string            `json:"confidence_low"`
	ConfidenceHigh *string            `json:"confidence_high"`
	Unavailable    *UnavailableReason `json:"unavailable"`
	RunID          domain.ID          `json:"run_id"`
	ResultHash     string             `json:"result_hash"`
}

func (v ReportMetricValue) Valid() bool {
	if !v.RunID.Valid() || !validHash(v.ResultHash) {
		return false
	}
	if v.Status == MetricUnavailable {
		return v.Value == nil && v.ConfidenceLow == nil && v.ConfidenceHigh == nil && v.Unavailable != nil && v.Unavailable.Valid()
	}
	if v.Status != MetricAvailable || v.Value == nil || v.ConfidenceLow == nil || v.ConfidenceHigh == nil || v.Unavailable != nil || !canonicalDecimal(*v.Value) || !canonicalDecimal(*v.ConfidenceLow) || !canonicalDecimal(*v.ConfidenceHigh) {
		return false
	}
	low, _ := formula.ParseDecimal(*v.ConfidenceLow)
	value, _ := formula.ParseDecimal(*v.Value)
	high, _ := formula.ParseDecimal(*v.ConfidenceHigh)
	return low.Compare(value) <= 0 && value.Compare(high) <= 0
}

type MetricComparisonEvidence struct {
	SceneID           string             `json:"scene_id"`
	SceneVersion      string             `json:"scene_version"`
	MetricID          string             `json:"metric_id"`
	MetricVersion     string             `json:"metric_version"`
	Unit              string             `json:"unit"`
	Direction         MetricDirection    `json:"direction"`
	TargetRange       *TargetRangeValue  `json:"target_range"`
	AbsoluteThreshold *string            `json:"absolute_threshold"`
	Subject           Subject            `json:"subject"`
	Candidate         ReportMetricValue  `json:"candidate"`
	Baseline          *ReportMetricValue `json:"baseline"`
	SignedDelta       *string            `json:"signed_delta"`
	RiskDelta         *string            `json:"risk_delta"`
	RelativeRisk      *string            `json:"relative_risk"`
	Assumptions       []string           `json:"assumptions"`
}

func (e MetricComparisonEvidence) Valid() bool {
	if strings.TrimSpace(e.SceneID) == "" || strings.TrimSpace(e.SceneVersion) == "" || strings.TrimSpace(e.MetricID) == "" || strings.TrimSpace(e.MetricVersion) == "" || strings.TrimSpace(e.Unit) == "" || !e.Subject.Valid() || !e.Candidate.Valid() {
		return false
	}
	if e.Direction != HigherIsRisk && e.Direction != LowerIsRisk && e.Direction != TargetRange {
		return false
	}
	if (e.Direction == TargetRange) != (e.TargetRange != nil) || (e.TargetRange != nil && !e.TargetRange.Valid()) || (e.AbsoluteThreshold != nil && !canonicalDecimal(*e.AbsoluteThreshold)) {
		return false
	}
	for _, assumption := range e.Assumptions {
		if strings.TrimSpace(assumption) == "" {
			return false
		}
	}
	if e.Baseline == nil {
		return e.SignedDelta == nil && e.RiskDelta == nil && e.RelativeRisk == nil
	}
	if !e.Baseline.Valid() {
		return false
	}
	for _, value := range []*string{e.SignedDelta, e.RiskDelta, e.RelativeRisk} {
		if value != nil && !canonicalDecimal(*value) {
			return false
		}
	}
	return true
}

type RiskItemV1 struct {
	ID           string                    `json:"id"`
	Ordinal      int                       `json:"ordinal"`
	Kind         RiskItemKind              `json:"kind"`
	Role         PolicyRole                `json:"role"`
	Status       ComparisonStatus          `json:"comparison_status"`
	Severity     *Severity                 `json:"severity"`
	Reason       string                    `json:"reason"`
	Rule         Identity                  `json:"rule"`
	Override     OverrideClass             `json:"override_classification"`
	EvidenceHash string                    `json:"evidence_hash"`
	Metric       *MetricComparisonEvidence `json:"metric_evidence"`
	Structural   *StructureEvidence        `json:"structural_evidence"`
}

func (i RiskItemV1) Valid() bool {
	if strings.TrimSpace(i.ID) == "" || i.Ordinal < 0 || (i.Role != Required && i.Role != Optional) || !i.Rule.Valid() || !validHash(i.EvidenceHash) {
		return false
	}
	if i.Kind == MetricRiskItem {
		if i.Metric == nil || !i.Metric.Valid() || i.Structural != nil {
			return false
		}
	} else if i.Kind == StructuralRiskItem {
		if i.Metric != nil || (i.Structural != nil && (!i.Structural.Valid() || i.Structural.Rule != i.Rule)) || (i.Status == Comparable && i.Structural == nil) {
			return false
		}
	} else {
		return false
	}
	if i.Status == Comparable {
		return i.Severity != nil && (*i.Severity == Block || *i.Severity == Warning || *i.Severity == Info) && i.Reason == "" && (i.Override == NumericOverrideEligible || i.Override == NonOverridable)
	}
	return (i.Status == NotComparable || i.Status == Unavailable || i.Status == Stale) && i.Severity == nil && strings.TrimSpace(i.Reason) != "" && i.Override == NonOverridable
}

type GateResult struct {
	State      GateState `json:"state"`
	ReportHash string    `json:"report_hash"`
	ItemIDs    []string  `json:"item_ids"`
	Reason     string    `json:"reason"`
}

func (r GateResult) Valid() bool {
	if !validHash(r.ReportHash) || (r.State != GatePass && r.State != GateWarning && r.State != GateBlock && r.State != GateUnavailable && r.State != GateStale) {
		return false
	}
	for _, itemID := range r.ItemIDs {
		if strings.TrimSpace(itemID) == "" {
			return false
		}
	}
	if r.State == GatePass {
		return len(r.ItemIDs) == 0 && r.Reason == ""
	}
	return len(r.ItemIDs) > 0 && strings.TrimSpace(r.Reason) != ""
}

type ImpactEvidenceRef struct {
	ReportID         string `json:"report_id"`
	EvidenceID       string `json:"evidence_id"`
	RevisionPairHash string `json:"revision_pair_hash"`
	SnapshotHash     string `json:"snapshot_hash"`
	Classification   string `json:"classification"`
	Freshness        string `json:"freshness"`
}

func (r ImpactEvidenceRef) Valid() bool {
	return strings.TrimSpace(r.ReportID) != "" && strings.TrimSpace(r.EvidenceID) != "" && validHash(r.RevisionPairHash) && validHash(r.SnapshotHash) && (r.Classification == "deterministic" || r.Classification == "suspected") && (r.Freshness == "fresh" || r.Freshness == "stale")
}

func validHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func canonicalDecimal(value string) bool {
	parsed, err := formula.ParseDecimal(value)
	return err == nil && parsed.String() == value
}

package contract

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

func testRiskInput(t *testing.T) RiskInputV1 {
	t.Helper()
	id := func(value string) domain.ID { return domain.ID(value) }
	hash := strings.Repeat("a", 64)
	metricValue := "1"
	confidenceLow := "0.9"
	confidenceHigh := "1.1"
	secondMetricValue := "10"
	secondConfidenceLow := "9"
	secondConfidenceHigh := "11"
	manifest := versioningrevision.VersionManifest{Entries: []versioningrevision.VersionEntry{{CapabilityID: "schema", ContractVersion: "v1", ImplementationVersion: "schema-v1", State: versioningrevision.Registered}}}
	manifestHash, err := manifest.Hash()
	if err != nil {
		t.Fatal(err)
	}
	revision := RevisionIdentity{RevisionID: id("01948c1e-0000-7000-8000-000000000001"), ConfigHash: hash, Manifest: manifest, ManifestHash: manifestHash}
	participants := []domain.ID{id("01948c1e-0000-7000-8000-000000000004"), id("01948c1e-0000-7000-8000-000000000005")}
	run := RunEvidence{RunID: id("01948c1e-0000-7000-8000-000000000002"), Revision: revision, SceneID: "scene", SceneVersion: "v1", Participants: participants, SampleCount: 1000, InputHash: hash, FingerprintHash: hash, ResultHash: hash, Status: "succeeded", Reproducible: true, Implementations: []Identity{{ID: "engine", Version: "v1", Hash: hash}, {ID: "evaluator", Version: "v1", Hash: hash}, {ID: "numeric-policy", Version: "decimal128-v1", Hash: hash}, {ID: "aggregation", Version: "v1", Hash: hash}}, Metrics: []MetricEvidence{{MetricID: "metric-resource", MetricVersion: "v1", Status: MetricAvailable, Unit: "ratio", Direction: TargetRange, TargetRange: &TargetRangeValue{Lower: "0.8", Upper: "1.2", Bounds: "inclusive"}, Value: &metricValue, ConfidenceLow: &confidenceLow, ConfidenceHigh: &confidenceHigh, SampleCount: 1000, CanonicalHash: hash}, {MetricID: "metric-power", MetricVersion: "v1", Status: MetricAvailable, Unit: "points", Direction: HigherIsRisk, Value: &secondMetricValue, ConfidenceLow: &secondConfidenceLow, ConfidenceHigh: &secondConfidenceHigh, SampleCount: 1000, CanonicalHash: hash}}}
	return RiskInputV1{SchemaVersion: "v1", ProjectID: id("01948c1e-0000-7000-8000-000000000000"), Candidate: revision, Baseline: Baseline{Kind: NoBaseline}, Policy: Identity{ID: "policy", Hash: hash, Version: "v1"}, Threshold: Identity{ID: "threshold", Hash: hash, Version: "v1"}, Validation: Identity{ID: "validation", Hash: hash, Version: "v1"}, PolicyRequirements: []PolicyRequirement{{SceneID: "scene", SceneVersion: "v1", MetricID: "metric-resource", MetricVersion: "v1", Role: Required}, {SceneID: "scene", SceneVersion: "v1", MetricID: "metric-power", MetricVersion: "v1", Role: Optional}}, Implementations: []Identity{{ID: "comparison", Hash: hash, Version: "v1"}, {ID: "cohort", Hash: hash, Version: "v1"}, {ID: "structural", Hash: hash, Version: "v1"}, {ID: "report", Hash: hash, Version: "v1"}}, Subjects: []Subject{{Kind: SingletonSubject, EntityKind: "character", StableID: participants[0], Members: []domain.ID{participants[0]}}, {Kind: SingletonSubject, EntityKind: "character", StableID: participants[1], Members: []domain.ID{participants[1]}}}, CandidateRuns: []RunEvidence{run}, ComparisonVersion: "v1", CohortVersion: "v1", StructuralVersion: "v1", ReportSchemaVersion: "v1"}
}

func testRiskItemEvidence() *MetricComparisonEvidence {
	value, low, high := "1", "0.9", "1.1"
	participant := domain.ID("01948c1e-0000-7000-8000-000000000021")
	baseline := ReportMetricValue{Status: MetricAvailable, Value: &value, ConfidenceLow: &low, ConfidenceHigh: &high, RunID: "01948c1e-0000-7000-8000-000000000023", ResultHash: strings.Repeat("a", 64)}
	return &MetricComparisonEvidence{SceneID: "scene", SceneVersion: "v1", MetricID: "metric-resource", MetricVersion: "v1", Unit: "ratio", Direction: TargetRange, TargetRange: &TargetRangeValue{Lower: "0.8", Upper: "1.2", Bounds: "inclusive"}, Subject: Subject{Kind: SingletonSubject, EntityKind: "character", StableID: participant, Members: []domain.ID{participant}}, Candidate: ReportMetricValue{Status: MetricAvailable, Value: &value, ConfidenceLow: &low, ConfidenceHigh: &high, RunID: "01948c1e-0000-7000-8000-000000000022", ResultHash: strings.Repeat("a", 64)}, Baseline: &baseline, Assumptions: []string{"fixed fixture"}}
}

func TestRiskInputHashIgnoresEnumerationAndOptionalImpactEvidence(t *testing.T) {
	input := testRiskInput(t)
	left, err := input.Hash()
	if err != nil {
		t.Fatal(err)
	}
	shuffled := input
	shuffled.Subjects = []Subject{input.Subjects[1], input.Subjects[0]}
	shuffled.CandidateRuns = append([]RunEvidence(nil), input.CandidateRuns...)
	shuffled.CandidateRuns[0].Participants = []domain.ID{input.CandidateRuns[0].Participants[1], input.CandidateRuns[0].Participants[0]}
	shuffled.CandidateRuns[0].Metrics = []MetricEvidence{input.CandidateRuns[0].Metrics[1], input.CandidateRuns[0].Metrics[0]}
	shuffled.PolicyRequirements = []PolicyRequirement{input.PolicyRequirements[1], input.PolicyRequirements[0]}
	shuffled.Implementations = []Identity{input.Implementations[3], input.Implementations[1], input.Implementations[0], input.Implementations[2]}
	right, err := shuffled.Hash()
	if err != nil || left != right {
		t.Fatalf("hashes left=%s right=%s err=%v", left, right, err)
	}
	refs := []ImpactEvidenceRef{{ReportID: "report", EvidenceID: "path", RevisionPairHash: strings.Repeat("b", 64), SnapshotHash: strings.Repeat("c", 64), Classification: "deterministic", Freshness: "fresh"}}
	if _, err = ImpactEvidenceHash(refs); err != nil {
		t.Fatal(err)
	}
	after, _ := input.Hash()
	if after != left {
		t.Fatal("optional impact evidence changed deterministic input hash")
	}
	changed := input
	changed.CandidateRuns = append([]RunEvidence(nil), input.CandidateRuns...)
	changed.CandidateRuns[0].Metrics = append([]MetricEvidence(nil), input.CandidateRuns[0].Metrics...)
	otherValue := "1.01"
	changed.CandidateRuns[0].Metrics[0].Value = &otherValue
	if changedHash, err := changed.Hash(); err != nil || changedHash == left {
		t.Fatalf("semantic Metric change did not alter hash: hash=%s err=%v", changedHash, err)
	}
	changed = input
	changed.Policy.Version = "v2"
	if changedHash, err := changed.Hash(); err != nil || changedHash == left {
		t.Fatalf("semantic policy version change did not alter hash: hash=%s err=%v", changedHash, err)
	}
	changed = input
	changed.Candidate.ConfigHash = strings.Repeat("b", 64)
	changed.CandidateRuns = append([]RunEvidence(nil), input.CandidateRuns...)
	changed.CandidateRuns[0].Revision = changed.Candidate
	if changedHash, err := changed.Hash(); err != nil || changedHash == left {
		t.Fatalf("semantic revision identity change did not alter hash: hash=%s err=%v", changedHash, err)
	}
}

func TestRiskItemRejectsStatusSeverityConflation(t *testing.T) {
	hash := strings.Repeat("a", 64)
	rule := Identity{ID: "comparison", Hash: hash, Version: "v1"}
	severity := Warning
	comparable := RiskItemV1{ID: "item", Kind: MetricRiskItem, Role: Required, Status: Comparable, Severity: &severity, Rule: rule, Override: NumericOverrideEligible, EvidenceHash: hash, Metric: testRiskItemEvidence()}
	if !comparable.Valid() {
		t.Fatal("valid comparable item rejected")
	}
	comparable.Status = Unavailable
	if comparable.Valid() {
		t.Fatal("unavailable item accepted a severity")
	}
	missing := RiskItemV1{ID: "missing", Kind: MetricRiskItem, Role: Optional, Status: Unavailable, Reason: "METRIC_UNAVAILABLE", Rule: rule, Override: NonOverridable, EvidenceHash: hash, Metric: testRiskItemEvidence()}
	if !missing.Valid() {
		t.Fatal("valid unavailable item rejected")
	}
}

func TestRiskInputRejectsIncompleteEvidenceAndManifestDrift(t *testing.T) {
	input := testRiskInput(t)
	input.CandidateRuns[0].Metrics[0].ConfidenceLow = nil
	if input.Valid() {
		t.Fatal("available Metric without confidence interval accepted")
	}
	input = testRiskInput(t)
	input.Candidate.ManifestHash = strings.Repeat("b", 64)
	if input.Valid() {
		t.Fatal("revision manifest hash drift accepted")
	}
}

func TestReportHashOrdersItemsAndCalculationItemHashIsSemantic(t *testing.T) {
	hash := strings.Repeat("a", 64)
	severity := Warning
	first := RiskItemV1{ID: "first", Ordinal: 0, Kind: MetricRiskItem, Role: Required, Status: Comparable, Severity: &severity, Rule: Identity{ID: "rule", Version: "v1", Hash: hash}, Override: NumericOverrideEligible, EvidenceHash: hash, Metric: testRiskItemEvidence()}
	second := first
	second.ID = "second"
	second.Ordinal = 1
	left, err := ReportHash(hash, []RiskItemV1{second, first})
	if err != nil {
		t.Fatal(err)
	}
	right, err := ReportHash(hash, []RiskItemV1{first, second})
	if err != nil || left != right {
		t.Fatalf("report item order changed hash: left=%s right=%s err=%v", left, right, err)
	}
	firstHash, err := CalculationItemHash(first)
	if err != nil {
		t.Fatal(err)
	}
	changedSeverity := Block
	first.Severity = &changedSeverity
	secondHash, err := CalculationItemHash(first)
	if err != nil || firstHash == secondHash {
		t.Fatalf("semantic item change did not alter hash: first=%s second=%s err=%v", firstHash, secondHash, err)
	}
}

func TestCanonicalV1GoldenHashes(t *testing.T) {
	input := testRiskInput(t)
	inputHash, err := input.Hash()
	if err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("a", 64)
	severity := Warning
	item := RiskItemV1{ID: "first", Ordinal: 0, Kind: MetricRiskItem, Role: Required, Status: Comparable, Severity: &severity, Rule: Identity{ID: "rule", Version: "v1", Hash: hash}, Override: NumericOverrideEligible, EvidenceHash: hash, Metric: testRiskItemEvidence()}
	itemHash, err := CalculationItemHash(item)
	if err != nil {
		t.Fatal(err)
	}
	reportHash, err := ReportHash(inputHash, []RiskItemV1{item})
	if err != nil {
		t.Fatal(err)
	}
	thresholdHash, err := ThresholdBodyHash(struct {
		RelativeWarning string `json:"relative_warning"`
		RelativeBlock   string `json:"relative_block"`
	}{RelativeWarning: "0.1", RelativeBlock: "0.25"})
	if err != nil {
		t.Fatal(err)
	}
	got := []string{inputHash, itemHash, reportHash, thresholdHash}
	want := []string{"61fdbfcca0befb5243d7617780aa8b9b735bba54901c7d15219088b4e6513775", "4b96779fb0e40918a37d63e19c856d7162589403376e3a13989d84c1164c3d75", "e62df14b64f867f36afdeb2e771318281f74ac8703c2db69221123a8d75f7378", "57fde63023beedd90756b91f71e3bd7b82836c560ffc33250713c265abfe886a"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("golden hashes: %#v", got)
		}
	}
}

func TestRiskCanonicalizationPropertyAcrossEnumerationOrders(t *testing.T) {
	input := testRiskInput(t)
	input.Subjects[0] = Subject{Kind: CohortSubject, EntityKind: "character", BalanceGroup: "alpha", Members: append([]domain.ID(nil), input.CandidateRuns[0].Participants...)}
	secondRun := input.CandidateRuns[0]
	secondRun.RunID = domain.ID("01948c1e-0000-7000-8000-000000000006")
	secondRun.SceneID = "scene-b"
	input.CandidateRuns = append(input.CandidateRuns, secondRun)
	input.PolicyRequirements = append(input.PolicyRequirements,
		PolicyRequirement{SceneID: "scene-b", SceneVersion: "v1", MetricID: "metric-resource", MetricVersion: "v1", Role: Required},
		PolicyRequirement{SceneID: "scene-b", SceneVersion: "v1", MetricID: "metric-power", MetricVersion: "v1", Role: Optional},
	)
	wantBytes, err := input.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	wantHash, err := input.Hash()
	if err != nil {
		t.Fatal(err)
	}
	random := rand.New(rand.NewSource(42))
	for iteration := 0; iteration < 100; iteration++ {
		shuffled := input
		shuffled.Subjects = append([]Subject(nil), input.Subjects...)
		shuffled.PolicyRequirements = append([]PolicyRequirement(nil), input.PolicyRequirements...)
		shuffled.Implementations = append([]Identity(nil), input.Implementations...)
		shuffled.CandidateRuns = append([]RunEvidence(nil), input.CandidateRuns...)
		random.Shuffle(len(shuffled.Subjects), func(i, j int) {
			shuffled.Subjects[i], shuffled.Subjects[j] = shuffled.Subjects[j], shuffled.Subjects[i]
		})
		for index := range shuffled.Subjects {
			shuffled.Subjects[index].Members = append([]domain.ID(nil), shuffled.Subjects[index].Members...)
			random.Shuffle(len(shuffled.Subjects[index].Members), func(i, j int) {
				shuffled.Subjects[index].Members[i], shuffled.Subjects[index].Members[j] = shuffled.Subjects[index].Members[j], shuffled.Subjects[index].Members[i]
			})
		}
		random.Shuffle(len(shuffled.PolicyRequirements), func(i, j int) {
			shuffled.PolicyRequirements[i], shuffled.PolicyRequirements[j] = shuffled.PolicyRequirements[j], shuffled.PolicyRequirements[i]
		})
		random.Shuffle(len(shuffled.Implementations), func(i, j int) {
			shuffled.Implementations[i], shuffled.Implementations[j] = shuffled.Implementations[j], shuffled.Implementations[i]
		})
		random.Shuffle(len(shuffled.CandidateRuns), func(i, j int) {
			shuffled.CandidateRuns[i], shuffled.CandidateRuns[j] = shuffled.CandidateRuns[j], shuffled.CandidateRuns[i]
		})
		for index := range shuffled.CandidateRuns {
			shuffled.CandidateRuns[index].Participants = append([]domain.ID(nil), shuffled.CandidateRuns[index].Participants...)
			shuffled.CandidateRuns[index].Implementations = append([]Identity(nil), shuffled.CandidateRuns[index].Implementations...)
			shuffled.CandidateRuns[index].Metrics = append([]MetricEvidence(nil), shuffled.CandidateRuns[index].Metrics...)
			random.Shuffle(len(shuffled.CandidateRuns[index].Participants), func(i, j int) {
				shuffled.CandidateRuns[index].Participants[i], shuffled.CandidateRuns[index].Participants[j] = shuffled.CandidateRuns[index].Participants[j], shuffled.CandidateRuns[index].Participants[i]
			})
			random.Shuffle(len(shuffled.CandidateRuns[index].Metrics), func(i, j int) {
				shuffled.CandidateRuns[index].Metrics[i], shuffled.CandidateRuns[index].Metrics[j] = shuffled.CandidateRuns[index].Metrics[j], shuffled.CandidateRuns[index].Metrics[i]
			})
			random.Shuffle(len(shuffled.CandidateRuns[index].Implementations), func(i, j int) {
				shuffled.CandidateRuns[index].Implementations[i], shuffled.CandidateRuns[index].Implementations[j] = shuffled.CandidateRuns[index].Implementations[j], shuffled.CandidateRuns[index].Implementations[i]
			})
		}
		gotBytes, err := shuffled.CanonicalBytes()
		if err != nil || string(gotBytes) != string(wantBytes) {
			t.Fatalf("iteration %d canonical bytes changed: err=%v", iteration, err)
		}
		gotHash, err := shuffled.Hash()
		if err != nil || gotHash != wantHash {
			t.Fatalf("iteration %d canonical hash changed: got=%s want=%s err=%v", iteration, gotHash, wantHash, err)
		}
	}

	leftMap := map[string]any{"policy": map[string]any{"required": true, "version": "v1"}, "metrics": []string{"a", "b"}}
	rightMap := map[string]any{"metrics": []string{"a", "b"}, "policy": map[string]any{"version": "v1", "required": true}}
	leftHash, err := ThresholdBodyHash(leftMap)
	if err != nil {
		t.Fatal(err)
	}
	rightHash, err := ThresholdBodyHash(rightMap)
	if err != nil || leftHash != rightHash {
		t.Fatalf("map insertion order changed hash: left=%s right=%s err=%v", leftHash, rightHash, err)
	}
}

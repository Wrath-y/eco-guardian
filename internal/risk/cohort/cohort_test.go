package cohort

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

func TestMaterializeUsesOnlyStableIDKindAndBalanceGroup(t *testing.T) {
	alpha, beta, item, singleton := cohortID(1), cohortID(2), cohortID(3), cohortID(4)
	entities := []domain.Entity{
		{ID: beta, Kind: domain.KindCharacter, Name: "ignored beta", BalanceGroup: "alpha", Status: domain.StatusActive, Payload: map[string]json.RawMessage{"power": json.RawMessage(`999`)}},
		{ID: item, Kind: domain.KindItem, Name: "same group different kind", BalanceGroup: "alpha", Status: domain.StatusActive},
		{ID: alpha, Kind: domain.KindCharacter, Name: "ignored alpha", BalanceGroup: "alpha", Status: domain.StatusActive},
		{ID: singleton, Kind: domain.KindCharacter, Name: "singleton", Status: domain.StatusActive},
	}
	materialized := Materialize(entities, []domain.ID{singleton, alpha})
	if len(materialized.Issues) != 0 || len(materialized.Subjects) != 2 {
		t.Fatalf("materialized=%#v", materialized)
	}
	if materialized.Subjects[0].Kind != riskcontract.CohortSubject || materialized.Subjects[0].EntityKind != "character" || !sameMembers(materialized.Subjects[0].Members, []domain.ID{alpha, beta}) {
		t.Fatalf("cohort=%#v", materialized.Subjects[0])
	}
	if materialized.Subjects[1].Kind != riskcontract.SingletonSubject || materialized.Subjects[1].StableID != singleton {
		t.Fatalf("singleton=%#v", materialized.Subjects[1])
	}
	shuffled := Materialize([]domain.Entity{entities[2], entities[3], entities[0], entities[1]}, []domain.ID{alpha, singleton})
	if len(shuffled.Issues) != 0 || len(shuffled.Subjects) != 2 || shuffled.Subjects[0].Key() != materialized.Subjects[0].Key() || !sameMembers(shuffled.Subjects[0].Members, materialized.Subjects[0].Members) {
		t.Fatalf("shuffle changed cohort identity: %#v", shuffled)
	}
	missing := Materialize(entities, []domain.ID{cohortID(9), alpha, alpha})
	if len(missing.Issues) != 2 || missing.Issues[0].Status == Resolved || missing.Issues[1].Status == Resolved {
		t.Fatalf("missing/duplicate statuses=%#v", missing.Issues)
	}
}

func TestPairReportsMemberKindGroupAndMissingStatuses(t *testing.T) {
	first, second, third := cohortID(1), cohortID(2), cohortID(3)
	candidate := []riskcontract.Subject{
		{Kind: riskcontract.CohortSubject, EntityKind: "character", BalanceGroup: "alpha", Members: []domain.ID{first, second}},
		{Kind: riskcontract.SingletonSubject, EntityKind: "character", StableID: third, Members: []domain.ID{third}},
	}
	changedMembers := Pair(candidate, []riskcontract.Subject{{Kind: riskcontract.CohortSubject, EntityKind: "character", BalanceGroup: "alpha", Members: []domain.ID{first}}})
	if changedMembers[0].Status != MemberMismatch || changedMembers[1].Status != Missing {
		t.Fatalf("pairing=%#v", changedMembers)
	}
	changedGroup := Pair(candidate[:1], []riskcontract.Subject{{Kind: riskcontract.CohortSubject, EntityKind: "character", BalanceGroup: "beta", Members: []domain.ID{first, second}}})
	if changedGroup[0].Status != GroupMismatch {
		t.Fatalf("group pairing=%#v", changedGroup)
	}
	changedKind := Pair(candidate[:1], []riskcontract.Subject{{Kind: riskcontract.CohortSubject, EntityKind: "item", BalanceGroup: "alpha", Members: []domain.ID{first, second}}})
	if changedKind[0].Status != KindMismatch {
		t.Fatalf("kind pairing=%#v", changedKind)
	}
}

func TestAdmissionRequiresExactCohortRunAndImplementationClosure(t *testing.T) {
	run, contract, subjects := cohortRunFixture(t)
	if err := AdmitRun(run, contract, subjects); err != nil {
		t.Fatal(err)
	}
	partial := run
	partial.Participants = partial.Participants[:1]
	if !errors.Is(AdmitRun(partial, contract, subjects), ErrAdmission) {
		t.Fatal("partial participant set admitted")
	}
	drifted := run
	drifted.Implementations = append([]riskcontract.Identity(nil), run.Implementations...)
	drifted.Implementations[0].Version = "v2"
	if !errors.Is(AdmitRun(drifted, contract, subjects), ErrAdmission) {
		t.Fatal("implementation drift admitted")
	}
	checkpoint := run
	checkpoint.Revision.RevisionID = cohortID(8)
	if !errors.Is(AdmitRun(checkpoint, contract, subjects), ErrAdmission) {
		t.Fatal("same-content checkpoint with a different revision identity admitted")
	}
	failed := run
	failed.Status = "failed"
	if !errors.Is(AdmitRun(failed, contract, subjects), ErrAdmission) {
		t.Fatal("failed run admitted")
	}
	for name, mutate := range map[string]func(*riskcontract.RunEvidence){
		"scene":           func(value *riskcontract.RunEvidence) { value.SceneVersion = "v2" },
		"sample count":    func(value *riskcontract.RunEvidence) { value.SampleCount = 999 },
		"seed policy":     func(value *riskcontract.RunEvidence) { value.Seed = 43 },
		"input hash":      func(value *riskcontract.RunEvidence) { value.InputHash = strings.Repeat("b", 64) },
		"result hash":     func(value *riskcontract.RunEvidence) { value.ResultHash = strings.Repeat("b", 64) },
		"reproducibility": func(value *riskcontract.RunEvidence) { value.Reproducible = false },
		"Metric version":  func(value *riskcontract.RunEvidence) { value.Metrics[0].MetricVersion = "v2" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := run
			changed.Metrics = append([]riskcontract.MetricEvidence(nil), run.Metrics...)
			mutate(&changed)
			if !errors.Is(AdmitRun(changed, contract, subjects), ErrAdmission) {
				t.Fatal("drifted run admitted")
			}
		})
	}
	if err := AdmitSets(riskcontract.NoBaseline, []riskcontract.RunEvidence{run}, []RunContract{contract}, nil, nil, subjects); err != nil {
		t.Fatalf("NO_BASELINE candidate admission: %v", err)
	}
	if !errors.Is(AdmitSets(riskcontract.NoBaseline, []riskcontract.RunEvidence{run}, []RunContract{contract}, []riskcontract.RunEvidence{run}, []RunContract{contract}, subjects), ErrAdmission) {
		t.Fatal("NO_BASELINE fabricated a baseline run")
	}
	if !errors.Is(AdmitSets(riskcontract.BaselineCurrent, []riskcontract.RunEvidence{run}, []RunContract{contract}, nil, nil, subjects), ErrAdmission) {
		t.Fatal("current baseline admitted without exact baseline runs")
	}
}

func TestCohortCanonicalPropertyAndGoldenIgnoreEnumerationAndOptionalEvidence(t *testing.T) {
	first, second, singleton := cohortID(1), cohortID(2), cohortID(3)
	subjects := []riskcontract.Subject{
		{Kind: riskcontract.SingletonSubject, EntityKind: "character", StableID: singleton, Members: []domain.ID{singleton}},
		{Kind: riskcontract.CohortSubject, EntityKind: "character", BalanceGroup: "alpha", Members: []domain.ID{first, second}},
	}
	canonical, golden, err := CanonicalSubjects(subjects)
	if err != nil {
		t.Fatal(err)
	}
	if golden != "62f8748a739e327d7ee3d0d0aba81f5f4d82ebea87610474a4622421ceb773a9" {
		t.Fatalf("cohort golden=%s", golden)
	}
	random := rand.New(rand.NewSource(42))
	for iteration := 0; iteration < 100; iteration++ {
		shuffled := []riskcontract.Subject{subjects[1], subjects[0]}
		shuffled[0].Members = append([]domain.ID(nil), subjects[1].Members...)
		random.Shuffle(len(shuffled[0].Members), func(i, j int) {
			shuffled[0].Members[i], shuffled[0].Members[j] = shuffled[0].Members[j], shuffled[0].Members[i]
		})
		got, hash, hashErr := CanonicalSubjects(shuffled)
		if hashErr != nil || hash != golden || fmt.Sprint(got) != fmt.Sprint(canonical) {
			t.Fatalf("iteration %d got=%#v hash=%s err=%v", iteration, got, hash, hashErr)
		}
	}
	optionalImpactRefs := []riskcontract.ImpactEvidenceRef{{ReportID: "report", EvidenceID: "path", RevisionPairHash: strings.Repeat("b", 64), SnapshotHash: strings.Repeat("c", 64), Classification: "deterministic", Freshness: "fresh"}}
	if _, err = riskcontract.ImpactEvidenceHash(optionalImpactRefs); err != nil {
		t.Fatal(err)
	}
	_, afterOptionalEvidence, err := CanonicalSubjects(subjects)
	if err != nil || afterOptionalEvidence != golden {
		t.Fatalf("optional impact evidence changed cohort: hash=%s err=%v", afterOptionalEvidence, err)
	}
	added := append([]riskcontract.Subject(nil), subjects...)
	added[1].Members = append([]domain.ID(nil), subjects[1].Members...)
	added[1].Members = append(added[1].Members, cohortID(4))
	_, addedHash, err := CanonicalSubjects(added)
	if err != nil || addedHash == golden {
		t.Fatalf("member addition did not change cohort hash: %s err=%v", addedHash, err)
	}
}

func cohortRunFixture(t *testing.T) (riskcontract.RunEvidence, RunContract, []riskcontract.Subject) {
	t.Helper()
	hash := strings.Repeat("a", 64)
	manifest := versioningrevision.VersionManifest{Entries: []versioningrevision.VersionEntry{{CapabilityID: "schema", ContractVersion: "v1", ImplementationVersion: "schema-v1", State: versioningrevision.Registered}}}
	manifestHash, err := manifest.Hash()
	if err != nil {
		t.Fatal(err)
	}
	revision := riskcontract.RevisionIdentity{RevisionID: cohortID(5), ConfigHash: hash, Manifest: manifest, ManifestHash: manifestHash}
	value, low, high := "1", "0.9", "1.1"
	implementations := []riskcontract.Identity{{ID: "engine", Version: "v1", Hash: hash}, {ID: "evaluator", Version: "v1", Hash: hash}, {ID: "numeric-policy", Version: "decimal128-v1", Hash: hash}, {ID: "aggregation", Version: "v1", Hash: hash}}
	participants := []domain.ID{cohortID(1), cohortID(2)}
	metric := riskcontract.MetricEvidence{MetricID: "metric-resource", MetricVersion: "v1", Status: riskcontract.MetricAvailable, Unit: "ratio", Direction: riskcontract.TargetRange, TargetRange: &riskcontract.TargetRangeValue{Lower: "0.8", Upper: "1.2", Bounds: "inclusive"}, Value: &value, ConfidenceLow: &low, ConfidenceHigh: &high, SampleCount: 1000, CanonicalHash: hash}
	run := riskcontract.RunEvidence{RunID: cohortID(6), Revision: revision, SceneID: "scene", SceneVersion: "v1", Participants: participants, SampleCount: 1000, Seed: 42, InputHash: hash, FingerprintHash: hash, ResultHash: hash, Status: "succeeded", Reproducible: true, Implementations: implementations, Metrics: []riskcontract.MetricEvidence{metric}}
	requirement := riskcontract.PolicyRequirement{SceneID: "scene", SceneVersion: "v1", MetricID: "metric-resource", MetricVersion: "v1", Role: riskcontract.Required}
	contract := RunContract{Revision: revision, SceneID: "scene", SceneVersion: "v1", SampleCount: 1000, Seed: 42, InputHash: hash, FingerprintHash: hash, ResultHash: hash, Implementations: implementations, Metrics: []riskcontract.PolicyRequirement{requirement}}
	subjects := []riskcontract.Subject{{Kind: riskcontract.CohortSubject, EntityKind: "character", BalanceGroup: "alpha", Members: participants}}
	return run, contract, subjects
}

func cohortID(suffix int) domain.ID {
	return domain.ID(fmt.Sprintf("01948c1e-0000-7000-8000-%012d", suffix))
}

package orchestration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/risk/contract"
	"github.com/zouyi/eco-guardian/internal/risk/threshold"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

type riskTestClock struct{ value time.Time }

func (c riskTestClock) Now() time.Time { return c.value }

type riskTestIDs struct {
	mu   sync.Mutex
	next int
}

func (g *riskTestIDs) New() (domain.ID, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next++
	return domain.ID(fmt.Sprintf("01948c1e-0000-7000-8000-%012d", g.next)), nil
}

func orchestrationInput(t *testing.T) contract.RiskInputV1 {
	t.Helper()
	hash := strings.Repeat("a", 64)
	manifest := versioningrevision.VersionManifest{Entries: []versioningrevision.VersionEntry{{CapabilityID: "schema", ContractVersion: "v1", ImplementationVersion: "schema-v1", State: versioningrevision.Registered}}}
	manifestHash, err := manifest.Hash()
	if err != nil {
		t.Fatal(err)
	}
	revision := contract.RevisionIdentity{RevisionID: "01948c1e-0000-7000-8000-000000000011", ConfigHash: hash, Manifest: manifest, ManifestHash: manifestHash}
	participant := domain.ID("01948c1e-0000-7000-8000-000000000012")
	value, low, high := "1", "0.9", "1.1"
	run := contract.RunEvidence{RunID: "01948c1e-0000-7000-8000-000000000013", Revision: revision, SceneID: "scene", SceneVersion: "v1", Participants: []domain.ID{participant}, SampleCount: 1000, Seed: 11, InputHash: hash, FingerprintHash: hash, ResultHash: hash, Status: "succeeded", Reproducible: true, Implementations: []contract.Identity{{ID: "engine", Version: "v1", Hash: hash}}, Metrics: []contract.MetricEvidence{{MetricID: "metric-resource", MetricVersion: "v1", Status: contract.MetricAvailable, Unit: "ratio", Direction: contract.TargetRange, TargetRange: &contract.TargetRangeValue{Lower: "0.8", Upper: "1.2", Bounds: "inclusive"}, Value: &value, ConfidenceLow: &low, ConfidenceHigh: &high, SampleCount: 1000, CanonicalHash: hash}}}
	return contract.RiskInputV1{SchemaVersion: "v1", ProjectID: "01948c1e-0000-7000-8000-000000000010", Candidate: revision, Baseline: contract.Baseline{Kind: contract.NoBaseline}, Policy: contract.Identity{ID: "01948c1e-0000-7000-8000-000000000014", Version: "1", Hash: hash}, Threshold: contract.Identity{ID: "01948c1e-0000-7000-8000-000000000015", Version: "1", Hash: hash}, Validation: contract.Identity{ID: "01948c1e-0000-7000-8000-000000000016", Version: "v1", Hash: hash}, PolicyRequirements: []contract.PolicyRequirement{{SceneID: "scene", SceneVersion: "v1", MetricID: "metric-resource", MetricVersion: "v1", Role: contract.Required}}, Implementations: []contract.Identity{{ID: "comparison", Version: "v1", Hash: hash}, {ID: "cohort", Version: "v1", Hash: hash}, {ID: "structural", Version: "v1", Hash: hash}, {ID: "report", Version: "v1", Hash: hash}}, Subjects: []contract.Subject{{Kind: contract.SingletonSubject, EntityKind: "character", StableID: participant, Members: []domain.ID{participant}}}, CandidateRuns: []contract.RunEvidence{run}, ComparisonVersion: "v1", CohortVersion: "v1", StructuralVersion: "v1", ReportSchemaVersion: "v1"}
}

type riskAdmissionSourceFake struct {
	value MaterializedInput
	err   error
}

func (f riskAdmissionSourceFake) MaterializeRiskInput(context.Context) (MaterializedInput, error) {
	return f.value, f.err
}

type riskJobStoreFake struct {
	mu      sync.Mutex
	ids     riskTestIDs
	clock   riskTestClock
	byID    map[domain.ID]sharedjob.Record
	byKey   map[string]domain.ID
	creates int
}

func newRiskJobStoreFake(clock riskTestClock) *riskJobStoreFake {
	return &riskJobStoreFake{clock: clock, byID: map[domain.ID]sharedjob.Record{}, byKey: map[string]domain.ID{}}
}

func (s *riskJobStoreFake) CreateOrGet(_ context.Context, request sharedjob.Request) (sharedjob.Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.creates++
	if id, found := s.byKey[string(request.ProjectID)+"\x00"+request.IdempotencyKey]; found {
		existing := s.byID[id]
		if !existing.Request().Equivalent(request) {
			return sharedjob.Record{}, false, errors.New("idempotency conflict")
		}
		return existing, true, nil
	}
	id, _ := s.ids.New()
	record := sharedjob.Record{ID: id, ProjectID: request.ProjectID, Kind: request.Kind, RevisionID: request.RevisionID, InputHash: request.InputHash, IdempotencyKey: request.IdempotencyKey, RequestHash: request.RequestHash, Status: sharedjob.Queued, CreatedAt: s.clock.Now(), UpdatedAt: s.clock.Now()}
	s.byID[id] = record
	s.byKey[string(request.ProjectID)+"\x00"+request.IdempotencyKey] = id
	return record, false, nil
}

func (s *riskJobStoreFake) GetJob(_ context.Context, id domain.ID) (sharedjob.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, found := s.byID[id]
	if !found {
		return sharedjob.Record{}, errors.New("not found")
	}
	return record, nil
}

func (s *riskJobStoreFake) Transition(_ context.Context, id domain.ID, expected, next sharedjob.Status, result *sharedjob.Result, generation int64) (sharedjob.Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, found := s.byID[id]
	if !found || record.Status != expected || record.CancelGeneration != generation || !expected.CanTransitionTo(next) {
		return sharedjob.Record{}, false, errors.New("transition")
	}
	record.Status, record.Result, record.UpdatedAt = next, result, s.clock.Now()
	s.byID[id] = record
	return record, true, nil
}

func (s *riskJobStoreFake) RequestCancellation(_ context.Context, id domain.ID) (sharedjob.Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.byID[id]
	if record.CancelGeneration > 0 || record.Status.Terminal() {
		return record, true, nil
	}
	record.CancelGeneration = 1
	now := s.clock.Now()
	record.CancelRequestedAt = &now
	s.byID[id] = record
	return record, false, nil
}

type riskMaterialStoreFake struct {
	values map[domain.ID]Materialization
	jobs   *riskJobStoreFake
}

func (s *riskMaterialStoreFake) SaveRiskJobMaterialization(_ context.Context, value Materialization) error {
	if existing, found := s.values[value.JobID]; found && existing.InputHash != value.InputHash {
		return errors.New("materialization conflict")
	}
	s.values[value.JobID] = value
	return nil
}

func (s *riskMaterialStoreFake) GetRiskJobMaterialization(_ context.Context, id domain.ID) (Materialization, error) {
	value, found := s.values[id]
	if !found {
		return Materialization{}, errors.New("not found")
	}
	return value, nil
}

func (s *riskMaterialStoreFake) ListRecoverableRiskJobs(_ context.Context) ([]sharedjob.Record, error) {
	s.jobs.mu.Lock()
	defer s.jobs.mu.Unlock()
	values := []sharedjob.Record{}
	for _, job := range s.jobs.byID {
		if job.Status == sharedjob.Queued || job.Status == sharedjob.Running || job.Status == sharedjob.Interrupted {
			values = append(values, job)
		}
	}
	return values, nil
}

type riskEventStoreFake struct {
	mu     sync.Mutex
	events map[domain.ID][]sharedjob.Event
	failAt string
}

func (s *riskEventStoreFake) Append(_ context.Context, event sharedjob.Event) (sharedjob.Event, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if event.Phase == s.failAt {
		return sharedjob.Event{}, false, errors.New("event failure")
	}
	s.events[event.JobID] = append(s.events[event.JobID], event)
	return event, false, nil
}

func (s *riskEventStoreFake) ListEvents(_ context.Context, id domain.ID, after int64) ([]sharedjob.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := []sharedjob.Event{}
	for _, event := range s.events[id] {
		if event.Ordinal > after {
			values = append(values, event)
		}
	}
	return values, nil
}

type riskReportStoreFake struct {
	jobs         *riskJobStoreFake
	reports      map[domain.ID]Report
	fail         bool
	cancelAtSeal bool
}

func (s *riskReportStoreFake) SealRiskReport(_ context.Context, report Report) error {
	if s.fail || !report.Valid() {
		return errors.New("seal failure")
	}
	if s.cancelAtSeal {
		_, _, _ = s.jobs.RequestCancellation(context.Background(), report.JobID)
		return errors.New("seal canceled")
	}
	s.jobs.mu.Lock()
	defer s.jobs.mu.Unlock()
	job := s.jobs.byID[report.JobID]
	if (job.Status != sharedjob.Running && job.Status != sharedjob.Interrupted) || job.CancelGeneration != report.CancelGeneration || job.InputHash != report.InputHash {
		return errors.New("seal identity")
	}
	result := &sharedjob.Result{Type: "risk_review", ID: report.ID, URL: "/api/v1/risk-reviews/" + string(report.ID)}
	job.Status, job.Result = sharedjob.Succeeded, result
	s.jobs.byID[job.ID] = job
	s.reports[report.ID] = report
	return nil
}

func (s *riskReportStoreFake) GetRiskReport(_ context.Context, id domain.ID) (Report, error) {
	report, found := s.reports[id]
	if !found {
		return Report{}, errors.New("not found")
	}
	return report, nil
}

func (s *riskReportStoreFake) ListRiskReports(context.Context, HistoryQuery) ([]Report, error) {
	values := make([]Report, 0, len(s.reports))
	for _, report := range s.reports {
		values = append(values, report)
	}
	return values, nil
}

type riskEngineFake struct {
	jobs          *riskJobStoreFake
	cancelOnKey   string
	compareCalls  []string
	structureCall []string
	failKey       string
	planFail      bool
}

func (e *riskEngineFake) Plan(context.Context, contract.RiskInputV1) (CalculationPlan, error) {
	if e.planFail {
		return CalculationPlan{}, errors.New("registry contract drift")
	}
	return CalculationPlan{ComparisonKeys: []string{"metric-b", "metric-a"}, StructuralKeys: []string{"structure-a"}}, nil
}

func (e *riskEngineFake) Compare(_ context.Context, input contract.RiskInputV1, key string) ([]contract.RiskItemV1, error) {
	e.compareCalls = append(e.compareCalls, key)
	if e.failKey == key {
		return nil, errors.New("provider path /tmp/secret")
	}
	if e.cancelOnKey == key {
		for id := range e.jobs.byID {
			_, _, _ = e.jobs.RequestCancellation(context.Background(), id)
		}
	}
	severity := contract.Info
	rule := contract.Identity{ID: "comparison-rule", Version: "v1", Hash: strings.Repeat("b", 64)}
	evidence := &contract.StructureEvidence{Kind: contract.NewMultiplierIssue, Rule: rule, EntityID: input.Subjects[0].Members[0], FieldPath: "/payload/value", Fingerprint: strings.Repeat("c", 64), Override: contract.NonOverridable}
	return []contract.RiskItemV1{{ID: key, Kind: contract.StructuralRiskItem, Role: contract.Required, Status: contract.Comparable, Severity: &severity, Rule: rule, Override: contract.NonOverridable, EvidenceHash: strings.Repeat("c", 64), Structural: evidence}}, nil
}

func (e *riskEngineFake) CheckStructure(_ context.Context, _ contract.RiskInputV1, key string) ([]contract.RiskItemV1, error) {
	e.structureCall = append(e.structureCall, key)
	if e.failKey == key {
		return nil, errors.New("structural provider stack /tmp/secret")
	}
	severity := contract.Warning
	rule := contract.Identity{ID: "structure-rule", Version: "v1", Hash: strings.Repeat("d", 64)}
	evidence := &contract.StructureEvidence{Kind: contract.RepeatedMultiplierIssue, Rule: rule, EntityID: "01948c1e-0000-7000-8000-000000000012", FieldPath: "/payload/value", Fingerprint: strings.Repeat("e", 64), Override: contract.NonOverridable}
	return []contract.RiskItemV1{{ID: key, Kind: contract.StructuralRiskItem, Role: contract.Required, Status: contract.Comparable, Severity: &severity, Rule: rule, Override: contract.NonOverridable, EvidenceHash: strings.Repeat("e", 64), Structural: evidence}}, nil
}

func TestAdmissionRejectsBeforeJobAndReplaysExactMaterialization(t *testing.T) {
	clock := riskTestClock{time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)}
	jobs := newRiskJobStoreFake(clock)
	materials := &riskMaterialStoreFake{values: map[domain.ID]Materialization{}, jobs: jobs}
	input := orchestrationInput(t)
	command := AdmissionCommand{ProjectID: input.ProjectID, IdempotencyKey: "risk-evaluate"}
	if _, err := AdmitRiskReview(context.Background(), riskAdmissionSourceFake{err: errors.New("THRESHOLD_NOT_CONFIGURED")}, jobs, materials, clock, command); err == nil || jobs.creates != 0 {
		t.Fatalf("preflight err=%v creates=%d", err, jobs.creates)
	}
	source := riskAdmissionSourceFake{value: MaterializedInput{Input: input}}
	first, err := AdmitRiskReview(context.Background(), source, jobs, materials, clock, command)
	if err != nil || first.Replayed || !first.Materialization.Valid() {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	second, err := AdmitRiskReview(context.Background(), source, jobs, materials, clock, command)
	if err != nil || !second.Replayed || second.Job.ID != first.Job.ID || len(materials.values) != 1 {
		t.Fatalf("second=%#v err=%v materials=%d", second, err, len(materials.values))
	}
	changed := input
	changed.Threshold.Hash = strings.Repeat("f", 64)
	if _, err = AdmitRiskReview(context.Background(), riskAdmissionSourceFake{value: MaterializedInput{Input: changed}}, jobs, materials, clock, command); err == nil || len(materials.values) != 1 {
		t.Fatalf("changed input err=%v materials=%d", err, len(materials.values))
	}
}

func TestWorkerPhasesAreMonotonicDeterministicAndDuplicateSafe(t *testing.T) {
	clock := riskTestClock{time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)}
	jobs := newRiskJobStoreFake(clock)
	materials := &riskMaterialStoreFake{values: map[domain.ID]Materialization{}, jobs: jobs}
	input := orchestrationInput(t)
	admitted, err := AdmitRiskReview(context.Background(), riskAdmissionSourceFake{value: MaterializedInput{Input: input}}, jobs, materials, clock, AdmissionCommand{ProjectID: input.ProjectID, IdempotencyKey: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	events := &riskEventStoreFake{events: map[domain.ID][]sharedjob.Event{}}
	reports := &riskReportStoreFake{jobs: jobs, reports: map[domain.ID]Report{}}
	engine := &riskEngineFake{jobs: jobs}
	worker := Worker{Jobs: jobs, Events: events, Materializations: materials, Reports: reports, Engine: engine, Clock: clock, IDs: &riskTestIDs{next: 100}}
	completed, err := worker.Run(context.Background(), admitted.Job.ID)
	if err != nil || completed.Status != sharedjob.Succeeded || completed.Result == nil || len(reports.reports) != 1 {
		t.Fatalf("completed=%#v reports=%d err=%v", completed, len(reports.reports), err)
	}
	if strings.Join(engine.compareCalls, ",") != "metric-a,metric-b" || strings.Join(engine.structureCall, ",") != "structure-a" {
		t.Fatalf("comparison=%v structure=%v", engine.compareCalls, engine.structureCall)
	}
	gotEvents := events.events[admitted.Job.ID]
	for index := 1; index < len(gotEvents); index++ {
		if gotEvents[index].Ordinal != gotEvents[index-1].Ordinal+1 || gotEvents[index].Progress < gotEvents[index-1].Progress {
			t.Fatalf("nonmonotonic events=%#v", gotEvents)
		}
	}
	beforeEvents := len(gotEvents)
	again, err := worker.Run(context.Background(), admitted.Job.ID)
	if err != nil || again.ID != completed.ID || len(reports.reports) != 1 || len(events.events[admitted.Job.ID]) != beforeEvents {
		t.Fatalf("again=%#v reports=%d events=%d err=%v", again, len(reports.reports), len(events.events[admitted.Job.ID]), err)
	}
}

func TestWorkerCancellationBetweenUnitsNeverSealsPartialReport(t *testing.T) {
	clock := riskTestClock{time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)}
	jobs := newRiskJobStoreFake(clock)
	materials := &riskMaterialStoreFake{values: map[domain.ID]Materialization{}, jobs: jobs}
	input := orchestrationInput(t)
	admitted, err := AdmitRiskReview(context.Background(), riskAdmissionSourceFake{value: MaterializedInput{Input: input}}, jobs, materials, clock, AdmissionCommand{ProjectID: input.ProjectID, IdempotencyKey: "cancel"})
	if err != nil {
		t.Fatal(err)
	}
	events := &riskEventStoreFake{events: map[domain.ID][]sharedjob.Event{}}
	reports := &riskReportStoreFake{jobs: jobs, reports: map[domain.ID]Report{}}
	engine := &riskEngineFake{jobs: jobs, cancelOnKey: "metric-a"}
	worker := Worker{Jobs: jobs, Events: events, Materializations: materials, Reports: reports, Engine: engine, Clock: clock, IDs: &riskTestIDs{next: 200}}
	completed, err := worker.Run(context.Background(), admitted.Job.ID)
	if !errors.Is(err, ErrRiskCanceled) || completed.Status != sharedjob.Canceled || len(reports.reports) != 0 || strings.Join(engine.compareCalls, ",") != "metric-a" {
		t.Fatalf("completed=%#v reports=%d calls=%v err=%v", completed, len(reports.reports), engine.compareCalls, err)
	}
}

type riskRematerializerFake struct {
	value MaterializedInput
	err   error
}

type riskThresholdReaderFake struct{ version threshold.Version }

func (f riskThresholdReaderFake) GetThresholdVersion(_ context.Context, projectID, id domain.ID) (threshold.Version, error) {
	if projectID != f.version.ProjectID || id != f.version.ID {
		return threshold.Version{}, errors.New("threshold not found")
	}
	return f.version, nil
}

func (f riskRematerializerFake) RematerializeRiskInput(context.Context, contract.RiskInputV1) (MaterializedInput, error) {
	return f.value, f.err
}

func TestRecoveryRerunsOnlyExactCaptureAndPersistsMismatch(t *testing.T) {
	clock := riskTestClock{time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)}
	jobs := newRiskJobStoreFake(clock)
	materials := &riskMaterialStoreFake{values: map[domain.ID]Materialization{}, jobs: jobs}
	input := orchestrationInput(t)
	admitted, err := AdmitRiskReview(context.Background(), riskAdmissionSourceFake{value: MaterializedInput{Input: input}}, jobs, materials, clock, AdmissionCommand{ProjectID: input.ProjectID, IdempotencyKey: "recover"})
	if err != nil {
		t.Fatal(err)
	}
	changed := input
	changed.Threshold.Hash = strings.Repeat("f", 64)
	events := &riskEventStoreFake{events: map[domain.ID][]sharedjob.Event{}}
	engine := &riskEngineFake{jobs: jobs}
	worker := Worker{Jobs: jobs, Events: events, Materializations: materials, Reports: &riskReportStoreFake{jobs: jobs, reports: map[domain.ID]Report{}}, Engine: engine, Clock: clock, IDs: &riskTestIDs{next: 300}}
	manager := RecoveryManager{Worker: worker, Source: riskRematerializerFake{value: MaterializedInput{Input: changed}}, Jobs: jobs, Materialized: materials}
	results, err := manager.RecoverAll(context.Background())
	if err != nil || len(results) != 1 || results[0].JobID != admitted.Job.ID || results[0].Status != sharedjob.Failed || results[0].Error != "RECOVERY_MISMATCH" || len(engine.compareCalls) != 0 {
		t.Fatalf("results=%#v calls=%v err=%v", results, engine.compareCalls, err)
	}
}

func TestRecoveryResumesInterruptedExactInputAndRejectsMissingImplementation(t *testing.T) {
	for _, test := range []struct {
		name       string
		source     func(contract.RiskInputV1) riskRematerializerFake
		wantStatus sharedjob.Status
		wantError  string
	}{
		{name: "exact", source: func(input contract.RiskInputV1) riskRematerializerFake {
			return riskRematerializerFake{value: MaterializedInput{Input: input}}
		}, wantStatus: sharedjob.Succeeded},
		{name: "missing implementation", source: func(contract.RiskInputV1) riskRematerializerFake {
			return riskRematerializerFake{err: ErrRiskRecoveryUnavailable}
		}, wantStatus: sharedjob.Failed, wantError: "RECOVERY_UNAVAILABLE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			clock := riskTestClock{time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)}
			jobs := newRiskJobStoreFake(clock)
			materials := &riskMaterialStoreFake{values: map[domain.ID]Materialization{}, jobs: jobs}
			input := orchestrationInput(t)
			admitted, err := AdmitRiskReview(context.Background(), riskAdmissionSourceFake{value: MaterializedInput{Input: input}}, jobs, materials, clock, AdmissionCommand{ProjectID: input.ProjectID, IdempotencyKey: "recover-" + test.name})
			if err != nil {
				t.Fatal(err)
			}
			jobs.mu.Lock()
			job := jobs.byID[admitted.Job.ID]
			job.Status = sharedjob.Interrupted
			jobs.byID[job.ID] = job
			jobs.mu.Unlock()
			events := &riskEventStoreFake{events: map[domain.ID][]sharedjob.Event{}}
			reports := &riskReportStoreFake{jobs: jobs, reports: map[domain.ID]Report{}}
			worker := Worker{Jobs: jobs, Events: events, Materializations: materials, Reports: reports, Engine: &riskEngineFake{jobs: jobs}, Clock: clock, IDs: &riskTestIDs{next: 400}}
			manager := RecoveryManager{Worker: worker, Source: test.source(input), Jobs: jobs, Materialized: materials}
			results, recoverErr := manager.RecoverAll(context.Background())
			if recoverErr != nil || len(results) != 1 || results[0].Status != test.wantStatus || results[0].Error != test.wantError {
				t.Fatalf("results=%#v err=%v", results, recoverErr)
			}
		})
	}
}

func TestRecoveryRejectsActiveContextThresholdRunAndImplementationDrift(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(contract.RiskInputV1) (MaterializedInput, error)
	}{
		{name: "active baseline", mutate: func(contract.RiskInputV1) (MaterializedInput, error) {
			return MaterializedInput{}, ErrRiskRecoveryMismatch
		}},
		{name: "policy", mutate: func(input contract.RiskInputV1) (MaterializedInput, error) {
			input.Policy.Hash = strings.Repeat("f", 64)
			return MaterializedInput{Input: input}, nil
		}},
		{name: "threshold", mutate: func(input contract.RiskInputV1) (MaterializedInput, error) {
			input.Threshold.Hash = strings.Repeat("f", 64)
			return MaterializedInput{Input: input}, nil
		}},
		{name: "simulation run", mutate: func(input contract.RiskInputV1) (MaterializedInput, error) {
			input.CandidateRuns[0].ResultHash = strings.Repeat("f", 64)
			return MaterializedInput{Input: input}, nil
		}},
		{name: "implementation", mutate: func(input contract.RiskInputV1) (MaterializedInput, error) {
			input.Implementations[0].Hash = strings.Repeat("f", 64)
			return MaterializedInput{Input: input}, nil
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			clock := riskTestClock{time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)}
			jobs := newRiskJobStoreFake(clock)
			materials := &riskMaterialStoreFake{values: map[domain.ID]Materialization{}, jobs: jobs}
			input := orchestrationInput(t)
			admitted, err := AdmitRiskReview(context.Background(), riskAdmissionSourceFake{value: MaterializedInput{Input: input}}, jobs, materials, clock, AdmissionCommand{ProjectID: input.ProjectID, IdempotencyKey: "drift-" + mutation.name})
			if err != nil {
				t.Fatal(err)
			}
			value, sourceErr := mutation.mutate(input)
			events := &riskEventStoreFake{events: map[domain.ID][]sharedjob.Event{}}
			worker := Worker{Jobs: jobs, Events: events, Materializations: materials, Reports: &riskReportStoreFake{jobs: jobs, reports: map[domain.ID]Report{}}, Engine: &riskEngineFake{jobs: jobs}, Clock: clock, IDs: &riskTestIDs{next: 450}}
			manager := RecoveryManager{Worker: worker, Source: riskRematerializerFake{value: value, err: sourceErr}, Jobs: jobs, Materialized: materials}
			results, recoverErr := manager.RecoverAll(context.Background())
			if recoverErr != nil || len(results) != 1 || results[0].JobID != admitted.Job.ID || results[0].Status != sharedjob.Failed || results[0].Error != "RECOVERY_MISMATCH" {
				t.Fatalf("results=%#v err=%v", results, recoverErr)
			}
		})
	}
}

func TestWorkerStorageAndCancellationRacesPersistOnlyTerminalJob(t *testing.T) {
	for _, test := range []struct {
		name         string
		engineFail   string
		planFail     bool
		eventFail    string
		sealFail     bool
		cancelAtSeal bool
		wantStatus   sharedjob.Status
	}{
		{name: "materialized event storage failure", eventFail: "MATERIALIZED", wantStatus: sharedjob.Failed},
		{name: "plan contract failure", planFail: true, wantStatus: sharedjob.Failed},
		{name: "comparison failure", engineFail: "metric-a", wantStatus: sharedjob.Failed},
		{name: "structure failure", engineFail: "structure-a", wantStatus: sharedjob.Failed},
		{name: "comparing event storage failure", eventFail: "COMPARING", wantStatus: sharedjob.Failed},
		{name: "structure event storage failure", eventFail: "STRUCTURE_CHECKING", wantStatus: sharedjob.Failed},
		{name: "sealing event storage failure", eventFail: "SEALING", wantStatus: sharedjob.Failed},
		{name: "seal storage failure", sealFail: true, wantStatus: sharedjob.Failed},
		{name: "cancel inside seal", cancelAtSeal: true, wantStatus: sharedjob.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			clock := riskTestClock{time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)}
			jobs := newRiskJobStoreFake(clock)
			materials := &riskMaterialStoreFake{values: map[domain.ID]Materialization{}, jobs: jobs}
			input := orchestrationInput(t)
			admitted, err := AdmitRiskReview(context.Background(), riskAdmissionSourceFake{value: MaterializedInput{Input: input}}, jobs, materials, clock, AdmissionCommand{ProjectID: input.ProjectID, IdempotencyKey: "fault-" + test.name})
			if err != nil {
				t.Fatal(err)
			}
			events := &riskEventStoreFake{events: map[domain.ID][]sharedjob.Event{}, failAt: test.eventFail}
			reports := &riskReportStoreFake{jobs: jobs, reports: map[domain.ID]Report{}, fail: test.sealFail, cancelAtSeal: test.cancelAtSeal}
			worker := Worker{Jobs: jobs, Events: events, Materializations: materials, Reports: reports, Engine: &riskEngineFake{jobs: jobs, failKey: test.engineFail, planFail: test.planFail}, Clock: clock, IDs: &riskTestIDs{next: 500}}
			_, _ = worker.Run(context.Background(), admitted.Job.ID)
			job, getErr := jobs.GetJob(context.Background(), admitted.Job.ID)
			if getErr != nil || job.Status != test.wantStatus || job.Result != nil || len(reports.reports) != 0 {
				t.Fatalf("job=%#v reports=%d err=%v", job, len(reports.reports), getErr)
			}
		})
	}
}

func TestSucceededEventCrashWindowIsRepairedWithoutSecondReport(t *testing.T) {
	clock := riskTestClock{time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)}
	jobs := newRiskJobStoreFake(clock)
	materials := &riskMaterialStoreFake{values: map[domain.ID]Materialization{}, jobs: jobs}
	input := orchestrationInput(t)
	admitted, err := AdmitRiskReview(context.Background(), riskAdmissionSourceFake{value: MaterializedInput{Input: input}}, jobs, materials, clock, AdmissionCommand{ProjectID: input.ProjectID, IdempotencyKey: "success-event-crash"})
	if err != nil {
		t.Fatal(err)
	}
	events := &riskEventStoreFake{events: map[domain.ID][]sharedjob.Event{}, failAt: "SUCCEEDED"}
	reports := &riskReportStoreFake{jobs: jobs, reports: map[domain.ID]Report{}}
	worker := Worker{Jobs: jobs, Events: events, Materializations: materials, Reports: reports, Engine: &riskEngineFake{jobs: jobs}, Clock: clock, IDs: &riskTestIDs{next: 600}}
	completed, err := worker.Run(context.Background(), admitted.Job.ID)
	if err == nil || completed.Status != sharedjob.Succeeded || len(reports.reports) != 1 {
		t.Fatalf("completed=%#v reports=%d err=%v", completed, len(reports.reports), err)
	}
	events.failAt = ""
	repaired, err := worker.Run(context.Background(), admitted.Job.ID)
	if err != nil || repaired.Status != sharedjob.Succeeded || len(reports.reports) != 1 {
		t.Fatalf("repaired=%#v reports=%d err=%v", repaired, len(reports.reports), err)
	}
	storedEvents := events.events[admitted.Job.ID]
	if storedEvents[len(storedEvents)-1].Phase != "SUCCEEDED" {
		t.Fatalf("events=%#v", storedEvents)
	}
}

func TestDetailRehydratesImmutableInputThresholdEvidenceAndCurrentProjection(t *testing.T) {
	clock := riskTestClock{time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)}
	jobs := newRiskJobStoreFake(clock)
	materials := &riskMaterialStoreFake{values: map[domain.ID]Materialization{}, jobs: jobs}
	input := orchestrationInput(t)
	body := threshold.Body{SchemaVersion: threshold.SchemaVersionV1, Source: "explicit test threshold", Assumptions: []string{"fixed fixture"}, Entries: []threshold.Entry{{SceneID: "scene", SceneVersion: "v1", MetricID: "metric-resource", MetricVersion: "v1", Unit: "ratio", Direction: contract.TargetRange, Relative: threshold.Boundaries{Warning: "0.10", Block: "0.25"}}}, StructuralRuleVersions: []contract.Identity{{ID: "structural", Version: "v1", Hash: strings.Repeat("a", 64)}}}
	version, err := threshold.NewVersion(input.ProjectID, domain.ID(input.Threshold.ID), 1, threshold.OriginStarter, true, body, "tester", clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	input.Threshold.Hash = version.BodyHash
	impact := contract.ImpactEvidenceRef{ReportID: "impact", EvidenceID: "path", RevisionPairHash: strings.Repeat("b", 64), SnapshotHash: strings.Repeat("c", 64), Classification: "deterministic", Freshness: "fresh"}
	admitted, err := AdmitRiskReview(context.Background(), riskAdmissionSourceFake{value: MaterializedInput{Input: input, ImpactEvidence: []contract.ImpactEvidenceRef{impact}}}, jobs, materials, clock, AdmissionCommand{ProjectID: input.ProjectID, IdempotencyKey: "detail"})
	if err != nil {
		t.Fatal(err)
	}
	events := &riskEventStoreFake{events: map[domain.ID][]sharedjob.Event{}}
	reports := &riskReportStoreFake{jobs: jobs, reports: map[domain.ID]Report{}}
	worker := Worker{Jobs: jobs, Events: events, Materializations: materials, Reports: reports, Engine: &riskEngineFake{jobs: jobs}, Clock: clock, IDs: &riskTestIDs{next: 700}}
	completed, err := worker.Run(context.Background(), admitted.Job.ID)
	if err != nil || completed.Result == nil {
		t.Fatalf("completed=%#v err=%v", completed, err)
	}
	report := reports.reports[completed.Result.ID]
	runResults := map[domain.ID]string{}
	for _, run := range report.SimulationRuns {
		runResults[run.RunID] = run.ResultHash
	}
	current := CurrentContext{Policy: report.Policy, Threshold: report.Threshold, Implementations: append([]contract.Identity(nil), report.Implementations...), SimulationResults: runResults, ImpactStates: map[string]string{"impact\x00path": "fresh"}}
	service := DetailService{Reports: reports, Materializations: materials, Thresholds: riskThresholdReaderFake{version}}
	detail, err := service.Get(context.Background(), report.ID, current, "/api/v1/releases/audit/1")
	if err != nil || detail.Input.Candidate.ManifestHash != input.Candidate.ManifestHash || detail.Threshold.BodyHash != version.BodyHash || len(detail.ImpactEvidence) != 1 || len(detail.Projection.ImpactEvidence) != 1 || detail.Projection.ImpactEvidence[0].State != "fresh" || detail.Projection.Freshness != Fresh || detail.Projection.Compatibility != Compatible || detail.ReleaseAuditURL == "" || detail.Projection.ReleaseAuditURL == "" || detail.Input.CandidateRuns[0].Metrics[0].TargetRange.Lower != "0.8" {
		t.Fatalf("detail=%#v err=%v", detail, err)
	}
	current.Policy.Hash = strings.Repeat("f", 64)
	stale, err := service.Get(context.Background(), report.ID, current, "")
	if err != nil || stale.Projection.Freshness != Stale {
		t.Fatalf("stale=%#v err=%v", stale.Projection, err)
	}
}

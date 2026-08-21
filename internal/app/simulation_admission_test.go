package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/simulation/contract"
	"github.com/zouyi/eco-guardian/internal/simulation/scenario"
)

type admissionSourceFake struct{ revision contract.Revision }

func (f admissionSourceFake) ResolveRevision(context.Context, contract.ID) (contract.Revision, error) {
	return f.revision, nil
}
func (f admissionSourceFake) ResolveReleaseRevision(context.Context, contract.ID) (contract.Revision, error) {
	return f.revision, nil
}

type admissionGateFake struct{ err error }

func (f admissionGateFake) RequireFull(context.Context, contract.ID) error { return f.err }

type admissionSceneFake struct {
	scene contract.CapturedScenario
	err   error
}

func (f admissionSceneFake) ResolveSimulationScenario(context.Context, string, string) (contract.CapturedScenario, error) {
	return f.scene, f.err
}

type admissionFingerprintFake struct {
	value string
	err   error
}

func (f admissionFingerprintFake) ResolveSimulationFingerprint(context.Context, contract.SimulationInputV1) (string, error) {
	return f.value, f.err
}

type admissionJobsFake struct {
	calls int
	seen  SimulationSubmission
}

func (f *admissionJobsFake) SubmitSimulation(_ context.Context, submission SimulationSubmission) (sharedjob.Record, bool, error) {
	f.calls++
	f.seen = submission
	return sharedjob.Record{ID: mustSimulationID()}, false, nil
}

type verificationSourceFake struct {
	source contract.VerificationSource
	err    error
}

func (f verificationSourceFake) ResolveSimulationVerificationSource(context.Context, domain.ID) (contract.VerificationSource, error) {
	return f.source, f.err
}

func TestSimulationAdmissionUsesCapturedPortsBeforeCreatingJob(t *testing.T) {
	project, revision, sceneID := mustSimulationID(), mustSimulationID(), mustSimulationID()
	source := admissionSourceFake{revision: contract.Revision{ID: contract.ID(revision), ProjectID: contract.ID(project), ConfigHash: strings.Repeat("a", 64), ManifestHash: strings.Repeat("b", 64)}}
	jobs := &admissionJobsFake{}
	service := SimulationAdmissionApplication{Revisions: source, Releases: source, Gate: admissionGateFake{}, Scenarios: admissionSceneFake{scene: contract.CapturedScenario{DefinitionID: sceneID, Template: scenario.BuiltinTemplates()[0]}}, Fingerprints: admissionFingerprintFake{value: strings.Repeat("c", 64)}, Jobs: jobs}
	result, err := service.AdmitSimulation(context.Background(), SimulationAdmission{ProjectID: project, RevisionID: revision, SceneID: "single-target-30s", SceneVersion: "v1", Metrics: []contract.MetricIdentity{{ID: "metric-dps", Version: "v1"}}, IdempotencyKey: "simulation"})
	if err != nil || !result.Job.Valid() || jobs.calls != 1 {
		t.Fatalf("result=%#v calls=%d err=%v", result, jobs.calls, err)
	}
}

func TestSimulationAdmissionBlocksBeforeJobCreation(t *testing.T) {
	project, revision := mustSimulationID(), mustSimulationID()
	source := admissionSourceFake{revision: contract.Revision{ID: contract.ID(revision), ProjectID: contract.ID(project), ConfigHash: strings.Repeat("a", 64), ManifestHash: strings.Repeat("b", 64)}}
	jobs := &admissionJobsFake{}
	service := SimulationAdmissionApplication{Revisions: source, Gate: admissionGateFake{err: errors.New("blocked")}, Scenarios: admissionSceneFake{}, Fingerprints: admissionFingerprintFake{}, Jobs: jobs}
	if _, err := service.AdmitSimulation(context.Background(), SimulationAdmission{ProjectID: project, RevisionID: revision, SceneID: "single-target-30s", SceneVersion: "v1", Metrics: []contract.MetricIdentity{{ID: "metric-dps", Version: "v1"}}, IdempotencyKey: "simulation"}); err == nil || jobs.calls != 0 {
		t.Fatalf("calls=%d err=%v", jobs.calls, err)
	}
}

func TestSimulationAdmissionRequiresExactVerificationSource(t *testing.T) {
	project, revision, sceneID, sourceRunID := mustSimulationID(), mustSimulationID(), mustSimulationID(), mustSimulationID()
	source := admissionSourceFake{revision: contract.Revision{ID: contract.ID(revision), ProjectID: contract.ID(project), ConfigHash: strings.Repeat("a", 64), ManifestHash: strings.Repeat("b", 64)}}
	jobs := &admissionJobsFake{}
	scene := contract.CapturedScenario{DefinitionID: sceneID, Template: scenario.BuiltinTemplates()[0]}
	fingerprint := strings.Repeat("c", 64)
	service := SimulationAdmissionApplication{Revisions: source, Releases: source, Gate: admissionGateFake{}, Scenarios: admissionSceneFake{scene: scene}, Fingerprints: admissionFingerprintFake{value: fingerprint}, Jobs: jobs}
	input, err := contract.NormalizeInput(contract.InputRequest{Revision: source.revision, Scene: scene.Template, Metrics: []contract.MetricIdentity{{ID: "metric-dps", Version: "v1"}}})
	if err != nil {
		t.Fatal(err)
	}
	inputHash, err := input.Hash()
	if err != nil {
		t.Fatal(err)
	}
	service.Verifications = verificationSourceFake{source: contract.VerificationSource{RunID: sourceRunID, ProjectID: project, RevisionID: revision, ScenarioDefinitionID: sceneID, InputHash: inputHash, FingerprintHash: fingerprint}}
	result, err := service.AdmitSimulation(context.Background(), SimulationAdmission{ProjectID: project, RevisionID: revision, SceneID: "single-target-30s", SceneVersion: "v1", Metrics: []contract.MetricIdentity{{ID: "metric-dps", Version: "v1"}}, VerifyRunID: sourceRunID, IdempotencyKey: "verify"})
	if err != nil || !result.Job.Valid() || jobs.calls != 1 || jobs.seen.VerifyRunID != sourceRunID {
		t.Fatalf("result=%#v calls=%d submission=%#v err=%v", result, jobs.calls, jobs.seen, err)
	}
	service.Verifications = verificationSourceFake{source: contract.VerificationSource{RunID: sourceRunID, ProjectID: project, RevisionID: revision, ScenarioDefinitionID: sceneID, InputHash: strings.Repeat("d", 64), FingerprintHash: fingerprint}}
	if _, err = service.AdmitSimulation(context.Background(), SimulationAdmission{ProjectID: project, RevisionID: revision, SceneID: "single-target-30s", SceneVersion: "v1", Metrics: []contract.MetricIdentity{{ID: "metric-dps", Version: "v1"}}, VerifyRunID: sourceRunID, IdempotencyKey: "mismatch"}); !errors.Is(err, ErrSimulationAdmissionUnavailable) || jobs.calls != 1 {
		t.Fatalf("calls=%d err=%v", jobs.calls, err)
	}
}

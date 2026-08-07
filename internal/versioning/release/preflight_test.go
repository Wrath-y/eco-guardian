package release

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/validation"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

func TestPreflightRequiresExactFullAndCompatibleGateResultsBeforeAnyJob(t *testing.T) {
	service, command, validation, results := validPreflight(t)
	preflight, err := service.Preflight(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if preflight.Validation != versioninggate.Pass || preflight.Assessment.State != versioninggate.Pass || validation.revisionID != command.CandidateRevisionID || validation.configHash != command.ConfigHash || results.calls != 1 {
		t.Fatalf("preflight=%#v validation=%#v result calls=%d", preflight, validation, results.calls)
	}
}

func TestPreflightRejectsUnavailableAndIncompleteInputsSynchronously(t *testing.T) {
	tests := []struct {
		name   string
		change func(*PreflightService, *Command, *preflightValidation, *preflightResults)
		want   error
	}{
		{"validation BLOCK", func(_ *PreflightService, _ *Command, validation *preflightValidation, _ *preflightResults) {
			validation.result = validationGateBlocked
		}, ErrPreflightFailed},
		{"validation stale", func(_ *PreflightService, _ *Command, full *preflightValidation, _ *preflightResults) {
			full.result = validation.GateRequiresValidation
		}, ErrPreflightFailed},
		{"validation offline", func(_ *PreflightService, _ *Command, validation *preflightValidation, _ *preflightResults) {
			validation.err = errors.New("offline")
		}, ErrPreflightUnavailable},
		{"unregistered gate", func(service *PreflightService, _ *Command, _ *preflightValidation, _ *preflightResults) {
			service.Registry = nil
		}, ErrReleaseCapabilityDisabled},
		{"offline result source", func(_ *PreflightService, _ *Command, _ *preflightValidation, results *preflightResults) {
			results.err = errors.New("offline")
		}, ErrPreflightUnavailable},
		{"missing required result", func(_ *PreflightService, _ *Command, _ *preflightValidation, results *preflightResults) {
			results.results = nil
		}, ErrPreflightFailed},
		{"stale required result", func(_ *PreflightService, _ *Command, _ *preflightValidation, results *preflightResults) {
			results.results[0].Context.Candidate.ConfigHash = strings.Repeat("e", 64)
		}, ErrPreflightFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, command, validation, results := validPreflight(t)
			test.change(&service, &command, validation, results)
			_, err := service.Preflight(context.Background(), command)
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want %v", err, test.want)
			}
		})
	}
}

func TestPreflightAuditsWarningAndPermittedNumericOverride(t *testing.T) {
	service, command, _, results := validPreflight(t)
	results.results[0].State = versioninggate.Warning
	if _, err := service.Preflight(context.Background(), command); !errors.Is(err, ErrWarningConfirmation) {
		t.Fatalf("warning error=%v", err)
	}
	command.Confirmations = append(command.Confirmations, Confirmation{Kind: ConfirmationAcknowledgeWarning, Confirmed: true})
	if _, err := service.Preflight(context.Background(), command); err != nil {
		t.Fatal(err)
	}

	service, command, _, results = validPreflight(t)
	results.results[0].State = versioninggate.Block
	results.results[0].Descriptor.OverridableNumericBlock = true
	command.Override = &OverrideAudit{GateResultID: results.results[0].ID, Reason: "measured impact accepted", Confirmed: true}
	command.Confirmations = append(command.Confirmations, Confirmation{Kind: ConfirmationNumericOverride, Confirmed: true})
	if _, err := service.Preflight(context.Background(), command); err != nil {
		t.Fatal(err)
	}
}

func TestPreflightRechecksEveryWorkerBoundaryAndMapsChangedPointer(t *testing.T) {
	service, command, validation, _ := validPreflight(t)
	for _, stage := range []RecheckStage{RecheckWorkerStart, RecheckBeforeBackup, RecheckBeforeGraph, RecheckBeforeCommit} {
		if _, err := service.Recheck(context.Background(), stage, command); err != nil {
			t.Fatalf("stage %s: %v", stage, err)
		}
	}
	if validation.calls != 4 {
		t.Fatalf("full validation rechecks=%d", validation.calls)
	}
	sources := service.Sources.(*commandSources)
	sources.pointer.ReleaseID = commandID(t)
	if _, err := service.Recheck(context.Background(), RecheckBeforeCommit, command); !errors.Is(err, ErrReleaseBaseConflict) {
		t.Fatalf("changed pointer error=%v", err)
	}
}

const validationGateBlocked = validation.GateBlocked

type preflightValidation struct {
	result     validation.GateResult
	err        error
	revisionID domain.ID
	configHash string
	versions   validation.VersionManifest
	calls      int
}

func (f *preflightValidation) Check(_ context.Context, revisionID domain.ID, configHash string, versions validation.VersionManifest) (validation.GateResult, error) {
	f.calls++
	f.revisionID, f.configHash, f.versions = revisionID, configHash, versions
	return f.result, f.err
}

type preflightResults struct {
	results []versioninggate.Result
	err     error
	calls   int
}

func (f *preflightResults) Results(_ context.Context, _ versioningrevision.CandidateContext, _ versioningpolicy.ReleasePolicy) ([]versioninggate.Result, error) {
	f.calls++
	return f.results, f.err
}

func validPreflight(t *testing.T) (PreflightService, Command, *preflightValidation, *preflightResults) {
	t.Helper()
	sources, command := validCommandSources(t, "")
	manifest := versioningrevision.VersionManifest{Entries: []versioningrevision.VersionEntry{
		{CapabilityID: "schema", ContractVersion: "validation-v1", ImplementationVersion: "schema-v1", State: versioningrevision.Registered},
		{CapabilityID: "dsl", ContractVersion: "validation-v1", ImplementationVersion: "dsl-v1", State: versioningrevision.Registered},
		{CapabilityID: "validator-registry", ContractVersion: "validation-v1", ImplementationVersion: "registry-v1", State: versioningrevision.Registered},
		{CapabilityID: "numeric-policy", ContractVersion: "validation-v1", ImplementationVersion: "numeric-v1", State: versioningrevision.Registered},
	}}
	manifestHash, err := manifest.Hash()
	if err != nil {
		t.Fatal(err)
	}
	sources.revision.Metadata.Manifest, sources.revision.Metadata.ManifestHash = manifest, manifestHash
	command.ManifestHash = manifestHash

	definition := versioningpolicy.Definition{Samples: 1000, ThresholdID: "threshold-v1", ThresholdOn: true, Capabilities: []versioningpolicy.CapabilityRequirement{{CapabilityID: "risk", GateID: "threshold", ContractVersion: "risk-v1", ImplementationVersion: "risk-v1"}}, Scenes: []versioningpolicy.Scene{{ID: "single-target", Required: true, Metrics: []versioningpolicy.Metric{{ID: "damage", Required: true}}}}}
	sources.policy.Definition = definition
	sources.policy.CanonicalHash, err = sources.policy.Hash()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := versioninggate.NewRegistry(versioninggate.Descriptor{CapabilityID: "risk", GateID: "threshold", ContractVersion: "risk-v1", ImplementationVersion: "risk-v1", RequiredInputs: []string{"revision_id"}, SupportedStates: []versioninggate.ResultState{versioninggate.Pass, versioninggate.Warning, versioninggate.Block, versioninggate.Unavailable, versioninggate.Stale}, OverridableNumericBlock: true})
	if err != nil {
		t.Fatal(err)
	}
	candidate := versioningrevision.CandidateContext{RevisionID: command.CandidateRevisionID, ConfigHash: command.ConfigHash, ManifestHash: command.ManifestHash, PolicyID: command.PolicyID}
	result := versioninggate.Result{ID: commandID(t), Descriptor: versioninggate.Descriptor{CapabilityID: "risk", GateID: "threshold", ContractVersion: "risk-v1", ImplementationVersion: "risk-v1", RequiredInputs: []string{"revision_id"}, SupportedStates: []versioninggate.ResultState{versioninggate.Pass, versioninggate.Warning, versioninggate.Block, versioninggate.Unavailable, versioninggate.Stale}, OverridableNumericBlock: true}, State: versioninggate.Pass, ResultHash: strings.Repeat("c", 64), Context: versioninggate.EvaluationContext{Candidate: candidate, PolicyHash: sources.policy.CanonicalHash, SceneID: "single-target", MetricID: "damage", ThresholdID: "threshold-v1", ImplementationVersions: map[string]string{"risk": "risk-v1"}}}
	validation := &preflightValidation{result: validation.GatePass}
	results := &preflightResults{results: []versioninggate.Result{result}}
	return PreflightService{Sources: sources, Validation: validation, Registry: registry, Results: results}, command, validation, results
}

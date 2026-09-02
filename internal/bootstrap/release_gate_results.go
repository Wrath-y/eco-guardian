package bootstrap

import (
	"context"

	"github.com/zouyi/eco-guardian/internal/domain"
	graphgate "github.com/zouyi/eco-guardian/internal/graph/gate"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
	riskgate "github.com/zouyi/eco-guardian/internal/risk/gate"
	riskorchestration "github.com/zouyi/eco-guardian/internal/risk/orchestration"
	simulationgate "github.com/zouyi/eco-guardian/internal/simulation/gate"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
	versioning "github.com/zouyi/eco-guardian/internal/versioning"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

// releaseGateResults adapts existing immutable Graph/simulation/risk evidence
// plus the mandatory backup boundary into the release preflight read port.
// Missing evidence stays UNAVAILABLE; this adapter never runs those jobs.
type releaseGateResults struct {
	registry          *versioninggate.Registry
	store             *store.Store
	graph             graphsync.GraphProvider
	simulationVersion string
	backupAvailable   func(context.Context) bool
}

func (source releaseGateResults) Results(ctx context.Context, candidate versioningrevision.CandidateContext, policy versioningpolicy.ReleasePolicy) ([]versioninggate.Result, error) {
	if source.registry == nil || source.store == nil || !candidate.Valid() || !policy.Valid() {
		return nil, versioninggate.ErrDescriptorInvalid
	}
	results := make([]versioninggate.Result, 0, len(policy.Capabilities)+len(policy.Scenes))
	if descriptor, ok := source.descriptor(policy, gateKindGraph); ok {
		results = append(results, source.graphResult(ctx, candidate, policy, descriptor))
	}
	if descriptor, ok := source.descriptor(policy, gateKindSimulation); ok {
		simulationResults, err := (simulationgate.ResultSource{
			Evidence: source.store, ImplementationVersion: source.simulationVersion,
			Implementations: map[string]string{},
		}).Results(ctx, candidate, policy)
		if err == nil {
			for index := range simulationResults {
				simulationResults[index] = remapGateResult(simulationResults[index], descriptor)
			}
			results = append(results, simulationResults...)
		}
	}
	if descriptor, ok := source.descriptor(policy, gateKindRisk); ok {
		results = append(results, source.riskResult(ctx, candidate, policy, descriptor))
	}
	if descriptor, ok := source.descriptor(policy, gateKindBackup); ok {
		state := versioninggate.Unavailable
		if source.backupAvailable != nil && source.backupAvailable(ctx) {
			state = versioninggate.Pass
		}
		results = append(results, newGateResult(candidate, policy, descriptor, state, nil, ""))
	}
	return results, nil
}

type releaseGateKind uint8

const (
	gateKindGraph releaseGateKind = iota + 1
	gateKindSimulation
	gateKindRisk
	gateKindBackup
)

func (source releaseGateResults) descriptor(policy versioningpolicy.ReleasePolicy, kind releaseGateKind) (versioninggate.Descriptor, bool) {
	for _, requirement := range policy.Capabilities {
		matches := false
		switch kind {
		case gateKindGraph:
			matches = (requirement.CapabilityID == "graph" && requirement.GateID == "projection") ||
				(requirement.CapabilityID == graphgate.CapabilityID && requirement.GateID == graphgate.GateID)
		case gateKindSimulation:
			matches = (requirement.CapabilityID == "simulation" || requirement.CapabilityID == simulationgate.SimulationCapabilityID) && requirement.GateID == simulationgate.SimulationGateID
		case gateKindRisk:
			matches = requirement.CapabilityID == riskgate.CapabilityID && (requirement.GateID == "threshold" || requirement.GateID == riskgate.GateID)
		case gateKindBackup:
			matches = (requirement.CapabilityID == "backup" && requirement.GateID == "online-backup") ||
				(requirement.CapabilityID == currentBackupGateDescriptor().CapabilityID && requirement.GateID == currentBackupGateDescriptor().GateID)
		}
		if !matches {
			continue
		}
		descriptor, err := source.registry.Descriptor(requirement.CapabilityID, requirement.GateID)
		return descriptor, err == nil
	}
	return versioninggate.Descriptor{}, false
}

func (source releaseGateResults) graphResult(ctx context.Context, candidate versioningrevision.CandidateContext, policy versioningpolicy.ReleasePolicy, descriptor versioninggate.Descriptor) versioninggate.Result {
	state := versioninggate.Unavailable
	evidence := []versioninggate.Evidence{}
	resultHash := ""
	summary, found, err := source.store.GraphProjectionSummary(ctx, candidate.RevisionID)
	if err == nil && found {
		syncState, stateFound, stateErr := source.store.GetGraphSyncState(ctx, candidate.RevisionID)
		if stateErr == nil && stateFound && domain.ID(syncState.LatestJobID).Valid() {
			job, jobErr := source.store.GetGraphJob(ctx, domain.ID(syncState.LatestJobID))
			if jobErr == nil {
				evaluation := (graphgate.Evaluator{Provider: source.graph}).Evaluate(ctx, graphgate.EvaluationRequest{
					Candidate: candidate, Summary: summary, State: syncState, Job: &job,
					RequestID: "release-graph-gate-" + string(candidate.RevisionID),
				})
				state, evidence, resultHash = evaluation.State, evaluation.Evidence, summary.ManifestHash
			}
		}
	}
	return newGateResult(candidate, policy, descriptor, state, evidence, resultHash)
}

func (source releaseGateResults) riskResult(ctx context.Context, candidate versioningrevision.CandidateContext, policy versioningpolicy.ReleasePolicy, descriptor versioninggate.Descriptor) versioninggate.Result {
	reports, err := source.store.ListRiskReports(ctx, riskorchestration.HistoryQuery{CandidateRevisionID: candidate.RevisionID, Limit: 100})
	if err != nil {
		return newGateResult(candidate, policy, descriptor, versioninggate.Unavailable, nil, "")
	}
	var report *riskorchestration.Report
	var decision *riskorchestration.Report
	for index := range reports {
		value := &reports[index]
		if value.Kind == riskcontract.DecisionReport && decision == nil {
			decision = value
		}
		if value.Kind == riskcontract.CalculationReport && value.Candidate.RevisionID == candidate.RevisionID && value.Candidate.ConfigHash == candidate.ConfigHash && value.Candidate.ManifestHash == candidate.ManifestHash && value.Policy.ID == string(policy.ID) && value.Policy.Hash == policy.CanonicalHash && ((value.Baseline.Kind == riskcontract.NoBaseline && candidate.BaselineReleaseID == "") || value.Baseline.ReleaseID == candidate.BaselineReleaseID) {
			report = value
			break
		}
	}
	if report == nil {
		return newGateResult(candidate, policy, descriptor, versioninggate.Unavailable, nil, "")
	}
	simulationResults := make(map[domain.ID]string, len(report.SimulationRuns))
	for _, run := range report.SimulationRuns {
		simulationResults[run.RunID] = run.ResultHash
	}
	requirements := make([]riskcontract.PolicyRequirement, 0)
	for _, scene := range policy.Scenes {
		for _, metric := range scene.Metrics {
			role := riskcontract.Optional
			if scene.Required && metric.Required {
				role = riskcontract.Required
			}
			requirements = append(requirements, riskcontract.PolicyRequirement{SceneID: scene.ID, SceneVersion: scene.Version, MetricID: metric.ID, MetricVersion: "v1", Role: role})
		}
	}
	var matchingDecision *riskorchestration.Report
	if decision != nil && decision.SourceReportID == report.ID {
		matchingDecision = decision
	}
	evaluation := riskgate.Evaluate(*report, riskgate.Context{
		Candidate: report.Candidate, ActiveBaselineReleaseID: candidate.BaselineReleaseID,
		Policy: report.Policy, Threshold: report.Threshold, ThresholdEnabled: true,
		Implementations: report.Implementations, SimulationResults: simulationResults, Requirements: requirements,
		EstablishBaselineConfirmed: true, NoBaselineRequiredFactsComplete: true, Decision: matchingDecision,
	})
	state := versioninggate.Unavailable
	switch evaluation.State {
	case riskcontract.GatePass:
		state = versioninggate.Pass
	case riskcontract.GateWarning:
		state = versioninggate.Warning
	case riskcontract.GateBlock:
		state = versioninggate.Block
	case riskcontract.GateStale:
		state = versioninggate.Stale
	}
	evidence := []versioninggate.Evidence{{ID: "risk-report:" + string(report.ID), Hash: report.ReportHash, URL: "/api/v1/risk-reviews/" + string(report.ID)}}
	result := newGateResult(candidate, policy, descriptor, state, evidence, report.ReportHash)
	result.Context.ThresholdID = report.Threshold.ID
	return result
}

func remapGateResult(result versioninggate.Result, descriptor versioninggate.Descriptor) versioninggate.Result {
	delete(result.Context.ImplementationVersions, result.Descriptor.CapabilityID)
	result.Descriptor = descriptor
	result.Context.ImplementationVersions[descriptor.CapabilityID] = descriptor.ImplementationVersion
	return result
}

func newGateResult(candidate versioningrevision.CandidateContext, policy versioningpolicy.ReleasePolicy, descriptor versioninggate.Descriptor, state versioninggate.ResultState, evidence []versioninggate.Evidence, resultHash string) versioninggate.Result {
	id, _ := domain.NewID()
	if len(resultHash) != 64 {
		resultHash = versioning.SHA256([]byte(string(state) + "\x00" + descriptor.CapabilityID + "\x00" + descriptor.GateID + "\x00" + string(candidate.RevisionID) + "\x00" + policy.CanonicalHash))
	}
	return versioninggate.Result{
		ID: id, Descriptor: descriptor, State: state, Evidence: append([]versioninggate.Evidence(nil), evidence...), ResultHash: resultHash,
		Context: versioninggate.EvaluationContext{
			Candidate: candidate, PolicyHash: policy.CanonicalHash,
			ImplementationVersions: map[string]string{descriptor.CapabilityID: descriptor.ImplementationVersion},
		},
	}
}

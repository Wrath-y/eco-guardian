package gate

import (
	"context"
	"errors"
	"sort"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/simulation/contract"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

const (
	SimulationGateID       = "scenario"
	SimulationGateContract = "simulation-v1"
)

var ErrEvidenceUnavailable = errors.New("simulation gate evidence is unavailable")

// MetricEvidence is an immutable result row needed for a simulation Gate.
type MetricEvidence struct{ ID, Version, Status string }

// RunEvidence is a read-only projection over one sealed simulation run. The
// Gate never runs a simulation, reads current working state, or repairs a
// historical fingerprint.
type RunEvidence struct {
	ID, RevisionID                         domain.ID
	Input                                  contract.SimulationInputV1
	InputHash, FingerprintHash, ResultHash string
	Metrics                                []MetricEvidence
	Reproducible                           bool
}

// EvidenceSource is the only persistence dependency of the Gate. Its caller
// supplies sealed, project-scoped rows for exactly one immutable revision.
type EvidenceSource interface {
	SimulationEvidence(context.Context, domain.ID) ([]RunEvidence, error)
}

// ResultSource implements release.GateResultSource structurally. It is kept
// independent of the release package so simulation does not depend on a
// release worker or persistence implementation.
type ResultSource struct {
	Evidence              EvidenceSource
	ImplementationVersion string
	Implementations       map[string]string
}

func (s ResultSource) Results(ctx context.Context, candidate versioningrevision.CandidateContext, policy versioningpolicy.ReleasePolicy) ([]versioninggate.Result, error) {
	if s.Evidence == nil || s.ImplementationVersion == "" || !candidate.Valid() || !policy.Valid() {
		return nil, ErrEvidenceUnavailable
	}
	runs, err := s.Evidence.SimulationEvidence(ctx, candidate.RevisionID)
	if err != nil {
		return nil, err
	}
	return EvaluatePolicy(candidate, policy, s.ImplementationVersion, s.Implementations, runs), nil
}

// Provider registers the stable simulation Gate descriptor with #7's Gate
// Registry. Result evaluation remains a separate read-only port.
type Provider struct{ ImplementationVersion string }

func (p Provider) Descriptor() versioninggate.Descriptor { return Descriptor(p.ImplementationVersion) }

func Descriptor(implementationVersion string) versioninggate.Descriptor {
	return versioninggate.Descriptor{CapabilityID: SimulationCapabilityID, GateID: SimulationGateID, ContractVersion: SimulationGateContract, ImplementationVersion: implementationVersion, RequiredInputs: []string{"candidate_revision", "config_hash", "scene_id", "scene_version", "sample_count", "seed", "rule_materialization_hash", "input_hash", "fingerprint_hash", "result_hash", "reproducible"}, SupportedStates: []versioninggate.ResultState{versioninggate.Pass, versioninggate.Warning, versioninggate.Block, versioninggate.Unavailable, versioninggate.Stale}, OverridableNumericBlock: false}
}

// EvaluatePolicy emits one exact Gate result for every policy scene/Metric.
// A missing, stale, unavailable, failed, or non-reproducible run is BLOCK;
// optionality is intentionally left to versioninggate.AssessPolicy, which
// turns an optional non-PASS result into a visible WARNING.
func EvaluatePolicy(candidate versioningrevision.CandidateContext, policy versioningpolicy.ReleasePolicy, implementationVersion string, implementations map[string]string, runs []RunEvidence) []versioninggate.Result {
	if implementationVersion == "" {
		return nil
	}
	descriptor := Descriptor(implementationVersion)
	versions := make(map[string]string, len(implementations)+1)
	for id, version := range implementations {
		versions[id] = version
	}
	versions[SimulationCapabilityID] = implementationVersion
	results := make([]versioninggate.Result, 0)
	for _, scene := range policy.Scenes {
		for _, metric := range scene.Metrics {
			state, evidence := findExactEvidence(candidate, policy, scene, metric, runs)
			id, err := domain.NewID()
			if err != nil {
				continue
			}
			results = append(results, versioninggate.Result{ID: id, Descriptor: descriptor, State: state, Evidence: evidence, ResultHash: resultHash(state, evidence), Context: versioninggate.EvaluationContext{Candidate: candidate, PolicyHash: policy.CanonicalHash, SceneID: scene.ID, MetricID: metric.ID, ImplementationVersions: versions}})
		}
	}
	return results
}

func findExactEvidence(candidate versioningrevision.CandidateContext, policy versioningpolicy.ReleasePolicy, scene versioningpolicy.Scene, metric versioningpolicy.Metric, runs []RunEvidence) (versioninggate.ResultState, []versioninggate.Evidence) {
	sawCandidateRun := false
	for _, run := range runs {
		if !run.ID.Valid() || run.RevisionID != candidate.RevisionID || run.Input.RevisionID != contract.ID(candidate.RevisionID) || run.Input.ConfigHash != candidate.ConfigHash || run.Input.ManifestHash != candidate.ManifestHash {
			continue
		}
		sawCandidateRun = true
		if !run.Reproducible {
			return versioninggate.Unavailable, nil
		}
		if !exactRunIdentity(run) || run.Input.SceneID != scene.ID || (scene.Version != "" && run.Input.SceneVersion != scene.Version) || run.Input.SampleCount != policy.Samples || (scene.Seed != nil && run.Input.Seed != *scene.Seed) || !containsMetric(run.Input.Metrics, metric.ID, "v1") {
			continue
		}
		for _, actual := range run.Metrics {
			if actual.ID == metric.ID && actual.Version == "v1" {
				if actual.Status != "available" {
					return versioninggate.Unavailable, nil
				}
				return versioninggate.Pass, []versioninggate.Evidence{{ID: string(run.ID), Hash: run.ResultHash, URL: "/api/v1/simulation-runs/" + string(run.ID)}}
			}
		}
	}
	if sawCandidateRun {
		return versioninggate.Stale, nil
	}
	return versioninggate.Unavailable, nil
}

func exactRunIdentity(run RunEvidence) bool {
	if !simulationHash(run.Input.RuleMaterializationHash) || !simulationHash(run.InputHash) || !simulationHash(run.FingerprintHash) || !simulationHash(run.ResultHash) {
		return false
	}
	inputHash, err := run.Input.Hash()
	return err == nil && inputHash == run.InputHash
}

func containsMetric(metrics []contract.MetricIdentity, id, version string) bool {
	for _, metric := range metrics {
		if metric.ID == id && metric.Version == version {
			return true
		}
	}
	return false
}

func simulationHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

func resultHash(state versioninggate.ResultState, evidence []versioninggate.Evidence) string {
	if len(evidence) == 1 {
		return evidence[0].Hash
	}
	// A fixed valid digest represents a deliberately non-passing result. This
	// contains no mutable error text and therefore cannot accidentally identify
	// a current implementation as historical evidence.
	const blocked = "0000000000000000000000000000000000000000000000000000000000000000"
	return blocked
}

func sortMetricEvidence(metrics []MetricEvidence) []MetricEvidence {
	copy := append([]MetricEvidence(nil), metrics...)
	sort.Slice(copy, func(i, j int) bool { return copy[i].ID+"\x00"+copy[i].Version < copy[j].ID+"\x00"+copy[j].Version })
	return copy
}

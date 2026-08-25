// Package acceptability is the single server-authoritative policy for deciding
// whether a sealed AI proposal has complete deterministic preview evidence.
package acceptability

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aipreview "github.com/zouyi/eco-guardian/internal/ai/preview"
	airisk "github.com/zouyi/eco-guardian/internal/ai/preview/risk"
	aisearch "github.com/zouyi/eco-guardian/internal/ai/preview/search"
	aisimulation "github.com/zouyi/eco-guardian/internal/ai/preview/simulation"
	aivalidation "github.com/zouyi/eco-guardian/internal/ai/preview/validation"
	"github.com/zouyi/eco-guardian/internal/domain"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
	riskpreview "github.com/zouyi/eco-guardian/internal/risk/preview"
	"github.com/zouyi/eco-guardian/internal/simulation/metric"
)

const VersionV1 = "ai-proposal-acceptability-v1"

var ErrInvalid = errors.New("AI proposal acceptability input is invalid")

type ReasonCode string

const (
	ValidationUnavailable ReasonCode = "VALIDATION_UNAVAILABLE"
	ValidationBlocked     ReasonCode = "VALIDATION_BLOCKED"
	SearchUnavailable     ReasonCode = "SEARCH_UNAVAILABLE"
	SearchBudgetExhausted ReasonCode = "SEARCH_BUDGET_EXHAUSTED"
	SearchNoFeasible      ReasonCode = "SEARCH_NO_FEASIBLE_CANDIDATE"
	SimulationUnavailable ReasonCode = "SIMULATION_UNAVAILABLE"
	RequiredSceneMissing  ReasonCode = "REQUIRED_SCENE_MISSING"
	RequiredMetricMissing ReasonCode = "REQUIRED_METRIC_MISSING"
	MetricUnavailable     ReasonCode = "METRIC_UNAVAILABLE"
	RiskUnavailable       ReasonCode = "RISK_UNAVAILABLE"
	RiskBlocked           ReasonCode = "RISK_BLOCKED"
	PreviewInconsistent   ReasonCode = "PREVIEW_INCONSISTENT"
)

type Reason struct {
	Code      ReasonCode `json:"code"`
	Component string     `json:"component"`
	Ref       string     `json:"ref,omitempty"`
}

type Evidence struct {
	ValidationHash string `json:"validation_hash,omitempty"`
	SearchHash     string `json:"search_hash,omitempty"`
	SimulationHash string `json:"simulation_hash,omitempty"`
	RiskHash       string `json:"risk_hash,omitempty"`
}

type Request struct {
	Input          aicontract.AIDesignInputV1
	Proposal       aipreview.ProposalMaterializationV1
	Validation     *aivalidation.ResultV1
	SearchRequired bool
	Search         *aisearch.ResultV1
	Simulation     *aisimulation.ResultV1
	Risk           *airisk.ResultV1
}

type ResultV1 struct {
	Version      string          `json:"version"`
	ProposalHash aicontract.Hash `json:"proposal_materialization_hash"`
	InputHash    aicontract.Hash `json:"input_hash"`
	Acceptable   bool            `json:"acceptable"`
	Reasons      []Reason        `json:"reasons"`
	Evidence     Evidence        `json:"evidence"`
	Canonical    []byte          `json:"-"`
	Hash         string          `json:"hash"`
}

func EvaluateV1(request Request) (ResultV1, error) {
	if !request.Input.Valid() || !request.Proposal.Valid() || request.Input.Base != request.Proposal.Base {
		return ResultV1{}, ErrInvalid
	}
	inputHash, err := aicontract.HashAIDesignInputV1(request.Input)
	if err != nil {
		return ResultV1{}, ErrInvalid
	}
	reasons := make([]Reason, 0)
	add := func(code ReasonCode, component, ref string) {
		reasons = append(reasons, Reason{Code: code, Component: component, Ref: ref})
	}
	evidence := Evidence{}
	if request.Validation == nil {
		add(ValidationUnavailable, "validation", "")
	} else if !request.Validation.Valid() || request.Validation.MaterializationHash != string(request.Proposal.Hash) {
		add(PreviewInconsistent, "validation", "")
	} else {
		evidence.ValidationHash = request.Validation.Hash
		if request.Validation.Summary.Block > 0 || request.Validation.Summary.Error > 0 {
			add(ValidationBlocked, "validation", request.Validation.Hash)
		}
	}
	if request.SearchRequired {
		if request.Search == nil {
			add(SearchUnavailable, "search", "")
		} else if !request.Search.Valid() || request.Search.MaterializationHash != request.Proposal.Hash {
			add(PreviewInconsistent, "search", "")
		} else {
			evidence.SearchHash = request.Search.ResultHash
			switch request.Search.StopReason {
			case aisearch.StopCandidateBudget, aisearch.StopTimeBudget:
				add(SearchBudgetExhausted, "search", request.Search.ResultHash)
			case aisearch.StopCompleted:
				if !hasFeasibleCandidate(*request.Search) {
					add(SearchNoFeasible, "search", request.Search.ResultHash)
				}
			default:
				add(SearchUnavailable, "search", request.Search.ResultHash)
			}
		}
	}
	if request.Simulation == nil {
		add(SimulationUnavailable, "simulation", "")
	} else if !request.Simulation.Valid() || request.Simulation.ProposalHash != request.Proposal.Hash {
		add(PreviewInconsistent, "simulation", "")
	} else {
		evidence.SimulationHash = request.Simulation.Hash
		checkSimulation(request.Input, *request.Simulation, add)
	}
	if request.Risk == nil {
		add(RiskUnavailable, "risk", "")
	} else if !request.Risk.Valid() || request.Risk.ProposalHash != request.Proposal.Hash {
		add(PreviewInconsistent, "risk", "")
	} else {
		evidence.RiskHash = request.Risk.Hash
		checkRequiredRiskMetrics(request.Input, *request.Risk, add)
		derivedAcceptable := derivedRiskAcceptable(request.Risk.Risk)
		if derivedAcceptable != request.Risk.Risk.Acceptable {
			add(PreviewInconsistent, "risk", request.Risk.Hash)
		}
		if !derivedAcceptable {
			add(RiskBlocked, "risk", request.Risk.Hash)
		}
		if request.Simulation != nil && request.Simulation.Valid() {
			checkRiskSimulationConsistency(*request.Simulation, *request.Risk, add)
		}
	}
	reasons = normalizeReasons(reasons)
	result := ResultV1{Version: VersionV1, ProposalHash: request.Proposal.Hash, InputHash: inputHash, Acceptable: len(reasons) == 0, Reasons: reasons, Evidence: evidence}
	result.Canonical, result.Hash, err = canonicalResult(result)
	if err != nil || !result.Valid() {
		return ResultV1{}, ErrInvalid
	}
	return result, nil
}

func checkRequiredRiskMetrics(input aicontract.AIDesignInputV1, risk airisk.ResultV1, add func(ReasonCode, string, string)) {
	for _, required := range input.Metrics {
		found := false
		for _, result := range risk.Risk.Metrics {
			if result.Candidate.MetricID == required.MetricID && result.Candidate.MetricVersion == required.Version {
				found = true
				if result.Candidate.Status == riskcontract.MetricUnavailable {
					add(MetricUnavailable, "risk", result.SceneID+"/"+required.MetricID+"@"+required.Version)
				}
			}
		}
		if !found {
			add(RequiredMetricMissing, "risk", required.MetricID+"@"+required.Version)
		}
	}
}

func (result ResultV1) Valid() bool {
	if result.Version != VersionV1 || !result.ProposalHash.Valid() || !result.InputHash.Valid() || !validHash(result.Hash) || result.Acceptable != (len(result.Reasons) == 0) || !equalReasons(result.Reasons, normalizeReasons(result.Reasons)) {
		return false
	}
	for _, reason := range result.Reasons {
		if !validReason(reason) {
			return false
		}
	}
	for _, hash := range []string{result.Evidence.ValidationHash, result.Evidence.SearchHash, result.Evidence.SimulationHash, result.Evidence.RiskHash} {
		if hash != "" && !validHash(hash) {
			return false
		}
	}
	canonical, hash, err := canonicalResult(result)
	return err == nil && hash == result.Hash && bytes.Equal(canonical, result.Canonical)
}

func checkSimulation(input aicontract.AIDesignInputV1, simulation aisimulation.ResultV1, add func(ReasonCode, string, string)) {
	byScene := make(map[string][]metric.CanonicalMetric, len(simulation.Scenes))
	for _, scene := range simulation.Scenes {
		byScene[scene.SceneID] = append(byScene[scene.SceneID], scene.Preview.Result.Metrics...)
		for _, value := range scene.Preview.Result.Metrics {
			if value.Status == metric.Unavailable {
				add(MetricUnavailable, "simulation", scene.SceneID+"/"+value.ID+"@"+value.Version)
			}
		}
	}
	for _, sceneID := range input.Scenes {
		metrics, found := byScene[sceneID]
		if !found {
			add(RequiredSceneMissing, "simulation", sceneID)
			continue
		}
		for _, required := range input.Metrics {
			foundMetric := false
			for _, value := range metrics {
				if value.ID == required.MetricID && value.Version == required.Version {
					foundMetric = true
					break
				}
			}
			if !foundMetric {
				add(RequiredMetricMissing, "simulation", sceneID+"/"+required.MetricID+"@"+required.Version)
			}
		}
	}
}

func checkRiskSimulationConsistency(simulation aisimulation.ResultV1, risk airisk.ResultV1, add func(ReasonCode, string, string)) {
	type identity struct{ input, fingerprint, result string }
	byScene := make(map[string]identity, len(simulation.Scenes))
	for _, scene := range simulation.Scenes {
		byScene[scene.SceneID+"\x00"+scene.SceneVersion] = identity{scene.Preview.InputHash, scene.Preview.FingerprintHash, scene.Preview.ResultHash}
	}
	for _, comparison := range risk.Risk.Metrics {
		expected, found := byScene[comparison.SceneID+"\x00"+comparison.SceneVersion]
		if !found || expected.input != comparison.CandidateEvidence.InputHash || expected.fingerprint != comparison.CandidateEvidence.FingerprintHash || expected.result != comparison.CandidateEvidence.ResultHash {
			add(PreviewInconsistent, "risk_simulation", comparison.ID)
		}
	}
}

func derivedRiskAcceptable(value riskpreview.ResultV1) bool {
	acceptable := true
	for _, result := range value.Metrics {
		if result.Candidate.Status == riskcontract.MetricUnavailable || result.Assessment.PolicyEffect == nil || *result.Assessment.PolicyEffect == riskcontract.Block {
			acceptable = false
		}
	}
	for _, result := range value.Structural {
		if !result.Finding.Valid() || result.Finding.Status != riskcontract.Comparable || result.Finding.Severity == riskcontract.Block {
			acceptable = false
		}
	}
	return acceptable
}

func hasFeasibleCandidate(result aisearch.ResultV1) bool {
	for _, record := range result.Records {
		if record.Evaluation == nil || record.Evaluation.Status != aisearch.EvaluationPassed {
			continue
		}
		feasible := true
		for _, constraint := range record.Evaluation.Constraints {
			if !constraint.Satisfied {
				feasible = false
				break
			}
		}
		if feasible {
			return true
		}
	}
	return false
}

func normalizeReasons(values []Reason) []Reason {
	out := append([]Reason(nil), values...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		if out[i].Component != out[j].Component {
			return out[i].Component < out[j].Component
		}
		return out[i].Ref < out[j].Ref
	})
	write := 0
	for _, reason := range out {
		if write > 0 && out[write-1] == reason {
			continue
		}
		out[write] = reason
		write++
	}
	return out[:write]
}

func equalReasons(left, right []Reason) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validReason(reason Reason) bool {
	known := false
	for _, code := range []ReasonCode{ValidationUnavailable, ValidationBlocked, SearchUnavailable, SearchBudgetExhausted, SearchNoFeasible, SimulationUnavailable, RequiredSceneMissing, RequiredMetricMissing, MetricUnavailable, RiskUnavailable, RiskBlocked, PreviewInconsistent} {
		if reason.Code == code {
			known = true
			break
		}
	}
	return known && stableText(reason.Component) && (reason.Ref == "" || stableText(reason.Ref))
}

func stableText(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= 512 && !strings.ContainsAny(value, "\x00\r\n")
}

func canonicalResult(result ResultV1) ([]byte, string, error) {
	payload := struct {
		Version      string          `json:"version"`
		ProposalHash aicontract.Hash `json:"proposal_materialization_hash"`
		InputHash    aicontract.Hash `json:"input_hash"`
		Acceptable   bool            `json:"acceptable"`
		Reasons      []Reason        `json:"reasons"`
		Evidence     Evidence        `json:"evidence"`
	}{result.Version, result.ProposalHash, result.InputHash, result.Acceptable, result.Reasons, result.Evidence}
	canonical, err := domain.CanonicalJSON(payload)
	if err != nil {
		return nil, "", err
	}
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("eco-guardian.ai-proposal-acceptability/v1"))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(canonical)
	return canonical, hex.EncodeToString(hasher.Sum(nil)), nil
}

func validHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

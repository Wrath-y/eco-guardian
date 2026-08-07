package gate

import (
	"errors"
	"maps"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

var (
	ErrDescriptorInvalid   = errors.New("invalid gate descriptor")
	ErrDescriptorDuplicate = errors.New("duplicate gate descriptor")
	ErrDescriptorNotFound  = errors.New("gate descriptor not found")
)

type ResultState string

const (
	Pass        ResultState = "PASS"
	Warning     ResultState = "WARNING"
	Block       ResultState = "BLOCK"
	Unavailable ResultState = "UNAVAILABLE"
	Stale       ResultState = "STALE"
)

func (s ResultState) Valid() bool {
	return s == Pass || s == Warning || s == Block || s == Unavailable || s == Stale
}

type Descriptor struct {
	CapabilityID            string        `json:"capability_id"`
	GateID                  string        `json:"gate_id"`
	ContractVersion         string        `json:"contract_version"`
	ImplementationVersion   string        `json:"implementation_version"`
	RequiredInputs          []string      `json:"required_inputs"`
	SupportedStates         []ResultState `json:"supported_states"`
	OverridableNumericBlock bool          `json:"overridable_numeric_block"`
}

func (d Descriptor) Valid() bool {
	if strings.TrimSpace(d.CapabilityID) == "" || strings.TrimSpace(d.GateID) == "" || strings.TrimSpace(d.ContractVersion) == "" || strings.TrimSpace(d.ImplementationVersion) == "" || len(d.RequiredInputs) == 0 || len(d.SupportedStates) == 0 {
		return false
	}
	inputs := map[string]bool{}
	for _, input := range d.RequiredInputs {
		if strings.TrimSpace(input) == "" || inputs[input] {
			return false
		}
		inputs[input] = true
	}
	states := map[ResultState]bool{}
	for _, state := range d.SupportedStates {
		if !state.Valid() || states[state] {
			return false
		}
		states[state] = true
	}
	return true
}

type Evidence struct {
	ID   string `json:"id"`
	Hash string `json:"hash"`
	URL  string `json:"url,omitempty"`
}

func (e Evidence) Valid() bool { return strings.TrimSpace(e.ID) != "" && len(e.Hash) == 64 }

// EvaluationContext binds an immutable Gate result to the exact candidate and
// policy inputs that produced it. Scene/Metric/threshold fields are optional
// for gates (such as validation) that do not consume them.
type EvaluationContext struct {
	Candidate              versioningrevision.CandidateContext `json:"candidate"`
	PolicyHash             string                              `json:"policy_hash"`
	SceneID                string                              `json:"scene_id,omitempty"`
	MetricID               string                              `json:"metric_id,omitempty"`
	ThresholdID            string                              `json:"threshold_id,omitempty"`
	ImplementationVersions map[string]string                   `json:"implementation_versions"`
}

func (c EvaluationContext) Valid() bool {
	if !c.Candidate.Valid() || !validHash(c.PolicyHash) || len(c.ImplementationVersions) == 0 || (c.SceneID == "") != (c.MetricID == "") {
		return false
	}
	for capabilityID, version := range c.ImplementationVersions {
		if strings.TrimSpace(capabilityID) == "" || strings.TrimSpace(version) == "" {
			return false
		}
	}
	return true
}

func validHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}

type Result struct {
	ID         domain.ID         `json:"id"`
	Descriptor Descriptor        `json:"descriptor"`
	State      ResultState       `json:"state"`
	Evidence   []Evidence        `json:"evidence"`
	ResultHash string            `json:"result_hash"`
	Context    EvaluationContext `json:"context"`
}

func (r Result) Valid() bool {
	if !r.ID.Valid() || !r.Descriptor.Valid() || !r.State.Valid() || !validHash(r.ResultHash) || !r.Context.Valid() {
		return false
	}
	foundState := false
	for _, state := range r.Descriptor.SupportedStates {
		foundState = foundState || state == r.State
	}
	if !foundState {
		return false
	}
	for _, e := range r.Evidence {
		if !e.Valid() {
			return false
		}
	}
	return true
}

// EvaluateCandidate marks a result stale unless it binds every candidate and
// policy input exactly. It never upgrades a result state.
func EvaluateCandidate(candidate versioningrevision.CandidateContext, policy versioningpolicy.ReleasePolicy, expected EvaluationContext, result Result) ResultState {
	if !candidate.Valid() || !policy.Valid() || !expected.Valid() || !result.Valid() {
		return Stale
	}
	if expected.Candidate != candidate || expected.PolicyHash != policy.CanonicalHash || expected.Candidate.PolicyID != policy.ID || result.Context.Candidate != expected.Candidate || result.Context.PolicyHash != expected.PolicyHash || result.Context.SceneID != expected.SceneID || result.Context.MetricID != expected.MetricID || result.Context.ThresholdID != expected.ThresholdID || !maps.Equal(result.Context.ImplementationVersions, expected.ImplementationVersions) {
		return Stale
	}
	if result.Context.ImplementationVersions[result.Descriptor.CapabilityID] != result.Descriptor.ImplementationVersion {
		return Stale
	}
	return result.State
}

func cloneContext(context EvaluationContext) EvaluationContext {
	copy := context
	copy.ImplementationVersions = maps.Clone(context.ImplementationVersions)
	return copy
}

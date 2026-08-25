package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
)

var ErrPolicyDenied = errors.New("AI tool policy denied the request")

type PolicyCode string

const (
	PolicyInvalidScope       PolicyCode = "TOOL_POLICY_INVALID_SCOPE"
	PolicyUnknownTool        PolicyCode = "TOOL_POLICY_UNKNOWN_TOOL"
	PolicyAttemptMismatch    PolicyCode = "TOOL_POLICY_ATTEMPT_MISMATCH"
	PolicyStageMismatch      PolicyCode = "TOOL_POLICY_STAGE_MISMATCH"
	PolicyBaseMismatch       PolicyCode = "TOOL_POLICY_BASE_MISMATCH"
	PolicyInputMismatch      PolicyCode = "TOOL_POLICY_INPUT_MISMATCH"
	PolicyTargetDenied       PolicyCode = "TOOL_POLICY_TARGET_DENIED"
	PolicyPathDenied         PolicyCode = "TOOL_POLICY_PATH_DENIED"
	PolicyOperationDenied    PolicyCode = "TOOL_POLICY_OPERATION_DENIED"
	PolicyCallBudgetExceeded PolicyCode = "TOOL_POLICY_CALL_BUDGET_EXCEEDED"
	PolicySearchBudget       PolicyCode = "TOOL_POLICY_SEARCH_BUDGET_EXCEEDED"
	PolicyTimeBudget         PolicyCode = "TOOL_POLICY_TIME_BUDGET_EXCEEDED"
	PolicyOutputInvalid      PolicyCode = "TOOL_POLICY_OUTPUT_INVALID"
	PolicyOutputTooLarge     PolicyCode = "TOOL_POLICY_OUTPUT_TOO_LARGE"
	PolicyResultMismatch     PolicyCode = "TOOL_POLICY_RESULT_MISMATCH"
)

type PolicyError struct{ Code PolicyCode }

func (e *PolicyError) Error() string { return fmt.Sprintf("%s: %v", e.Code, ErrPolicyDenied) }
func (e *PolicyError) Unwrap() error { return ErrPolicyDenied }

type Scope struct {
	AttemptID      aicontract.AttemptID
	InputHash      aicontract.Hash
	Base           aicontract.FrozenBaseIdentity
	Stage          aicontract.AttemptStage
	AllowedTargets []aicontract.AllowedTarget
	Limits         aicontract.BudgetLimits
	StartedAt      time.Time
}

func (v Scope) Valid() bool {
	if !v.AttemptID.Valid() || !v.InputHash.Valid() || !v.Base.Valid() || !v.Stage.Valid() || !v.Limits.Valid() || v.StartedAt.IsZero() || len(v.AllowedTargets) == 0 {
		return false
	}
	seen := map[aicontract.EntityID]struct{}{}
	for _, target := range v.AllowedTargets {
		if !target.Valid() {
			return false
		}
		if _, duplicate := seen[target.EntityID]; duplicate {
			return false
		}
		seen[target.EntityID] = struct{}{}
	}
	return v.Limits.MaxToolCalls <= aicontract.V1MaxToolCalls &&
		v.Limits.MaxSearchCandidates <= aicontract.V1MaxSearchCandidates &&
		v.Limits.MaxDurationMillis <= aicontract.V1MaxDurationMillis &&
		v.Limits.MaxToolResultBytes <= aicontract.V1MaxToolResultBytes
}

type TargetAccess struct {
	EntityID  aicontract.EntityID
	Path      aicontract.FieldPath
	Operation aicontract.PatchOperationKind
}

func (v TargetAccess) Valid() bool {
	return v.EntityID.Valid() && v.Path.Valid() && v.Operation.Valid()
}

type Invocation struct {
	CallID           aicontract.ToolCallID
	AttemptID        aicontract.AttemptID
	InputHash        aicontract.Hash
	Tool             aicontract.VersionIdentity
	Stage            aicontract.AttemptStage
	Base             aicontract.FrozenBaseIdentity
	Targets          []TargetAccess
	SearchCandidates int
}

type Permit struct {
	CallID         aicontract.ToolCallID
	Tool           aicontract.VersionIdentity
	Deadline       time.Time
	maxResultBytes int
	guard          *PolicyGuard

	mu       sync.Mutex
	finished bool
}

// PolicyGuard is an attempt-scoped, transport-neutral execution boundary. It
// grants permits only for the built-in v1 read/evaluate allowlist. Possession
// of arbitrary HTTP, SQL, file, process, repository, revision, release, graph
// activation, or credential adapters is deliberately outside this API.
type PolicyGuard struct {
	mu sync.Mutex

	scope       Scope
	registry    *aicontract.AIToolRegistry
	totalCalls  int
	toolCalls   map[string]int
	searchCount int
}

func NewPolicyGuard(scope Scope) (*PolicyGuard, error) {
	if !scope.Valid() {
		return nil, deny(PolicyInvalidScope)
	}
	registries, err := aicontract.NewV1RegistrySet()
	if err != nil {
		return nil, deny(PolicyInvalidScope)
	}
	scope.AllowedTargets = cloneAllowedTargets(scope.AllowedTargets)
	return &PolicyGuard{scope: scope, registry: registries.Tools, toolCalls: map[string]int{}}, nil
}

// Authorize validates and accounts for a call before any implementation is
// executed. Budget counters are consumed atomically when a permit is granted.
func (g *PolicyGuard) Authorize(now time.Time, call Invocation) (*Permit, error) {
	if g == nil || now.IsZero() {
		return nil, deny(PolicyInvalidScope)
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	manifest, ok := g.registry.Resolve(call.Tool.ID, call.Tool.Version)
	if !ok || manifest.Identity != call.Tool {
		return nil, deny(PolicyUnknownTool)
	}
	if call.Tool.ID == "retrieve_evidence" && g.scope.Stage != aicontract.StageInputPinned ||
		call.Tool.ID != "retrieve_evidence" && g.scope.Stage != aicontract.StageProviderToolLoop {
		return nil, deny(PolicyStageMismatch)
	}
	if !call.CallID.Valid() || call.AttemptID != g.scope.AttemptID {
		return nil, deny(PolicyAttemptMismatch)
	}
	if call.Stage != g.scope.Stage {
		return nil, deny(PolicyStageMismatch)
	}
	if call.Base != g.scope.Base {
		return nil, deny(PolicyBaseMismatch)
	}
	if call.InputHash != g.scope.InputHash {
		return nil, deny(PolicyInputMismatch)
	}
	if call.Tool.ID != "retrieve_evidence" && len(call.Targets) == 0 {
		return nil, deny(PolicyTargetDenied)
	}
	if err := g.validateTargets(call.Targets); err != nil {
		return nil, err
	}
	if call.SearchCandidates < 0 || call.Tool.ID != "search_parameters" && call.SearchCandidates != 0 {
		return nil, deny(PolicySearchBudget)
	}
	if g.totalCalls >= g.scope.Limits.MaxToolCalls || g.toolCalls[call.Tool.ID] >= manifest.MaxCalls {
		return nil, deny(PolicyCallBudgetExceeded)
	}
	if g.searchCount+call.SearchCandidates > g.scope.Limits.MaxSearchCandidates {
		return nil, deny(PolicySearchBudget)
	}
	attemptDeadline := g.scope.StartedAt.Add(time.Duration(g.scope.Limits.MaxDurationMillis) * time.Millisecond)
	if !now.Before(attemptDeadline) {
		return nil, deny(PolicyTimeBudget)
	}
	toolDeadline := now.Add(time.Duration(manifest.TimeoutMillis) * time.Millisecond)
	if attemptDeadline.Before(toolDeadline) {
		toolDeadline = attemptDeadline
	}
	g.totalCalls++
	g.toolCalls[call.Tool.ID]++
	g.searchCount += call.SearchCandidates
	maxResultBytes := manifest.MaxResultBytes
	if g.scope.Limits.MaxToolResultBytes < maxResultBytes {
		maxResultBytes = g.scope.Limits.MaxToolResultBytes
	}
	return &Permit{CallID: call.CallID, Tool: manifest.Identity, Deadline: toolDeadline, maxResultBytes: maxResultBytes, guard: g}, nil
}

// ValidateResult closes a permit exactly once and enforces the registered
// output envelope before it can be exposed to a model or audit sink.
func (p *Permit) ValidateResult(now time.Time, callID aicontract.ToolCallID, payload json.RawMessage) error {
	if p == nil || p.guard == nil || now.IsZero() {
		return deny(PolicyResultMismatch)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished || callID != p.CallID {
		return deny(PolicyResultMismatch)
	}
	p.finished = true
	if !now.Before(p.Deadline) {
		return deny(PolicyTimeBudget)
	}
	if len(payload) > p.maxResultBytes {
		return deny(PolicyOutputTooLarge)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil || object == nil {
		return deny(PolicyOutputInvalid)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return deny(PolicyOutputInvalid)
	}
	return nil
}

func (g *PolicyGuard) validateTargets(targets []TargetAccess) error {
	seen := map[string]struct{}{}
	for _, requested := range targets {
		if !requested.Valid() {
			return deny(PolicyTargetDenied)
		}
		key := string(requested.EntityID) + "\x00" + string(requested.Path) + "\x00" + string(requested.Operation)
		if _, duplicate := seen[key]; duplicate {
			return deny(PolicyTargetDenied)
		}
		seen[key] = struct{}{}
		var target *aicontract.AllowedTarget
		for index := range g.scope.AllowedTargets {
			if g.scope.AllowedTargets[index].EntityID == requested.EntityID {
				target = &g.scope.AllowedTargets[index]
				break
			}
		}
		if target == nil {
			return deny(PolicyTargetDenied)
		}
		var path *aicontract.AllowedPath
		for index := range target.Paths {
			if target.Paths[index].Path == requested.Path {
				path = &target.Paths[index]
				break
			}
		}
		if path == nil {
			return deny(PolicyPathDenied)
		}
		allowed := false
		for _, operation := range path.Operations {
			if operation == requested.Operation {
				allowed = true
				break
			}
		}
		if !allowed {
			return deny(PolicyOperationDenied)
		}
	}
	return nil
}

func cloneAllowedTargets(values []aicontract.AllowedTarget) []aicontract.AllowedTarget {
	result := append([]aicontract.AllowedTarget(nil), values...)
	for targetIndex := range result {
		result[targetIndex].Paths = append([]aicontract.AllowedPath(nil), result[targetIndex].Paths...)
		for pathIndex := range result[targetIndex].Paths {
			result[targetIndex].Paths[pathIndex].Operations = append([]aicontract.PatchOperationKind(nil), result[targetIndex].Paths[pathIndex].Operations...)
		}
	}
	return result
}

func deny(code PolicyCode) error { return &PolicyError{Code: code} }

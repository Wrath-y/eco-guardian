package contract

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/zouyi/eco-guardian/internal/formula"
)

type ProviderState string

const (
	ProviderUnconfigured ProviderState = "unconfigured"
	ProviderAvailable    ProviderState = "available"
	ProviderDegraded     ProviderState = "degraded"
	ProviderUnavailable  ProviderState = "unavailable"
)

func (v ProviderState) Valid() bool {
	return v == ProviderUnconfigured || v == ProviderAvailable || v == ProviderDegraded || v == ProviderUnavailable
}

type CapabilityState string

const (
	CapabilityUnconfigured CapabilityState = "unconfigured"
	CapabilityAvailable    CapabilityState = "available"
	CapabilityDegraded     CapabilityState = "degraded"
	CapabilityUnavailable  CapabilityState = "unavailable"
)

func (v CapabilityState) Valid() bool {
	return v == CapabilityUnconfigured || v == CapabilityAvailable || v == CapabilityDegraded || v == CapabilityUnavailable
}

type EndpointClassification string

const (
	EndpointLoopback EndpointClassification = "loopback"
	EndpointCloud    EndpointClassification = "cloud"
)

func (v EndpointClassification) Valid() bool { return v == EndpointLoopback || v == EndpointCloud }

type BaselineKind string

const (
	BaselineCurrent BaselineKind = "BASELINE"
	BaselineNone    BaselineKind = "NO_BASELINE"
)

func (v BaselineKind) Valid() bool { return v == BaselineCurrent || v == BaselineNone }

type MetricDirection string

const (
	MetricMinimize MetricDirection = "minimize"
	MetricMaximize MetricDirection = "maximize"
	MetricTarget   MetricDirection = "target"
)

func (v MetricDirection) Valid() bool {
	return v == MetricMinimize || v == MetricMaximize || v == MetricTarget
}

type ConstraintOperator string

const (
	ConstraintEqual        ConstraintOperator = "equal"
	ConstraintNotEqual     ConstraintOperator = "not_equal"
	ConstraintLess         ConstraintOperator = "less"
	ConstraintLessOrEqual  ConstraintOperator = "less_or_equal"
	ConstraintGreater      ConstraintOperator = "greater"
	ConstraintGreaterEqual ConstraintOperator = "greater_or_equal"
	ConstraintIn           ConstraintOperator = "in"
	ConstraintRange        ConstraintOperator = "range"
)

func (v ConstraintOperator) Valid() bool {
	switch v {
	case ConstraintEqual, ConstraintNotEqual, ConstraintLess, ConstraintLessOrEqual, ConstraintGreater, ConstraintGreaterEqual, ConstraintIn, ConstraintRange:
		return true
	default:
		return false
	}
}

type PatchOperationKind string

const (
	OperationReplace PatchOperationKind = "replace"
	OperationAdd     PatchOperationKind = "add"
	OperationRemove  PatchOperationKind = "remove"
)

func (v PatchOperationKind) Valid() bool {
	return v == OperationReplace || v == OperationAdd || v == OperationRemove
}

type EvidenceKind string

const (
	EvidenceRetrieval  EvidenceKind = "retrieval"
	EvidenceValidation EvidenceKind = "validation"
	EvidenceSimulation EvidenceKind = "simulation"
	EvidenceRisk       EvidenceKind = "risk"
	EvidenceSearch     EvidenceKind = "search"
)

func (v EvidenceKind) Valid() bool {
	return v == EvidenceRetrieval || v == EvidenceValidation || v == EvidenceSimulation || v == EvidenceRisk || v == EvidenceSearch
}

type AttemptStage string

const (
	StageInputPinned          AttemptStage = "input_pinned"
	StageEvidencePinned       AttemptStage = "evidence_pinned"
	StageProviderToolLoop     AttemptStage = "provider_tool_loop"
	StageDeterministicPreview AttemptStage = "deterministic_preview"
	StagePatchSealed          AttemptStage = "patch_sealed"
)

func (v AttemptStage) Valid() bool {
	return v == StageInputPinned || v == StageEvidencePinned || v == StageProviderToolLoop || v == StageDeterministicPreview || v == StagePatchSealed
}

func (v AttemptStage) Order() int {
	switch v {
	case StageInputPinned:
		return 0
	case StageEvidencePinned:
		return 1
	case StageProviderToolLoop:
		return 2
	case StageDeterministicPreview:
		return 3
	case StagePatchSealed:
		return 4
	default:
		return -1
	}
}

type AttemptOutcome string

const (
	OutcomeRunning           AttemptOutcome = "running"
	OutcomeSucceeded         AttemptOutcome = "succeeded"
	OutcomeFailed            AttemptOutcome = "failed"
	OutcomeCanceled          AttemptOutcome = "canceled"
	OutcomeInterrupted       AttemptOutcome = "interrupted"
	OutcomeIgnoredLateResult AttemptOutcome = "ignored_late_result"
)

func (v AttemptOutcome) Valid() bool {
	return v == OutcomeRunning || v == OutcomeSucceeded || v == OutcomeFailed || v == OutcomeCanceled || v == OutcomeInterrupted || v == OutcomeIgnoredLateResult
}

type FreshnessState string

const (
	FreshnessFresh FreshnessState = "fresh"
	FreshnessStale FreshnessState = "stale"
)

func (v FreshnessState) Valid() bool { return v == FreshnessFresh || v == FreshnessStale }

type HumanDecisionKind string

const (
	DecisionAccepted  HumanDecisionKind = "accepted"
	DecisionDiscarded HumanDecisionKind = "discarded"
)

func (v HumanDecisionKind) Valid() bool { return v == DecisionAccepted || v == DecisionDiscarded }

type ProjectID string
type RevisionID string
type ReleaseID string
type EntityID string
type EvidenceID string
type AttemptID string
type ToolCallID string
type PatchID string
type DecisionID string
type Hash string
type FieldPath string

func (v ProjectID) Valid() bool  { return validToken(string(v)) }
func (v RevisionID) Valid() bool { return validToken(string(v)) }
func (v ReleaseID) Valid() bool  { return validToken(string(v)) }
func (v EntityID) Valid() bool   { return validToken(string(v)) }
func (v EvidenceID) Valid() bool { return validToken(string(v)) }
func (v AttemptID) Valid() bool  { return validToken(string(v)) }
func (v ToolCallID) Valid() bool { return validToken(string(v)) }
func (v PatchID) Valid() bool    { return validToken(string(v)) }
func (v DecisionID) Valid() bool { return validToken(string(v)) }
func (v Hash) Valid() bool       { return validHash(string(v)) }
func (v FieldPath) Valid() bool  { return validJSONPointer(string(v)) }

type VersionIdentity struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Hash    Hash   `json:"hash"`
}

func (v VersionIdentity) Valid() bool {
	return validToken(v.ID) && validToken(v.Version) && v.Hash.Valid()
}

type FrozenBaseIdentity struct {
	ProjectID           ProjectID  `json:"project_id"`
	ConfigRevisionID    RevisionID `json:"config_revision_id"`
	ConfigHash          Hash       `json:"config_hash"`
	VersionManifestHash Hash       `json:"version_manifest_hash"`
	MaterializationHash Hash       `json:"materialization_hash"`
	GraphNamespace      string     `json:"graph_namespace"`
	GraphSnapshot       string     `json:"graph_snapshot"`
	GraphContentHash    Hash       `json:"graph_content_hash"`
}

func (v FrozenBaseIdentity) Valid() bool {
	return v.ProjectID.Valid() && v.ConfigRevisionID.Valid() && v.ConfigHash.Valid() &&
		v.VersionManifestHash.Valid() && v.MaterializationHash.Valid() && validToken(v.GraphNamespace) &&
		v.GraphSnapshot == string(v.ConfigRevisionID) && v.GraphContentHash.Valid()
}

type BaselineIdentity struct {
	Kind             BaselineKind `json:"kind"`
	ReleaseID        ReleaseID    `json:"release_id,omitempty"`
	ConfigRevisionID RevisionID   `json:"config_revision_id,omitempty"`
	ConfigHash       Hash         `json:"config_hash,omitempty"`
}

func (v BaselineIdentity) Valid() bool {
	if v.Kind == BaselineNone {
		return v.ReleaseID == "" && v.ConfigRevisionID == "" && v.ConfigHash == ""
	}
	return v.Kind == BaselineCurrent && v.ReleaseID.Valid() && v.ConfigRevisionID.Valid() && v.ConfigHash.Valid()
}

type Goal struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

func (v Goal) Valid() bool { return validToken(v.ID) && validText(v.Description) }

type MetricGoal struct {
	MetricID  string          `json:"metric_id"`
	Version   string          `json:"version"`
	Direction MetricDirection `json:"direction"`
	Target    string          `json:"target,omitempty"`
	Unit      string          `json:"unit"`
}

func (v MetricGoal) Valid() bool {
	if !validToken(v.MetricID) || !validToken(v.Version) || !v.Direction.Valid() || !validToken(v.Unit) {
		return false
	}
	if v.Direction != MetricTarget {
		return v.Target == ""
	}
	return canonicalDecimal(v.Target)
}

type Constraint struct {
	ID       string             `json:"id"`
	Path     FieldPath          `json:"path"`
	Operator ConstraintOperator `json:"operator"`
	Value    json.RawMessage    `json:"value"`
}

func (v Constraint) Valid() bool {
	return validToken(v.ID) && v.Path.Valid() && v.Operator.Valid() && validJSONValue(v.Value)
}

type AllowedPath struct {
	Path       FieldPath            `json:"path"`
	Operations []PatchOperationKind `json:"operations"`
}

func (v AllowedPath) Valid() bool {
	if !v.Path.Valid() || len(v.Operations) == 0 {
		return false
	}
	seen := map[PatchOperationKind]struct{}{}
	for _, operation := range v.Operations {
		if !operation.Valid() {
			return false
		}
		if _, duplicate := seen[operation]; duplicate {
			return false
		}
		seen[operation] = struct{}{}
	}
	return true
}

type AllowedTarget struct {
	EntityID              EntityID      `json:"entity_id"`
	Kind                  string        `json:"kind"`
	ExpectedEntityVersion int64         `json:"expected_entity_version"`
	Paths                 []AllowedPath `json:"paths"`
}

func (v AllowedTarget) Valid() bool {
	if !v.EntityID.Valid() || !validToken(v.Kind) || v.ExpectedEntityVersion < 1 || len(v.Paths) == 0 {
		return false
	}
	seen := map[FieldPath]struct{}{}
	for _, allowed := range v.Paths {
		if !allowed.Valid() {
			return false
		}
		if _, duplicate := seen[allowed.Path]; duplicate {
			return false
		}
		seen[allowed.Path] = struct{}{}
	}
	return true
}

type Budget struct {
	Policy VersionIdentity `json:"policy"`
	BudgetLimits
}

type BudgetLimits struct {
	MaxFormatRepairs     int   `json:"max_format_repairs"`
	MaxProviderTurns     int   `json:"max_provider_turns"`
	MaxToolCalls         int   `json:"max_tool_calls"`
	MaxSearchCandidates  int   `json:"max_search_candidates"`
	MaxDurationMillis    int64 `json:"max_duration_millis"`
	MaxContextBytes      int   `json:"max_context_bytes"`
	MaxOutputBytes       int   `json:"max_output_bytes"`
	MaxToolResultBytes   int   `json:"max_tool_result_bytes"`
	RetrievalSeedLimit   int   `json:"retrieval_seed_limit"`
	RetrievalResultLimit int   `json:"retrieval_result_limit"`
	RetrievalGraphDepth  int   `json:"retrieval_graph_depth"`
}

func (v Budget) Valid() bool {
	return v.Policy.Valid() && v.BudgetLimits.Valid()
}

func (v BudgetLimits) Valid() bool {
	return v.MaxFormatRepairs > 0 && v.MaxProviderTurns > 0 && v.MaxToolCalls > 0 &&
		v.MaxSearchCandidates > 0 && v.MaxDurationMillis > 0 && v.MaxContextBytes > 0 && v.MaxOutputBytes > 0 &&
		v.MaxToolResultBytes > 0 && v.RetrievalSeedLimit > 0 && v.RetrievalSeedLimit <= 100 &&
		v.RetrievalResultLimit > 0 && v.RetrievalResultLimit <= 100 && v.RetrievalGraphDepth >= 0 && v.RetrievalGraphDepth <= 3
}

type AIDesignInputV1 struct {
	Schema           VersionIdentity    `json:"schema"`
	Base             FrozenBaseIdentity `json:"base"`
	Baseline         BaselineIdentity   `json:"baseline"`
	Goals            []Goal             `json:"goals"`
	Metrics          []MetricGoal       `json:"metrics"`
	Constraints      []Constraint       `json:"constraints"`
	AllowedTargets   []AllowedTarget    `json:"allowed_targets"`
	Scenes           []string           `json:"scenes"`
	Budget           Budget             `json:"budget"`
	RequiredVersions []VersionIdentity  `json:"required_versions"`
}

func (v AIDesignInputV1) Valid() bool {
	if !v.Schema.Valid() || !v.Base.Valid() || !v.Baseline.Valid() || len(v.Goals) == 0 || len(v.AllowedTargets) == 0 || len(v.Scenes) == 0 || !v.Budget.Valid() || len(v.RequiredVersions) == 0 {
		return false
	}
	if !validUnique(v.Goals, func(value Goal) (string, bool) { return value.ID, value.Valid() }) ||
		!validUnique(v.Metrics, func(value MetricGoal) (string, bool) { return value.MetricID, value.Valid() }) ||
		!validUnique(v.Constraints, func(value Constraint) (string, bool) { return value.ID, value.Valid() }) ||
		!validUnique(v.AllowedTargets, func(value AllowedTarget) (string, bool) { return string(value.EntityID), value.Valid() }) ||
		!validUnique(v.RequiredVersions, func(value VersionIdentity) (string, bool) { return value.ID, value.Valid() }) {
		return false
	}
	seenScenes := map[string]struct{}{}
	for _, scene := range v.Scenes {
		if !validToken(scene) {
			return false
		}
		if _, duplicate := seenScenes[scene]; duplicate {
			return false
		}
		seenScenes[scene] = struct{}{}
	}
	return true
}

type EvidenceRef struct {
	ID           EvidenceID   `json:"id"`
	Kind         EvidenceKind `json:"kind"`
	ManifestHash Hash         `json:"manifest_hash"`
}

func (v EvidenceRef) Valid() bool { return v.ID.Valid() && v.Kind.Valid() && v.ManifestHash.Valid() }

type Attempt struct {
	ID               AttemptID       `json:"id"`
	Ordinal          int             `json:"ordinal"`
	ParentAttemptID  AttemptID       `json:"parent_attempt_id,omitempty"`
	Stage            AttemptStage    `json:"stage"`
	Outcome          AttemptOutcome  `json:"outcome"`
	ManifestIdentity VersionIdentity `json:"manifest_identity"`
}

func (v Attempt) Valid() bool {
	return v.ID.Valid() && v.Ordinal >= 1 && (v.ParentAttemptID == "" || v.ParentAttemptID.Valid()) &&
		v.ParentAttemptID != v.ID && v.Stage.Valid() && v.Outcome.Valid() && v.ManifestIdentity.Valid()
}

type ToolCall struct {
	ID        ToolCallID         `json:"id"`
	AttemptID AttemptID          `json:"attempt_id"`
	Ordinal   int                `json:"ordinal"`
	Tool      VersionIdentity    `json:"tool"`
	Base      FrozenBaseIdentity `json:"base"`
	InputHash Hash               `json:"input_hash"`
}

func (v ToolCall) Valid() bool {
	return v.ID.Valid() && v.AttemptID.Valid() && v.Ordinal >= 1 && v.Tool.Valid() && v.Base.Valid() && v.InputHash.Valid()
}

type ToolResult struct {
	CallID                ToolCallID    `json:"call_id"`
	ImplementationVersion string        `json:"implementation_version"`
	ResultHash            Hash          `json:"result_hash"`
	Evidence              []EvidenceRef `json:"evidence"`
}

func (v ToolResult) Valid() bool {
	if !v.CallID.Valid() || !validToken(v.ImplementationVersion) || !v.ResultHash.Valid() {
		return false
	}
	return validEvidenceRefs(v.Evidence, false)
}

type DraftOperation struct {
	Ordinal  int                `json:"ordinal"`
	Kind     PatchOperationKind `json:"kind"`
	Path     FieldPath          `json:"path"`
	Value    json.RawMessage    `json:"value"`
	Evidence []EvidenceID       `json:"evidence"`
}

func (v DraftOperation) Valid() bool {
	if v.Ordinal < 1 || !v.Kind.Valid() || !v.Path.Valid() || !validJSONValue(v.Value) || len(v.Evidence) == 0 {
		return false
	}
	seen := map[EvidenceID]struct{}{}
	for _, evidence := range v.Evidence {
		if !evidence.Valid() {
			return false
		}
		if _, duplicate := seen[evidence]; duplicate {
			return false
		}
		seen[evidence] = struct{}{}
	}
	return true
}

type DraftTarget struct {
	EntityID              EntityID         `json:"entity_id"`
	Kind                  string           `json:"kind"`
	ExpectedEntityVersion int64            `json:"expected_entity_version"`
	Operations            []DraftOperation `json:"operations"`
}

func (v DraftTarget) Valid() bool {
	if !v.EntityID.Valid() || !validToken(v.Kind) || v.ExpectedEntityVersion < 1 || len(v.Operations) == 0 {
		return false
	}
	ordinals := map[int]struct{}{}
	for _, operation := range v.Operations {
		if !operation.Valid() {
			return false
		}
		if _, duplicate := ordinals[operation.Ordinal]; duplicate {
			return false
		}
		ordinals[operation.Ordinal] = struct{}{}
	}
	return true
}

type DraftPatchV1 struct {
	ID                       PatchID            `json:"id"`
	Schema                   VersionIdentity    `json:"schema"`
	Base                     FrozenBaseIdentity `json:"base"`
	EvidenceManifestIdentity VersionIdentity    `json:"evidence_manifest_identity"`
	Targets                  []DraftTarget      `json:"targets"`
	Rationale                string             `json:"rationale"`
	Assumptions              []string           `json:"assumptions"`
	Hash                     Hash               `json:"hash"`
}

func (v DraftPatchV1) Valid() bool {
	if !v.ID.Valid() || !v.Schema.Valid() || !v.Base.Valid() || !v.EvidenceManifestIdentity.Valid() || len(v.Targets) == 0 || !validText(v.Rationale) || len(v.Assumptions) == 0 || !v.Hash.Valid() {
		return false
	}
	targets := map[EntityID]struct{}{}
	for _, target := range v.Targets {
		if !target.Valid() {
			return false
		}
		if _, duplicate := targets[target.EntityID]; duplicate {
			return false
		}
		targets[target.EntityID] = struct{}{}
	}
	for _, assumption := range v.Assumptions {
		if !validText(assumption) {
			return false
		}
	}
	return true
}

type Preview struct {
	Advisory   bool              `json:"advisory"`
	InputHash  Hash              `json:"input_hash"`
	ResultHash Hash              `json:"result_hash"`
	Evaluators []VersionIdentity `json:"evaluators"`
	Evidence   []EvidenceRef     `json:"evidence"`
	Acceptable bool              `json:"acceptable"`
}

func (v Preview) Valid() bool {
	if !v.Advisory || !v.InputHash.Valid() || !v.ResultHash.Valid() || len(v.Evaluators) == 0 {
		return false
	}
	for _, evaluator := range v.Evaluators {
		if !evaluator.Valid() {
			return false
		}
	}
	return validEvidenceRefs(v.Evidence, false)
}

type Freshness struct {
	State             FreshnessState `json:"state"`
	ConflictingTarget []EntityID     `json:"conflicting_targets"`
}

func (v Freshness) Valid() bool {
	if !v.State.Valid() || (v.State == FreshnessFresh && len(v.ConflictingTarget) != 0) || (v.State == FreshnessStale && len(v.ConflictingTarget) == 0) {
		return false
	}
	seen := map[EntityID]struct{}{}
	for _, id := range v.ConflictingTarget {
		if !id.Valid() {
			return false
		}
		if _, duplicate := seen[id]; duplicate {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

type HumanDecision struct {
	ID                 DecisionID        `json:"id"`
	Kind               HumanDecisionKind `json:"kind"`
	Actor              string            `json:"actor"`
	RequestHash        Hash              `json:"request_hash"`
	ResultHash         Hash              `json:"result_hash"`
	AcceptedRevisionID RevisionID        `json:"accepted_revision_id,omitempty"`
}

func (v HumanDecision) Valid() bool {
	if !v.ID.Valid() || !v.Kind.Valid() || !validToken(v.Actor) || !v.RequestHash.Valid() || !v.ResultHash.Valid() {
		return false
	}
	return (v.Kind == DecisionAccepted && v.AcceptedRevisionID.Valid()) || (v.Kind == DecisionDiscarded && v.AcceptedRevisionID == "")
}

func validEvidenceRefs(values []EvidenceRef, required bool) bool {
	if required && len(values) == 0 {
		return false
	}
	seen := map[EvidenceID]struct{}{}
	for _, value := range values {
		if !value.Valid() {
			return false
		}
		if _, duplicate := seen[value.ID]; duplicate {
			return false
		}
		seen[value.ID] = struct{}{}
	}
	return true
}

func validUnique[T any](values []T, identity func(T) (string, bool)) bool {
	seen := map[string]struct{}{}
	for _, value := range values {
		id, valid := identity(value)
		if !valid {
			return false
		}
		if _, duplicate := seen[id]; duplicate {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

func validHash(value string) bool {
	if len(value) != sha256HexLength || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256ByteLength
}

func validToken(value string) bool {
	return validText(value) && !strings.ContainsAny(value, "\r\n\t")
}

func validText(value string) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != ""
}

func validJSONPointer(value string) bool {
	if value == "" || value[0] != '/' || !utf8.ValidString(value) || strings.ContainsAny(value, "\r\n\t") {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] != '~' {
			continue
		}
		if index+1 >= len(value) || (value[index+1] != '0' && value[index+1] != '1') {
			return false
		}
		index++
	}
	return true
}

func validJSONValue(value json.RawMessage) bool {
	return len(bytes.TrimSpace(value)) != 0 && json.Valid(value)
}

func canonicalDecimal(value string) bool {
	decimal, err := formula.ParseDecimal(value)
	return err == nil && decimal.String() == value
}

const (
	sha256ByteLength = 32
	sha256HexLength  = sha256ByteLength * 2
)

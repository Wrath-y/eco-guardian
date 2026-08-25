package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/zouyi/eco-guardian/internal/formula"
)

const (
	domainAIDesignInput     = "eco-guardian.ai-design-input/v1"
	domainProviderManifest  = "eco-guardian.ai-provider-manifest/v1"
	domainPromptManifest    = "eco-guardian.ai-prompt-manifest/v1"
	domainSchemaManifest    = "eco-guardian.ai-schema-manifest/v1"
	domainToolManifest      = "eco-guardian.ai-tool-manifest/v1"
	domainOrchestrator      = "eco-guardian.ai-orchestrator-manifest/v1"
	domainBudgetPolicy      = "eco-guardian.ai-budget-policy-manifest/v1"
	domainRetrievalEvidence = "eco-guardian.ai-retrieval-evidence/v1"
	domainToolInput         = "eco-guardian.ai-tool-input/v1"
	domainToolResult        = "eco-guardian.ai-tool-result/v1"
	domainDraftPatch        = "eco-guardian.ai-draft-patch/v1"
	domainDraftDiff         = "eco-guardian.ai-draft-diff/v1"
	domainPreview           = "eco-guardian.ai-preview/v1"
	domainAuditRecord       = "eco-guardian.ai-audit-record/v1"
	domainAuditChain        = "eco-guardian.ai-audit-chain/v1"
)

var errInvalidCanonicalValue = errors.New("invalid value for canonical AI contract")

type ProviderManifest struct {
	Identity               VersionIdentity        `json:"identity"`
	ProviderID             string                 `json:"provider_id"`
	EndpointClassification EndpointClassification `json:"endpoint_classification"`
	Model                  string                 `json:"model"`
	Capabilities           []string               `json:"capabilities"`
	Parameters             json.RawMessage        `json:"parameters"`
}

func (v ProviderManifest) Valid() bool {
	return v.Identity.Valid() && validToken(v.ProviderID) && v.EndpointClassification.Valid() && validToken(v.Model) &&
		validStringSet(v.Capabilities, true) && validJSONValue(v.Parameters)
}

type PromptManifest struct {
	Identity VersionIdentity `json:"identity"`
	Template string          `json:"template"`
}

func (v PromptManifest) Valid() bool { return v.Identity.Valid() && validText(v.Template) }

type SchemaManifest struct {
	Identity VersionIdentity `json:"identity"`
	Schema   json.RawMessage `json:"schema"`
}

func (v SchemaManifest) Valid() bool { return v.Identity.Valid() && validJSONValue(v.Schema) }

type ToolManifest struct {
	Identity             VersionIdentity `json:"identity"`
	InputSchemaHash      Hash            `json:"input_schema_hash"`
	ResultSchemaHash     Hash            `json:"result_schema_hash"`
	RequiredIdentities   []string        `json:"required_identities"`
	Deterministic        bool            `json:"deterministic"`
	SideEffect           string          `json:"side_effect"`
	MaxCalls             int             `json:"max_calls"`
	MaxResultBytes       int             `json:"max_result_bytes"`
	TimeoutMillis        int64           `json:"timeout_millis"`
	CancellationBehavior string          `json:"cancellation_behavior"`
}

func (v ToolManifest) Valid() bool {
	return v.Identity.Valid() && v.InputSchemaHash.Valid() && v.ResultSchemaHash.Valid() &&
		validStringSet(v.RequiredIdentities, false) && (v.SideEffect == "none" || v.SideEffect == "external_read_only") &&
		v.MaxCalls > 0 && v.MaxResultBytes > 0 && v.TimeoutMillis > 0 && validToken(v.CancellationBehavior)
}

type OrchestratorManifest struct {
	Identity VersionIdentity `json:"identity"`
	Stages   []AttemptStage  `json:"stages"`
}

func (v OrchestratorManifest) Valid() bool {
	if !v.Identity.Valid() || len(v.Stages) != 5 {
		return false
	}
	for index, stage := range v.Stages {
		if !stage.Valid() || stage.Order() != index {
			return false
		}
	}
	return true
}

type BudgetPolicyManifest struct {
	Identity VersionIdentity `json:"identity"`
	Limits   BudgetLimits    `json:"limits"`
}

func (v BudgetPolicyManifest) Valid() bool { return v.Identity.Valid() && v.Limits.Valid() }

type RetrievalEvidenceManifest struct {
	Identity     VersionIdentity    `json:"identity"`
	Base         FrozenBaseIdentity `json:"base"`
	RequestHash  Hash               `json:"request_hash"`
	ResponseHash Hash               `json:"response_hash"`
	Mode         string             `json:"mode"`
	Degraded     bool               `json:"degraded"`
	Evidence     []EvidenceRef      `json:"evidence"`
}

func (v RetrievalEvidenceManifest) Valid() bool {
	return v.Identity.Valid() && v.Base.Valid() && v.RequestHash.Valid() && v.ResponseHash.Valid() &&
		(v.Mode == "hybrid" || v.Mode == "bm25_only" || v.Mode == "vector_only") && validEvidenceRefs(v.Evidence, true)
}

type ToolInputEnvelope struct {
	Call    ToolCall        `json:"call"`
	Payload json.RawMessage `json:"payload"`
}

func (v ToolInputEnvelope) Valid() bool { return v.Call.Valid() && validJSONValue(v.Payload) }

type ToolResultEnvelope struct {
	Result  ToolResult      `json:"result"`
	Payload json.RawMessage `json:"payload"`
}

func (v ToolResultEnvelope) Valid() bool { return v.Result.Valid() && validJSONValue(v.Payload) }

type DraftDiffChange struct {
	EntityID  EntityID           `json:"entity_id"`
	Path      FieldPath          `json:"path"`
	Ordinal   int                `json:"ordinal"`
	Kind      PatchOperationKind `json:"kind"`
	Original  json.RawMessage    `json:"original"`
	Canonical json.RawMessage    `json:"canonical"`
}

func (v DraftDiffChange) Valid() bool {
	return v.EntityID.Valid() && v.Path.Valid() && v.Ordinal > 0 && v.Kind.Valid() && validJSONValue(v.Original) && validJSONValue(v.Canonical)
}

type DraftDiff struct {
	PatchHash Hash              `json:"patch_hash"`
	Changes   []DraftDiffChange `json:"changes"`
}

func (v DraftDiff) Valid() bool {
	if !v.PatchHash.Valid() || len(v.Changes) == 0 {
		return false
	}
	for _, change := range v.Changes {
		if !change.Valid() {
			return false
		}
	}
	return true
}

type AuditRecord struct {
	Ordinal     int               `json:"ordinal"`
	AttemptID   AttemptID         `json:"attempt_id"`
	EventType   string            `json:"event_type"`
	PayloadHash Hash              `json:"payload_hash"`
	Versions    []VersionIdentity `json:"versions"`
}

func (v AuditRecord) Valid() bool {
	if v.Ordinal < 1 || !v.AttemptID.Valid() || !validToken(v.EventType) || !v.PayloadHash.Valid() {
		return false
	}
	return validUnique(v.Versions, func(value VersionIdentity) (string, bool) { return value.ID, value.Valid() })
}

type AuditChainEntry struct {
	PreviousHash Hash        `json:"previous_hash,omitempty"`
	Record       AuditRecord `json:"record"`
	ChainHash    Hash        `json:"chain_hash"`
}

func CanonicalAIDesignInputV1(value AIDesignInputV1) ([]byte, error) {
	if !value.Valid() {
		return nil, errInvalidCanonicalValue
	}
	value.Goals = sortedCopy(value.Goals, func(a, b Goal) bool { return a.ID < b.ID })
	value.Metrics = sortedCopy(value.Metrics, func(a, b MetricGoal) bool {
		return a.MetricID < b.MetricID || (a.MetricID == b.MetricID && a.Version < b.Version)
	})
	value.Constraints = sortedCopy(value.Constraints, func(a, b Constraint) bool { return a.ID < b.ID })
	value.AllowedTargets = sortedCopy(value.AllowedTargets, func(a, b AllowedTarget) bool { return a.EntityID < b.EntityID })
	for index := range value.AllowedTargets {
		value.AllowedTargets[index].Paths = sortedCopy(value.AllowedTargets[index].Paths, func(a, b AllowedPath) bool { return a.Path < b.Path })
		for pathIndex := range value.AllowedTargets[index].Paths {
			operations := append([]PatchOperationKind(nil), value.AllowedTargets[index].Paths[pathIndex].Operations...)
			sort.Slice(operations, func(i, j int) bool {
				return operations[i] < operations[j]
			})
			value.AllowedTargets[index].Paths[pathIndex].Operations = operations
		}
	}
	value.Scenes = sortedStrings(value.Scenes)
	value.RequiredVersions = sortedVersions(value.RequiredVersions)
	return canonicalJSON(value)
}

func HashAIDesignInputV1(value AIDesignInputV1) (Hash, error) {
	encoded, err := CanonicalAIDesignInputV1(value)
	return hashEncoded(domainAIDesignInput, encoded, err)
}

func CanonicalProviderManifest(value ProviderManifest) ([]byte, error) {
	if !value.Valid() {
		return nil, errInvalidCanonicalValue
	}
	value.Capabilities = sortedStrings(value.Capabilities)
	return canonicalJSON(value)
}

func HashProviderManifest(value ProviderManifest) (Hash, error) {
	encoded, err := CanonicalProviderManifest(value)
	return hashManifest(domainProviderManifest, encoded, err)
}

func CanonicalPromptManifest(value PromptManifest) ([]byte, error) {
	if !value.Valid() {
		return nil, errInvalidCanonicalValue
	}
	return canonicalJSON(value)
}

func HashPromptManifest(value PromptManifest) (Hash, error) {
	encoded, err := CanonicalPromptManifest(value)
	return hashManifest(domainPromptManifest, encoded, err)
}

func CanonicalSchemaManifest(value SchemaManifest) ([]byte, error) {
	if !value.Valid() {
		return nil, errInvalidCanonicalValue
	}
	return canonicalJSON(value)
}

func HashSchemaManifest(value SchemaManifest) (Hash, error) {
	encoded, err := CanonicalSchemaManifest(value)
	return hashManifest(domainSchemaManifest, encoded, err)
}

func CanonicalToolManifest(value ToolManifest) ([]byte, error) {
	if !value.Valid() {
		return nil, errInvalidCanonicalValue
	}
	value.RequiredIdentities = sortedStrings(value.RequiredIdentities)
	return canonicalJSON(value)
}

func HashToolManifest(value ToolManifest) (Hash, error) {
	encoded, err := CanonicalToolManifest(value)
	return hashManifest(domainToolManifest, encoded, err)
}

func CanonicalOrchestratorManifest(value OrchestratorManifest) ([]byte, error) {
	if !value.Valid() {
		return nil, errInvalidCanonicalValue
	}
	return canonicalJSON(value)
}

func HashOrchestratorManifest(value OrchestratorManifest) (Hash, error) {
	encoded, err := CanonicalOrchestratorManifest(value)
	return hashManifest(domainOrchestrator, encoded, err)
}

func CanonicalBudgetPolicyManifest(value BudgetPolicyManifest) ([]byte, error) {
	if !value.Valid() {
		return nil, errInvalidCanonicalValue
	}
	return canonicalJSON(value)
}

func HashBudgetPolicyManifest(value BudgetPolicyManifest) (Hash, error) {
	encoded, err := CanonicalBudgetPolicyManifest(value)
	return hashManifest(domainBudgetPolicy, encoded, err)
}

func CanonicalRetrievalEvidenceManifest(value RetrievalEvidenceManifest) ([]byte, error) {
	if !value.Valid() {
		return nil, errInvalidCanonicalValue
	}
	value.Evidence = sortedEvidence(value.Evidence)
	return canonicalJSON(value)
}

func HashRetrievalEvidenceManifest(value RetrievalEvidenceManifest) (Hash, error) {
	encoded, err := CanonicalRetrievalEvidenceManifest(value)
	return hashManifest(domainRetrievalEvidence, encoded, err)
}

func CanonicalToolInput(value ToolInputEnvelope) ([]byte, error) {
	if !value.Valid() {
		return nil, errInvalidCanonicalValue
	}
	return canonicalJSON(value)
}

func HashToolInput(value ToolInputEnvelope) (Hash, error) {
	encoded, err := CanonicalToolInput(value)
	return hashEncoded(domainToolInput, encoded, err)
}

func CanonicalToolResult(value ToolResultEnvelope) ([]byte, error) {
	if !value.Valid() {
		return nil, errInvalidCanonicalValue
	}
	value.Result.Evidence = sortedEvidence(value.Result.Evidence)
	return canonicalJSON(value)
}

func HashToolResult(value ToolResultEnvelope) (Hash, error) {
	encoded, err := CanonicalToolResult(value)
	return hashEncoded(domainToolResult, encoded, err)
}

func CanonicalDraftPatch(value DraftPatchV1) ([]byte, error) {
	if !value.Valid() {
		return nil, errInvalidCanonicalValue
	}
	value.Targets = sortedCopy(value.Targets, func(a, b DraftTarget) bool { return a.EntityID < b.EntityID })
	for targetIndex := range value.Targets {
		value.Targets[targetIndex].Operations = sortedCopy(value.Targets[targetIndex].Operations, func(a, b DraftOperation) bool {
			if a.Path != b.Path {
				return a.Path < b.Path
			}
			return a.Ordinal < b.Ordinal
		})
		for operationIndex := range value.Targets[targetIndex].Operations {
			evidence := append([]EvidenceID(nil), value.Targets[targetIndex].Operations[operationIndex].Evidence...)
			sort.Slice(evidence, func(i, j int) bool {
				return evidence[i] < evidence[j]
			})
			value.Targets[targetIndex].Operations[operationIndex].Evidence = evidence
		}
	}
	return canonicalJSON(value)
}

func HashDraftPatch(value DraftPatchV1) (Hash, error) {
	encoded, err := CanonicalDraftPatch(value)
	return hashWithoutFields(domainDraftPatch, encoded, err, "id", "hash")
}

func CanonicalDraftDiff(value DraftDiff) ([]byte, error) {
	if !value.Valid() {
		return nil, errInvalidCanonicalValue
	}
	value.Changes = sortedCopy(value.Changes, func(a, b DraftDiffChange) bool {
		if a.EntityID != b.EntityID {
			return a.EntityID < b.EntityID
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Ordinal < b.Ordinal
	})
	return canonicalJSON(value)
}

func HashDraftDiff(value DraftDiff) (Hash, error) {
	encoded, err := CanonicalDraftDiff(value)
	return hashEncoded(domainDraftDiff, encoded, err)
}

func CanonicalPreview(value Preview) ([]byte, error) {
	if !value.Valid() {
		return nil, errInvalidCanonicalValue
	}
	value.Evaluators = sortedVersions(value.Evaluators)
	value.Evidence = sortedEvidence(value.Evidence)
	return canonicalJSON(value)
}

func HashPreview(value Preview) (Hash, error) {
	encoded, err := CanonicalPreview(value)
	return hashEncoded(domainPreview, encoded, err)
}

func CanonicalAuditRecord(value AuditRecord) ([]byte, error) {
	if !value.Valid() {
		return nil, errInvalidCanonicalValue
	}
	value.Versions = sortedVersions(value.Versions)
	return canonicalJSON(value)
}

func HashAuditRecord(value AuditRecord) (Hash, error) {
	encoded, err := CanonicalAuditRecord(value)
	return hashEncoded(domainAuditRecord, encoded, err)
}

func AppendAuditChain(previous Hash, record AuditRecord) (AuditChainEntry, error) {
	if previous != "" && !previous.Valid() {
		return AuditChainEntry{}, errInvalidCanonicalValue
	}
	canonical, err := CanonicalAuditRecord(record)
	if err != nil {
		return AuditChainEntry{}, err
	}
	hash, err := domainHash(domainAuditChain, struct {
		Previous Hash            `json:"previous_hash,omitempty"`
		Record   json.RawMessage `json:"record"`
	}{Previous: previous, Record: canonical})
	if err != nil {
		return AuditChainEntry{}, err
	}
	return AuditChainEntry{PreviousHash: previous, Record: record, ChainHash: hash}, nil
}

func hashEncoded(domain string, encoded []byte, err error) (Hash, error) {
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	hasher.Write([]byte(domain))
	hasher.Write([]byte{0})
	hasher.Write(encoded)
	return Hash(hex.EncodeToString(hasher.Sum(nil))), nil
}

func hashManifest(domain string, encoded []byte, err error) (Hash, error) {
	if err != nil {
		return "", err
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	identity, ok := value["identity"].(map[string]any)
	if !ok {
		return "", errInvalidCanonicalValue
	}
	delete(identity, "hash")
	canonical, err := canonicalJSON(value)
	return hashEncoded(domain, canonical, err)
}

func hashWithoutFields(domain string, encoded []byte, err error, fields ...string) (Hash, error) {
	if err != nil {
		return "", err
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	for _, field := range fields {
		delete(value, field)
	}
	canonical, err := canonicalJSON(value)
	return hashEncoded(domain, canonical, err)
}

func domainHash(domain string, value any) (Hash, error) {
	encoded, err := canonicalJSON(value)
	return hashEncoded(domain, encoded, err)
}

func canonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := writeCanonicalJSON(&output, decoded); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func writeCanonicalJSON(output *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case nil:
		output.WriteString("null")
	case bool:
		output.WriteString(strconv.FormatBool(typed))
	case string:
		encoded, _ := json.Marshal(typed)
		output.Write(encoded)
	case json.Number:
		decimal, err := formula.ParseDecimal(string(typed))
		if err != nil || decimal.String() != string(typed) {
			return fmt.Errorf("%w: non-canonical decimal %q", errInvalidCanonicalValue, typed)
		}
		output.WriteString(string(typed))
	case []any:
		output.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				output.WriteByte(',')
			}
			if err := writeCanonicalJSON(output, item); err != nil {
				return err
			}
		}
		output.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		output.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				output.WriteByte(',')
			}
			encoded, _ := json.Marshal(key)
			output.Write(encoded)
			output.WriteByte(':')
			if err := writeCanonicalJSON(output, typed[key]); err != nil {
				return err
			}
		}
		output.WriteByte('}')
	default:
		return fmt.Errorf("%w: unsupported JSON type %T", errInvalidCanonicalValue, value)
	}
	return nil
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func sortedVersions(values []VersionIdentity) []VersionIdentity {
	return sortedCopy(values, func(a, b VersionIdentity) bool {
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		if a.Version != b.Version {
			return a.Version < b.Version
		}
		return a.Hash < b.Hash
	})
}

func sortedEvidence(values []EvidenceRef) []EvidenceRef {
	return sortedCopy(values, func(a, b EvidenceRef) bool { return a.ID < b.ID })
}

func sortedCopy[T any](values []T, less func(T, T) bool) []T {
	result := append([]T(nil), values...)
	sort.Slice(result, func(i, j int) bool { return less(result[i], result[j]) })
	return result
}

func validStringSet(values []string, required bool) bool {
	if required && len(values) == 0 {
		return false
	}
	seen := map[string]struct{}{}
	for _, value := range values {
		if !validToken(value) {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

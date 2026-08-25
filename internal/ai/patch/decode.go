package patch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/google/uuid"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	"github.com/zouyi/eco-guardian/internal/domain"
)

type ErrorCode string

const (
	ErrorMalformedJSON       ErrorCode = "DRAFT_PATCH_MALFORMED_JSON"
	ErrorSchemaViolation     ErrorCode = "DRAFT_PATCH_SCHEMA_VIOLATION"
	ErrorIdentityMismatch    ErrorCode = "DRAFT_PATCH_IDENTITY_MISMATCH"
	ErrorScopeViolation      ErrorCode = "DRAFT_PATCH_SCOPE_VIOLATION"
	ErrorCollectionViolation ErrorCode = "DRAFT_PATCH_COLLECTION_VIOLATION"
	ErrorEvidenceViolation   ErrorCode = "DRAFT_PATCH_EVIDENCE_VIOLATION"
	ErrorTypeViolation       ErrorCode = "DRAFT_PATCH_TYPE_VIOLATION"
	ErrorNumericViolation    ErrorCode = "DRAFT_PATCH_NUMERIC_VIOLATION"
	ErrorUnitViolation       ErrorCode = "DRAFT_PATCH_UNIT_VIOLATION"
	ErrorArrayAmbiguous      ErrorCode = "DRAFT_PATCH_ARRAY_AMBIGUOUS"
)

var ErrInvalid = errors.New("DraftPatchV1 is invalid")

type DecodeError struct{ Code ErrorCode }

func (e *DecodeError) Error() string { return fmt.Sprintf("%s: %v", e.Code, ErrInvalid) }
func (e *DecodeError) Unwrap() error { return ErrInvalid }

type JSONType string

const (
	JSONString  JSONType = "string"
	JSONBoolean JSONType = "boolean"
	JSONInteger JSONType = "integer"
	JSONDecimal JSONType = "decimal"
	JSONObject  JSONType = "object"
	JSONArray   JSONType = "array"
)

func (v JSONType) Valid() bool {
	return v == JSONString || v == JSONBoolean || v == JSONInteger || v == JSONDecimal || v == JSONObject || v == JSONArray
}

type ValueScope struct {
	EntityID      aicontract.EntityID
	Path          aicontract.FieldPath
	ValueType     JSONType
	ElementType   JSONType
	Schema        json.RawMessage
	ElementSchema json.RawMessage
	Original      json.RawMessage
	UnitDimension string
	UnitValueType string
}

type DecodeContext struct {
	PatchID                  aicontract.PatchID
	Input                    aicontract.AIDesignInputV1
	EvidenceManifestIdentity aicontract.VersionIdentity
	EvidenceIDs              []aicontract.EvidenceID
	Values                   []ValueScope
	Registry                 *domain.Registry
}

func (v DecodeContext) Valid() bool {
	parsed, err := uuid.Parse(string(v.PatchID))
	if err != nil || parsed.Version() != 7 || !v.Input.Valid() || !v.EvidenceManifestIdentity.Valid() || len(v.EvidenceIDs) == 0 {
		return false
	}
	evidence := map[aicontract.EvidenceID]struct{}{}
	for _, id := range v.EvidenceIDs {
		if !id.Valid() {
			return false
		}
		if _, duplicate := evidence[id]; duplicate {
			return false
		}
		evidence[id] = struct{}{}
	}
	values := map[string]struct{}{}
	for _, value := range v.Values {
		if !value.EntityID.Valid() || !value.Path.Valid() || !value.ValueType.Valid() || (value.ElementType != "" && !value.ElementType.Valid()) || !allowedValue(v.Input.AllowedTargets, value) || len(value.Original) == 0 || !json.Valid(value.Original) {
			return false
		}
		if (value.ValueType == JSONArray) != (value.ElementType != "") {
			return false
		}
		key := string(value.EntityID) + "\x00" + string(value.Path)
		if _, duplicate := values[key]; duplicate {
			return false
		}
		values[key] = struct{}{}
	}
	want := 0
	for _, target := range v.Input.AllowedTargets {
		want += len(target.Paths)
	}
	return len(values) == want
}

type wirePatchV1 struct {
	ID                       aicontract.PatchID            `json:"id"`
	Schema                   aicontract.VersionIdentity    `json:"schema"`
	Base                     aicontract.FrozenBaseIdentity `json:"base"`
	EvidenceManifestIdentity aicontract.VersionIdentity    `json:"evidence_manifest_identity"`
	Targets                  []aicontract.DraftTarget      `json:"targets"`
	Rationale                string                        `json:"rationale"`
	Assumptions              []string                      `json:"assumptions"`
}

// DecodeV1 accepts exactly the registered DraftPatchV1 response shape. It
// binds all identities and operations to the frozen server context, computes
// the content hash, and returns both the normalized value and canonical bytes.
func DecodeV1(raw json.RawMessage, scope DecodeContext) (aicontract.DraftPatchV1, []byte, error) {
	if !scope.Valid() || len(raw) == 0 || len(raw) > scope.Input.Budget.MaxOutputBytes {
		return aicontract.DraftPatchV1{}, nil, decodeError(ErrorSchemaViolation)
	}
	if err := rejectDuplicateMembers(raw); err != nil {
		return aicontract.DraftPatchV1{}, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var wire wirePatchV1
	if err := decoder.Decode(&wire); err != nil {
		return aicontract.DraftPatchV1{}, nil, decodeError(ErrorSchemaViolation)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return aicontract.DraftPatchV1{}, nil, decodeError(ErrorMalformedJSON)
	}
	if wire.ID != scope.PatchID || wire.Schema != scope.Input.Schema || wire.Base != scope.Input.Base || wire.EvidenceManifestIdentity != scope.EvidenceManifestIdentity {
		return aicontract.DraftPatchV1{}, nil, decodeError(ErrorIdentityMismatch)
	}
	patch := aicontract.DraftPatchV1{
		ID: wire.ID, Schema: wire.Schema, Base: wire.Base, EvidenceManifestIdentity: wire.EvidenceManifestIdentity,
		Targets: wire.Targets, Rationale: wire.Rationale, Assumptions: wire.Assumptions,
		Hash: aicontract.Hash(strings.Repeat("0", 64)),
	}
	if err := validateScope(patch, scope); err != nil {
		return aicontract.DraftPatchV1{}, nil, err
	}
	patch = normalize(patch)
	hash, err := aicontract.HashDraftPatch(patch)
	if err != nil {
		return aicontract.DraftPatchV1{}, nil, decodeError(ErrorSchemaViolation)
	}
	patch.Hash = hash
	canonical, err := aicontract.CanonicalDraftPatch(patch)
	if err != nil {
		return aicontract.DraftPatchV1{}, nil, decodeError(ErrorSchemaViolation)
	}
	return patch, canonical, nil
}

func validateScope(value aicontract.DraftPatchV1, scope DecodeContext) error {
	if !value.Valid() || len(value.Targets) == 0 || len(value.Targets) > len(scope.Input.AllowedTargets) {
		return decodeError(ErrorSchemaViolation)
	}
	evidence := make(map[aicontract.EvidenceID]struct{}, len(scope.EvidenceIDs))
	for _, id := range scope.EvidenceIDs {
		evidence[id] = struct{}{}
	}
	values := make(map[string]ValueScope, len(scope.Values))
	for _, value := range scope.Values {
		values[string(value.EntityID)+"\x00"+string(value.Path)] = value
	}
	seenTargets := map[aicontract.EntityID]struct{}{}
	for _, target := range value.Targets {
		if _, duplicate := seenTargets[target.EntityID]; duplicate {
			return decodeError(ErrorScopeViolation)
		}
		seenTargets[target.EntityID] = struct{}{}
		allowed := findTarget(scope.Input.AllowedTargets, target.EntityID)
		if allowed == nil || target.Kind != allowed.Kind || target.ExpectedEntityVersion != allowed.ExpectedEntityVersion || len(target.Operations) == 0 {
			return decodeError(ErrorScopeViolation)
		}
		ordinals := make(map[int]struct{}, len(target.Operations))
		seenPaths := make(map[aicontract.FieldPath]struct{}, len(target.Operations))
		for _, operation := range target.Operations {
			if _, duplicate := ordinals[operation.Ordinal]; duplicate || operation.Ordinal > len(target.Operations) {
				return decodeError(ErrorSchemaViolation)
			}
			ordinals[operation.Ordinal] = struct{}{}
			if !allowedOperation(*allowed, operation.Path, operation.Kind) {
				return decodeError(ErrorScopeViolation)
			}
			if forbiddenMutationPath(operation.Path) {
				return decodeError(ErrorScopeViolation)
			}
			if _, duplicate := seenPaths[operation.Path]; duplicate {
				return decodeError(ErrorArrayAmbiguous)
			}
			seenPaths[operation.Path] = struct{}{}
			valueScope, found := values[string(target.EntityID)+"\x00"+string(operation.Path)]
			if !found {
				return decodeError(ErrorScopeViolation)
			}
			expectedType := valueScope.ValueType
			if operation.Kind != aicontract.OperationReplace {
				if valueScope.ValueType != JSONArray || valueScope.ElementType == "" {
					return decodeError(ErrorCollectionViolation)
				}
				expectedType = valueScope.ElementType
			}
			if !valueMatchesType(operation.Value, expectedType) {
				return decodeError(ErrorTypeViolation)
			}
			schema := valueScope.Schema
			if operation.Kind != aicontract.OperationReplace {
				schema = valueScope.ElementSchema
				if !unambiguousCollectionOperation(valueScope.Original, operation) {
					return decodeError(ErrorArrayAmbiguous)
				}
			}
			if len(schema) > 0 {
				if err := validateSchemaValue(operation.Value, schema, scope.Registry, valueScope); err != nil {
					return err
				}
			}
			for _, id := range operation.Evidence {
				if _, found := evidence[id]; !found {
					return decodeError(ErrorEvidenceViolation)
				}
			}
		}
		if len(ordinals) != len(target.Operations) {
			return decodeError(ErrorSchemaViolation)
		}
	}
	return nil
}

func normalize(value aicontract.DraftPatchV1) aicontract.DraftPatchV1 {
	value.Targets = append([]aicontract.DraftTarget(nil), value.Targets...)
	sort.Slice(value.Targets, func(i, j int) bool { return value.Targets[i].EntityID < value.Targets[j].EntityID })
	for targetIndex := range value.Targets {
		value.Targets[targetIndex].Operations = append([]aicontract.DraftOperation(nil), value.Targets[targetIndex].Operations...)
		sort.Slice(value.Targets[targetIndex].Operations, func(i, j int) bool {
			left, right := value.Targets[targetIndex].Operations[i], value.Targets[targetIndex].Operations[j]
			if left.Path != right.Path {
				return left.Path < right.Path
			}
			return left.Ordinal < right.Ordinal
		})
		for operationIndex := range value.Targets[targetIndex].Operations {
			operation := &value.Targets[targetIndex].Operations[operationIndex]
			operation.Value = append(json.RawMessage(nil), operation.Value...)
			operation.Evidence = append([]aicontract.EvidenceID(nil), operation.Evidence...)
			sort.Slice(operation.Evidence, func(i, j int) bool { return operation.Evidence[i] < operation.Evidence[j] })
		}
	}
	value.Assumptions = append([]string(nil), value.Assumptions...)
	return value
}

func findTarget(values []aicontract.AllowedTarget, id aicontract.EntityID) *aicontract.AllowedTarget {
	for index := range values {
		if values[index].EntityID == id {
			return &values[index]
		}
	}
	return nil
}

func allowedOperation(target aicontract.AllowedTarget, path aicontract.FieldPath, operation aicontract.PatchOperationKind) bool {
	for _, allowed := range target.Paths {
		if allowed.Path != path {
			continue
		}
		for _, candidate := range allowed.Operations {
			if candidate == operation {
				return true
			}
		}
	}
	return false
}

func allowedValue(targets []aicontract.AllowedTarget, value ValueScope) bool {
	target := findTarget(targets, value.EntityID)
	if target == nil {
		return false
	}
	for _, allowed := range target.Paths {
		if allowed.Path == value.Path {
			return true
		}
	}
	return false
}

func valueMatchesType(raw json.RawMessage, expected JSONType) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return false
	}
	switch expected {
	case JSONString:
		_, ok := value.(string)
		return ok
	case JSONBoolean:
		_, ok := value.(bool)
		return ok
	case JSONInteger:
		number, ok := value.(json.Number)
		return ok && !strings.ContainsAny(string(number), ".eE")
	case JSONDecimal:
		_, ok := value.(json.Number)
		return ok
	case JSONObject:
		_, ok := value.(map[string]any)
		return ok
	case JSONArray:
		_, ok := value.([]any)
		return ok
	default:
		return false
	}
}

func forbiddenMutationPath(path aicontract.FieldPath) bool {
	tokens, ok := pointerTokens(string(path))
	if !ok || len(tokens) == 0 {
		return true
	}
	switch strings.ToLower(tokens[0]) {
	case "id", "kind", "key", "status", "schema_version", "entity_version", "created_at", "updated_at", "extensions", "revision", "revisions", "release", "releases", "registry", "graph":
		return true
	default:
		return false
	}
}

func unambiguousCollectionOperation(original json.RawMessage, operation aicontract.DraftOperation) bool {
	var values []json.RawMessage
	if json.Unmarshal(original, &values) != nil {
		return false
	}
	canonicalValue, err := aicontract.CanonicalToolPayload(operation.Value)
	if err != nil {
		return false
	}
	matches := 0
	for _, candidate := range values {
		canonicalCandidate, err := aicontract.CanonicalToolPayload(candidate)
		if err == nil && bytes.Equal(canonicalCandidate, canonicalValue) {
			matches++
		}
	}
	if operation.Kind == aicontract.OperationAdd {
		return matches == 0
	}
	return operation.Kind == aicontract.OperationRemove && matches == 1
}

func rejectDuplicateMembers(raw json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF || token != nil {
		return decodeError(ErrorMalformedJSON)
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return decodeError(ErrorMalformedJSON)
	}
	delim, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return decodeError(ErrorMalformedJSON)
			}
			name, ok := nameToken.(string)
			if !ok {
				return decodeError(ErrorMalformedJSON)
			}
			if _, duplicate := seen[name]; duplicate {
				return decodeError(ErrorSchemaViolation)
			}
			seen[name] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return decodeError(ErrorMalformedJSON)
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return decodeError(ErrorMalformedJSON)
		}
	default:
		return decodeError(ErrorMalformedJSON)
	}
	return nil
}

func decodeError(code ErrorCode) error { return &DecodeError{Code: code} }

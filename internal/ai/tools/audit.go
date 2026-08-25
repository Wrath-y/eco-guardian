package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
)

var (
	ErrAuditEnvelopeInvalid  = errors.New("AI tool audit envelope is invalid")
	ErrAuditEnvelopeConflict = errors.New("AI tool audit envelope conflicts with recorded call")
)

type ToolUsage struct {
	InputBytes       int `json:"input_bytes"`
	SearchCandidates int `json:"search_candidates"`
	ResultBytes      int `json:"result_bytes"`
}

type CallAuditEnvelope struct {
	Ordinal             int                     `json:"ordinal"`
	Call                aicontract.ToolCall     `json:"call"`
	Payload             json.RawMessage         `json:"payload"`
	PayloadHash         aicontract.Hash         `json:"payload_hash"`
	InputEnvelopeHash   aicontract.Hash         `json:"input_envelope_hash"`
	StartedAtUnixMillis int64                   `json:"started_at_unix_millis"`
	Stage               aicontract.AttemptStage `json:"stage"`
	Targets             []TargetAccess          `json:"targets"`
	SearchCandidates    int                     `json:"search_candidates"`
}

type PolicyAuditError struct {
	Code PolicyCode `json:"code"`
}

type ResultAuditEnvelope struct {
	Ordinal               int                        `json:"ordinal"`
	CallID                aicontract.ToolCallID      `json:"call_id"`
	Tool                  aicontract.VersionIdentity `json:"tool"`
	Result                *aicontract.ToolResult     `json:"result,omitempty"`
	Payload               json.RawMessage            `json:"payload,omitempty"`
	ResultEnvelopeHash    aicontract.Hash            `json:"result_envelope_hash,omitempty"`
	PolicyError           *PolicyAuditError          `json:"policy_error,omitempty"`
	StartedAtUnixMillis   int64                      `json:"started_at_unix_millis"`
	CompletedAtUnixMillis int64                      `json:"completed_at_unix_millis"`
	DurationMillis        int64                      `json:"duration_millis"`
	Usage                 ToolUsage                  `json:"usage"`
}

type auditCallState struct {
	envelope CallAuditEnvelope
	closed   bool
}

// AuditRecorder creates stable call ordinals and closes every call with
// exactly one data result or stable policy error. Persistence remains behind
// later audit-store ports; this type owns only the immutable envelope logic.
type AuditRecorder struct {
	mu     sync.Mutex
	next   int
	byCall map[aicontract.ToolCallID]*auditCallState
}

func NewAuditRecorder() *AuditRecorder {
	return &AuditRecorder{next: 1, byCall: map[aicontract.ToolCallID]*auditCallState{}}
}

func (r *AuditRecorder) Begin(call Invocation, payload json.RawMessage, started time.Time) (CallAuditEnvelope, error) {
	if r == nil || started.IsZero() || !call.CallID.Valid() || !call.AttemptID.Valid() || !call.InputHash.Valid() || !call.Tool.Valid() || !call.Base.Valid() || !call.Stage.Valid() || call.SearchCandidates < 0 {
		return CallAuditEnvelope{}, ErrAuditEnvelopeInvalid
	}
	for _, target := range call.Targets {
		if !target.Valid() {
			return CallAuditEnvelope{}, ErrAuditEnvelopeInvalid
		}
	}
	canonical, err := canonicalDataObject(payload)
	if err != nil {
		return CallAuditEnvelope{}, err
	}
	payloadHash, err := aicontract.HashToolPayload(canonical)
	if err != nil {
		return CallAuditEnvelope{}, ErrAuditEnvelopeInvalid
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, duplicate := r.byCall[call.CallID]; duplicate {
		return CallAuditEnvelope{}, ErrAuditEnvelopeConflict
	}
	record := aicontract.ToolCall{
		ID: call.CallID, AttemptID: call.AttemptID, Ordinal: r.next,
		Tool: call.Tool, Base: call.Base, InputHash: call.InputHash,
	}
	inputHash, err := aicontract.HashToolInput(aicontract.ToolInputEnvelope{Call: record, Payload: canonical})
	if err != nil {
		return CallAuditEnvelope{}, ErrAuditEnvelopeInvalid
	}
	envelope := CallAuditEnvelope{
		Ordinal: r.next, Call: record, Payload: canonical, PayloadHash: payloadHash, InputEnvelopeHash: inputHash,
		StartedAtUnixMillis: started.UTC().UnixMilli(), Stage: call.Stage, Targets: cloneTargetAccess(call.Targets),
		SearchCandidates: call.SearchCandidates,
	}
	r.next++
	r.byCall[call.CallID] = &auditCallState{envelope: cloneCallAudit(envelope)}
	return cloneCallAudit(envelope), nil
}

func (r *AuditRecorder) Complete(
	callID aicontract.ToolCallID,
	implementationVersion string,
	evidence []aicontract.EvidenceRef,
	payload json.RawMessage,
	completed time.Time,
	searchCandidatesUsed int,
) (ResultAuditEnvelope, error) {
	canonical, err := canonicalDataObject(payload)
	if err != nil {
		return ResultAuditEnvelope{}, err
	}
	resultHash, err := aicontract.HashToolPayload(canonical)
	if err != nil {
		return ResultAuditEnvelope{}, ErrAuditEnvelopeInvalid
	}
	result := aicontract.ToolResult{CallID: callID, ImplementationVersion: implementationVersion, ResultHash: resultHash, Evidence: sortedEvidence(evidence)}
	if !result.Valid() || searchCandidatesUsed < 0 {
		return ResultAuditEnvelope{}, ErrAuditEnvelopeInvalid
	}
	resultEnvelopeHash, err := aicontract.HashToolResult(aicontract.ToolResultEnvelope{Result: result, Payload: canonical})
	if err != nil {
		return ResultAuditEnvelope{}, ErrAuditEnvelopeInvalid
	}
	return r.close(callID, completed, searchCandidatesUsed, &result, canonical, resultEnvelopeHash, nil)
}

func (r *AuditRecorder) Reject(callID aicontract.ToolCallID, policyErr error, completed time.Time) (ResultAuditEnvelope, error) {
	var typed *PolicyError
	if !errors.As(policyErr, &typed) || typed.Code == "" {
		return ResultAuditEnvelope{}, ErrAuditEnvelopeInvalid
	}
	return r.close(callID, completed, 0, nil, nil, "", &PolicyAuditError{Code: typed.Code})
}

func (r *AuditRecorder) close(
	callID aicontract.ToolCallID,
	completed time.Time,
	searchCandidatesUsed int,
	result *aicontract.ToolResult,
	payload json.RawMessage,
	resultEnvelopeHash aicontract.Hash,
	policyErr *PolicyAuditError,
) (ResultAuditEnvelope, error) {
	if r == nil || completed.IsZero() {
		return ResultAuditEnvelope{}, ErrAuditEnvelopeInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, found := r.byCall[callID]
	if !found || state.closed {
		return ResultAuditEnvelope{}, ErrAuditEnvelopeConflict
	}
	completedMillis := completed.UTC().UnixMilli()
	if completedMillis < state.envelope.StartedAtUnixMillis || (result == nil) == (policyErr == nil) || searchCandidatesUsed < 0 || searchCandidatesUsed > state.envelope.SearchCandidates {
		return ResultAuditEnvelope{}, ErrAuditEnvelopeInvalid
	}
	state.closed = true
	usage := ToolUsage{InputBytes: len(state.envelope.Payload), SearchCandidates: searchCandidatesUsed, ResultBytes: len(payload)}
	return ResultAuditEnvelope{
		Ordinal: state.envelope.Ordinal, CallID: callID, Tool: state.envelope.Call.Tool,
		Result: cloneToolResult(result), Payload: append(json.RawMessage(nil), payload...), ResultEnvelopeHash: resultEnvelopeHash, PolicyError: clonePolicyError(policyErr),
		StartedAtUnixMillis: state.envelope.StartedAtUnixMillis, CompletedAtUnixMillis: completedMillis,
		DurationMillis: completedMillis - state.envelope.StartedAtUnixMillis, Usage: usage,
	}, nil
}

func canonicalDataObject(payload json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	value, err := decodeData(decoder)
	if err != nil {
		return nil, err
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, ErrAuditEnvelopeInvalid
	}
	if token, err := decoder.Token(); err != io.EOF || token != nil {
		return nil, ErrAuditEnvelopeInvalid
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, ErrAuditEnvelopeInvalid
	}
	canonical, err := aicontract.CanonicalToolPayload(encoded)
	if err != nil {
		return nil, ErrAuditEnvelopeInvalid
	}
	return canonical, nil
}

func decodeData(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, ErrAuditEnvelopeInvalid
	}
	switch typed := token.(type) {
	case json.Delim:
		switch typed {
		case '{':
			object := map[string]any{}
			for decoder.More() {
				nameToken, err := decoder.Token()
				if err != nil {
					return nil, ErrAuditEnvelopeInvalid
				}
				name, ok := nameToken.(string)
				if !ok {
					return nil, ErrAuditEnvelopeInvalid
				}
				if _, duplicate := object[name]; duplicate {
					return nil, ErrAuditEnvelopeInvalid
				}
				if controlPlaneKey(name) {
					return nil, fmt.Errorf("%w: control-plane member %q", ErrAuditEnvelopeInvalid, name)
				}
				child, err := decodeData(decoder)
				if err != nil {
					return nil, err
				}
				object[name] = child
			}
			closing, err := decoder.Token()
			if err != nil || closing != json.Delim('}') {
				return nil, ErrAuditEnvelopeInvalid
			}
			return object, nil
		case '[':
			values := []any{}
			for decoder.More() {
				child, err := decodeData(decoder)
				if err != nil {
					return nil, err
				}
				values = append(values, child)
			}
			closing, err := decoder.Token()
			if err != nil || closing != json.Delim(']') {
				return nil, ErrAuditEnvelopeInvalid
			}
			return values, nil
		default:
			return nil, ErrAuditEnvelopeInvalid
		}
	case nil, bool, string, json.Number:
		return typed, nil
	default:
		return nil, ErrAuditEnvelopeInvalid
	}
}

func controlPlaneKey(value string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "_", " ", "_", ".", "_").Replace(value))
	switch normalized {
	case "tools", "tool_definition", "tool_definitions", "tool_schema", "allowed_tools", "permission", "permissions", "capability", "capabilities":
		return true
	default:
		return false
	}
}

func sortedEvidence(values []aicontract.EvidenceRef) []aicontract.EvidenceRef {
	result := append([]aicontract.EvidenceRef(nil), values...)
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func cloneTargetAccess(values []TargetAccess) []TargetAccess {
	return append([]TargetAccess(nil), values...)
}

func cloneCallAudit(value CallAuditEnvelope) CallAuditEnvelope {
	value.Payload = append(json.RawMessage(nil), value.Payload...)
	value.Targets = cloneTargetAccess(value.Targets)
	return value
}

func cloneToolResult(value *aicontract.ToolResult) *aicontract.ToolResult {
	if value == nil {
		return nil
	}
	result := *value
	result.Evidence = append([]aicontract.EvidenceRef(nil), value.Evidence...)
	return &result
}

func clonePolicyError(value *PolicyAuditError) *PolicyAuditError {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
)

var (
	ErrAttemptInvalid           = errors.New("provider attempt request is invalid")
	ErrEventInvalid             = errors.New("provider event is invalid")
	ErrEventOrder               = errors.New("provider event order is invalid")
	ErrTerminalConflict         = errors.New("provider terminal event conflicts")
	canonicalNonnegativeDecimal = regexp.MustCompile(`^(?:0|[1-9][0-9]*)(?:\.[0-9]*[1-9])?$`)
)

type ModelParameters struct {
	Temperature string `json:"temperature"`
	TopP        string `json:"top_p"`
	Seed        *int64 `json:"seed,omitempty"`
}

func (v ModelParameters) Valid() bool {
	return boundedDecimal(v.Temperature, "0", "2") && boundedDecimal(v.TopP, "0", "1")
}

type AttemptManifest struct {
	AttemptID                aicontract.AttemptID         `json:"attempt_id"`
	Provider                 aicontract.VersionIdentity   `json:"provider"`
	Model                    aicontract.VersionIdentity   `json:"model"`
	EndpointClassification   EndpointClassification       `json:"endpoint_classification"`
	Prompt                   aicontract.VersionIdentity   `json:"prompt"`
	StructuredResponseSchema aicontract.VersionIdentity   `json:"structured_response_schema"`
	Tools                    []aicontract.VersionIdentity `json:"tools"`
	Orchestrator             aicontract.VersionIdentity   `json:"orchestrator"`
	Budget                   aicontract.VersionIdentity   `json:"budget"`
	InputHash                aicontract.Hash              `json:"input_hash"`
	EvidenceManifestHash     aicontract.Hash              `json:"evidence_manifest_hash"`
	CancelGeneration         uint64                       `json:"cancel_generation"`
}

func (v AttemptManifest) Valid() bool {
	if !v.AttemptID.Valid() || !v.Provider.Valid() || !v.Model.Valid() ||
		(v.EndpointClassification != EndpointLoopback && v.EndpointClassification != EndpointCloud) ||
		!v.Prompt.Valid() || !v.StructuredResponseSchema.Valid() || !v.Orchestrator.Valid() || !v.Budget.Valid() ||
		!v.InputHash.Valid() || !v.EvidenceManifestHash.Valid() || len(v.Tools) == 0 {
		return false
	}
	seen := map[string]struct{}{}
	for _, tool := range v.Tools {
		if !tool.Valid() {
			return false
		}
		key := tool.ID + "\x00" + tool.Version
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

type PromptMessages struct {
	System    string `json:"system"`
	Developer string `json:"developer"`
}

func (v PromptMessages) Valid() bool {
	return boundedText(v.System, 64<<10) && boundedText(v.Developer, 64<<10)
}

type ToolDefinition struct {
	Identity    aicontract.VersionIdentity `json:"identity"`
	Description string                     `json:"description"`
	InputSchema json.RawMessage            `json:"input_schema"`
}

func (v ToolDefinition) Valid() bool {
	return v.Identity.Valid() && boundedText(v.Description, 2048) && validJSONObject(v.InputSchema)
}

type ToolResult struct {
	CallID     aicontract.ToolCallID      `json:"call_id"`
	Tool       aicontract.VersionIdentity `json:"tool"`
	Arguments  json.RawMessage            `json:"arguments"`
	Result     json.RawMessage            `json:"result"`
	ResultHash aicontract.Hash            `json:"result_hash"`
}

func (v ToolResult) Valid() bool {
	return v.CallID.Valid() && v.Tool.Valid() && validJSONObject(v.Arguments) && validJSON(v.Result) && v.ResultHash.Valid()
}

// CancellationToken combines context cancellation with the durable Job cancel
// generation captured by the attempt manifest.
type CancellationToken interface {
	Done() <-chan struct{}
	Err() error
	Generation() uint64
}

type ContextCancellation struct {
	Context          context.Context
	CancelGeneration uint64
}

func (v ContextCancellation) Done() <-chan struct{} {
	if v.Context == nil {
		return nil
	}
	return v.Context.Done()
}
func (v ContextCancellation) Err() error {
	if v.Context == nil {
		return nil
	}
	return v.Context.Err()
}
func (v ContextCancellation) Generation() uint64 { return v.CancelGeneration }

type AttemptRequest struct {
	Manifest        AttemptManifest         `json:"manifest"`
	Prompt          PromptMessages          `json:"prompt"`
	UserInput       json.RawMessage         `json:"user_input"`
	EvidenceSummary json.RawMessage         `json:"evidence_summary"`
	ResponseSchema  json.RawMessage         `json:"response_schema"`
	Tools           []ToolDefinition        `json:"tools"`
	ToolResults     []ToolResult            `json:"tool_results"`
	Parameters      ModelParameters         `json:"parameters"`
	Limits          aicontract.BudgetLimits `json:"limits"`
	Timeout         time.Duration           `json:"-"`
	Cancellation    CancellationToken       `json:"-"`
}

func (v AttemptRequest) Valid() bool {
	if !v.Manifest.Valid() || !v.Prompt.Valid() || !validJSONObject(v.UserInput) || !validJSONObject(v.EvidenceSummary) ||
		!validJSONObject(v.ResponseSchema) || !v.Parameters.Valid() || !boundedLimits(v.Limits) || v.Timeout <= 0 || v.Timeout > 10*time.Minute ||
		v.Cancellation == nil || v.Cancellation.Done() == nil || v.Cancellation.Generation() != v.Manifest.CancelGeneration ||
		len(v.Tools) != len(v.Manifest.Tools) {
		return false
	}
	for index, tool := range v.Tools {
		if !tool.Valid() || tool.Identity != v.Manifest.Tools[index] {
			return false
		}
	}
	for _, result := range v.ToolResults {
		if !result.Valid() {
			return false
		}
		registered := false
		for _, tool := range v.Manifest.Tools {
			if result.Tool == tool {
				registered = true
				break
			}
		}
		if !registered {
			return false
		}
	}
	return true
}

func boundedLimits(value aicontract.BudgetLimits) bool {
	return value.Valid() && value.MaxFormatRepairs <= aicontract.V1MaxFormatRepairs &&
		value.MaxProviderTurns <= aicontract.V1MaxProviderTurns && value.MaxToolCalls <= aicontract.V1MaxToolCalls &&
		value.MaxSearchCandidates <= aicontract.V1MaxSearchCandidates && value.MaxDurationMillis <= aicontract.V1MaxDurationMillis &&
		value.MaxContextBytes <= aicontract.V1MaxContextBytes && value.MaxOutputBytes <= aicontract.V1MaxOutputBytes &&
		value.MaxToolResultBytes <= aicontract.V1MaxToolResultBytes
}

func (v AttemptRequest) Clone() AttemptRequest {
	v.Manifest.Tools = append([]aicontract.VersionIdentity(nil), v.Manifest.Tools...)
	v.UserInput = append(json.RawMessage(nil), v.UserInput...)
	v.EvidenceSummary = append(json.RawMessage(nil), v.EvidenceSummary...)
	v.ResponseSchema = append(json.RawMessage(nil), v.ResponseSchema...)
	v.Tools = append([]ToolDefinition(nil), v.Tools...)
	for index := range v.Tools {
		v.Tools[index].InputSchema = append(json.RawMessage(nil), v.Tools[index].InputSchema...)
	}
	v.ToolResults = append([]ToolResult(nil), v.ToolResults...)
	for index := range v.ToolResults {
		v.ToolResults[index].Arguments = append(json.RawMessage(nil), v.ToolResults[index].Arguments...)
		v.ToolResults[index].Result = append(json.RawMessage(nil), v.ToolResults[index].Result...)
	}
	return v
}

type EventType string

const (
	EventUsage              EventType = "usage"
	EventToolCall           EventType = "tool_call"
	EventStructuredResponse EventType = "structured_response"
	EventWarning            EventType = "warning"
	EventError              EventType = "error"
)

type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

func (v Usage) Valid() bool {
	return v.InputTokens >= 0 && v.OutputTokens >= 0 && v.TotalTokens == v.InputTokens+v.OutputTokens
}

type ToolCall struct {
	CallID    aicontract.ToolCallID      `json:"call_id"`
	Tool      aicontract.VersionIdentity `json:"tool"`
	Arguments json.RawMessage            `json:"arguments"`
}

func (v ToolCall) Valid() bool {
	return v.CallID.Valid() && v.Tool.Valid() && validJSONObject(v.Arguments)
}

type StructuredResponse struct {
	Schema   aicontract.VersionIdentity `json:"schema"`
	Body     json.RawMessage            `json:"body"`
	BodyHash aicontract.Hash            `json:"body_hash"`
}

func (v StructuredResponse) Valid() bool {
	return v.Schema.Valid() && validJSON(v.Body) && v.BodyHash.Valid()
}

type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (v Warning) Valid() bool { return stableToken(v.Code) && boundedText(v.Message, 1024) }

type ErrorClass string

const (
	ErrorPermanent   ErrorClass = "permanent"
	ErrorTransient   ErrorClass = "transient"
	ErrorTimeout     ErrorClass = "timeout"
	ErrorCanceled    ErrorClass = "canceled"
	ErrorInterrupted ErrorClass = "interrupted"
)

type TerminalError struct {
	Code      string     `json:"code"`
	Class     ErrorClass `json:"class"`
	Retryable bool       `json:"retryable"`
	Message   string     `json:"message"`
	RequestID string     `json:"request_id,omitempty"`
}

func (v TerminalError) Valid() bool {
	validClass := v.Class == ErrorPermanent || v.Class == ErrorTransient || v.Class == ErrorTimeout || v.Class == ErrorCanceled || v.Class == ErrorInterrupted
	if !validClass || !stableToken(v.Code) || !boundedText(v.Message, 1024) || !validRequestID(v.RequestID) {
		return false
	}
	return v.Retryable == (v.Class == ErrorTransient || v.Class == ErrorTimeout || v.Class == ErrorInterrupted)
}

// SafeRequestID accepts only a small opaque-token alphabet. Provider headers
// are untrusted and must never become a channel for response bodies, secrets,
// control characters, or other arbitrary text in public Job projections.
func SafeRequestID(value string) string {
	if !validRequestID(value) {
		return ""
	}
	return value
}

func validRequestID(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 128 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("._:/-", character) {
			continue
		}
		return false
	}
	return true
}

type Event struct {
	Sequence           int64               `json:"sequence"`
	Type               EventType           `json:"type"`
	Usage              *Usage              `json:"usage,omitempty"`
	ToolCall           *ToolCall           `json:"tool_call,omitempty"`
	StructuredResponse *StructuredResponse `json:"structured_response,omitempty"`
	Warning            *Warning            `json:"warning,omitempty"`
	Error              *TerminalError      `json:"error,omitempty"`
}

func (v Event) Terminal() bool { return v.Type == EventStructuredResponse || v.Type == EventError }

func (v Event) Valid() bool {
	if v.Sequence < 1 {
		return false
	}
	payloads := 0
	for _, present := range []bool{v.Usage != nil, v.ToolCall != nil, v.StructuredResponse != nil, v.Warning != nil, v.Error != nil} {
		if present {
			payloads++
		}
	}
	if payloads != 1 {
		return false
	}
	switch v.Type {
	case EventUsage:
		return v.Usage != nil && v.Usage.Valid()
	case EventToolCall:
		return v.ToolCall != nil && v.ToolCall.Valid()
	case EventStructuredResponse:
		return v.StructuredResponse != nil && v.StructuredResponse.Valid()
	case EventWarning:
		return v.Warning != nil && v.Warning.Valid()
	case EventError:
		return v.Error != nil && v.Error.Valid()
	default:
		return false
	}
}

type EventSink interface {
	Emit(Event) error
}

// Port is transport-neutral. Concrete adapters translate SDK or wire objects
// into these immutable request and event DTOs before crossing this boundary.
type Port interface {
	Invoke(AttemptRequest, EventSink) error
}

type EventSinkFunc func(Event) error

func (f EventSinkFunc) Emit(event Event) error { return f(event) }

// OrderedSink rejects gaps, reordering, invalid events and any event after one
// terminal structured response or terminal error.
type OrderedSink struct {
	mu       sync.Mutex
	next     int64
	terminal bool
	target   EventSink
	tools    map[string]aicontract.Hash
	schema   *aicontract.VersionIdentity
}

func NewOrderedSink(target EventSink) *OrderedSink { return &OrderedSink{next: 1, target: target} }

func NewAttemptOrderedSink(manifest AttemptManifest, target EventSink) (*OrderedSink, error) {
	if !manifest.Valid() || target == nil {
		return nil, ErrAttemptInvalid
	}
	sink := &OrderedSink{next: 1, target: target, tools: make(map[string]aicontract.Hash, len(manifest.Tools))}
	for _, tool := range manifest.Tools {
		sink.tools[tool.ID+"\x00"+tool.Version] = tool.Hash
	}
	schema := manifest.StructuredResponseSchema
	sink.schema = &schema
	return sink, nil
}

func (s *OrderedSink) Emit(event Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !event.Valid() || s.target == nil {
		return ErrEventInvalid
	}
	if s.terminal {
		return ErrTerminalConflict
	}
	if event.Sequence != s.next {
		return ErrEventOrder
	}
	if s.tools != nil && event.ToolCall != nil {
		hash, ok := s.tools[event.ToolCall.Tool.ID+"\x00"+event.ToolCall.Tool.Version]
		if !ok || hash != event.ToolCall.Tool.Hash {
			return ErrEventInvalid
		}
	}
	if s.schema != nil && event.StructuredResponse != nil && event.StructuredResponse.Schema != *s.schema {
		return ErrEventInvalid
	}
	if err := s.target.Emit(event); err != nil {
		return err
	}
	s.next++
	s.terminal = event.Terminal()
	return nil
}

func boundedText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func stableToken(value string) bool {
	return boundedText(value, 256) && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n")
}

func validJSON(value json.RawMessage) bool { return len(value) != 0 && json.Valid(value) }

func validJSONObject(value json.RawMessage) bool {
	if !validJSON(value) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil
}

func boundedDecimal(value, minimum, maximum string) bool {
	if !canonicalNonnegativeDecimal.MatchString(value) {
		return false
	}
	number, ok := new(big.Rat).SetString(value)
	if !ok {
		return false
	}
	lower, lowerOK := new(big.Rat).SetString(minimum)
	upper, upperOK := new(big.Rat).SetString(maximum)
	return lowerOK && upperOK && number.Cmp(lower) >= 0 && number.Cmp(upper) <= 0
}

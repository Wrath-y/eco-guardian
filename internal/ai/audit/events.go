package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	"github.com/zouyi/eco-guardian/internal/domain"
)

const MaxCanonicalEventBytes = 65_536

var (
	ErrAuditEventInvalid  = errors.New("AI audit event is invalid")
	ErrAuditEventConflict = errors.New("AI audit event conflicts with the append-only chain")
)

type EventKind string

const (
	EventAttemptContext     EventKind = "attempt_context"
	EventEvidencePinned     EventKind = "evidence_pinned"
	EventProviderUsage      EventKind = "provider_usage"
	EventProviderWarning    EventKind = "provider_warning"
	EventProviderError      EventKind = "provider_error"
	EventStructuredResponse EventKind = "structured_response"
	EventToolCall           EventKind = "tool_call"
	EventToolResult         EventKind = "tool_result"
	EventPatch              EventKind = "patch"
	EventDiff               EventKind = "diff"
	EventPreview            EventKind = "preview"
	EventExplanation        EventKind = "explanation"
	EventAttemptOutcome     EventKind = "attempt_outcome"
	EventCancellation       EventKind = "cancellation"
	EventRetry              EventKind = "retry"
	EventHumanDecision      EventKind = "human_decision"
)

func (kind EventKind) Valid() bool {
	switch kind {
	case EventAttemptContext, EventEvidencePinned, EventProviderUsage, EventProviderWarning, EventProviderError,
		EventStructuredResponse, EventToolCall, EventToolResult, EventPatch, EventDiff, EventPreview,
		EventExplanation, EventAttemptOutcome, EventCancellation, EventRetry, EventHumanDecision:
		return true
	default:
		return false
	}
}

type AttemptContextPayload struct {
	Manifest   aiprovider.AttemptManifest `json:"manifest"`
	Parameters aiprovider.ModelParameters `json:"parameters"`
	Input      aicontract.AIDesignInputV1 `json:"input"`
}

type ProviderEventPayload struct {
	Event          aiprovider.Event `json:"event"`
	DurationMillis int64            `json:"duration_millis"`
}

type EventDraft struct {
	Ordinal   int                          `json:"ordinal"`
	AttemptID aicontract.AttemptID         `json:"attempt_id"`
	Kind      EventKind                    `json:"kind"`
	Payload   json.RawMessage              `json:"payload"`
	Versions  []aicontract.VersionIdentity `json:"versions"`
}

func NewAttemptContextEvent(ordinal int, manifest aiprovider.AttemptManifest, parameters aiprovider.ModelParameters, input aicontract.AIDesignInputV1) (EventDraft, error) {
	inputHash, err := aicontract.HashAIDesignInputV1(input)
	if err != nil || !manifest.Valid() || !parameters.Valid() || inputHash != manifest.InputHash {
		return EventDraft{}, ErrAuditEventInvalid
	}
	versions, err := attemptVersions(manifest, input.RequiredVersions)
	if err != nil {
		return EventDraft{}, err
	}
	return NewEventDraft(ordinal, manifest.AttemptID, EventAttemptContext, AttemptContextPayload{Manifest: manifest, Parameters: parameters, Input: input}, versions)
}

// NewProviderEvent records only a previously sanitized transport-neutral
// Provider event. It retains usage/error metadata and elapsed time without
// admitting SDK or wire objects into the audit contract.
func NewProviderEvent(ordinal int, attemptID aicontract.AttemptID, event aiprovider.Event, durationMillis int64, versions []aicontract.VersionIdentity) (EventDraft, error) {
	if !event.Valid() || durationMillis < 0 {
		return EventDraft{}, ErrAuditEventInvalid
	}
	var kind EventKind
	switch event.Type {
	case aiprovider.EventUsage:
		kind = EventProviderUsage
	case aiprovider.EventStructuredResponse:
		kind = EventStructuredResponse
	case aiprovider.EventWarning:
		kind = EventProviderWarning
	case aiprovider.EventError:
		kind = EventProviderError
	case aiprovider.EventToolCall:
		kind = EventToolCall
	default:
		return EventDraft{}, ErrAuditEventInvalid
	}
	return NewEventDraft(ordinal, attemptID, kind, ProviderEventPayload{Event: event, DurationMillis: durationMillis}, versions)
}

func NewEventDraft(ordinal int, attemptID aicontract.AttemptID, kind EventKind, payload any, versions []aicontract.VersionIdentity) (EventDraft, error) {
	canonical, err := domain.CanonicalJSON(payload)
	if err != nil || len(canonical) == 0 || len(canonical) > MaxCanonicalEventBytes {
		return EventDraft{}, ErrAuditEventInvalid
	}
	draft := EventDraft{Ordinal: ordinal, AttemptID: attemptID, Kind: kind, Payload: canonical, Versions: sortedAuditVersions(versions)}
	if !draft.Valid() {
		return EventDraft{}, ErrAuditEventInvalid
	}
	return draft, nil
}

func (draft EventDraft) Valid() bool {
	if draft.Ordinal < 1 || !draft.AttemptID.Valid() || !draft.Kind.Valid() || len(draft.Payload) == 0 || len(draft.Payload) > MaxCanonicalEventBytes || !json.Valid(draft.Payload) {
		return false
	}
	canonical, err := canonicalRawJSON(draft.Payload)
	if err != nil || !bytes.Equal(canonical, draft.Payload) {
		return false
	}
	seen := map[string]struct{}{}
	for index, version := range draft.Versions {
		if !version.Valid() || index > 0 && version.ID <= draft.Versions[index-1].ID {
			return false
		}
		if _, duplicate := seen[version.ID]; duplicate {
			return false
		}
		seen[version.ID] = struct{}{}
	}
	return true
}

type Event struct {
	PreviousHash aicontract.Hash        `json:"previous_hash,omitempty"`
	Record       aicontract.AuditRecord `json:"record"`
	Payload      json.RawMessage        `json:"payload"`
	ChainHash    aicontract.Hash        `json:"chain_hash"`
}

func BuildEvent(previous aicontract.Hash, draft EventDraft) (Event, error) {
	if !draft.Valid() {
		return Event{}, ErrAuditEventInvalid
	}
	payloadHash := sha256.Sum256(draft.Payload)
	record := aicontract.AuditRecord{
		Ordinal: draft.Ordinal, AttemptID: draft.AttemptID, EventType: string(draft.Kind),
		PayloadHash: aicontract.Hash(hex.EncodeToString(payloadHash[:])), Versions: append([]aicontract.VersionIdentity(nil), draft.Versions...),
	}
	entry, err := aicontract.AppendAuditChain(previous, record)
	if err != nil {
		return Event{}, ErrAuditEventInvalid
	}
	event := Event{PreviousHash: entry.PreviousHash, Record: entry.Record, Payload: append(json.RawMessage(nil), draft.Payload...), ChainHash: entry.ChainHash}
	if !event.Valid() {
		return Event{}, ErrAuditEventInvalid
	}
	return event, nil
}

func (event Event) Valid() bool {
	if !event.Record.Valid() || !event.ChainHash.Valid() || len(event.Payload) == 0 || len(event.Payload) > MaxCanonicalEventBytes {
		return false
	}
	canonical, err := canonicalRawJSON(event.Payload)
	if err != nil || !bytes.Equal(canonical, event.Payload) {
		return false
	}
	payloadHash := sha256.Sum256(event.Payload)
	if event.Record.PayloadHash != aicontract.Hash(hex.EncodeToString(payloadHash[:])) {
		return false
	}
	entry, err := aicontract.AppendAuditChain(event.PreviousHash, event.Record)
	return err == nil && entry.ChainHash == event.ChainHash
}

func CanonicalEvent(event Event) ([]byte, error) {
	if !event.Valid() {
		return nil, ErrAuditEventInvalid
	}
	canonical, err := domain.CanonicalJSON(event)
	if err != nil || len(canonical) > MaxCanonicalEventBytes {
		return nil, ErrAuditEventInvalid
	}
	return canonical, nil
}

type EventRepository interface {
	AppendAuditEvent(context.Context, EventDraft) (Event, bool, error)
	ListAuditEvents(context.Context, aicontract.AttemptID) ([]Event, error)
}

type Trail struct{ Repository EventRepository }

func (trail Trail) Append(ctx context.Context, draft EventDraft) (Event, bool, error) {
	if ctx == nil || trail.Repository == nil || !draft.Valid() {
		return Event{}, false, ErrAuditEventInvalid
	}
	event, replay, err := trail.Repository.AppendAuditEvent(ctx, draft)
	if err != nil {
		return Event{}, false, err
	}
	if !event.Valid() || event.Record.Ordinal != draft.Ordinal || event.Record.AttemptID != draft.AttemptID || event.Record.EventType != string(draft.Kind) || !bytes.Equal(event.Payload, draft.Payload) {
		return Event{}, false, ErrAuditEventConflict
	}
	return event, replay, nil
}

func attemptVersions(manifest aiprovider.AttemptManifest, required []aicontract.VersionIdentity) ([]aicontract.VersionIdentity, error) {
	values := []aicontract.VersionIdentity{manifest.Provider, manifest.Model, manifest.Prompt, manifest.StructuredResponseSchema, manifest.Orchestrator, manifest.Budget}
	values = append(values, manifest.Tools...)
	values = append(values, required...)
	byID := map[string]aicontract.VersionIdentity{}
	for _, value := range values {
		if !value.Valid() {
			return nil, ErrAuditEventInvalid
		}
		if existing, found := byID[value.ID]; found && existing != value {
			return nil, ErrAuditEventConflict
		}
		byID[value.ID] = value
	}
	result := make([]aicontract.VersionIdentity, 0, len(byID))
	for _, value := range byID {
		result = append(result, value)
	}
	return sortedAuditVersions(result), nil
}

func sortedAuditVersions(values []aicontract.VersionIdentity) []aicontract.VersionIdentity {
	result := append([]aicontract.VersionIdentity(nil), values...)
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	return result
}

func canonicalRawJSON(raw json.RawMessage) ([]byte, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return domain.CanonicalJSON(value)
}

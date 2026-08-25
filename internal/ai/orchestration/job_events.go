package orchestration

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

const MaxAIJobEventBytesV1 = 8 << 10

var (
	ErrAIJobEventInvalid = errors.New("AI Job event is invalid")
	ErrAIJobEventReplay  = errors.New("AI Job event replay is invalid")
)

type AIJobEventKind string

const (
	EventStage    AIJobEventKind = "stage"
	EventProgress AIJobEventKind = "progress"
	EventWarning  AIJobEventKind = "warning"
	EventTool     AIJobEventKind = "tool"
	EventRepair   AIJobEventKind = "repair"
	EventTerminal AIJobEventKind = "terminal"
)

func (kind AIJobEventKind) Valid() bool {
	return kind == EventStage || kind == EventProgress || kind == EventWarning || kind == EventTool || kind == EventRepair || kind == EventTerminal
}

type AIJobEventDraft struct {
	JobID         domain.ID                   `json:"job_id"`
	EventKey      string                      `json:"event_key"`
	Kind          AIJobEventKind              `json:"kind"`
	Phase         JobPhase                    `json:"phase"`
	Progress      int                         `json:"progress"`
	AttemptID     aicontract.AttemptID        `json:"attempt_id,omitempty"`
	WarningCode   string                      `json:"warning_code,omitempty"`
	WarningRef    string                      `json:"warning_ref,omitempty"`
	Tool          *aicontract.VersionIdentity `json:"tool,omitempty"`
	RepairCount   int                         `json:"repair_count,omitempty"`
	Outcome       aicontract.AttemptOutcome   `json:"outcome,omitempty"`
	SafeErrorCode string                      `json:"error_code,omitempty"`
	Result        *sharedjob.Result           `json:"result,omitempty"`
}

func (draft AIJobEventDraft) Valid() bool {
	if !draft.JobID.Valid() || !stableEventText(draft.EventKey, 128) || !draft.Kind.Valid() || !draft.Phase.Valid() || draft.Progress < 0 || draft.Progress > 100 || (draft.AttemptID != "" && !draft.AttemptID.Valid()) || !optionalEventText(draft.WarningCode, 128) || !optionalEventText(draft.WarningRef, 256) || !optionalEventText(draft.SafeErrorCode, 128) || (draft.Tool != nil && !draft.Tool.Valid()) || (draft.Result != nil && !draft.Result.Valid()) {
		return false
	}
	switch draft.Kind {
	case EventStage:
		return draft.Progress == draft.Phase.Progress() && emptyEventDetail(draft)
	case EventProgress:
		return emptyEventDetail(draft)
	case EventWarning:
		return draft.WarningCode != "" && draft.Tool == nil && draft.RepairCount == 0 && draft.Outcome == "" && draft.SafeErrorCode == "" && draft.Result == nil
	case EventTool:
		return draft.Phase == PhaseProviderToolLoop && draft.Tool != nil && draft.WarningCode == "" && draft.WarningRef == "" && draft.RepairCount == 0 && draft.Outcome == "" && draft.SafeErrorCode == "" && draft.Result == nil
	case EventRepair:
		return draft.Phase == PhaseProviderToolLoop && draft.AttemptID.Valid() && draft.RepairCount >= 1 && draft.RepairCount <= aicontract.V1MaxFormatRepairs && draft.WarningCode == "" && draft.WarningRef == "" && draft.Tool == nil && draft.Outcome == "" && draft.SafeErrorCode == "" && draft.Result == nil
	case EventTerminal:
		if draft.Tool != nil || draft.WarningCode != "" || draft.WarningRef != "" || draft.RepairCount != 0 || !terminalAttemptOutcome(draft.Outcome) {
			return false
		}
		if draft.Outcome == aicontract.OutcomeSucceeded {
			return draft.Phase == PhasePatchSealed && draft.Progress == 100 && draft.SafeErrorCode == "" && validPatchResult(draft.Result)
		}
		return draft.Result == nil && draft.SafeErrorCode != ""
	default:
		return false
	}
}

func emptyEventDetail(draft AIJobEventDraft) bool {
	return draft.WarningCode == "" && draft.WarningRef == "" && draft.Tool == nil && draft.RepairCount == 0 && draft.Outcome == "" && draft.SafeErrorCode == "" && draft.Result == nil
}

type AIJobEvent struct {
	AIJobEventDraft
	Ordinal   int64     `json:"ordinal"`
	CreatedAt time.Time `json:"created_at"`
}

func (event AIJobEvent) Valid() bool {
	if !event.AIJobEventDraft.Valid() || event.Ordinal < 1 || event.CreatedAt.IsZero() {
		return false
	}
	body, err := domain.CanonicalJSON(event)
	return err == nil && len(body) <= MaxAIJobEventBytesV1
}

type AIJobEventRepository interface {
	CommitAIJobEvent(context.Context, AIJobEventDraft) (AIJobEvent, bool, error)
	ListAIJobEvents(context.Context, domain.ID, int64) ([]AIJobEvent, error)
	GetAIJobState(context.Context, domain.ID) (AIJobState, error)
}

type SSEEvent struct {
	ID    string
	Event string
	Data  []byte
}

func (event SSEEvent) Valid() bool {
	ordinal, err := strconv.ParseInt(event.ID, 10, 64)
	return err == nil && ordinal > 0 && event.Event != "" && len(event.Data) > 0 && len(event.Data) <= MaxAIJobEventBytesV1
}

type AIJobEventStream struct{ Repository AIJobEventRepository }

func (stream AIJobEventStream) Publish(ctx context.Context, draft AIJobEventDraft) (SSEEvent, bool, error) {
	if ctx == nil || stream.Repository == nil || !draft.Valid() {
		return SSEEvent{}, false, ErrAIJobEventInvalid
	}
	committed, replay, err := stream.Repository.CommitAIJobEvent(ctx, cloneEventDraft(draft))
	if err != nil {
		return SSEEvent{}, false, err
	}
	if !committed.Valid() || !equalEventDraft(committed.AIJobEventDraft, draft) {
		return SSEEvent{}, false, ErrAIJobEventInvalid
	}
	projected, err := projectSSE(committed)
	return projected, replay, err
}

func (stream AIJobEventStream) Replay(ctx context.Context, jobID domain.ID, lastEventID string) ([]SSEEvent, error) {
	if ctx == nil || stream.Repository == nil || !jobID.Valid() {
		return nil, ErrAIJobEventReplay
	}
	after := int64(0)
	if lastEventID != "" {
		value, err := strconv.ParseInt(lastEventID, 10, 64)
		if err != nil || value < 0 {
			return nil, ErrAIJobEventReplay
		}
		after = value
	}
	events, err := stream.Repository.ListAIJobEvents(ctx, jobID, after)
	if err != nil {
		return nil, err
	}
	result := make([]SSEEvent, len(events))
	previous := after
	for index, event := range events {
		if !event.Valid() || event.JobID != jobID || event.Ordinal <= previous {
			return nil, ErrAIJobEventReplay
		}
		result[index], err = projectSSE(event)
		if err != nil {
			return nil, err
		}
		previous = event.Ordinal
	}
	return result, nil
}

// Poll is the fallback projection for clients that cannot keep an SSE
// connection. It reads the same committed state and cursor-ordered events.
func (stream AIJobEventStream) Poll(ctx context.Context, jobID domain.ID, after int64) (AIJobState, []AIJobEvent, error) {
	if ctx == nil || stream.Repository == nil || !jobID.Valid() || after < 0 {
		return AIJobState{}, nil, ErrAIJobEventReplay
	}
	state, err := stream.Repository.GetAIJobState(ctx, jobID)
	if err != nil {
		return AIJobState{}, nil, err
	}
	events, err := stream.Repository.ListAIJobEvents(ctx, jobID, after)
	if err != nil {
		return AIJobState{}, nil, err
	}
	if !state.Valid() {
		return AIJobState{}, nil, ErrAIJobEventReplay
	}
	for index, event := range events {
		if !event.Valid() || event.JobID != jobID || event.Ordinal <= after || index > 0 && event.Ordinal <= events[index-1].Ordinal {
			return AIJobState{}, nil, ErrAIJobEventReplay
		}
	}
	return state, cloneAIJobEvents(events), nil
}

func projectSSE(event AIJobEvent) (SSEEvent, error) {
	canonical, err := domain.CanonicalJSON(event)
	if err != nil || len(canonical) > MaxAIJobEventBytesV1 {
		return SSEEvent{}, ErrAIJobEventInvalid
	}
	result := SSEEvent{ID: strconv.FormatInt(event.Ordinal, 10), Event: string(event.Kind), Data: canonical}
	if !result.Valid() {
		return SSEEvent{}, ErrAIJobEventInvalid
	}
	return result, nil
}

func stableEventText(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && utf8.ValidString(value) && len(value) <= maximum && !strings.ContainsAny(value, "\x00\r\n")
}

func optionalEventText(value string, maximum int) bool {
	return value == "" || stableEventText(value, maximum)
}

func cloneEventDraft(value AIJobEventDraft) AIJobEventDraft {
	if value.Tool != nil {
		tool := *value.Tool
		value.Tool = &tool
	}
	value.Result = cloneJobResult(value.Result)
	return value
}

func equalEventDraft(left, right AIJobEventDraft) bool {
	leftJSON, _ := domain.CanonicalJSON(left)
	rightJSON, _ := domain.CanonicalJSON(right)
	return bytes.Equal(leftJSON, rightJSON)
}

func cloneAIJobEvents(values []AIJobEvent) []AIJobEvent {
	out := make([]AIJobEvent, len(values))
	for index, value := range values {
		value.AIJobEventDraft = cloneEventDraft(value.AIJobEventDraft)
		out[index] = value
	}
	return out
}

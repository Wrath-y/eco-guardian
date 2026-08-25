package orchestration

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

type aiEventRepositoryFake struct {
	state   AIJobState
	events  []AIJobEvent
	byKey   map[string]AIJobEvent
	commits int
}

func newAIEventRepositoryFake(t *testing.T) *aiEventRepositoryFake {
	t.Helper()
	now := time.Unix(1_700_000_000, 0).UTC()
	jobID, _ := domain.NewID()
	projectID, _ := domain.NewID()
	revisionID, _ := domain.NewID()
	state := AIJobState{Job: sharedjob.Record{ID: jobID, ProjectID: projectID, Kind: AIJobKind, RevisionID: revisionID, InputHash: strings.Repeat("a", 64), IdempotencyKey: "ai-event", RequestHash: strings.Repeat("b", 64), Status: sharedjob.Running, CreatedAt: now, UpdatedAt: now}, Phase: PhaseProviderToolLoop, Owner: "worker-1"}
	return &aiEventRepositoryFake{state: state, byKey: map[string]AIJobEvent{}}
}

func (repository *aiEventRepositoryFake) CommitAIJobEvent(_ context.Context, draft AIJobEventDraft) (AIJobEvent, bool, error) {
	repository.commits++
	if draft.JobID != repository.state.Job.ID {
		return AIJobEvent{}, false, ErrAIJobEventInvalid
	}
	key := string(draft.JobID) + "\x00" + draft.EventKey
	if existing, found := repository.byKey[key]; found {
		if !equalEventDraft(existing.AIJobEventDraft, draft) {
			return AIJobEvent{}, false, ErrAIJobEventInvalid
		}
		return existing, true, nil
	}
	if len(repository.events) > 0 {
		previous := repository.events[len(repository.events)-1]
		if draft.Progress < previous.Progress || draft.Phase.Order() < previous.Phase.Order() {
			return AIJobEvent{}, false, ErrAIJobEventInvalid
		}
	}
	event := AIJobEvent{AIJobEventDraft: cloneEventDraft(draft), Ordinal: int64(len(repository.events) + 1), CreatedAt: time.Unix(1_700_000_000+int64(len(repository.events)), 0).UTC()}
	if !event.Valid() {
		return AIJobEvent{}, false, ErrAIJobEventInvalid
	}
	repository.events = append(repository.events, event)
	repository.byKey[key] = event
	return event, false, nil
}

func (repository *aiEventRepositoryFake) ListAIJobEvents(_ context.Context, jobID domain.ID, after int64) ([]AIJobEvent, error) {
	if jobID != repository.state.Job.ID {
		return nil, ErrAIJobEventReplay
	}
	result := make([]AIJobEvent, 0)
	for _, event := range repository.events {
		if event.Ordinal > after {
			result = append(result, event)
		}
	}
	return cloneAIJobEvents(result), nil
}

func (repository *aiEventRepositoryFake) GetAIJobState(_ context.Context, jobID domain.ID) (AIJobState, error) {
	if jobID != repository.state.Job.ID {
		return AIJobState{}, ErrAIJobEventReplay
	}
	return repository.state, nil
}

func TestAIJobEventsPublishOnlyCommittedBoundedStructuredFacts(t *testing.T) {
	repository := newAIEventRepositoryFake(t)
	stream := AIJobEventStream{Repository: repository}
	jobID := repository.state.Job.ID
	attemptID := aicontract.AttemptID("018f9e40-0000-7000-8000-000000000220")
	tool := aicontract.V1Fixture().Tools[0].Identity
	drafts := []AIJobEventDraft{
		{JobID: jobID, EventKey: "stage-provider", Kind: EventStage, Phase: PhaseProviderToolLoop, Progress: PhaseProviderToolLoop.Progress(), AttemptID: attemptID},
		{JobID: jobID, EventKey: "tool-1", Kind: EventTool, Phase: PhaseProviderToolLoop, Progress: 55, AttemptID: attemptID, Tool: &tool},
		{JobID: jobID, EventKey: "repair-1", Kind: EventRepair, Phase: PhaseProviderToolLoop, Progress: 60, AttemptID: attemptID, RepairCount: 1},
		{JobID: jobID, EventKey: "warning-1", Kind: EventWarning, Phase: PhaseProviderToolLoop, Progress: 60, AttemptID: attemptID, WarningCode: "RETRIEVAL_DEGRADED", WarningRef: "bm25_only"},
		{JobID: jobID, EventKey: "terminal", Kind: EventTerminal, Phase: PhaseProviderToolLoop, Progress: 60, AttemptID: attemptID, Outcome: aicontract.OutcomeFailed, SafeErrorCode: "PROVIDER_TIMEOUT"},
	}
	for index, draft := range drafts {
		event, replay, err := stream.Publish(context.Background(), draft)
		if err != nil || replay || !event.Valid() || event.ID != strconv.Itoa(index+1) {
			t.Fatalf("index=%d event=%#v replay=%v err=%v", index, event, replay, err)
		}
		body := string(event.Data)
		if strings.Contains(body, "token") || strings.Contains(body, "secret") || strings.Contains(body, "provider_body") {
			t.Fatalf("unsafe event payload=%s", body)
		}
	}
	if replayed, replay, err := stream.Publish(context.Background(), drafts[1]); err != nil || !replay || replayed.ID != "2" {
		t.Fatalf("replayed=%#v replay=%v err=%v", replayed, replay, err)
	}
}

func TestAIJobEventReplayUsesLastEventIDAndPollingSameCommittedSource(t *testing.T) {
	repository := newAIEventRepositoryFake(t)
	stream := AIJobEventStream{Repository: repository}
	jobID := repository.state.Job.ID
	for index, code := range []string{"WARNING_A", "WARNING_B", "WARNING_C"} {
		_, _, err := stream.Publish(context.Background(), AIJobEventDraft{JobID: jobID, EventKey: "warning-" + code, Kind: EventWarning, Phase: PhaseProviderToolLoop, Progress: 50 + index, WarningCode: code})
		if err != nil {
			t.Fatal(err)
		}
	}
	commits := repository.commits
	replayed, err := stream.Replay(context.Background(), jobID, "1")
	if err != nil || len(replayed) != 2 || replayed[0].ID != "2" || replayed[1].ID != "3" || repository.commits != commits {
		t.Fatalf("replayed=%#v commits=%d/%d err=%v", replayed, repository.commits, commits, err)
	}
	state, events, err := stream.Poll(context.Background(), jobID, 1)
	if err != nil || state.Job.ID != jobID || len(events) != 2 || events[0].Ordinal != 2 || repository.commits != commits {
		t.Fatalf("state=%#v events=%#v err=%v", state, events, err)
	}
	if _, err := stream.Replay(context.Background(), jobID, "not-an-event-id"); !errors.Is(err, ErrAIJobEventReplay) {
		t.Fatalf("invalid cursor err=%v", err)
	}
}

func TestAIJobEventSchemaRejectsRawOrUnboundedShapes(t *testing.T) {
	repository := newAIEventRepositoryFake(t)
	stream := AIJobEventStream{Repository: repository}
	jobID := repository.state.Job.ID
	tool := aicontract.V1Fixture().Tools[0].Identity
	for name, draft := range map[string]AIJobEventDraft{
		"warning newline":        {JobID: jobID, EventKey: "bad-warning", Kind: EventWarning, Phase: PhaseProviderToolLoop, Progress: 50, WarningCode: "unsafe\nbody"},
		"repair overflow":        {JobID: jobID, EventKey: "bad-repair", Kind: EventRepair, Phase: PhaseProviderToolLoop, Progress: 50, AttemptID: "attempt", RepairCount: aicontract.V1MaxFormatRepairs + 1},
		"tool outside loop":      {JobID: jobID, EventKey: "bad-tool", Kind: EventTool, Phase: PhaseDeterministicPreview, Progress: 80, Tool: &tool},
		"terminal without error": {JobID: jobID, EventKey: "bad-terminal", Kind: EventTerminal, Phase: PhaseProviderToolLoop, Progress: 50, Outcome: aicontract.OutcomeFailed},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := stream.Publish(context.Background(), draft); !errors.Is(err, ErrAIJobEventInvalid) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestSuccessfulTerminalEventCarriesOnlyCanonicalPatchLink(t *testing.T) {
	repository := newAIEventRepositoryFake(t)
	stream := AIJobEventStream{Repository: repository}
	patchID := domain.ID("018f9e40-0000-7000-8000-000000000299")
	result := &sharedjob.Result{Type: DraftPatchResultType, ID: patchID, URL: "/api/v1/draft-patches/" + string(patchID)}
	event, _, err := stream.Publish(context.Background(), AIJobEventDraft{JobID: repository.state.Job.ID, EventKey: "success", Kind: EventTerminal, Phase: PhasePatchSealed, Progress: 100, Outcome: aicontract.OutcomeSucceeded, Result: result})
	if err != nil || !event.Valid() || !strings.Contains(string(event.Data), "/api/v1/draft-patches/") {
		t.Fatalf("event=%#v err=%v", event, err)
	}
}

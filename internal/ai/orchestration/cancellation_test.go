package orchestration

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

type cancellationEventPublisherFake struct {
	events map[string]AIJobEvent
}

func (publisher *cancellationEventPublisherFake) Publish(_ context.Context, draft AIJobEventDraft) (SSEEvent, bool, error) {
	if !draft.Valid() {
		return SSEEvent{}, false, ErrAIJobEventInvalid
	}
	if publisher.events == nil {
		publisher.events = map[string]AIJobEvent{}
	}
	if existing, found := publisher.events[draft.EventKey]; found {
		projected, err := projectSSE(existing)
		return projected, true, err
	}
	event := AIJobEvent{AIJobEventDraft: cloneEventDraft(draft), Ordinal: int64(len(publisher.events) + 1), CreatedAt: time.Unix(1_700_000_100, 0).UTC()}
	if !event.Valid() {
		return SSEEvent{}, false, ErrAIJobEventInvalid
	}
	publisher.events[draft.EventKey] = event
	projected, err := projectSSE(event)
	return projected, false, err
}

func newCancellationJob(t *testing.T, key string) (*aiJobRepositoryFake, AIJobController, AIJobState) {
	t.Helper()
	repository := &aiJobRepositoryFake{}
	controller := AIJobController{Jobs: repository}
	state, _, err := controller.Admit(context.Background(), aiJobInput(t), key)
	if err != nil {
		t.Fatal(err)
	}
	return repository, controller, state
}

func TestCancellationIsIdempotentAndPreventsUnstartedCalls(t *testing.T) {
	_, controller, queued := newCancellationJob(t, "cancel-before-call")
	coordinator := CancellationCoordinator{Controller: controller, Signals: NewInvocationCancellationRegistry(), Events: &cancellationEventPublisherFake{}}

	canceled, replay, err := coordinator.Cancel(context.Background(), queued.Job.ID)
	if err != nil || replay || canceled.Job.Status != sharedjob.Canceled || canceled.Job.CancelGeneration != 1 || canceled.Job.CancelRequestedAt == nil {
		t.Fatalf("canceled=%#v replay=%v err=%v", canceled, replay, err)
	}
	replayed, replay, err := coordinator.Cancel(context.Background(), queued.Job.ID)
	if err != nil || !replay || replayed.Job.CancelGeneration != 1 || replayed.Job.Status != sharedjob.Canceled {
		t.Fatalf("replayed=%#v replay=%v err=%v", replayed, replay, err)
	}
	providerCalls := 0
	if lease, err := coordinator.BeginCalls(context.Background(), canceled); !errors.Is(err, ErrAIJobCallCanceled) || lease != nil {
		providerCalls++
	}
	if providerCalls != 0 {
		t.Fatalf("provider calls=%d", providerCalls)
	}
}

func TestCancellationSignalsProviderAndToolsAndMapsUncertainRemoteToInterrupted(t *testing.T) {
	_, controller, queued := newCancellationJob(t, "cancel-running-call")
	running, _, err := controller.Claim(context.Background(), queued, "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	coordinator := CancellationCoordinator{Controller: controller, Signals: NewInvocationCancellationRegistry(), Events: &cancellationEventPublisherFake{}}
	lease, err := coordinator.BeginCalls(context.Background(), running)
	if err != nil || lease.Context.Err() != nil || lease.ProviderCancellation().Generation() != 0 {
		t.Fatalf("lease=%#v err=%v", lease, err)
	}

	canceled, replay, err := coordinator.Cancel(context.Background(), running.Job.ID)
	if err != nil || replay || canceled.Job.Status != sharedjob.Running || canceled.Job.CancelGeneration != 1 {
		t.Fatalf("cancel request=%#v replay=%v err=%v", canceled, replay, err)
	}
	select {
	case <-lease.Context.Done():
	case <-time.After(time.Second):
		t.Fatal("tool context was not canceled")
	}
	select {
	case <-lease.ProviderCancellation().Done():
	default:
		t.Fatal("Provider cancellation was not signaled")
	}
	if replayed, replay, err := coordinator.Cancel(context.Background(), running.Job.ID); err != nil || !replay || replayed.Job.CancelGeneration != 1 {
		t.Fatalf("replayed=%#v replay=%v err=%v", replayed, replay, err)
	}
	settled, _, err := coordinator.SettleCancellation(context.Background(), lease, false)
	if err != nil || settled.Job.Status != sharedjob.Interrupted || settled.Owner != "" || settled.Job.Result != nil {
		t.Fatalf("settled=%#v err=%v", settled, err)
	}
	if replayed, replay, err := coordinator.SettleCancellation(context.Background(), lease, false); err != nil || !replay || replayed.Job.Status != sharedjob.Interrupted {
		t.Fatalf("settle replay=%#v replay=%v err=%v", replayed, replay, err)
	}
}

func TestLateResultIsRejectedBeforePatchSealAndOnlyRedactedEventIsRecorded(t *testing.T) {
	_, controller, queued := newCancellationJob(t, "cancel-late-result")
	running, _, _ := controller.Claim(context.Background(), queued, "worker-1")
	for _, phase := range []JobPhase{PhaseEvidencePinned, PhaseProviderToolLoop, PhaseDeterministicPreview, PhasePatchSealed} {
		running, _, _ = controller.Advance(context.Background(), running, phase)
	}
	publisher := &cancellationEventPublisherFake{}
	coordinator := CancellationCoordinator{Controller: controller, Signals: NewInvocationCancellationRegistry(), Events: publisher}
	lease, err := coordinator.BeginCalls(context.Background(), running)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := coordinator.Cancel(context.Background(), running.Job.ID); err != nil {
		t.Fatal(err)
	}
	attemptID := aicontract.AttemptID("018f9e40-0000-7000-8000-000000000220")
	for index := 0; index < 2; index++ {
		if err := coordinator.AcceptResult(context.Background(), lease, attemptID); !errors.Is(err, ErrAIJobLateResult) {
			t.Fatalf("late result %d err=%v", index, err)
		}
	}
	if len(publisher.events) != 1 {
		t.Fatalf("events=%d", len(publisher.events))
	}
	key := "ignored-late-result/" + string(attemptID) + "/" + strconv.Itoa(1)
	event := publisher.events[key]
	if event.Kind != EventIgnoredLateResult || event.Outcome != aicontract.OutcomeIgnoredLateResult || event.WarningCode != "" || event.SafeErrorCode != "" || event.Result != nil || event.Tool != nil {
		t.Fatalf("unsafe ignored event=%#v", event)
	}
	patchID := aicontract.PatchID("018f9e40-0000-7000-8000-000000000299")
	if _, _, err := controller.SealPatch(context.Background(), running, patchID); !errors.Is(err, ErrAIJobTransition) {
		t.Fatalf("late Patch seal err=%v", err)
	}
}

func TestConfirmedRemoteStopSettlesCanceled(t *testing.T) {
	_, controller, queued := newCancellationJob(t, "cancel-confirmed-stop")
	running, _, _ := controller.Claim(context.Background(), queued, "worker-1")
	coordinator := CancellationCoordinator{Controller: controller, Signals: NewInvocationCancellationRegistry(), Events: &cancellationEventPublisherFake{}}
	lease, err := coordinator.BeginCalls(context.Background(), running)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := coordinator.Cancel(context.Background(), running.Job.ID); err != nil {
		t.Fatal(err)
	}
	settled, _, err := coordinator.SettleCancellation(context.Background(), lease, true)
	if err != nil || settled.Job.Status != sharedjob.Canceled || settled.Job.CancelGeneration != 1 {
		t.Fatalf("settled=%#v err=%v", settled, err)
	}
}

var _ AIJobEventPublisher = (*cancellationEventPublisherFake)(nil)

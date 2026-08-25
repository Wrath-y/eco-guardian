package orchestration

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
)

func TestCancelFaultMatrixBeforeDuringAndAfterProviderAndTools(t *testing.T) {
	t.Run("before unstarted calls", func(t *testing.T) {
		_, controller, queued := newCancellationJob(t, "fault-cancel-before")
		coordinator := CancellationCoordinator{Controller: controller, Signals: NewInvocationCancellationRegistry(), Events: &cancellationEventPublisherFake{}}
		if _, _, err := coordinator.Cancel(context.Background(), queued.Job.ID); err != nil {
			t.Fatal(err)
		}
		var providerCalls, toolCalls atomic.Int32
		if lease, err := coordinator.BeginCalls(context.Background(), queued); err == nil {
			providerCalls.Add(1)
			toolCalls.Add(1)
			lease.Release()
		}
		if providerCalls.Load() != 0 || toolCalls.Load() != 0 {
			t.Fatalf("provider=%d tools=%d", providerCalls.Load(), toolCalls.Load())
		}
	})

	for _, callKind := range []string{"provider", "tool"} {
		t.Run("during "+callKind, func(t *testing.T) {
			_, controller, queued := newCancellationJob(t, "fault-cancel-during-"+callKind)
			running, _, _ := controller.Claim(context.Background(), queued, "worker-1")
			coordinator := CancellationCoordinator{Controller: controller, Signals: NewInvocationCancellationRegistry(), Events: &cancellationEventPublisherFake{}}
			lease, err := coordinator.BeginCalls(context.Background(), running)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release()
			started := make(chan struct{})
			finished := make(chan struct{})
			var calls atomic.Int32
			go func() {
				calls.Add(1)
				close(started)
				if callKind == "provider" {
					<-lease.ProviderCancellation().Done()
				} else {
					<-lease.Context.Done()
				}
				close(finished)
			}()
			<-started
			if _, _, err := coordinator.Cancel(context.Background(), running.Job.ID); err != nil {
				t.Fatal(err)
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("call did not observe cancellation")
			}
			if _, _, err := coordinator.SettleCancellation(context.Background(), lease, true); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatalf("calls=%d", calls.Load())
			}
		})
	}

	for _, callKind := range []string{"provider", "tool"} {
		t.Run("after "+callKind+" before seal", func(t *testing.T) {
			_, controller, queued := newCancellationJob(t, "fault-cancel-after-"+callKind)
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
			defer lease.Release()
			var calls atomic.Int32
			calls.Add(1) // the call returned, but its result is not sealed yet
			if err := coordinator.AcceptResult(context.Background(), lease, "018f9e40-0000-7000-8000-000000000280"); err != nil {
				t.Fatal(err)
			}
			if _, _, err := coordinator.Cancel(context.Background(), running.Job.ID); err != nil {
				t.Fatal(err)
			}
			if err := coordinator.AcceptResult(context.Background(), lease, "018f9e40-0000-7000-8000-000000000280"); !errors.Is(err, ErrAIJobLateResult) {
				t.Fatalf("late result err=%v", err)
			}
			if _, _, err := controller.SealPatch(context.Background(), running, "018f9e40-0000-7000-8000-000000000281"); !errors.Is(err, ErrAIJobTransition) {
				t.Fatalf("seal err=%v", err)
			}
			if calls.Load() != 1 || len(publisher.events) != 1 {
				t.Fatalf("calls=%d ignored_events=%d", calls.Load(), len(publisher.events))
			}
		})
	}
}

func TestSSEDisconnectAndDuplicateWorkerDeliveryNeverInvokeProvider(t *testing.T) {
	events := newAIEventRepositoryFake(t)
	stream := AIJobEventStream{Repository: events}
	for index := 0; index < 3; index++ {
		if _, _, err := stream.Publish(context.Background(), AIJobEventDraft{
			JobID: events.state.Job.ID, EventKey: "disconnect-" + string(rune('a'+index)), Kind: EventProgress,
			Phase: PhaseProviderToolLoop, Progress: 50 + index,
		}); err != nil {
			t.Fatal(err)
		}
	}
	commits := events.commits
	var providerCalls atomic.Int32
	for _, lastID := range []string{"", "1", "2"} {
		if _, err := stream.Replay(context.Background(), events.state.Job.ID, lastID); err != nil {
			t.Fatal(err)
		}
	}
	if events.commits != commits || providerCalls.Load() != 0 {
		t.Fatalf("event commits=%d/%d provider=%d", events.commits, commits, providerCalls.Load())
	}

	_, controller, queued := newCancellationJob(t, "duplicate-worker-delivery")
	if _, _, err := controller.Claim(context.Background(), queued, "worker-1"); err == nil {
		providerCalls.Add(1)
	}
	if _, _, err := controller.Claim(context.Background(), queued, "worker-2"); err == nil {
		providerCalls.Add(1)
	}
	if providerCalls.Load() != 1 {
		t.Fatalf("provider calls=%d", providerCalls.Load())
	}
}

func TestProcessCrashRecoveryMatrixNeverCreatesDuplicateProviderCall(t *testing.T) {
	type crashCase struct {
		name           string
		phase          JobPhase
		withAttempt    bool
		withResponse   bool
		withOutcome    aicontract.AttemptOutcome
		withCheckpoint bool
		withPatch      bool
		want           AIRecoveryAction
		callsBefore    int32
	}
	cases := []crashCase{
		{name: "input pinned", phase: PhaseInputPinned, want: RecoveryReranDeterministic},
		{name: "evidence pinned", phase: PhaseEvidencePinned, want: RecoveryReranDeterministic},
		{name: "provider phase before attempt receipt", phase: PhaseProviderToolLoop, want: RecoveryInterruptedProvider, callsBefore: 1},
		{name: "attempt persisted without terminal receipt", phase: PhaseProviderToolLoop, withAttempt: true, want: RecoveryInterruptedProvider, callsBefore: 1},
		{name: "terminal response committed", phase: PhaseProviderToolLoop, withAttempt: true, withResponse: true, want: RecoveryReranDeterministic, callsBefore: 1},
		{name: "provider timeout receipt committed", phase: PhaseProviderToolLoop, withAttempt: true, withOutcome: aicontract.OutcomeFailed, want: RecoveryObservedTerminal, callsBefore: 1},
		{name: "deterministic preview checkpoint", phase: PhaseDeterministicPreview, withAttempt: true, withResponse: true, withCheckpoint: true, want: RecoveryReusedCheckpoint, callsBefore: 1},
		{name: "patch seal committed before Job seal", phase: PhasePatchSealed, withAttempt: true, withResponse: true, withOutcome: aicontract.OutcomeSucceeded, withCheckpoint: true, withPatch: true, want: RecoveryReusedCheckpoint, callsBefore: 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			state := recoveryState(t, test.phase)
			targetPhase := test.phase
			if targetPhase == PhaseProviderToolLoop {
				targetPhase = PhaseDeterministicPreview
			}
			expected := recoveryIdentity(state, targetPhase)
			snapshot := AIRecoverySnapshot{State: state, Pinned: expected}
			if test.withAttempt {
				attempt := recoveryAttempt(t, state, expected)
				snapshot.Attempt = &attempt
				if test.withResponse {
					response, err := NewTerminalResponseReceipt(state.Job.ID, attempt.AttemptID, attemptLedgerResponse())
					if err != nil {
						t.Fatal(err)
					}
					snapshot.Response = &response
				}
				if test.withOutcome != "" {
					code := ""
					if test.withOutcome != aicontract.OutcomeSucceeded {
						code = "AI_PROVIDER_TIMEOUT"
					}
					outcome, err := NewAttemptOutcomeReceipt(state.Job.ID, attempt.AttemptID, test.withOutcome, code)
					if err != nil {
						t.Fatal(err)
					}
					snapshot.Outcome = &outcome
				}
				if test.withPatch {
					seal, err := NewAttemptPatchSeal(state.Job.ID, attempt.AttemptID, "018f9e40-0000-7000-8000-000000000282", aicontract.Hash(strings.Repeat("d", 64)))
					if err != nil {
						t.Fatal(err)
					}
					snapshot.PatchSeal = &seal
				}
			}
			if test.withCheckpoint {
				checkpoint := DeterministicCheckpoint{DeterministicRecoveryIdentity: expected, OutputHash: aicontract.Hash(strings.Repeat("e", 64))}
				snapshot.Checkpoint = &checkpoint
			}
			repository := &aiRecoveryRepositoryFake{snapshot: snapshot}
			executor := &deterministicRecoveryExecutorFake{}
			var providerCalls atomic.Int32
			providerCalls.Store(test.callsBefore)
			result, err := (AIRecoveryService{Repository: repository, Executor: executor}).Recover(context.Background(), expected)
			if err != nil || result.Action != test.want || providerCalls.Load() != test.callsBefore {
				t.Fatalf("result=%#v provider=%d/%d err=%v", result, providerCalls.Load(), test.callsBefore, err)
			}
			if test.want == RecoveryInterruptedProvider {
				replayed, err := (AIRecoveryService{Repository: repository, Executor: executor}).Recover(context.Background(), expected)
				if err != nil || replayed.Action != RecoveryAlreadyInterrupted || providerCalls.Load() != test.callsBefore {
					t.Fatalf("replayed=%#v provider=%d err=%v", replayed, providerCalls.Load(), err)
				}
			}
		})
	}
}

func TestRecoveryAndExplicitRetryCreateOnlyOneUserAuthorizedNewProviderCall(t *testing.T) {
	service, _, repository, _, _, request := explicitRetryFixture(t)
	var providerCalls atomic.Int32
	providerCalls.Store(1) // the original attempt may have reached the remote Provider
	first, replay, err := service.Retry(context.Background(), request)
	if err != nil || replay || !first.Valid() {
		t.Fatalf("first=%#v replay=%v err=%v", first, replay, err)
	}
	providerCalls.Add(1) // only the user-confirmed retry worker may make this call
	replayed, replay, err := service.Retry(context.Background(), request)
	if err != nil || !replay || replayed.State.Job.ID != first.State.Job.ID || repository.creates != 1 || providerCalls.Load() != 2 {
		t.Fatalf("replayed=%#v replay=%v creates=%d provider=%d err=%v", replayed, replay, repository.creates, providerCalls.Load(), err)
	}
}

package sync

import (
	"context"
	"errors"
	"testing"
)

type recoveryPollerFake struct {
	calls  int
	result PollingResult
	err    error
	order  *[]string
}

func (fake *recoveryPollerFake) Poll(context.Context, PollingRequest) (PollingResult, error) {
	fake.calls++
	*fake.order = append(*fake.order, "poll")
	return fake.result, fake.err
}

type recoveryReconcilerFake struct {
	calls int
	order *[]string
}

func (fake *recoveryReconcilerFake) ReconcileTaskNotFound(_ context.Context, _ ReconciliationRequest, err error) (ReconciliationDecision, error) {
	fake.calls++
	*fake.order = append(*fake.order, "reconcile")
	if !isProviderErrorCode(err, "TASK_NOT_FOUND") {
		return ReconciliationDecision{}, ErrReconciliationInvalid
	}
	return ReconciliationDecision{Resubmit: true}, nil
}

type recoveryResumerFake struct {
	resume, observe int
	order           *[]string
}

func (fake *recoveryResumerFake) ResumeUnsubmittedGraphJob(context.Context, RecoveryWork) error {
	fake.resume++
	*fake.order = append(*fake.order, "resume")
	return nil
}
func (fake *recoveryResumerFake) ObserveRecoveredTask(context.Context, RecoveryWork, PollingResult) error {
	fake.observe++
	*fake.order = append(*fake.order, "observe")
	return nil
}

func graphRecoveryDispatcherFixture(taskID string, pollError error, taskState string) (DurableRecoveryDispatcher, *recoveryPollerFake, *recoveryReconcilerFake, *recoveryResumerFake, *[]string) {
	order := &[]string{}
	poller := &recoveryPollerFake{result: PollingResult{Task: Task{ID: taskID, State: taskState}}, err: pollError, order: order}
	reconciler := &recoveryReconcilerFake{order: order}
	resumer := &recoveryResumerFake{order: order}
	dispatcher := DurableRecoveryDispatcher{
		Requests: RecoveryRequestResolverFunc(func(context.Context, RecoveryWork) (RecoveryRequests, error) { return RecoveryRequests{}, nil }),
		Poller:   poller, Reconciler: reconciler, Resumer: resumer,
	}
	return dispatcher, poller, reconciler, resumer, order
}

func TestGraphRecoveryPollsSavedOriginalTaskBeforeAnyReconciliation(t *testing.T) {
	taskErr := &ProviderError{Code: "TASK_NOT_FOUND", Message: "missing", RequestID: "request-1", Details: map[string]any{}}
	dispatcher, poller, reconciler, resumer, order := graphRecoveryDispatcherFixture("task-1", taskErr, "")
	work := RecoveryWork{State: SyncState{ProviderTaskID: "task-1", ExternalTaskID: "task-1"}}
	if err := dispatcher.ReconcileInterruptedGraphJob(context.Background(), work); err != nil {
		t.Fatal(err)
	}
	if poller.calls != 1 || reconciler.calls != 1 || resumer.resume != 0 || len(*order) != 2 || (*order)[0] != "poll" || (*order)[1] != "reconcile" {
		t.Fatalf("order=%v poll=%d reconcile=%d resume=%d", *order, poller.calls, reconciler.calls, resumer.resume)
	}
}

func TestGraphRecoveryPreservesExactFourTaskStatesAndDoesNotResubmit(t *testing.T) {
	for _, state := range []string{"queued", "running", "succeeded", "failed"} {
		t.Run(state, func(t *testing.T) {
			dispatcher, poller, reconciler, resumer, _ := graphRecoveryDispatcherFixture("task-1", nil, state)
			work := RecoveryWork{State: SyncState{ProviderTaskID: "task-1", ExternalTaskID: "task-1"}}
			if err := dispatcher.ResumeGraphJob(context.Background(), work); err != nil {
				t.Fatal(err)
			}
			if poller.calls != 1 || reconciler.calls != 0 || resumer.observe != 1 || resumer.resume != 0 {
				t.Fatalf("poll=%d reconcile=%d observe=%d resume=%d", poller.calls, reconciler.calls, resumer.observe, resumer.resume)
			}
		})
	}
}

func TestGraphRecoveryRejectsProviderInterruptedAndMismatchedTaskIdentity(t *testing.T) {
	dispatcher, _, _, _, _ := graphRecoveryDispatcherFixture("task-other", nil, "running")
	work := RecoveryWork{State: SyncState{ProviderTaskID: "task-1", ExternalTaskID: "task-1"}}
	if err := dispatcher.ResumeGraphJob(context.Background(), work); !errors.Is(err, ErrRecoveryInvalid) {
		t.Fatalf("err=%v", err)
	}
	dispatcher, _, _, _, _ = graphRecoveryDispatcherFixture("task-1", nil, "interrupted")
	if err := dispatcher.ResumeGraphJob(context.Background(), work); !errors.Is(err, ErrRecoveryInvalid) {
		t.Fatalf("err=%v", err)
	}
}

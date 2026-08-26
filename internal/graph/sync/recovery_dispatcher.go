package sync

import "context"

type RecoveryRequests struct {
	Polling        PollingRequest
	Reconciliation ReconciliationRequest
}

type RecoveryRequestResolver interface {
	ResolveGraphRecovery(context.Context, RecoveryWork) (RecoveryRequests, error)
}

type RecoveryRequestResolverFunc func(context.Context, RecoveryWork) (RecoveryRequests, error)

func (function RecoveryRequestResolverFunc) ResolveGraphRecovery(ctx context.Context, work RecoveryWork) (RecoveryRequests, error) {
	return function(ctx, work)
}

type OriginalTaskPoller interface {
	Poll(context.Context, PollingRequest) (PollingResult, error)
}

type MissingTaskReconciler interface {
	ReconcileTaskNotFound(context.Context, ReconciliationRequest, error) (ReconciliationDecision, error)
}

type GraphRecoveryResumer interface {
	ResumeUnsubmittedGraphJob(context.Context, RecoveryWork) error
	ObserveRecoveredTask(context.Context, RecoveryWork, PollingResult) error
}

// DurableRecoveryDispatcher makes the saved provider Task identity the first
// branch of Graph recovery. Only the stable TASK_NOT_FOUND branch reaches the
// existing reconciliation service, which inspects the exact target Snapshot
// before it can replay the same immutable PUT.
type DurableRecoveryDispatcher struct {
	Requests   RecoveryRequestResolver
	Poller     OriginalTaskPoller
	Reconciler MissingTaskReconciler
	Resumer    GraphRecoveryResumer
}

func (dispatcher DurableRecoveryDispatcher) ResumeGraphJob(ctx context.Context, work RecoveryWork) error {
	if err := dispatcher.valid(); err != nil {
		return err
	}
	if work.State.ProviderTaskID == "" && work.State.ExternalTaskID == "" {
		return dispatcher.Resumer.ResumeUnsubmittedGraphJob(ctx, work)
	}
	if work.State.ProviderTaskID == "" || work.State.ProviderTaskID != work.State.ExternalTaskID {
		return ErrRecoveryInvalid
	}
	return dispatcher.pollOriginal(ctx, work)
}

func (dispatcher DurableRecoveryDispatcher) ReconcileInterruptedGraphJob(ctx context.Context, work RecoveryWork) error {
	if err := dispatcher.valid(); err != nil {
		return err
	}
	if work.State.ProviderTaskID == "" || work.State.ProviderTaskID != work.State.ExternalTaskID {
		return ErrRecoveryInvalid
	}
	return dispatcher.pollOriginal(ctx, work)
}

func (dispatcher DurableRecoveryDispatcher) pollOriginal(ctx context.Context, work RecoveryWork) error {
	requests, err := dispatcher.Requests.ResolveGraphRecovery(ctx, work)
	if err != nil {
		return err
	}
	result, err := dispatcher.Poller.Poll(ctx, requests.Polling)
	if isProviderErrorCode(err, "TASK_NOT_FOUND") {
		_, reconcileErr := dispatcher.Reconciler.ReconcileTaskNotFound(ctx, requests.Reconciliation, err)
		return reconcileErr
	}
	if err != nil {
		return err
	}
	if result.Task.ID != work.State.ProviderTaskID || (result.Task.State != "queued" && result.Task.State != "running" && result.Task.State != "succeeded" && result.Task.State != "failed") {
		return ErrRecoveryInvalid
	}
	return dispatcher.Resumer.ObserveRecoveredTask(ctx, work, result)
}

func (dispatcher DurableRecoveryDispatcher) valid() error {
	if dispatcher.Requests == nil || dispatcher.Poller == nil || dispatcher.Reconciler == nil || dispatcher.Resumer == nil {
		return ErrRecoveryInvalid
	}
	return nil
}

var _ RecoveryDispatcher = DurableRecoveryDispatcher{}
var _ OriginalTaskPoller = PollingService{}
var _ MissingTaskReconciler = ReconciliationService{}

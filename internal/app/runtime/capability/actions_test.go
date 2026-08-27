package capability

import (
	"errors"
	"reflect"
	"testing"
)

func TestDefaultActionRegistryIsClosedAndDeterministic(t *testing.T) {
	descriptors := DefaultActionRegistry().Descriptors()
	ids := make([]string, len(descriptors))
	for index := range descriptors {
		ids[index] = descriptors[index].ID
	}
	want := []string{ActionBackupRetry, ActionBackupSettings, ActionCredentialConfigure, ActionGraphReconnect, ActionGraphRetry, ActionJobCancel, ActionProviderSettings, ActionRuntimeReprobe}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids=%v want=%v", ids, want)
	}
	for _, descriptor := range descriptors {
		if descriptor.URI == "" || descriptor.Method == "DELETE" || descriptor.ID == "process.kill" {
			t.Fatalf("unsafe descriptor=%#v", descriptor)
		}
	}
}

func TestActionResolutionEnforcesServerPreconditionsAndIdempotency(t *testing.T) {
	registry := DefaultActionRegistry()
	if _, err := registry.Resolve(ActionGraphRetry, map[string]bool{"graph_job_retryable": false}, "retry-1"); !errors.Is(err, ErrActionPrecondition) {
		t.Fatalf("precondition err=%v", err)
	}
	if _, err := registry.Resolve(ActionGraphRetry, map[string]bool{"graph_job_retryable": true}, ""); !errors.Is(err, ErrIdempotencyKey) {
		t.Fatalf("key err=%v", err)
	}
	action, err := registry.Resolve(ActionGraphRetry, map[string]bool{"graph_job_retryable": true}, "retry-1")
	if err != nil || action.URI != "/api/v1/revisions/{revision_id}/graph-sync" || !action.IdempotencyRequired {
		t.Fatalf("action=%#v err=%v", action, err)
	}
	if _, err = registry.Resolve(ActionProviderSettings, map[string]bool{"settings_writable": true}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = registry.Resolve(ActionBackupRetry, map[string]bool{"backup_retryable": true}, "backup-retry-1"); err != nil {
		t.Fatal(err)
	}
}

func TestActionRegistryRejectsArbitraryCommandAndProcessActions(t *testing.T) {
	for _, descriptor := range []ActionDescriptor{
		{Action: Action{ID: "process.kill", Method: "POST", URI: "/api/v1/process/kill"}},
		{Action: Action{ID: ActionRuntimeReprobe, Method: "POST", URI: "/api/v1/runtime/command", IdempotencyRequired: true}, Preconditions: []string{"dependency_observed"}},
		{Action: Action{ID: ActionJobCancel, Method: "DELETE", URI: "/api/v1/jobs/{job_id}/cancel", IdempotencyRequired: true}, Preconditions: []string{"job_nonterminal"}},
	} {
		if _, err := NewActionRegistry(descriptor); !errors.Is(err, ErrActionInvalid) {
			t.Fatalf("descriptor=%#v err=%v", descriptor, err)
		}
	}
}

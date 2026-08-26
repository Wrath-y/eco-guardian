package capability

import (
	"errors"
	"regexp"
	"sort"
)

var (
	ErrActionInvalid      = errors.New("runtime action descriptor is invalid")
	ErrActionPrecondition = errors.New("runtime action precondition failed")
	ErrIdempotencyKey     = errors.New("runtime action idempotency key is invalid")
)

const (
	ActionRuntimeReprobe      = "runtime.reprobe"
	ActionGraphReconnect      = "graph.reconnect"
	ActionGraphRetry          = "graph.retry"
	ActionJobCancel           = "job.cancel"
	ActionProviderSettings    = "provider.settings"
	ActionCredentialConfigure = "credential.configure"
)

type ActionDescriptor struct {
	Action
	Preconditions []string
}

type ActionRegistry struct{ descriptors map[string]ActionDescriptor }

var idempotencyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

var closedActions = map[string]ActionDescriptor{
	ActionRuntimeReprobe:      {Action: Action{ID: ActionRuntimeReprobe, Method: "POST", URI: "/api/v1/runtime/reprobe", IdempotencyRequired: true}, Preconditions: []string{"dependency_observed"}},
	ActionGraphReconnect:      {Action: Action{ID: ActionGraphReconnect, Method: "POST", URI: "/api/v1/runtime/reconnect", IdempotencyRequired: true}, Preconditions: []string{"graph_configured"}},
	ActionGraphRetry:          {Action: Action{ID: ActionGraphRetry, Method: "POST", URI: "/api/v1/revisions/{revision_id}/graph-sync", IdempotencyRequired: true}, Preconditions: []string{"graph_job_retryable"}},
	ActionJobCancel:           {Action: Action{ID: ActionJobCancel, Method: "POST", URI: "/api/v1/jobs/{job_id}/cancel", IdempotencyRequired: true}, Preconditions: []string{"job_nonterminal"}},
	ActionProviderSettings:    {Action: Action{ID: ActionProviderSettings, Method: "PATCH", URI: "/api/v1/settings", IdempotencyRequired: false}, Preconditions: []string{"settings_writable"}},
	ActionCredentialConfigure: {Action: Action{ID: ActionCredentialConfigure, Method: "PUT", URI: "/api/v1/settings/credentials/{provider}", IdempotencyRequired: false}, Preconditions: []string{"provider_supported"}},
}

func DefaultActionRegistry() *ActionRegistry {
	descriptors := make([]ActionDescriptor, 0, len(closedActions))
	for _, descriptor := range closedActions {
		descriptors = append(descriptors, descriptor)
	}
	registry, err := NewActionRegistry(descriptors...)
	if err != nil {
		panic(err)
	}
	return registry
}

func NewActionRegistry(descriptors ...ActionDescriptor) (*ActionRegistry, error) {
	registry := &ActionRegistry{descriptors: map[string]ActionDescriptor{}}
	for _, descriptor := range descriptors {
		expected, allowed := closedActions[descriptor.ID]
		if !allowed || descriptor.Method != expected.Method || descriptor.URI != expected.URI || descriptor.IdempotencyRequired != expected.IdempotencyRequired || !sameStrings(descriptor.Preconditions, expected.Preconditions) {
			return nil, ErrActionInvalid
		}
		if _, duplicate := registry.descriptors[descriptor.ID]; duplicate {
			return nil, ErrActionInvalid
		}
		descriptor.Preconditions = append([]string(nil), descriptor.Preconditions...)
		registry.descriptors[descriptor.ID] = descriptor
	}
	return registry, nil
}

func (registry *ActionRegistry) Resolve(id string, preconditions map[string]bool, idempotencyKey string) (Action, error) {
	if registry == nil {
		return Action{}, ErrActionInvalid
	}
	descriptor, ok := registry.descriptors[id]
	if !ok {
		return Action{}, ErrActionInvalid
	}
	for _, precondition := range descriptor.Preconditions {
		if !preconditions[precondition] {
			return Action{}, ErrActionPrecondition
		}
	}
	if descriptor.IdempotencyRequired && !idempotencyPattern.MatchString(idempotencyKey) {
		return Action{}, ErrIdempotencyKey
	}
	return descriptor.Action, nil
}

func (registry *ActionRegistry) Descriptors() []ActionDescriptor {
	if registry == nil {
		return []ActionDescriptor{}
	}
	ids := make([]string, 0, len(registry.descriptors))
	for id := range registry.descriptors {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]ActionDescriptor, 0, len(ids))
	for _, id := range ids {
		value := registry.descriptors[id]
		value.Preconditions = append([]string(nil), value.Preconditions...)
		result = append(result, value)
	}
	return result
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

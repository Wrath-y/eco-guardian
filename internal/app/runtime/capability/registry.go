// Package capability owns the process-wide, read-only capability projection.
// It adapts module observations but never owns their business admission rules.
package capability

import (
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	ErrDescriptorInvalid   = errors.New("capability descriptor is invalid")
	ErrDescriptorDuplicate = errors.New("capability descriptor is duplicated")
	ErrVersionConflict     = errors.New("capability descriptor version conflicts")
	ErrDependencyCycle     = errors.New("capability dependency cycle")
)

type State string

const (
	Available   State = "available"
	Degraded    State = "degraded"
	Unavailable State = "unavailable"
)

type Prerequisite struct {
	CapabilityID string
	Required     bool
}

type Reason struct {
	Code                  string `json:"code"`
	Component             string `json:"component"`
	ObservationGeneration uint64 `json:"observation_generation,omitempty"`
}

type Action struct {
	ID                  string `json:"id"`
	Method              string `json:"method"`
	URI                 string `json:"uri"`
	IdempotencyRequired bool   `json:"idempotency_required"`
}

type Observation struct {
	ID         string
	State      State
	Generation uint64
	ObservedAt time.Time
	Reasons    []Reason
}

type Result struct {
	ID                    string   `json:"id"`
	Version               string   `json:"version"`
	State                 State    `json:"state"`
	Reasons               []Reason `json:"reasons"`
	Actions               []Action `json:"actions"`
	ObservationGeneration uint64   `json:"observation_generation"`
}

type EvaluationContext struct {
	Observations map[string]Observation
	Capabilities map[string]Result
}

type Evaluator func(EvaluationContext) Result

type Descriptor struct {
	ID            string
	Version       string
	Prerequisites []Prerequisite
	Evaluator     Evaluator
}

type Registry struct {
	descriptors map[string]Descriptor
	order       []string
}

var identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
var versionPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){0,2}$`)

func NewRegistry(descriptors ...Descriptor) (*Registry, error) {
	registry := &Registry{descriptors: map[string]Descriptor{}}
	for _, descriptor := range descriptors {
		if !identifierPattern.MatchString(descriptor.ID) || !versionPattern.MatchString(descriptor.Version) || descriptor.Evaluator == nil || !validPrerequisites(descriptor.Prerequisites) {
			return nil, ErrDescriptorInvalid
		}
		if existing, duplicate := registry.descriptors[descriptor.ID]; duplicate {
			if existing.Version != descriptor.Version {
				return nil, ErrVersionConflict
			}
			return nil, ErrDescriptorDuplicate
		}
		descriptor.Prerequisites = append([]Prerequisite(nil), descriptor.Prerequisites...)
		registry.descriptors[descriptor.ID] = descriptor
	}
	order, err := topologicalOrder(registry.descriptors)
	if err != nil {
		return nil, err
	}
	registry.order = order
	return registry, nil
}

func (registry *Registry) Evaluate(observations map[string]Observation) []Result {
	if registry == nil {
		return []Result{}
	}
	detachedObservations := cloneObservations(observations)
	results := map[string]Result{}
	for _, id := range registry.order {
		descriptor := registry.descriptors[id]
		blocked, degraded, prerequisiteReasons, generation := evaluatePrerequisites(descriptor, results)
		context := EvaluationContext{Observations: cloneObservations(detachedObservations), Capabilities: cloneResults(results)}
		result := descriptor.Evaluator(context)
		result.ID, result.Version = descriptor.ID, descriptor.Version
		if !validState(result.State) {
			result.State = Unavailable
			result.Reasons = append(result.Reasons, Reason{Code: "CAPABILITY_EVALUATOR_INVALID", Component: descriptor.ID})
		}
		if blocked {
			result.State = Unavailable
		} else if degraded && result.State == Available {
			result.State = Degraded
		}
		result.Reasons = append(result.Reasons, prerequisiteReasons...)
		result.ObservationGeneration = maxGeneration(result.ObservationGeneration, generation, observationGeneration(result.Reasons))
		result = normalizeResult(result)
		results[id] = result
	}
	ordered := make([]Result, 0, len(results))
	ids := make([]string, 0, len(results))
	for id := range results {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		ordered = append(ordered, cloneResult(results[id]))
	}
	return ordered
}

func AvailableResult() Result {
	return Result{State: Available, Reasons: []Reason{}, Actions: []Action{}}
}

func validPrerequisites(values []Prerequisite) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if !identifierPattern.MatchString(value.CapabilityID) || seen[value.CapabilityID] {
			return false
		}
		seen[value.CapabilityID] = true
	}
	return true
}

func topologicalOrder(descriptors map[string]Descriptor) ([]string, error) {
	state := map[string]uint8{}
	order := []string{}
	ids := make([]string, 0, len(descriptors))
	for id := range descriptors {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 1 {
			return ErrDependencyCycle
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		prerequisites := append([]Prerequisite(nil), descriptors[id].Prerequisites...)
		sort.Slice(prerequisites, func(left, right int) bool {
			return prerequisites[left].CapabilityID < prerequisites[right].CapabilityID
		})
		for _, prerequisite := range prerequisites {
			if _, registered := descriptors[prerequisite.CapabilityID]; registered {
				if err := visit(prerequisite.CapabilityID); err != nil {
					return err
				}
			}
		}
		state[id] = 2
		order = append(order, id)
		return nil
	}
	for _, id := range ids {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return order, nil
}

func evaluatePrerequisites(descriptor Descriptor, results map[string]Result) (blocked, degraded bool, reasons []Reason, generation uint64) {
	for _, prerequisite := range descriptor.Prerequisites {
		result, registered := results[prerequisite.CapabilityID]
		if !registered {
			reasons = append(reasons, Reason{Code: "CAPABILITY_UNREGISTERED", Component: prerequisite.CapabilityID})
			if prerequisite.Required {
				blocked = true
			} else {
				degraded = true
			}
			continue
		}
		generation = maxGeneration(generation, result.ObservationGeneration)
		if result.State == Unavailable {
			code := "OPTIONAL_CAPABILITY_UNAVAILABLE"
			if prerequisite.Required {
				code = "REQUIRED_CAPABILITY_UNAVAILABLE"
				blocked = true
			} else {
				degraded = true
			}
			reasons = append(reasons, Reason{Code: code, Component: prerequisite.CapabilityID, ObservationGeneration: result.ObservationGeneration})
		} else if result.State == Degraded {
			degraded = true
			reasons = append(reasons, Reason{Code: "CAPABILITY_DEGRADED", Component: prerequisite.CapabilityID, ObservationGeneration: result.ObservationGeneration})
		}
	}
	return blocked, degraded, reasons, generation
}

func normalizeResult(value Result) Result {
	value.Reasons = append([]Reason(nil), value.Reasons...)
	sort.Slice(value.Reasons, func(left, right int) bool {
		if value.Reasons[left].Code != value.Reasons[right].Code {
			return value.Reasons[left].Code < value.Reasons[right].Code
		}
		if value.Reasons[left].Component != value.Reasons[right].Component {
			return value.Reasons[left].Component < value.Reasons[right].Component
		}
		return value.Reasons[left].ObservationGeneration < value.Reasons[right].ObservationGeneration
	})
	value.Actions = append([]Action(nil), value.Actions...)
	sort.Slice(value.Actions, func(left, right int) bool { return value.Actions[left].ID < value.Actions[right].ID })
	return value
}

func validState(value State) bool {
	return value == Available || value == Degraded || value == Unavailable
}
func maxGeneration(values ...uint64) uint64 {
	var result uint64
	for _, value := range values {
		if value > result {
			result = value
		}
	}
	return result
}
func observationGeneration(reasons []Reason) uint64 {
	var result uint64
	for _, reason := range reasons {
		result = maxGeneration(result, reason.ObservationGeneration)
	}
	return result
}

func cloneObservations(values map[string]Observation) map[string]Observation {
	result := make(map[string]Observation, len(values))
	for id, value := range values {
		value.Reasons = append([]Reason(nil), value.Reasons...)
		result[id] = value
	}
	return result
}
func cloneResults(values map[string]Result) map[string]Result {
	result := make(map[string]Result, len(values))
	for id, value := range values {
		result[id] = cloneResult(value)
	}
	return result
}
func cloneResult(value Result) Result {
	value.Reasons = append([]Reason(nil), value.Reasons...)
	value.Actions = append([]Action(nil), value.Actions...)
	return value
}

func SafeReason(code, component string, generation uint64) Reason {
	if !identifierPattern.MatchString(strings.ToLower(strings.ReplaceAll(code, "_", "."))) || !identifierPattern.MatchString(component) {
		return Reason{Code: "CAPABILITY_REASON_INVALID", Component: "runtime"}
	}
	return Reason{Code: code, Component: component, ObservationGeneration: generation}
}

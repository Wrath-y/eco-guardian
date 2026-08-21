package metric

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/zouyi/eco-guardian/internal/formula"
)

type Direction string

const (
	HigherIsRisk Direction = "higher_is_risk"
	LowerIsRisk  Direction = "lower_is_risk"
	TargetRange  Direction = "target_range"
)

type Status string

const (
	Available   Status = "available"
	Unavailable Status = "unavailable"
)

type Descriptor struct {
	ID                   string
	Version              string
	RequiredObservations []string
	Unit                 string
	Direction            Direction
	AbsoluteThreshold    *formula.Decimal
	AggregationVersion   string
	ConfidenceVersion    string
	Assumptions          []string
}

func (descriptor Descriptor) Valid() bool {
	if strings.TrimSpace(descriptor.ID) == "" || strings.TrimSpace(descriptor.Version) == "" || strings.TrimSpace(descriptor.Unit) == "" || strings.TrimSpace(descriptor.AggregationVersion) == "" || strings.TrimSpace(descriptor.ConfidenceVersion) == "" {
		return false
	}
	if descriptor.Direction != HigherIsRisk && descriptor.Direction != LowerIsRisk && descriptor.Direction != TargetRange {
		return false
	}
	seen := map[string]struct{}{}
	for _, observation := range descriptor.RequiredObservations {
		if strings.TrimSpace(observation) == "" {
			return false
		}
		if _, duplicate := seen[observation]; duplicate {
			return false
		}
		seen[observation] = struct{}{}
	}
	return true
}

type Observation struct {
	ID    string
	Value formula.Decimal
	Unit  string
}
type Sample struct {
	Ordinal      uint64
	Observations []Observation
}
type UnavailableReason struct {
	Code    string
	Missing []string
	Message string
}

func (reason UnavailableReason) Valid() bool {
	return strings.TrimSpace(reason.Code) != "" && strings.TrimSpace(reason.Message) != "" && len(reason.Missing) > 0
}

type Result struct {
	Descriptor  Descriptor
	Status      Status
	Value       *formula.Decimal
	Unavailable *UnavailableReason
}

func (result Result) Valid() bool {
	return result.Descriptor.Valid() && ((result.Status == Available && result.Value != nil && result.Unavailable == nil) || (result.Status == Unavailable && result.Value == nil && result.Unavailable != nil && result.Unavailable.Valid()))
}

type Module interface {
	Descriptor() Descriptor
	Evaluate(Sample) (Result, error)
}
type Registry struct{ modules map[string]Module }

var ErrMetricRegistryInvalid = errors.New("simulation metric registry is invalid")

func NewRegistry(modules []Module) (*Registry, error) {
	registry := &Registry{modules: map[string]Module{}}
	for _, module := range modules {
		if module == nil || !module.Descriptor().Valid() {
			return nil, ErrMetricRegistryInvalid
		}
		if _, duplicate := registry.modules[module.Descriptor().ID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate metric %q", ErrMetricRegistryInvalid, module.Descriptor().ID)
		}
		registry.modules[module.Descriptor().ID] = module
	}
	if len(registry.modules) == 0 {
		return nil, ErrMetricRegistryInvalid
	}
	return registry, nil
}
func (registry *Registry) Module(id string) (Module, bool) {
	module, found := registry.modules[id]
	return module, found
}
func (registry *Registry) Descriptors() []Descriptor {
	descriptors := make([]Descriptor, 0, len(registry.modules))
	for _, module := range registry.modules {
		descriptors = append(descriptors, module.Descriptor())
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].ID < descriptors[j].ID })
	return descriptors
}

func MissingResult(descriptor Descriptor, missing []string, code, message string) Result {
	copyMissing := append([]string(nil), missing...)
	sort.Strings(copyMissing)
	return Result{Descriptor: descriptor, Status: Unavailable, Unavailable: &UnavailableReason{Code: code, Missing: copyMissing, Message: message}}
}

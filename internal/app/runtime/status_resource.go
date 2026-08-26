package runtime

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zouyi/eco-guardian/internal/app/runtime/capability"
	"github.com/zouyi/eco-guardian/internal/app/runtime/graphprocess"
	"github.com/zouyi/eco-guardian/internal/buildinfo"
)

var ErrRuntimeStatusInvalid = errors.New("runtime status resource is invalid")

// StatusResource is the application-owned, immutable input to the HTTP DTO.
// It contains only bounded, display-safe summaries. In particular, process
// handles, diagnostic PIDs, filesystem paths, child output, provider payloads,
// credentials, and project business content have no representation here.
type StatusResource struct {
	SchemaVersion uint64
	Generation    uint64
	Build         BuildIdentity
	ListenerURL   string
	Phase         Phase
	Project       ProjectStatus
	Process       ProcessStatus
	Dependencies  []DependencyStatus
	Capabilities  []capability.Result
	Recovery      []RecoveryStatus
	LogLocation   string
	UpdatedAt     time.Time
}

type BuildIdentity struct {
	Version, Build, Commit string
	PackageMode            buildinfo.PackageMode
}

type ProjectStatus struct {
	State            string
	ProjectID        string
	RecentCount      int
	RecoveryRequired bool
}

type ProcessStatus struct {
	Ownership      string
	State          string
	Generation     uint64
	Endpoint       string
	RestartAttempt int
	Reason         string
}

type DependencyStatus struct {
	ID         string
	State      string
	Generation uint64
	ObservedAt time.Time
	ExpiresAt  time.Time
	Reasons    []capability.Reason
}

type RecoveryStatus struct {
	JobKind string
	State   string
	Count   int
}

type StatusResourceInput struct {
	Lifecycle    StatusSnapshot
	Build        buildinfo.Info
	Project      ProjectStatus
	Process      graphprocess.ProcessObservation
	Dependencies []DependencyStatus
	Capabilities []capability.Result
	Recovery     []RecoveryStatus
	LogLocation  string
}

// StatusAssembler publishes the whole resource under one lock. Readers never
// combine independently changing module values and always receive detached
// slices, so repeated GETs cannot mutate or trigger the underlying modules.
type StatusAssembler struct {
	mu       sync.RWMutex
	snapshot StatusResource
}

func NewStatusAssembler(input StatusResourceInput) (*StatusAssembler, error) {
	assembler := &StatusAssembler{}
	if _, err := assembler.Publish(input); err != nil {
		return nil, err
	}
	return assembler, nil
}

func (assembler *StatusAssembler) Publish(input StatusResourceInput) (StatusResource, error) {
	if assembler == nil || !input.Lifecycle.Phase.Valid() || input.Build.Version == "" || input.Build.Build == "" || input.Build.Commit == "" || !input.Build.PackageMode.Valid() || input.LogLocation == "" || len(input.LogLocation) > 256 {
		return StatusResource{}, ErrRuntimeStatusInvalid
	}
	value := StatusResource{
		SchemaVersion: 1,
		Generation:    input.Lifecycle.Generation,
		Build:         BuildIdentity{Version: input.Build.Version, Build: input.Build.Build, Commit: input.Build.Commit, PackageMode: input.Build.PackageMode},
		ListenerURL:   input.Lifecycle.ListenerURL,
		Phase:         input.Lifecycle.Phase,
		Project:       input.Project,
		Process:       ProcessStatus{Ownership: string(input.Process.Ownership), State: string(input.Process.State), Generation: uint64(input.Process.Generation), Endpoint: input.Process.Endpoint, RestartAttempt: input.Process.Attempt, Reason: input.Process.Reason},
		Dependencies:  append([]DependencyStatus(nil), input.Dependencies...),
		Capabilities:  append([]capability.Result(nil), input.Capabilities...),
		Recovery:      append([]RecoveryStatus(nil), input.Recovery...),
		LogLocation:   input.LogLocation,
		UpdatedAt:     input.Lifecycle.UpdatedAt.UTC(),
	}
	if value.Project.State == "" {
		value.Project.State = "none"
	}
	if value.Process.Ownership == "" {
		value.Process.Ownership = "not_selected"
	}
	if value.Process.State == "" {
		value.Process.State = "not_selected"
	}
	if value.UpdatedAt.IsZero() || value.Project.RecentCount < 0 || value.Project.RecentCount > 100 || value.Process.RestartAttempt < 0 || value.Process.RestartAttempt > 100 || strings.ContainsAny(value.Process.Reason, "\r\n\x00") || len(value.Process.Reason) > 128 {
		return StatusResource{}, ErrRuntimeStatusInvalid
	}
	for index := range value.Dependencies {
		if !value.Dependencies[index].ExpiresAt.IsZero() && !value.UpdatedAt.Before(value.Dependencies[index].ExpiresAt) {
			value.Dependencies[index].State = "unknown"
			value.Dependencies[index].Reasons = append(value.Dependencies[index].Reasons, capability.Reason{Code: "OBSERVATION_STALE", Component: value.Dependencies[index].ID, ObservationGeneration: value.Dependencies[index].Generation})
		}
		value.Dependencies[index].Reasons = append([]capability.Reason(nil), value.Dependencies[index].Reasons...)
		sort.Slice(value.Dependencies[index].Reasons, func(left, right int) bool {
			return reasonLess(value.Dependencies[index].Reasons[left], value.Dependencies[index].Reasons[right])
		})
	}
	for index := range value.Capabilities {
		value.Capabilities[index].Reasons = append([]capability.Reason(nil), value.Capabilities[index].Reasons...)
		value.Capabilities[index].Actions = append([]capability.Action(nil), value.Capabilities[index].Actions...)
	}
	sort.Slice(value.Dependencies, func(left, right int) bool { return value.Dependencies[left].ID < value.Dependencies[right].ID })
	sort.Slice(value.Capabilities, func(left, right int) bool { return value.Capabilities[left].ID < value.Capabilities[right].ID })
	sort.Slice(value.Recovery, func(left, right int) bool {
		if value.Recovery[left].JobKind != value.Recovery[right].JobKind {
			return value.Recovery[left].JobKind < value.Recovery[right].JobKind
		}
		return value.Recovery[left].State < value.Recovery[right].State
	})
	value = value.clone()
	assembler.mu.Lock()
	assembler.snapshot = value
	assembler.mu.Unlock()
	return value.clone(), nil
}

func (assembler *StatusAssembler) Snapshot() StatusResource {
	if assembler == nil {
		return StatusResource{}
	}
	assembler.mu.RLock()
	defer assembler.mu.RUnlock()
	return assembler.snapshot.clone()
}

func (value StatusResource) clone() StatusResource {
	value.Dependencies = append([]DependencyStatus(nil), value.Dependencies...)
	for index := range value.Dependencies {
		value.Dependencies[index].Reasons = append([]capability.Reason(nil), value.Dependencies[index].Reasons...)
	}
	value.Capabilities = append([]capability.Result(nil), value.Capabilities...)
	for index := range value.Capabilities {
		value.Capabilities[index].Reasons = append([]capability.Reason(nil), value.Capabilities[index].Reasons...)
		value.Capabilities[index].Actions = append([]capability.Action(nil), value.Capabilities[index].Actions...)
	}
	value.Recovery = append([]RecoveryStatus(nil), value.Recovery...)
	return value
}

func reasonLess(left, right capability.Reason) bool {
	if left.Code != right.Code {
		return left.Code < right.Code
	}
	if left.Component != right.Component {
		return left.Component < right.Component
	}
	return left.ObservationGeneration < right.ObservationGeneration
}

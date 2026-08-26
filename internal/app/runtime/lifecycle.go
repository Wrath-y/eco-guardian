package runtime

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrRuntimePhaseInvalid      = errors.New("runtime phase is invalid")
	ErrRuntimeTransitionInvalid = errors.New("runtime phase transition is invalid")
)

type Phase string

const (
	PhaseLoadingSettings      Phase = "loading_settings"
	PhaseVerifyingPackage     Phase = "verifying_package"
	PhaseBindingHTTP          Phase = "binding_http"
	PhaseStartingDependencies Phase = "starting_dependencies"
	PhaseRecovering           Phase = "recovering"
	PhaseOpeningRecentProject Phase = "opening_recent_project"
	PhaseReady                Phase = "ready"
	PhaseDegraded             Phase = "degraded"
	PhaseStopping             Phase = "stopping"
	PhaseStopped              Phase = "stopped"
)

func (p Phase) Valid() bool {
	switch p {
	case PhaseLoadingSettings, PhaseVerifyingPackage, PhaseBindingHTTP, PhaseStartingDependencies, PhaseRecovering, PhaseOpeningRecentProject, PhaseReady, PhaseDegraded, PhaseStopping, PhaseStopped:
		return true
	default:
		return false
	}
}

type RuntimeReason struct {
	Code      string `json:"code"`
	Component string `json:"component"`
	Message   string `json:"message"`
}

type RuntimeObservation struct {
	ID         string    `json:"id"`
	State      string    `json:"state"`
	Generation uint64    `json:"generation"`
	ObservedAt time.Time `json:"observed_at"`
}

// StatusSnapshot is an immutable process-wide observation. Every accessor and
// observer receives detached slices so publication cannot be mutated by a
// handler, UI projection, or module callback.
type StatusSnapshot struct {
	SchemaVersion uint64               `json:"schema_version"`
	Generation    uint64               `json:"generation"`
	Phase         Phase                `json:"phase"`
	ListenerURL   string               `json:"listener_url,omitempty"`
	Reasons       []RuntimeReason      `json:"reasons"`
	Observations  []RuntimeObservation `json:"observations"`
	UpdatedAt     time.Time            `json:"updated_at"`
}

func (s StatusSnapshot) clone() StatusSnapshot {
	s.Reasons = append([]RuntimeReason(nil), s.Reasons...)
	s.Observations = append([]RuntimeObservation(nil), s.Observations...)
	return s
}

type RuntimeClock func() time.Time

type StatusStore struct {
	mu          sync.RWMutex
	now         RuntimeClock
	snapshot    StatusSnapshot
	nextID      uint64
	subscribers map[uint64]chan StatusSnapshot
}

func NewStatusStore(now RuntimeClock) *StatusStore {
	if now == nil {
		now = time.Now
	}
	return &StatusStore{
		now: now, snapshot: StatusSnapshot{SchemaVersion: 1, Phase: PhaseLoadingSettings, Reasons: []RuntimeReason{}, Observations: []RuntimeObservation{}, UpdatedAt: now().UTC()},
		subscribers: map[uint64]chan StatusSnapshot{},
	}
}

func (s *StatusStore) Snapshot() StatusSnapshot {
	if s == nil {
		return StatusSnapshot{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot.clone()
}

// Transition validates lifecycle direction, increments generation exactly
// once, and publishes the complete detached snapshot to bounded observers.
func (s *StatusStore) Transition(next Phase, update func(*StatusSnapshot)) (StatusSnapshot, error) {
	if s == nil || !next.Valid() {
		return StatusSnapshot{}, ErrRuntimePhaseInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !canTransition(s.snapshot.Phase, next) {
		return StatusSnapshot{}, ErrRuntimeTransitionInvalid
	}
	value := s.snapshot.clone()
	value.Generation++
	value.Phase = next
	value.UpdatedAt = s.now().UTC()
	if update != nil {
		update(&value)
	}
	value.Reasons = append([]RuntimeReason(nil), value.Reasons...)
	value.Observations = append([]RuntimeObservation(nil), value.Observations...)
	s.snapshot = value
	for _, subscriber := range s.subscribers {
		publishLatest(subscriber, value.clone())
	}
	return value.clone(), nil
}

func canTransition(current, next Phase) bool {
	if current == next && (current == PhaseReady || current == PhaseDegraded || current == PhaseStopping) {
		return true
	}
	if next == PhaseStopping {
		return current != PhaseStopped
	}
	if current == PhaseStopping {
		return next == PhaseStopped
	}
	if current == PhaseStopped {
		return false
	}
	if current == PhaseReady || current == PhaseDegraded {
		return next == PhaseReady || next == PhaseDegraded
	}
	order := map[Phase]int{
		PhaseLoadingSettings: 0, PhaseVerifyingPackage: 1, PhaseBindingHTTP: 2,
		PhaseStartingDependencies: 3, PhaseRecovering: 4, PhaseOpeningRecentProject: 5,
		PhaseReady: 6, PhaseDegraded: 6,
	}
	currentOrder, currentOK := order[current]
	nextOrder, nextOK := order[next]
	return currentOK && nextOK && nextOrder == currentOrder+1
}

// Subscribe immediately publishes the current snapshot and then only the
// latest generation. A slow observer cannot block runtime convergence.
func (s *StatusStore) Subscribe() (uint64, <-chan StatusSnapshot) {
	if s == nil {
		closed := make(chan StatusSnapshot)
		close(closed)
		return 0, closed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := s.nextID
	updates := make(chan StatusSnapshot, 1)
	updates <- s.snapshot.clone()
	s.subscribers[id] = updates
	return id, updates
}

func (s *StatusStore) Unsubscribe(id uint64) {
	if s == nil || id == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if updates, ok := s.subscribers[id]; ok {
		delete(s.subscribers, id)
		close(updates)
	}
}

func publishLatest(updates chan StatusSnapshot, value StatusSnapshot) {
	select {
	case updates <- value:
		return
	default:
	}
	select {
	case <-updates:
	default:
	}
	select {
	case updates <- value:
	default:
	}
}

// The coordinator depends only on process-level ports. Concrete HTTP,
// platform, project, package, and module implementations are composition-root
// concerns and cannot leak repositories or handlers into this package.
type SettingsStartupPort interface{ LoadSettings(context.Context) error }
type PackageStartupPort interface{ VerifyPackage(context.Context) error }
type HostStartupPort interface {
	AcquireListener(context.Context) (string, error)
	StartHost(context.Context) error
	WaitReachable(context.Context) error
	StopHost(context.Context) error
}
type DependencyStartupPort interface {
	StartDependencies(context.Context) error
	StopDependencies(context.Context) error
}
type RecoveryStartupPort interface{ Recover(context.Context) error }
type RecentProjectStartupPort interface{ OpenRecentProject(context.Context) error }
type BrowserStartupPort interface {
	OpenBrowser(context.Context, string) error
}

type CapabilityConvergence struct {
	Degraded     bool
	Reasons      []RuntimeReason
	Observations []RuntimeObservation
}

type CapabilityStartupPort interface {
	ConvergeCapabilities(context.Context) (CapabilityConvergence, error)
}

type LifecyclePorts struct {
	Settings     SettingsStartupPort
	Package      PackageStartupPort
	Host         HostStartupPort
	Dependencies DependencyStartupPort
	Recovery     RecoveryStartupPort
	Projects     RecentProjectStartupPort
	Capabilities CapabilityStartupPort
	Browser      BrowserStartupPort
}

type Coordinator struct {
	Status  *StatusStore
	Ports   LifecyclePorts
	mu      sync.Mutex
	started bool
}

func NewCoordinator(status *StatusStore, ports LifecyclePorts) (*Coordinator, error) {
	if status == nil || ports.Settings == nil || ports.Package == nil || ports.Host == nil {
		return nil, errors.New("runtime coordinator requires settings, package, host, and status ports")
	}
	return &Coordinator{Status: status, Ports: ports}, nil
}

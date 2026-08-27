// Package project owns selection capabilities and the single-active-project
// state machine. Its core only depends on the ports defined in this package.
package project

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
)

var (
	ErrInvalidSelection = errors.New("invalid or expired project selection")
	ErrActiveProject    = errors.New("an active project must be closed first")
	ErrProjectLocked    = errors.New("project is locked by another process")
	ErrCloseBlocked     = errors.New("project close is blocked")
	ErrMaintenance      = errors.New("project is in exclusive maintenance")
)

type Clock interface{ Now() time.Time }
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

type TokenStore interface {
	Issue(directory string) (token string, expiresAt time.Time, err error)
	Consume(token string) (directory string, err error)
}
type DirectorySelector interface {
	SelectDirectory(context.Context) (string, error)
}
type Lock interface{ Release() error }
type Locker interface {
	Acquire(directory string) (Lock, error)
}
type ProjectHandle interface {
	Close() error
	ID() domain.ID
}
type ProjectFactory interface {
	Create(context.Context, string) (ProjectHandle, error)
	Open(context.Context, string) (ProjectHandle, error)
}
type CloseGuard interface{ Preflight(context.Context) error }
type RecentProjects interface {
	Record(ProjectInfo) error
	List() ([]ProjectInfo, error)
}

type ProjectInfo struct {
	ID   domain.ID `json:"id"`
	Name string    `json:"name"`
	Path string    `json:"-"`
}
type ActiveProject struct {
	ProjectInfo
	handle ProjectHandle
	lock   Lock
}

type memoryTokens struct {
	mu     sync.Mutex
	ttl    time.Duration
	clock  Clock
	values map[string]tokenValue
}
type tokenValue struct {
	directory string
	expires   time.Time
}

func NewTokenStore(ttl time.Duration, clock Clock) TokenStore {
	if clock == nil {
		clock = realClock{}
	}
	return &memoryTokens{ttl: ttl, clock: clock, values: map[string]tokenValue{}}
}
func (s *memoryTokens) Issue(directory string) (string, time.Time, error) {
	clean, err := filepath.Abs(filepath.Clean(directory))
	if err != nil {
		return "", time.Time{}, err
	}
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	expires := s.clock.Now().Add(s.ttl)
	s.mu.Lock()
	s.values[token] = tokenValue{clean, expires}
	s.mu.Unlock()
	return token, expires, nil
}
func (s *memoryTokens) Consume(token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[token]
	delete(s.values, token)
	if !ok || !s.clock.Now().Before(v.expires) {
		return "", ErrInvalidSelection
	}
	return v.directory, nil
}

type Manager struct {
	mu          sync.Mutex
	tokens      TokenStore
	locker      Locker
	factory     ProjectFactory
	guard       CloseGuard
	recent      RecentProjects
	active      *ActiveProject
	maintenance *maintenanceState
}

func NewManager(tokens TokenStore, locker Locker, factory ProjectFactory, guard CloseGuard, recent RecentProjects) *Manager {
	return &Manager{tokens: tokens, locker: locker, factory: factory, guard: guard, recent: recent}
}
func (m *Manager) IssueSelection(ctx context.Context, selector DirectorySelector) (string, time.Time, error) {
	directory, err := selector.SelectDirectory(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	return m.tokens.Issue(directory)
}
func (m *Manager) Create(ctx context.Context, token string) (ProjectInfo, error) {
	return m.install(ctx, token, true)
}
func (m *Manager) Open(ctx context.Context, token string) (ProjectInfo, error) {
	return m.install(ctx, token, false)
}

// Recent returns UI metadata only. The stored path is deliberately never
// serialized to the browser.
func (m *Manager) Recent() ([]ProjectInfo, error) {
	if m.recent == nil {
		return []ProjectInfo{}, nil
	}
	return m.recent.List()
}

// OpenRecent resolves a server-owned recent-project identifier to its saved
// path before entering the normal token-gated open flow. This keeps local paths
// out of the HTTP contract while retaining a useful recent-project shortcut.
func (m *Manager) OpenRecent(ctx context.Context, id domain.ID) (ProjectInfo, error) {
	if m.recent == nil {
		return ProjectInfo{}, ErrInvalidSelection
	}
	values, err := m.recent.List()
	if err != nil {
		return ProjectInfo{}, err
	}
	for _, value := range values {
		if value.ID == id {
			token, _, issueErr := m.tokens.Issue(value.Path)
			if issueErr != nil {
				return ProjectInfo{}, issueErr
			}
			return m.Open(ctx, token)
		}
	}
	return ProjectInfo{}, ErrInvalidSelection
}
func (m *Manager) install(ctx context.Context, token string, create bool) (ProjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active != nil || m.maintenance != nil {
		return ProjectInfo{}, ErrActiveProject
	}
	directory, err := m.tokens.Consume(token)
	if err != nil {
		return ProjectInfo{}, err
	}
	lock, err := m.locker.Acquire(directory)
	if err != nil {
		return ProjectInfo{}, err
	}
	var handle ProjectHandle
	if create {
		handle, err = m.factory.Create(ctx, directory)
	} else {
		handle, err = m.factory.Open(ctx, directory)
	}
	if err != nil {
		_ = lock.Release()
		return ProjectInfo{}, err
	}
	info := ProjectInfo{ID: handle.ID(), Name: filepath.Base(directory), Path: directory}
	m.active = &ActiveProject{ProjectInfo: info, handle: handle, lock: lock}
	if m.recent != nil {
		if err := m.recent.Record(info); err != nil {
			_ = handle.Close()
			_ = lock.Release()
			m.active = nil
			return ProjectInfo{}, err
		}
	}
	return info, nil
}
func (m *Manager) Current() (ProjectInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return ProjectInfo{}, false
	}
	return m.active.ProjectInfo, true
}
func (m *Manager) ActiveHandle() (ProjectHandle, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil || m.maintenance != nil || m.active.handle == nil {
		return nil, false
	}
	return m.active.handle, true
}

// MaintenanceState is a read-only projection for runtime admission UI. It
// exposes no path or handle and remains true while the active handle is
// deliberately closed for restore replacement.
func (m *Manager) MaintenanceState() (active, nonInterruptible bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.maintenance == nil {
		return false, false
	}
	return true, m.maintenance.nonInterruptible
}

// RestoredTargetMaintenance identifies the no-active-project adoption window
// without exposing its path or lock.
func (m *Manager) RestoredTargetMaintenance() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maintenance != nil && m.maintenance.restoredTarget
}
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return nil
	}
	if m.maintenance != nil {
		return ErrMaintenance
	}
	if m.guard != nil {
		if err := m.guard.Preflight(ctx); err != nil {
			return err
		}
	}
	active := m.active
	if err := active.handle.Close(); err != nil {
		return err
	}
	if err := active.lock.Release(); err != nil {
		return err
	}
	m.active = nil
	return nil
}

type maintenanceState struct {
	projectID        domain.ID
	nonInterruptible bool
	restoredTarget   bool
}

// MaintenanceSession preserves the active OS lock while allowing the owning
// restore coordinator to close and reopen the database handle exactly once.
type MaintenanceSession struct {
	manager   *Manager
	projectID domain.ID
	path      string
	closed    bool
}

func (m *Manager) AcquireMaintenance(ctx context.Context, projectID domain.ID) (*MaintenanceSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil || m.active.ID != projectID || m.active.handle == nil || m.maintenance != nil {
		return nil, ErrMaintenance
	}
	if m.guard != nil {
		if err := m.guard.Preflight(ctx); err != nil {
			return nil, err
		}
	}
	m.maintenance = &maintenanceState{projectID: projectID}
	return &MaintenanceSession{manager: m, projectID: projectID, path: m.active.Path}, nil
}

// AcquireRestoredTarget transfers an already validated and exclusively held
// empty-directory lock into the single active-project lifecycle. The project
// is not registered or exposed as active until Reopen verifies its UUID.
func (m *Manager) AcquireRestoredTarget(ctx context.Context, projectID domain.ID, path string, lock Lock) (*MaintenanceSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !projectID.Valid() || path == "" || lock == nil {
		return nil, ErrMaintenance
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active != nil || m.maintenance != nil {
		return nil, ErrMaintenance
	}
	info := ProjectInfo{ID: projectID, Name: filepath.Base(path), Path: path}
	m.active = &ActiveProject{ProjectInfo: info, lock: lock}
	m.maintenance = &maintenanceState{projectID: projectID, restoredTarget: true}
	return &MaintenanceSession{manager: m, projectID: projectID, path: path}, nil
}

func (session *MaintenanceSession) ProjectID() domain.ID { return session.projectID }
func (session *MaintenanceSession) Path() string         { return session.path }

func (session *MaintenanceSession) NonInterruptible() bool {
	if session == nil || session.manager == nil {
		return false
	}
	session.manager.mu.Lock()
	defer session.manager.mu.Unlock()
	return session.manager.maintenance != nil && session.manager.maintenance.projectID == session.projectID && session.manager.maintenance.nonInterruptible
}

func (session *MaintenanceSession) MarkNonInterruptible() error {
	if session == nil || session.manager == nil {
		return ErrMaintenance
	}
	session.manager.mu.Lock()
	defer session.manager.mu.Unlock()
	if session.manager.maintenance == nil || session.manager.maintenance.projectID != session.projectID {
		return ErrMaintenance
	}
	session.manager.maintenance.nonInterruptible = true
	return nil
}

func (session *MaintenanceSession) CloseConnections(ctx context.Context) error {
	if session == nil || session.manager == nil {
		return ErrMaintenance
	}
	session.manager.mu.Lock()
	defer session.manager.mu.Unlock()
	if session.manager.maintenance == nil || session.manager.maintenance.projectID != session.projectID || session.manager.active == nil {
		return ErrMaintenance
	}
	if session.manager.active.handle == nil && session.manager.maintenance.restoredTarget {
		return nil
	}
	if session.manager.active.handle == nil {
		return ErrMaintenance
	}
	if preparer, ok := session.manager.active.handle.(interface{ PrepareRestoreClose(context.Context) error }); ok {
		if err := preparer.PrepareRestoreClose(ctx); err != nil {
			return err
		}
	}
	if err := session.manager.active.handle.Close(); err != nil {
		return err
	}
	session.manager.active.handle = nil
	return nil
}

func (session *MaintenanceSession) Reopen(ctx context.Context) (ProjectHandle, error) {
	if session == nil || session.manager == nil {
		return nil, ErrMaintenance
	}
	session.manager.mu.Lock()
	defer session.manager.mu.Unlock()
	if session.manager.maintenance == nil || session.manager.maintenance.projectID != session.projectID || session.manager.active == nil || session.manager.active.handle != nil {
		return nil, ErrMaintenance
	}
	handle, err := session.manager.factory.Open(ctx, session.path)
	if err != nil {
		return nil, err
	}
	if handle.ID() != session.projectID {
		_ = handle.Close()
		return nil, ErrMaintenance
	}
	if session.manager.maintenance.restoredTarget && session.manager.recent != nil {
		if err = session.manager.recent.Record(session.manager.active.ProjectInfo); err != nil {
			_ = handle.Close()
			return nil, err
		}
	}
	session.manager.active.handle = handle
	return handle, nil
}

func (session *MaintenanceSession) Release() error {
	if session == nil || session.manager == nil || session.closed {
		return nil
	}
	session.manager.mu.Lock()
	defer session.manager.mu.Unlock()
	if session.manager.maintenance == nil || session.manager.maintenance.projectID != session.projectID || session.manager.active == nil {
		return ErrMaintenance
	}
	if session.manager.active.handle == nil {
		if !session.manager.maintenance.restoredTarget {
			return ErrMaintenance
		}
		if err := session.manager.active.lock.Release(); err != nil {
			return err
		}
		session.manager.active = nil
		session.manager.maintenance = nil
		session.closed = true
		return nil
	}
	session.manager.maintenance = nil
	session.closed = true
	return nil
}

type NoJobs struct{}

func (NoJobs) Preflight(context.Context) error { return nil }

type CloseJobMode string

const (
	BlockClose        CloseJobMode = "block"
	CancelAndWait     CloseJobMode = "cancel_and_wait"
	InterruptExternal CloseJobMode = "interrupt_external"
)

type CloseJob interface {
	Mode() CloseJobMode
	Name() string
	CancelAndWait(context.Context) error
	MarkInterrupted(context.Context) error
}
type JobGuard struct{ Jobs []CloseJob }

func (g JobGuard) Preflight(ctx context.Context) error {
	for _, job := range g.Jobs {
		switch job.Mode() {
		case BlockClose:
			return fmt.Errorf("%w: %s", ErrCloseBlocked, job.Name())
		case CancelAndWait:
			if err := job.CancelAndWait(ctx); err != nil {
				return err
			}
		case InterruptExternal:
			if err := job.MarkInterrupted(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// FileRecentProjects records only application UI metadata, never project facts.
type FileRecentProjects struct{ path string }
type recentRecord struct {
	ID   domain.ID `json:"id"`
	Name string    `json:"name"`
	Path string    `json:"path"`
}

func NewFileRecentProjects(appData string) *FileRecentProjects {
	return &FileRecentProjects{path: filepath.Join(appData, "EcoGuardian", "recent-projects.json")}
}
func (r *FileRecentProjects) List() ([]ProjectInfo, error) {
	b, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return []ProjectInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	var values []recentRecord
	if err = json.Unmarshal(b, &values); err != nil {
		return nil, err
	}
	out := make([]ProjectInfo, len(values))
	for i, v := range values {
		out[i] = ProjectInfo{ID: v.ID, Name: v.Name, Path: v.Path}
	}
	return out, nil
}
func (r *FileRecentProjects) Record(info ProjectInfo) error {
	values, err := r.List()
	if err != nil {
		return err
	}
	out := []ProjectInfo{info}
	for _, v := range values {
		if v.ID != info.ID {
			out = append(out, v)
		}
	}
	if len(out) > 20 {
		out = out[:20]
	}
	if err = os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	records := make([]recentRecord, len(out))
	for i, v := range out {
		records[i] = recentRecord{v.ID, v.Name, v.Path}
	}
	b, err := json.Marshal(records)
	if err != nil {
		return err
	}
	return os.WriteFile(r.path, b, 0o600)
}

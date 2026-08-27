package integration

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/project"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

var (
	ErrRestoreTargetNotEmpty = errors.New("restore target is not empty")
	ErrRestoreTargetUnsafe   = errors.New("restore target is unsafe")
)

type ProjectMaintenance struct {
	Manager *project.Manager
	Targets *ProjectTargets
}

var _ ports.MaintenanceLeaser = ProjectMaintenance{}

func (adapter ProjectMaintenance) Acquire(ctx context.Context, projectID domain.ID, targetIdentity string) (ports.MaintenanceLease, error) {
	if adapter.Manager == nil || targetIdentity == "" {
		return nil, project.ErrMaintenance
	}
	if adapter.Targets != nil {
		if reservation, found := adapter.Targets.takeEmpty(targetIdentity, projectID); found {
			session, err := adapter.Manager.AcquireRestoredTarget(ctx, projectID, reservation.state.CanonicalPath, reservation.lock)
			if err != nil {
				adapter.Targets.restoreEmpty(reservation)
				return nil, err
			}
			return &maintenanceLease{session: session, path: reservation.state.CanonicalPath}, nil
		}
	}
	targets := ProjectTargets{Manager: adapter.Manager}
	state, err := targets.ResolveActive(ctx, projectID)
	if err != nil || state.Identity != targetIdentity {
		return nil, project.ErrMaintenance
	}
	session, err := adapter.Manager.AcquireMaintenance(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return &maintenanceLease{session: session, path: state.CanonicalPath}, nil
}

func (targets *ProjectTargets) takeEmpty(identity string, projectID domain.ID) (emptyTargetReservation, bool) {
	if targets == nil {
		return emptyTargetReservation{}, false
	}
	targets.mu.Lock()
	defer targets.mu.Unlock()
	reservation, found := targets.empty[identity]
	if !found || reservation.state.ProjectID != projectID {
		return emptyTargetReservation{}, false
	}
	delete(targets.empty, identity)
	return reservation, true
}

func (targets *ProjectTargets) restoreEmpty(reservation emptyTargetReservation) {
	if targets == nil {
		return
	}
	targets.mu.Lock()
	defer targets.mu.Unlock()
	if targets.empty == nil {
		targets.empty = map[string]emptyTargetReservation{}
	}
	targets.empty[reservation.state.Identity] = reservation
}

type maintenanceLease struct {
	session *project.MaintenanceSession
	path    string
}

func (lease *maintenanceLease) ProjectID() domain.ID   { return lease.session.ProjectID() }
func (lease *maintenanceLease) Path() string           { return lease.path }
func (lease *maintenanceLease) NonInterruptible() bool { return lease.session.NonInterruptible() }
func (lease *maintenanceLease) MarkNonInterruptible() error {
	return lease.session.MarkNonInterruptible()
}
func (lease *maintenanceLease) CloseConnections(ctx context.Context) error {
	return lease.session.CloseConnections(ctx)
}
func (lease *maintenanceLease) Reopen(ctx context.Context) (ports.RestoreReconciliationStore, error) {
	handle, err := lease.session.Reopen(ctx)
	if err != nil {
		return nil, err
	}
	provider, ok := handle.(interface{ Store() *store.Store })
	if !ok {
		return nil, project.ErrMaintenance
	}
	return provider.Store(), nil
}
func (lease *maintenanceLease) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return lease.session.Release()
}

type ProjectTargets struct {
	Manager *project.Manager
	Tokens  project.TokenStore
	mu      sync.Mutex
	empty   map[string]emptyTargetReservation
}

type emptyTargetReservation struct {
	state ports.RestoreTargetState
	lock  project.Lock
}

func NewProjectTargets(manager *project.Manager, tokens project.TokenStore) *ProjectTargets {
	return &ProjectTargets{Manager: manager, Tokens: tokens, empty: map[string]emptyTargetReservation{}}
}

var _ ports.RestoreTargetResolver = (*ProjectTargets)(nil)

func (targets *ProjectTargets) ResolveActive(ctx context.Context, projectID domain.ID) (ports.RestoreTargetState, error) {
	if err := ctx.Err(); err != nil {
		return ports.RestoreTargetState{}, err
	}
	if targets.Manager == nil {
		return ports.RestoreTargetState{}, project.ErrMaintenance
	}
	info, active := targets.Manager.Current()
	if !active || info.ID != projectID {
		return ports.RestoreTargetState{}, project.ErrMaintenance
	}
	handle, available := targets.Manager.ActiveHandle()
	if !available {
		return ports.RestoreTargetState{}, project.ErrMaintenance
	}
	provider, ok := handle.(interface{ Store() *store.Store })
	if !ok {
		return ports.RestoreTargetState{}, project.ErrMaintenance
	}
	businessGeneration, err := provider.Store().BusinessGeneration(ctx)
	if err != nil {
		return ports.RestoreTargetState{}, err
	}
	return observeActiveTarget(info, businessGeneration)
}

func (targets *ProjectTargets) ResolveEmpty(ctx context.Context, projectID domain.ID, selectionToken string) (ports.RestoreTargetState, error) {
	if err := ctx.Err(); err != nil {
		return ports.RestoreTargetState{}, err
	}
	if targets == nil || targets.Manager == nil || targets.Tokens == nil || !projectID.Valid() || selectionToken == "" {
		return ports.RestoreTargetState{}, project.ErrInvalidSelection
	}
	directory, err := targets.Tokens.Consume(selectionToken)
	if err != nil {
		return ports.RestoreTargetState{}, project.ErrInvalidSelection
	}
	canonical, err := canonicalEmptyTarget(directory)
	if err != nil {
		return ports.RestoreTargetState{}, err
	}
	if active, ok := targets.Manager.Current(); ok {
		activePath, activeErr := filepath.EvalSymlinks(filepath.Clean(active.Path))
		if activeErr != nil {
			return ports.RestoreTargetState{}, ErrRestoreTargetUnsafe
		}
		activePath, activeErr = filepath.Abs(activePath)
		if activeErr != nil || pathsOverlap(canonical, activePath) {
			return ports.RestoreTargetState{}, ErrRestoreTargetUnsafe
		}
		return ports.RestoreTargetState{}, project.ErrActiveProject
	}
	lock, err := (project.FileLocker{}).Acquire(canonical)
	if err != nil {
		return ports.RestoreTargetState{}, err
	}
	entries, err := os.ReadDir(canonical)
	if err != nil || len(entries) != 1 || entries[0].Name() != ".eco-guardian.lock" || !entries[0].Type().IsRegular() {
		_ = lock.Release()
		return ports.RestoreTargetState{}, ErrRestoreTargetNotEmpty
	}
	identityDigest := sha256.Sum256([]byte("restore-empty-target:" + canonical))
	generationDigest := sha256.Sum256([]byte("restore-empty-generation:" + string(projectID) + ":" + canonical + ":" + selectionToken))
	state := ports.RestoreTargetState{ProjectID: projectID, Mode: backupdomain.RestoreEmptySelection, CanonicalPath: canonical, Identity: fmt.Sprintf("%x", identityDigest), Generation: fmt.Sprintf("%x", generationDigest), MaintenanceAvailable: true, RegistryState: "matched"}
	targets.mu.Lock()
	if targets.empty == nil {
		targets.empty = map[string]emptyTargetReservation{}
	}
	if previous, exists := targets.empty[state.Identity]; exists {
		targets.mu.Unlock()
		_ = lock.Release()
		_ = previous
		return ports.RestoreTargetState{}, project.ErrProjectLocked
	}
	targets.empty[state.Identity] = emptyTargetReservation{state: state, lock: lock}
	targets.mu.Unlock()
	return state, nil
}

func (targets *ProjectTargets) Revalidate(ctx context.Context, expected ports.RestoreTargetState) (ports.RestoreTargetState, error) {
	if expected.Mode == backupdomain.RestoreEmptySelection {
		return targets.revalidateEmpty(ctx, expected)
	}
	actual, err := targets.ResolveActive(ctx, expected.ProjectID)
	if err != nil {
		return ports.RestoreTargetState{}, err
	}
	if actual.Identity != expected.Identity || actual.Generation != expected.Generation || expected.CanonicalPath != "" && actual.CanonicalPath != expected.CanonicalPath {
		return ports.RestoreTargetState{}, project.ErrMaintenance
	}
	return actual, nil
}

func (targets *ProjectTargets) revalidateEmpty(ctx context.Context, expected ports.RestoreTargetState) (ports.RestoreTargetState, error) {
	if err := ctx.Err(); err != nil {
		return ports.RestoreTargetState{}, err
	}
	if targets == nil || expected.Mode != backupdomain.RestoreEmptySelection || !expected.ProjectID.Valid() || expected.Identity == "" || expected.Generation == "" {
		return ports.RestoreTargetState{}, project.ErrMaintenance
	}
	targets.mu.Lock()
	reservation, found := targets.empty[expected.Identity]
	targets.mu.Unlock()
	if !found || reservation.state.ProjectID != expected.ProjectID || reservation.state.Mode != expected.Mode || reservation.state.CanonicalPath != expected.CanonicalPath || reservation.state.Identity != expected.Identity || reservation.state.Generation != expected.Generation {
		return ports.RestoreTargetState{}, project.ErrMaintenance
	}
	canonical, err := canonicalReservedTarget(expected.CanonicalPath)
	if err != nil || canonical != expected.CanonicalPath {
		return ports.RestoreTargetState{}, project.ErrMaintenance
	}
	if active, ok := targets.Manager.Current(); ok && pathsOverlap(canonical, active.Path) {
		return ports.RestoreTargetState{}, project.ErrMaintenance
	}
	return reservation.state, nil
}

func canonicalEmptyTarget(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) || strings.ContainsRune(path, '\x00') {
		return "", ErrRestoreTargetUnsafe
	}
	info, err := os.Lstat(filepath.Clean(path))
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", ErrRestoreTargetUnsafe
	}
	canonical, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", ErrRestoreTargetUnsafe
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return "", ErrRestoreTargetUnsafe
	}
	entries, err := os.ReadDir(canonical)
	if err != nil || len(entries) != 0 {
		return "", ErrRestoreTargetNotEmpty
	}
	return canonical, nil
}

func canonicalReservedTarget(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", project.ErrMaintenance
	}
	canonical, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", project.ErrMaintenance
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return "", project.ErrMaintenance
	}
	entries, err := os.ReadDir(canonical)
	if err != nil || len(entries) != 1 || entries[0].Name() != ".eco-guardian.lock" || !entries[0].Type().IsRegular() {
		return "", project.ErrMaintenance
	}
	return canonical, nil
}

func pathsOverlap(left, right string) bool {
	left, leftErr := filepath.Abs(filepath.Clean(left))
	right, rightErr := filepath.Abs(filepath.Clean(right))
	if leftErr != nil || rightErr != nil {
		return true
	}
	return within(left, right) || within(right, left)
}

func within(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && (relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && !filepath.IsAbs(relative))
}

func observeActiveTarget(info project.ProjectInfo, businessGeneration string) (ports.RestoreTargetState, error) {
	canonical, err := filepath.EvalSymlinks(filepath.Clean(info.Path))
	if err != nil {
		return ports.RestoreTargetState{}, err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return ports.RestoreTargetState{}, err
	}
	database, err := os.Lstat(filepath.Join(canonical, "project.db"))
	if err != nil || !database.Mode().IsRegular() || database.Mode()&os.ModeSymlink != 0 {
		return ports.RestoreTargetState{}, project.ErrMaintenance
	}
	identityDigest := sha256.Sum256([]byte("restore-target:" + canonical))
	generationDigest := sha256.Sum256([]byte(fmt.Sprintf("restore-generation:%s:%s:%s", info.ID, canonical, businessGeneration)))
	return ports.RestoreTargetState{ProjectID: info.ID, Mode: backupdomain.RestoreActive, CanonicalPath: canonical, Identity: fmt.Sprintf("%x", identityDigest), Generation: fmt.Sprintf("%x", generationDigest), MaintenanceAvailable: true, RegistryState: "matched"}, nil
}

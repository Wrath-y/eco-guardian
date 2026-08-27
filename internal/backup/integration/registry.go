package integration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/project"
)

var (
	ErrProjectRegistryConflict     = errors.New("project registry path conflicts with restore target")
	ErrRegistryConfirmationInvalid = errors.New("project registry confirmation is invalid or expired")
)

type RecentProjectRecords interface {
	List() ([]project.ProjectInfo, error)
	Record(project.ProjectInfo) error
}

type RegistryClock interface{ Now() time.Time }
type registryClockFunc func() time.Time

func (function registryClockFunc) Now() time.Time { return function() }

type registryConfirmation struct {
	projectID domain.ID
	current   string
	target    string
	expires   time.Time
}

// RecentProjectRegistry adapts the one existing recent-project owner. Tokens
// bind the exact UUID/current/target triple and are consumed on every attempt.
type RecentProjectRegistry struct {
	Records RecentProjectRecords
	TTL     time.Duration
	Clock   RegistryClock
	mu      sync.Mutex
	tokens  map[string]registryConfirmation
}

var _ ports.ProjectRegistry = (*RecentProjectRegistry)(nil)

func NewRecentProjectRegistry(records RecentProjectRecords) *RecentProjectRegistry {
	return &RecentProjectRegistry{Records: records, TTL: 5 * time.Minute, Clock: registryClockFunc(time.Now), tokens: map[string]registryConfirmation{}}
}

func (registry *RecentProjectRegistry) Resolve(ctx context.Context, projectID domain.ID) (ports.ProjectRegistration, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.ProjectRegistration{}, false, err
	}
	if registry == nil || registry.Records == nil || !projectID.Valid() {
		return ports.ProjectRegistration{}, false, ErrProjectRegistryConflict
	}
	values, err := registry.Records.List()
	if err != nil {
		return ports.ProjectRegistration{}, false, err
	}
	for _, value := range values {
		if value.ID != projectID {
			continue
		}
		canonical, canonicalErr := canonicalRegisteredDirectory(value.Path)
		if errors.Is(canonicalErr, os.ErrNotExist) {
			return ports.ProjectRegistration{}, false, nil
		}
		if canonicalErr != nil {
			return ports.ProjectRegistration{}, false, ErrProjectRegistryConflict
		}
		return ports.ProjectRegistration{ProjectID: projectID, CanonicalPath: canonical, Generation: registryGeneration(projectID, canonical)}, true, nil
	}
	return ports.ProjectRegistration{}, false, nil
}

func (registry *RecentProjectRegistry) IssueMigrationConfirmation(ctx context.Context, projectID domain.ID, currentPath, targetPath string) (string, time.Time, error) {
	current, found, err := registry.Resolve(ctx, projectID)
	if err != nil || !found {
		return "", time.Time{}, ErrProjectRegistryConflict
	}
	canonicalCurrent, err := canonicalRegisteredDirectory(currentPath)
	if err != nil || canonicalCurrent != current.CanonicalPath {
		return "", time.Time{}, ErrProjectRegistryConflict
	}
	canonicalTarget, err := canonicalRegisteredDirectory(targetPath)
	if err != nil || canonicalTarget == canonicalCurrent {
		return "", time.Time{}, ErrProjectRegistryConflict
	}
	bytes := make([]byte, 32)
	if _, err = rand.Read(bytes); err != nil {
		return "", time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	clock := registry.Clock
	if clock == nil {
		clock = registryClockFunc(time.Now)
	}
	ttl := registry.TTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	expires := clock.Now().Add(ttl)
	registry.mu.Lock()
	if registry.tokens == nil {
		registry.tokens = map[string]registryConfirmation{}
	}
	registry.tokens[token] = registryConfirmation{projectID: projectID, current: canonicalCurrent, target: canonicalTarget, expires: expires}
	registry.mu.Unlock()
	return token, expires, nil
}

func (registry *RecentProjectRegistry) ConfirmMigration(ctx context.Context, projectID domain.ID, token, targetPath string) (ports.ProjectRegistration, error) {
	if err := ctx.Err(); err != nil {
		return ports.ProjectRegistration{}, err
	}
	if registry == nil || registry.Records == nil || !projectID.Valid() || token == "" {
		return ports.ProjectRegistration{}, ErrRegistryConfirmationInvalid
	}
	registry.mu.Lock()
	confirmation, found := registry.tokens[token]
	delete(registry.tokens, token)
	registry.mu.Unlock()
	clock := registry.Clock
	if clock == nil {
		clock = registryClockFunc(time.Now)
	}
	if !found || confirmation.projectID != projectID || !clock.Now().Before(confirmation.expires) {
		return ports.ProjectRegistration{}, ErrRegistryConfirmationInvalid
	}
	canonicalTarget, err := canonicalRegisteredDirectory(targetPath)
	if err != nil || canonicalTarget != confirmation.target {
		return ports.ProjectRegistration{}, ErrRegistryConfirmationInvalid
	}
	current, currentFound, err := registry.Resolve(ctx, projectID)
	if err != nil || !currentFound || current.CanonicalPath != confirmation.current {
		return ports.ProjectRegistration{}, ErrProjectRegistryConflict
	}
	// Confirmation only grants one-use authority for this exact migration.
	// ProjectManager records the target after the restored DB opens and proves
	// the expected UUID; preflight must not mutate the recent-project owner.
	return ports.ProjectRegistration{ProjectID: projectID, CanonicalPath: canonicalTarget, Generation: registryGeneration(projectID, canonicalTarget)}, nil
}

func canonicalRegisteredDirectory(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", ErrProjectRegistryConflict
	}
	info, err := os.Lstat(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", ErrProjectRegistryConflict
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}

func registryGeneration(projectID domain.ID, path string) int64 {
	digest := sha256.Sum256([]byte("project-registry:" + string(projectID) + ":" + path))
	return int64(binary.BigEndian.Uint64(digest[:8]) & uint64(^uint64(0)>>1))
}

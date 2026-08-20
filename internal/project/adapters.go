package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/zouyi/eco-guardian/internal/domain"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
	"github.com/zouyi/eco-guardian/internal/validation"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

// FileLocker uses an exclusive lock file held for the active project lifetime.
// It is intentionally independent from SQLite and works on Windows as well as
// developer platforms; a second process cannot acquire the same lock name.
type FileLocker struct{}
type fileLock struct {
	file *os.File
	path string
}

func (FileLocker) Acquire(dir string) (Lock, error) {
	path := filepath.Join(dir, ".eco-guardian.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil, ErrProjectLocked
	}
	if err != nil {
		return nil, err
	}
	return &fileLock{file, path}, nil
}
func (l *fileLock) Release() error {
	err := l.file.Close()
	remove := os.Remove(l.path)
	if err != nil {
		return err
	}
	return remove
}

// SQLiteFactory optionally invokes release recovery after SQLite has opened
// the project. Composition supplies the recovery service with its registered
// Graph adapter; keeping it a callback avoids coupling project lifecycle to a
// concrete external capability implementation.
type SQLiteFactory struct {
	Registry                *domain.Registry
	GraphVersionContributor versioningrevision.VersionContributor
	Recover                 func(context.Context, *store.Store) error
	AfterRevision           func(context.Context, *store.Store, domain.RevisionSummary)
}
type sqliteHandle struct{ store *store.Store }

func (h *sqliteHandle) Close() error        { return h.store.Close() }
func (h *sqliteHandle) ID() domain.ID       { return h.store.ProjectID() }
func (h *sqliteHandle) Store() *store.Store { return h.store }
func (f SQLiteFactory) Create(ctx context.Context, dir string) (ProjectHandle, error) {
	s, _, err := store.Create(ctx, dir, f.Registry)
	if err != nil {
		return nil, err
	}
	if err = f.configureGraphVersion(s); err != nil {
		_ = s.Close()
		return nil, err
	}
	return &sqliteHandle{s}, nil
}
func (f SQLiteFactory) Open(ctx context.Context, dir string) (ProjectHandle, error) {
	s, _, err := store.Open(dir, f.Registry)
	if err != nil {
		return nil, err
	}
	if err = f.configureGraphVersion(s); err != nil {
		_ = s.Close()
		return nil, err
	}
	if f.Recover != nil {
		if err := f.Recover(ctx, s); err != nil {
			_ = s.Close()
			return nil, err
		}
	}
	return &sqliteHandle{s}, nil
}

func (f SQLiteFactory) configureGraphVersion(s *store.Store) error {
	if f.AfterRevision != nil {
		s.RegisterRevisionObserver(func(ctx context.Context, revision domain.RevisionSummary) { f.AfterRevision(ctx, s, revision) })
	} else if f.GraphVersionContributor != nil {
		s.RegisterRevisionObserver(func(ctx context.Context, revision domain.RevisionSummary) {
			startGraphValidationPipeline(ctx, s, revision)
		})
	}
	if f.GraphVersionContributor == nil {
		return nil
	}
	return s.RegisterGraphVersionContributor(f.GraphVersionContributor)
}

func startGraphValidationPipeline(ctx context.Context, s *store.Store, revision domain.RevisionSummary) {
	record, err := s.GetRevisionRecord(ctx, revision.ID)
	if err != nil {
		return
	}
	versions, ok := validationVersions(record.Metadata.Manifest)
	if !ok {
		return
	}
	pipeline := graphsync.ValidationPipeline{States: s, Validation: validation.NewValidationGate(s), Runner: s, Jobs: s}
	_, _ = pipeline.Start(ctx, graphsync.ValidationPipelineRequest{ProjectID: s.ProjectID(), RevisionID: revision.ID, ConfigHash: revision.ConfigHash, Versions: versions})
}

func validationVersions(manifest versioningrevision.VersionManifest) (validation.VersionManifest, bool) {
	values := map[string]string{}
	for _, entry := range manifest.Entries {
		if entry.State == versioningrevision.Registered {
			values[entry.CapabilityID] = entry.ImplementationVersion
		}
	}
	versions := validation.VersionManifest{Schema: values["schema"], DSL: values["dsl"], Registry: values["validator-registry"], NumericPolicy: values["numeric-policy"]}
	return versions, versions.Valid()
}

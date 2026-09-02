package project

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
	"github.com/zouyi/eco-guardian/internal/validation"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

// FileLocker uses an exclusive lock file held for the active project lifetime.
// It is intentionally independent from SQLite and works on Windows as well as
// developer platforms; a second process cannot acquire the same lock name.
type FileLocker struct {
	Owner func() InstanceOwner
}
type fileLock struct {
	file *os.File
	path string
}

func (locker FileLocker) Acquire(dir string) (Lock, error) {
	path := filepath.Join(dir, ".eco-guardian.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil, ErrProjectLocked
	}
	if err != nil {
		return nil, err
	}
	if locker.Owner != nil {
		owner := locker.Owner()
		// Test/embedding code can create a project before the HTTP listener is
		// acquired. Such a lock remains valid but cannot be remotely closed.
		if owner.URL != "" {
			if owner.Acquired == "" {
				owner.Acquired = time.Now().UTC().Format(time.RFC3339Nano)
			}
			if !owner.valid() {
				_ = file.Close()
				_ = os.Remove(path)
				return nil, ErrLockOwnerUnknown
			}
			if err = json.NewEncoder(file).Encode(owner); err == nil {
				err = file.Sync()
			}
			if err != nil {
				_ = file.Close()
				_ = os.Remove(path)
				return nil, err
			}
		}
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
	Registry                     *domain.Registry
	GraphVersionContributor      versioningrevision.VersionContributor
	SimulationVersionContributor versioningrevision.VersionContributor
	RiskVersionContributor       versioningrevision.VersionContributor
	GraphGateRegistry            *versioninggate.Registry
	GraphGateProvider            versioninggate.Provider
	GraphRecovery                graphsync.RecoveryDispatcher
	RecoveryStages               *RecoveryStages
	Recover                      func(context.Context, *store.Store) error
	AfterRevision                func(context.Context, *store.Store, domain.RevisionSummary)
	MigrationBackup              store.MigrationBackup
	ConfigureBackup              func(context.Context, *store.Store) error
}
type sqliteHandle struct{ store *store.Store }

func (h *sqliteHandle) Close() error        { return h.store.Close() }
func (h *sqliteHandle) ID() domain.ID       { return h.store.ProjectID() }
func (h *sqliteHandle) Store() *store.Store { return h.store }
func (h *sqliteHandle) PrepareRestoreClose(ctx context.Context) error {
	return h.store.PrepareRestoreClose(ctx)
}
func (f SQLiteFactory) Create(ctx context.Context, dir string) (ProjectHandle, error) {
	s, _, err := store.Create(ctx, dir, f.Registry)
	if err != nil {
		return nil, err
	}
	if err = f.configureGraphVersion(s); err != nil {
		_ = s.Close()
		return nil, err
	}
	if f.ConfigureBackup != nil {
		if err = f.ConfigureBackup(ctx, s); err != nil {
			_ = s.Close()
			return nil, err
		}
	}
	return &sqliteHandle{s}, nil
}
func (f SQLiteFactory) Open(ctx context.Context, dir string) (ProjectHandle, error) {
	s, _, err := store.OpenWithMigrationBackup(ctx, dir, f.Registry, f.MigrationBackup)
	if err != nil {
		return nil, err
	}
	if err = f.configureGraphVersion(s); err != nil {
		_ = s.Close()
		return nil, err
	}
	if f.ConfigureBackup != nil {
		if err = f.ConfigureBackup(ctx, s); err != nil {
			_ = s.Close()
			return nil, err
		}
	}
	if f.RecoveryStages != nil {
		if err = f.RecoveryStages.Recover(ctx, s); err != nil {
			_ = s.Close()
			return nil, err
		}
		return &sqliteHandle{s}, nil
	}
	if f.GraphRecovery != nil {
		if _, err = (graphsync.RecoveryService{States: s, Jobs: s, Events: s, Dispatcher: f.GraphRecovery}).Recover(ctx); err != nil {
			_ = s.Close()
			return nil, err
		}
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
	if err := f.registerGraphGate(); err != nil {
		return err
	}
	if f.AfterRevision != nil {
		s.RegisterRevisionObserver(func(ctx context.Context, revision domain.RevisionSummary) { f.AfterRevision(ctx, s, revision) })
	} else if f.GraphVersionContributor != nil {
		s.RegisterRevisionObserver(func(ctx context.Context, revision domain.RevisionSummary) {
			startGraphValidationPipeline(ctx, s, revision)
		})
	}
	if f.GraphVersionContributor == nil {
		return f.configureDeterministicVersions(s)
	}
	if err := s.RegisterGraphVersionContributor(f.GraphVersionContributor); err != nil {
		return err
	}
	return f.configureDeterministicVersions(s)
}

func (f SQLiteFactory) configureDeterministicVersions(s *store.Store) error {
	if f.SimulationVersionContributor != nil {
		if err := s.RegisterSimulationVersionContributor(f.SimulationVersionContributor); err != nil {
			return err
		}
	}
	if f.RiskVersionContributor != nil {
		if err := s.RegisterRiskVersionContributor(f.RiskVersionContributor); err != nil {
			return err
		}
	}
	return nil
}

func (f SQLiteFactory) registerGraphGate() error {
	if f.GraphGateProvider == nil {
		return nil
	}
	if f.GraphGateRegistry == nil {
		return versioninggate.ErrDescriptorInvalid
	}
	want := f.GraphGateProvider.Descriptor()
	existing, err := f.GraphGateRegistry.Descriptor(want.CapabilityID, want.GateID)
	if err == nil {
		if reflect.DeepEqual(existing, want) {
			return nil
		}
		return versioninggate.ErrDescriptorDuplicate
	}
	if err != versioninggate.ErrDescriptorNotFound {
		return err
	}
	return f.GraphGateRegistry.RegisterProvider(f.GraphGateProvider)
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
	pipeline := graphsync.ValidationPipeline{States: s, Validation: validation.NewValidationGate(s), Runner: s, Evidence: s, Jobs: s}
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

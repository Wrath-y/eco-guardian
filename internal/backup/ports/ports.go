// Package ports contains the compile-time boundaries used by backup and
// restore application services. Optional integrations register adapters here;
// this package never imports their implementations.
package ports

import (
	"context"
	"io"
	"time"

	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

type SnapshotIdentity struct {
	ProjectID     domain.ID
	ProjectPath   string
	AppVersion    string
	SchemaVersion int
	RevisionID    domain.ID
}

type BackupProgress struct {
	CopiedPages int64
	TotalPages  int64
	CopiedBytes int64
}

type ProjectSnapshotSource interface {
	Identity(context.Context) (SnapshotIdentity, error)
	OnlineBackup(context.Context, string, func(BackupProgress) error) error
}

type StagingArtifact interface {
	ID() domain.ID
	DatabasePath() string
	WriteManifest(context.Context, []byte) error
	Flush(context.Context) error
	Close() error
}

type ArtifactLease interface {
	DatabasePath() string
	Release() error
}

type InventoryQuery struct {
	ProjectID domain.ID
	After     string
	Limit     int
}

type InventoryPage struct {
	Items      []backupdomain.InventoryRecord
	NextCursor string
}

type BackupArtifactStore interface {
	CreateStaging(context.Context, domain.ID, domain.ID) (StagingArtifact, error)
	Publish(context.Context, StagingArtifact, backupdomain.Manifest) (backupdomain.Result, error)
	Discard(context.Context, StagingArtifact) error
	List(context.Context, InventoryQuery) (InventoryPage, error)
	Acquire(context.Context, domain.ID, domain.ID) (backupdomain.InventoryRecord, ArtifactLease, error)
	TrashAndDelete(context.Context, domain.ID, domain.ID, string) error
}

// ManagedBackupInventory resolves an opaque backup identity across only the
// configured server-owned project roots. It is used when no project is active.
type ManagedBackupInventory interface {
	Resolve(context.Context, domain.ID) (backupdomain.InventoryRecord, ArtifactLease, error)
	ListAll(context.Context, string, int) (InventoryPage, error)
}

type ManagedRootSelection interface {
	ResolveProjectRoot(context.Context, domain.ID) (string, error)
	ApplyNativeSelection(context.Context, string, string) error
	ResetDefault(context.Context) error
}

type MaintenanceLease interface {
	ProjectID() domain.ID
	Path() string
	NonInterruptible() bool
	MarkNonInterruptible() error
	CloseConnections(context.Context) error
	Reopen(context.Context) (RestoreReconciliationStore, error)
	Close(context.Context) error
}

type MaintenanceLeaser interface {
	Acquire(context.Context, domain.ID, string) (MaintenanceLease, error)
}

type RestoreTargetState struct {
	ProjectID            domain.ID
	Mode                 backupdomain.RestoreTargetMode
	CanonicalPath        string
	Identity             string
	Generation           string
	MaintenanceAvailable bool
	RegistryState        string
}

type RestoreTargetResolver interface {
	ResolveActive(context.Context, domain.ID) (RestoreTargetState, error)
	ResolveEmpty(context.Context, domain.ID, string) (RestoreTargetState, error)
	Revalidate(context.Context, RestoreTargetState) (RestoreTargetState, error)
}

type BackupInvoker interface {
	Invoke(context.Context, backupdomain.Command) (backupdomain.Result, error)
}

// BackupCommandStore atomically admits a shared Job with its immutable
// canonical command, so a restarted worker never reconstructs work from HTTP
// input or from a hash alone.
type BackupCommandStore interface {
	AdmitBackup(context.Context, backupdomain.Command, string) (sharedjob.Record, bool, error)
	GetBackupCommand(context.Context, domain.ID) (backupdomain.Command, error)
}

type BackupPublicationStore interface {
	BeginBackupPublication(context.Context, domain.ID, int64, time.Time) (bool, error)
}

type BackupAuditStore interface {
	SealBackupResult(context.Context, domain.ID, backupdomain.Result) (bool, error)
	GetBackupResult(context.Context, domain.ID) (backupdomain.Result, error)
}

type DailyAdmissionRecord struct {
	ProjectID   domain.ID
	LocalDate   string
	JobID       domain.ID
	CommandHash string
	CreatedAt   time.Time
}

type DailyAdmissionStore interface {
	EnsureDailyAdmission(context.Context, DailyAdmissionRecord) (DailyAdmissionRecord, bool, error)
	ReplaceFailedDailyAdmission(context.Context, DailyAdmissionRecord, domain.ID) (DailyAdmissionRecord, bool, error)
	GetDailyAdmission(context.Context, domain.ID, string) (DailyAdmissionRecord, bool, error)
	PutDailyWaiver(context.Context, backupdomain.DailyWaiver) (bool, error)
	GetDailyWaiver(context.Context, domain.ID, string) (backupdomain.DailyWaiver, bool, error)
}

type Clock interface{ Now() time.Time }

type HashProbe interface {
	SHA256(context.Context, io.Reader) (hash string, bytes int64, err error)
}

type SpaceProbe interface {
	RequireAvailable(context.Context, string, int64) error
	RequireWritable(context.Context, string) error
}

type VerifiedSnapshot struct {
	ProjectID     domain.ID
	SchemaVersion int
	Bytes         int64
	SHA256        string
}

type SnapshotVerifier interface {
	Verify(context.Context, string, domain.ID, int) (VerifiedSnapshot, error)
}

type IDGenerator interface{ New() (domain.ID, error) }

type RestoreJournal struct {
	Version                 string                         `json:"version"`
	Job                     sharedjob.Record               `json:"job"`
	Generation              int64                          `json:"generation"`
	Phase                   backupdomain.RestorePhase      `json:"phase"`
	ProjectID               domain.ID                      `json:"project_uuid"`
	BackupID                domain.ID                      `json:"backup_id"`
	ManifestHash            string                         `json:"manifest_hash"`
	DatabaseHash            string                         `json:"database_hash"`
	DatabaseBytes           int64                          `json:"database_bytes"`
	SchemaVersion           int                            `json:"schema_version"`
	CommandHash             string                         `json:"command_hash"`
	PreflightGeneration     string                         `json:"preflight_generation"`
	TargetMode              backupdomain.RestoreTargetMode `json:"target_mode"`
	TargetPath              string                         `json:"target_path"`
	StagedPath              string                         `json:"staged_path,omitempty"`
	OriginalPath            string                         `json:"original_path,omitempty"`
	InstalledPath           string                         `json:"installed_path,omitempty"`
	TargetIdentity          string                         `json:"target_identity"`
	TargetGeneration        string                         `json:"target_generation"`
	OriginalIdentity        string                         `json:"original_identity,omitempty"`
	StagedIdentity          string                         `json:"staged_identity,omitempty"`
	InstalledIdentity       string                         `json:"installed_identity,omitempty"`
	RestorePreResult        *backupdomain.Result           `json:"restore_pre_result,omitempty"`
	RestorePreNotApplicable bool                           `json:"restore_pre_not_applicable,omitempty"`
	OriginalNotApplicable   bool                           `json:"original_not_applicable,omitempty"`
	Events                  []sharedjob.Event              `json:"events,omitempty"`
	LastEventOrdinal        int64                          `json:"last_event_ordinal"`
}

type RestoreJournalStore interface {
	Load(context.Context, domain.ID) (RestoreJournal, bool, error)
	List(context.Context) ([]RestoreJournal, error)
	CompareAndSwap(context.Context, int64, RestoreJournal) (bool, error)
	DeleteTerminal(context.Context, domain.ID, int64) error
}

type ProjectRegistration struct {
	ProjectID     domain.ID
	CanonicalPath string
	Generation    int64
}

type ProjectRegistry interface {
	Resolve(context.Context, domain.ID) (ProjectRegistration, bool, error)
	IssueMigrationConfirmation(context.Context, domain.ID, string, string) (string, time.Time, error)
	ConfirmMigration(context.Context, domain.ID, string, string) (ProjectRegistration, error)
}

type SchemaMigrationHandoff interface {
	CurrentSchema() int
	MigrateInstalled(context.Context, domain.ID, BackupInvoker) error
}

type DerivedStateInvalidator interface {
	InvalidateAfterRestore(context.Context, domain.ID, domain.ID, string) error
	EnqueueFullRebuild(context.Context, domain.ID, domain.ID, string) error
}

type RestoreFileEvidence struct {
	Path   string
	Bytes  int64
	SHA256 string
}

type DatabaseReplacement interface {
	Stage(context.Context, string, string, domain.ID, domain.ID, int, int64, string) (RestoreFileEvidence, error)
	ParkOriginal(context.Context, string, domain.ID) (RestoreFileEvidence, error)
	Install(context.Context, string, domain.ID, RestoreFileEvidence) (RestoreFileEvidence, error)
	InstallEmpty(context.Context, string, domain.ID, RestoreFileEvidence) (RestoreFileEvidence, error)
	DiscardStaged(context.Context, string, domain.ID, RestoreFileEvidence) error
	VerifyInstalled(context.Context, string, domain.ID, int, int64, string) (RestoreFileEvidence, error)
	Rollback(context.Context, string, domain.ID, RestoreFileEvidence) error
	Cleanup(context.Context, string, domain.ID, RestoreFileEvidence) error
}

type RestoreReconciliationStore interface {
	sharedjob.Store
	sharedjob.EventStore
	ReconcileRestoreJob(context.Context, sharedjob.Record, int64, int64) error
	ReconcileRestoreBackup(context.Context, domain.ID, backupdomain.Result) error
	VerifyRestoreRuntime(context.Context) error
}

type RegistrationDescriptor struct {
	ID      string
	Version string
}

type GateRegistrar interface {
	RegisterBackupGate(RegistrationDescriptor, BackupInvoker) error
}
type CapabilityRegistrar interface {
	RegisterBackupCapability(RegistrationDescriptor, func(context.Context) error) error
}
type RecoveryRegistrar interface {
	RegisterRestoreRecovery(RegistrationDescriptor, func(context.Context) error) error
}

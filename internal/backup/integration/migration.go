// Package integration contains optional composition adapters between backup
// and already-applied project/release/runtime seams.
package integration

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zouyi/eco-guardian/internal/backup/application"
	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	backupfs "github.com/zouyi/eco-guardian/internal/backup/filesystem"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

// MigrationBackup performs the pre-transaction Online Backup against the
// Store already opened by the migration coordinator. It deliberately does not
// read or write v22 backup tables, because the source database may predate
// them.
type MigrationBackup struct {
	Roots      ports.ManagedRootSelection
	AppVersion string
	Space      ports.SpaceProbe
}

var _ store.MigrationBackup = MigrationBackup{}

func (adapter MigrationBackup) Backup(ctx context.Context, opened *store.Store, projectDirectory string, projectID domain.ID) (store.BackupEvidence, error) {
	if adapter.Roots == nil || adapter.AppVersion == "" || opened == nil || !projectID.Valid() {
		return store.BackupEvidence{}, application.ErrUnavailable
	}
	projectRoot, err := adapter.Roots.ResolveProjectRoot(ctx, projectID)
	if err != nil || filepath.Base(projectRoot) != string(projectID) {
		return store.BackupEvidence{}, application.ErrUnavailable
	}
	artifacts, err := backupfs.NewStore(filepath.Dir(projectRoot), store.DBSchemaVersion())
	if err != nil {
		return store.BackupEvidence{}, err
	}
	source := store.BackupSource{Store: opened, AppVersion: adapter.AppVersion}
	identity, err := source.Identity(ctx)
	identityInfo, identityStatErr := os.Stat(identity.ProjectPath)
	projectInfo, projectStatErr := os.Stat(projectDirectory)
	if err != nil || identity.ProjectID != projectID || identityStatErr != nil || projectStatErr != nil || !os.SameFile(identityInfo, projectInfo) {
		return store.BackupEvidence{}, fmt.Errorf("%w: source_identity=%t source_directory=%t", application.ErrCommandMismatch, err == nil && identity.ProjectID == projectID, identityStatErr == nil && projectStatErr == nil && os.SameFile(identityInfo, projectInfo))
	}
	callerID, requestHash := migrationCaller(projectID, identity.SchemaVersion, store.DBSchemaVersion())
	command := backupdomain.Command{
		CommandVersion: backupdomain.CommandVersion,
		ProjectID:      projectID,
		Purpose:        backupdomain.Migration,
		CallerJobID:    callerID,
		CallerHash:     requestHash,
		Source: backupdomain.SourceIdentity{
			MigrationID: fmt.Sprintf("schema-%d-to-%d", identity.SchemaVersion, store.DBSchemaVersion()),
			CallerJobID: callerID,
			RequestHash: requestHash,
		},
	}
	service := application.NewService(source, artifacts, store.BackupVerifier{}, nil, nil, adapter.Space)
	invoker := application.MandatoryInvoker{Service: func() *application.Service { return service }}
	result, err := invoker.Invoke(ctx, command)
	if err != nil {
		return store.BackupEvidence{}, err
	}
	return store.BackupEvidence{Online: true, IntegrityChecked: result.Integrity == "ok", Checksum: result.DBSHA256}, nil
}

func migrationCaller(projectID domain.ID, from, to int) (domain.ID, string) {
	digest := sha256.Sum256([]byte(fmt.Sprintf("eco-guardian:migration:%s:%d:%d", projectID, from, to)))
	requestHash := fmt.Sprintf("%x", digest)
	uuidBytes := digest
	uuidBytes[6] = (uuidBytes[6] & 0x0f) | 0x70
	uuidBytes[8] = (uuidBytes[8] & 0x3f) | 0x80
	caller := domain.ID(fmt.Sprintf("%x-%x-%x-%x-%x",
		uuidBytes[0:4], uuidBytes[4:6], uuidBytes[6:8], uuidBytes[8:10], uuidBytes[10:16]))
	return caller, requestHash
}

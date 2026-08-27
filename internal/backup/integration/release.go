package integration

import (
	"context"

	"github.com/zouyi/eco-guardian/internal/backup/application"
	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
	versioningrelease "github.com/zouyi/eco-guardian/internal/versioning/release"
)

type ReleaseBackupGate struct {
	Invoker  ports.BackupInvoker
	Identity func(context.Context) (ports.SnapshotIdentity, error)
}

func NewReleaseBackupGate(service func() *application.Service) ReleaseBackupGate {
	return ReleaseBackupGate{
		Invoker: application.MandatoryInvoker{Service: service},
		Identity: func(ctx context.Context) (ports.SnapshotIdentity, error) {
			if service == nil || service() == nil {
				return ports.SnapshotIdentity{}, application.ErrUnavailable
			}
			return service().Source.Identity(ctx)
		},
	}
}

var _ versioningrelease.BackupGate = ReleaseBackupGate{}

func (gate ReleaseBackupGate) Backup(ctx context.Context, jobID domain.ID, requestHash string) (versioningrelease.BackupEvidence, error) {
	if gate.Invoker == nil || gate.Identity == nil || !jobID.Valid() {
		return versioningrelease.BackupEvidence{}, versioningrelease.ErrMandatoryBackupFailed
	}
	identity, err := gate.Identity(ctx)
	if err != nil {
		return versioningrelease.BackupEvidence{}, err
	}
	command := backupdomain.Command{CommandVersion: backupdomain.CommandVersion, ProjectID: identity.ProjectID, Purpose: backupdomain.Release, CallerJobID: jobID, CallerHash: requestHash, Source: backupdomain.SourceIdentity{CallerJobID: jobID, RequestHash: requestHash, RevisionID: identity.RevisionID}}
	result, err := gate.Invoker.Invoke(ctx, command)
	if err != nil {
		return versioningrelease.BackupEvidence{}, err
	}
	gateEvidence := backupdomain.MandatoryEvidence{Descriptor: backupdomain.CurrentBackupCapabilityDescriptor(), Result: result}
	if !gateEvidence.Matches(command) {
		return versioningrelease.BackupEvidence{}, versioningrelease.ErrMandatoryBackupFailed
	}
	return versioningrelease.BackupEvidence{Online: true, IntegrityChecked: result.Integrity == "ok", Checksum: result.DBSHA256}, nil
}

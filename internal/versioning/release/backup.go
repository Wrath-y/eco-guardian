package release

import (
	"context"
	"errors"
	"fmt"

	versioning "github.com/zouyi/eco-guardian/internal/versioning"
)

var ErrMandatoryBackupFailed = errors.New("mandatory release backup failed")

// PerformMandatoryBackup calls the registered #14 boundary with the durable
// Job ID and canonical request hash. It accepts no partial evidence and has no
// intent/Graph/pointer side effects, so callers can stop safely on failure.
func PerformMandatoryBackup(ctx context.Context, gate BackupGate, job Job) (BackupEvidence, error) {
	if gate == nil || !job.Valid() {
		return BackupEvidence{}, ErrMandatoryBackupFailed
	}
	evidence, err := gate.Backup(ctx, job.ID, job.RequestHash)
	if err != nil || !evidence.Valid() {
		return BackupEvidence{}, fmt.Errorf("%w: %v", ErrMandatoryBackupFailed, err)
	}
	return evidence, nil
}

func (e BackupEvidence) CanonicalJSON() ([]byte, error) { return versioning.CanonicalJSON(e) }
func (e BackupEvidence) Hash() (string, error) {
	body, err := e.CanonicalJSON()
	return versioning.SHA256(body), err
}

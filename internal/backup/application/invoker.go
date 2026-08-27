package application

import (
	"context"

	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
)

// MandatoryInvoker is the single migration/restore/release boundary. It has
// no waiver, warning, degraded-mode, release-note, or risk-override input, and
// returns only evidence bound to the exact mandatory command.
type MandatoryInvoker struct{ Service func() *Service }

var _ ports.BackupInvoker = MandatoryInvoker{}

func (invoker MandatoryInvoker) Invoke(ctx context.Context, command backupdomain.Command) (backupdomain.Result, error) {
	if invoker.Service == nil {
		return backupdomain.Result{}, ErrUnavailable
	}
	service := invoker.Service()
	if service == nil {
		return backupdomain.Result{}, ErrUnavailable
	}
	result, err := service.ExecuteDirect(ctx, command, nil)
	if err != nil {
		return backupdomain.Result{}, err
	}
	evidence := backupdomain.MandatoryEvidence{Descriptor: backupdomain.CurrentBackupCapabilityDescriptor(), Result: result}
	if !evidence.Matches(command) {
		return backupdomain.Result{}, ErrCommandMismatch
	}
	return result, nil
}

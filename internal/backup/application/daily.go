package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

var (
	ErrDailyBackupRequired = errors.New("daily backup is required before this business write")
	ErrDailyWaiverInvalid  = errors.New("daily backup waiver is invalid")
)

type DailyRequiredError struct {
	JobID domain.ID
	State backupdomain.DailyState
}

func (failure DailyRequiredError) Error() string {
	return fmt.Sprintf("%s: state=%s job=%s", ErrDailyBackupRequired, failure.State, failure.JobID)
}
func (DailyRequiredError) Unwrap() error { return ErrDailyBackupRequired }

// DailyAdmission is installed only at the business-write boundary. Its own
// Job/event/audit writes use system repositories directly and therefore never
// recurse into this guard.
type DailyAdmission struct {
	ProjectID domain.ID
	Backups   *Service
	Jobs      sharedjob.Store
	State     ports.DailyAdmissionStore
	Clock     ports.Clock
	Location  *time.Location
}

func (admission *DailyAdmission) AdmitBusinessWrite(ctx context.Context) error {
	if admission == nil || !admission.ProjectID.Valid() || admission.Backups == nil || admission.Jobs == nil || admission.State == nil || admission.Clock == nil {
		return ErrUnavailable
	}
	location := admission.Location
	if location == nil {
		location = time.Local
	}
	localDate := admission.Clock.Now().In(location).Format("2006-01-02")
	command := backupdomain.Command{CommandVersion: backupdomain.CommandVersion, ProjectID: admission.ProjectID, Purpose: backupdomain.Daily, LocalDate: localDate, Source: backupdomain.SourceIdentity{}}
	hash, err := command.Hash()
	if err != nil {
		return err
	}
	key := "daily:" + string(admission.ProjectID) + ":" + localDate
	existing, found, err := admission.State.GetDailyAdmission(ctx, admission.ProjectID, localDate)
	if err != nil {
		return err
	}
	if found {
		if existing.CommandHash != hash {
			return ErrDailyBackupRequired
		}
		job, getErr := admission.Jobs.GetJob(ctx, existing.JobID)
		if getErr != nil {
			return getErr
		}
		// A retry intentionally replaces the date's original fixed-key Job.
		// Handle that replacement directly; the original path below retains
		// its existing write-serialization behavior for concurrent first writes.
		if job.IdempotencyKey != key {
			if job.Status == sharedjob.Succeeded {
				return nil
			}
			if job.Status == sharedjob.Failed || job.Status == sharedjob.Canceled {
				return admission.requireOrWaive(ctx, localDate, job)
			}
			if _, executeErr := admission.Backups.ExecuteStored(ctx, job.ID); executeErr == nil {
				return nil
			}
			current, currentErr := admission.Jobs.GetJob(context.WithoutCancel(ctx), job.ID)
			if currentErr != nil {
				return currentErr
			}
			if current.Status == sharedjob.Failed || current.Status == sharedjob.Canceled {
				return admission.requireOrWaive(context.WithoutCancel(ctx), localDate, current)
			}
			return ErrDailyBackupRequired
		}
	}
	job, _, err := admission.Backups.Submit(ctx, command, key)
	if err != nil {
		return err
	}
	if _, _, err = admission.State.EnsureDailyAdmission(ctx, ports.DailyAdmissionRecord{ProjectID: admission.ProjectID, LocalDate: localDate, JobID: job.ID, CommandHash: hash, CreatedAt: job.CreatedAt}); err != nil {
		return err
	}
	if job.Status == sharedjob.Succeeded {
		return nil
	}
	if job.Status == sharedjob.Failed || job.Status == sharedjob.Canceled {
		return admission.requireOrWaive(ctx, localDate, job)
	}
	if _, err = admission.Backups.ExecuteStored(ctx, job.ID); err == nil {
		return nil
	}
	current, getErr := admission.Jobs.GetJob(context.WithoutCancel(ctx), job.ID)
	if getErr != nil {
		return err
	}
	if current.Status == sharedjob.Failed || current.Status == sharedjob.Canceled {
		return admission.requireOrWaive(context.WithoutCancel(ctx), localDate, current)
	}
	return err
}

func (admission *DailyAdmission) requireOrWaive(ctx context.Context, localDate string, job sharedjob.Record) error {
	waiver, found, err := admission.State.GetDailyWaiver(ctx, admission.ProjectID, localDate)
	if err != nil {
		return err
	}
	if found && waiver.Valid() && waiver.FailedJobID == job.ID {
		return nil
	}
	return DailyRequiredError{JobID: job.ID, State: backupdomain.DailyAwaitingWaiver}
}

func (admission *DailyAdmission) ConfirmWaiver(ctx context.Context, failedJobID domain.ID, confirmedBy string) (backupdomain.DailyWaiver, bool, error) {
	if admission == nil || admission.Jobs == nil || admission.State == nil || admission.Clock == nil || !failedJobID.Valid() {
		return backupdomain.DailyWaiver{}, false, ErrDailyWaiverInvalid
	}
	job, err := admission.Jobs.GetJob(ctx, failedJobID)
	if err != nil || job.ProjectID != admission.ProjectID || job.Kind != JobKind || (job.Status != sharedjob.Failed && job.Status != sharedjob.Canceled) {
		return backupdomain.DailyWaiver{}, false, ErrDailyWaiverInvalid
	}
	location := admission.Location
	if location == nil {
		location = time.Local
	}
	now := admission.Clock.Now()
	waiver := backupdomain.DailyWaiver{Version: "daily-waiver-v1", ProjectID: admission.ProjectID, LocalDate: now.In(location).Format("2006-01-02"), FailedJobID: failedJobID, Confirmed: true, ConfirmedBy: confirmedBy, ConfirmedAt: now.UTC()}
	if !waiver.Valid() {
		return backupdomain.DailyWaiver{}, false, ErrDailyWaiverInvalid
	}
	replay, err := admission.State.PutDailyWaiver(ctx, waiver)
	return waiver, replay, err
}

func (admission *DailyAdmission) RetryFailed(ctx context.Context, failedJobID domain.ID) (sharedjob.Record, error) {
	if admission == nil || admission.Backups == nil || admission.Jobs == nil || admission.State == nil || admission.Clock == nil || !failedJobID.Valid() {
		return sharedjob.Record{}, ErrDailyWaiverInvalid
	}
	failed, err := admission.Jobs.GetJob(ctx, failedJobID)
	if err != nil || failed.ProjectID != admission.ProjectID || failed.Kind != JobKind || (failed.Status != sharedjob.Failed && failed.Status != sharedjob.Canceled) {
		return sharedjob.Record{}, ErrDailyWaiverInvalid
	}
	location := admission.Location
	if location == nil {
		location = time.Local
	}
	localDate := admission.Clock.Now().In(location).Format("2006-01-02")
	existing, found, err := admission.State.GetDailyAdmission(ctx, admission.ProjectID, localDate)
	if err != nil || !found || existing.JobID != failedJobID {
		return sharedjob.Record{}, ErrDailyWaiverInvalid
	}
	command := backupdomain.Command{CommandVersion: backupdomain.CommandVersion, ProjectID: admission.ProjectID, Purpose: backupdomain.Daily, LocalDate: localDate, Source: backupdomain.SourceIdentity{}}
	hash, err := command.Hash()
	if err != nil {
		return sharedjob.Record{}, err
	}
	job, _, err := admission.Backups.Submit(ctx, command, "daily-retry:"+string(failedJobID))
	if err != nil {
		return sharedjob.Record{}, err
	}
	if _, _, err = admission.State.ReplaceFailedDailyAdmission(ctx, ports.DailyAdmissionRecord{ProjectID: admission.ProjectID, LocalDate: localDate, JobID: job.ID, CommandHash: hash, CreatedAt: job.CreatedAt}, failedJobID); err != nil {
		return sharedjob.Record{}, err
	}
	if _, err = admission.Backups.ExecuteStored(ctx, job.ID); err != nil {
		current, getErr := admission.Jobs.GetJob(context.WithoutCancel(ctx), job.ID)
		if getErr == nil {
			return current, DailyRequiredError{JobID: current.ID, State: backupdomain.DailyAwaitingWaiver}
		}
		return job, err
	}
	return admission.Jobs.GetJob(ctx, job.ID)
}

package release

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
)

func TestPerformMandatoryBackupBindsJobAndRejectsPartialOrFailedEvidence(t *testing.T) {
	job := workerJob(t)
	evidence := BackupEvidence{Online: true, IntegrityChecked: true, Checksum: strings.Repeat("a", 64)}
	gate := &backupFake{evidence: evidence}
	stored, err := PerformMandatoryBackup(context.Background(), gate, job)
	if err != nil || stored != evidence || gate.jobID != job.ID || gate.requestHash != job.RequestHash {
		t.Fatalf("evidence=%#v gate=%#v err=%v", stored, gate, err)
	}
	for _, failing := range []*backupFake{{err: errors.New("offline")}, {evidence: BackupEvidence{Online: true, Checksum: strings.Repeat("a", 64)}}} {
		if _, err := PerformMandatoryBackup(context.Background(), failing, job); !errors.Is(err, ErrMandatoryBackupFailed) {
			t.Fatalf("backup error=%v", err)
		}
	}
}

func TestBackupFailureStopsWorkerBeforeAnyIntentOrExternalEffect(t *testing.T) {
	job := workerJob(t)
	store := &workerStore{job: job}
	worker := Worker{Jobs: store, Lane: NewWriteLane()}
	intentOrExternalCalled := false
	completed, err := worker.Run(context.Background(), job.ID, func(ctx context.Context, running Job) (JobResult, error) {
		if _, backupErr := PerformMandatoryBackup(ctx, &backupFake{err: errors.New("backup failed")}, running); backupErr != nil {
			return JobResult{}, backupErr
		}
		intentOrExternalCalled = true
		return JobResult{Type: "release", ID: workerID(t), URL: "/api/v1/releases/result"}, nil
	})
	if !errors.Is(err, ErrMandatoryBackupFailed) || completed.Status != JobFailed || intentOrExternalCalled {
		t.Fatalf("completed=%#v effect=%v err=%v", completed, intentOrExternalCalled, err)
	}
}

type backupFake struct {
	evidence    BackupEvidence
	err         error
	jobID       domain.ID
	requestHash string
}

func (f *backupFake) Backup(_ context.Context, jobID domain.ID, requestHash string) (BackupEvidence, error) {
	f.jobID, f.requestHash = jobID, requestHash
	return f.evidence, f.err
}

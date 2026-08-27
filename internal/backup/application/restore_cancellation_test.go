package application_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/backup/application"
	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/backup/restorejournal"
	"github.com/zouyi/eco-guardian/internal/domain"
)

type cancellationTargets struct {
	state ports.RestoreTargetState
}

func (targets cancellationTargets) ResolveActive(context.Context, domain.ID) (ports.RestoreTargetState, error) {
	return targets.state, nil
}
func (cancellationTargets) ResolveEmpty(context.Context, domain.ID, string) (ports.RestoreTargetState, error) {
	return ports.RestoreTargetState{}, errors.New("empty unavailable")
}
func (targets cancellationTargets) Revalidate(_ context.Context, expected ports.RestoreTargetState) (ports.RestoreTargetState, error) {
	if expected.ProjectID != targets.state.ProjectID || expected.Mode != targets.state.Mode || expected.Identity != targets.state.Identity || expected.Generation != targets.state.Generation {
		return ports.RestoreTargetState{}, errors.New("target changed")
	}
	return targets.state, nil
}

type cancellationMaintenance struct {
	lease *cancellationLease
}

func (maintenance cancellationMaintenance) Acquire(context.Context, domain.ID, string) (ports.MaintenanceLease, error) {
	return maintenance.lease, nil
}

type cancellationLease struct {
	projectID        domain.ID
	path             string
	nonInterruptible bool
	closed           bool
}

func (lease *cancellationLease) ProjectID() domain.ID   { return lease.projectID }
func (lease *cancellationLease) Path() string           { return lease.path }
func (lease *cancellationLease) NonInterruptible() bool { return lease.nonInterruptible }
func (lease *cancellationLease) MarkNonInterruptible() error {
	lease.nonInterruptible = true
	return nil
}
func (*cancellationLease) CloseConnections(context.Context) error { return nil }
func (*cancellationLease) Reopen(context.Context) (ports.RestoreReconciliationStore, error) {
	return nil, errors.New("unused")
}
func (lease *cancellationLease) Close(context.Context) error { lease.closed = true; return nil }

type cancellationReplacement struct{}

func (cancellationReplacement) Stage(context.Context, string, string, domain.ID, domain.ID, int, int64, string) (ports.RestoreFileEvidence, error) {
	return ports.RestoreFileEvidence{}, errors.New("replacement must not start")
}
func (cancellationReplacement) ParkOriginal(context.Context, string, domain.ID) (ports.RestoreFileEvidence, error) {
	return ports.RestoreFileEvidence{}, errors.New("replacement must not start")
}
func (cancellationReplacement) Install(context.Context, string, domain.ID, ports.RestoreFileEvidence) (ports.RestoreFileEvidence, error) {
	return ports.RestoreFileEvidence{}, errors.New("replacement must not start")
}
func (cancellationReplacement) InstallEmpty(context.Context, string, domain.ID, ports.RestoreFileEvidence) (ports.RestoreFileEvidence, error) {
	return ports.RestoreFileEvidence{}, errors.New("replacement must not start")
}
func (cancellationReplacement) DiscardStaged(context.Context, string, domain.ID, ports.RestoreFileEvidence) error {
	return errors.New("replacement must not start")
}
func (cancellationReplacement) VerifyInstalled(context.Context, string, domain.ID, int, int64, string) (ports.RestoreFileEvidence, error) {
	return ports.RestoreFileEvidence{}, errors.New("replacement must not start")
}
func (cancellationReplacement) Rollback(context.Context, string, domain.ID, ports.RestoreFileEvidence) error {
	return errors.New("replacement must not start")
}
func (cancellationReplacement) Cleanup(context.Context, string, domain.ID, ports.RestoreFileEvidence) error {
	return errors.New("replacement must not start")
}

type blockingMandatory struct {
	entered chan struct{}
	release chan struct{}
}

func (invoker blockingMandatory) Invoke(context.Context, backupdomain.Command) (backupdomain.Result, error) {
	invoker.entered <- struct{}{}
	<-invoker.release
	return backupdomain.Result{}, errors.New("injected mandatory backup failure")
}

func TestRestoreCancellationIsAcceptedBeforeMaintenanceAndDeniedAfterBoundary(t *testing.T) {
	backups, project, artifacts := backupServiceFixture(t)
	backupCommand := manualCommand(project.ProjectID(), "restore cancellation")
	backupJob, _, err := backups.Submit(context.Background(), backupCommand, "restore-cancellation-artifact")
	if err != nil {
		t.Fatal(err)
	}
	backupResult, err := backups.ExecuteStored(context.Background(), backupJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Clean(t.TempDir())
	targets := cancellationTargets{state: ports.RestoreTargetState{ProjectID: project.ProjectID(), Mode: backupdomain.RestoreActive, CanonicalPath: targetPath, Identity: strings.Repeat("a", 64), Generation: strings.Repeat("b", 64), MaintenanceAvailable: true, RegistryState: "matched"}}
	journal, err := restorejournal.New(filepath.Join(filepath.Clean(t.TempDir()), "restore"))
	if err != nil {
		t.Fatal(err)
	}
	lease := &cancellationLease{projectID: project.ProjectID(), path: targetPath}
	restores := application.NewRestoreService(backups, targets, journal, cancellationMaintenance{lease: lease}, cancellationReplacement{})

	preflight, err := restores.Preflight(context.Background(), backupResult.BackupID, backupdomain.RestoreActive)
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := restores.Submit(context.Background(), preflight.Generation, backupResult.BackupID, backupdomain.RestoreActive, "RESTORE", "cancel-before-maintenance")
	if err != nil {
		t.Fatal(err)
	}
	if canceled, denied, cancelErr := restores.Cancel(context.Background(), job.ID); cancelErr != nil || denied || canceled.CancelGeneration != 1 {
		t.Fatalf("cancel before boundary=%#v denied=%v err=%v", canceled, denied, cancelErr)
	}
	completed, executeErr := restores.Execute(context.Background(), job.ID)
	if !errors.Is(executeErr, context.Canceled) || completed.Status != "canceled" || lease.nonInterruptible {
		t.Fatalf("completed=%#v lease=%#v err=%v", completed, lease, executeErr)
	}
	if _, found, loadErr := journal.Load(context.Background(), job.ID); loadErr != nil || found {
		t.Fatalf("canceled terminal journal found=%v err=%v", found, loadErr)
	}

	preflight, err = restores.Preflight(context.Background(), backupResult.BackupID, backupdomain.RestoreActive)
	if err != nil {
		t.Fatal(err)
	}
	job, _, err = restores.Submit(context.Background(), preflight.Generation, backupResult.BackupID, backupdomain.RestoreActive, "RESTORE", "deny-after-maintenance")
	if err != nil {
		t.Fatal(err)
	}
	mandatory := blockingMandatory{entered: make(chan struct{}, 1), release: make(chan struct{})}
	restores.Mandatory = mandatory
	type outcome struct {
		job sharedJobRecord
		err error
	}
	result := make(chan outcome, 1)
	go func() {
		value, runErr := restores.Execute(context.Background(), job.ID)
		result <- outcome{job: sharedJobRecord{status: string(value.Status), cancelGeneration: value.CancelGeneration}, err: runErr}
	}()
	<-mandatory.entered
	if current, denied, cancelErr := restores.Cancel(context.Background(), job.ID); cancelErr != nil || !denied || current.CancelGeneration != 0 || current.Status != "running" {
		t.Fatalf("cancel after boundary=%#v denied=%v err=%v", current, denied, cancelErr)
	}
	close(mandatory.release)
	finished := <-result
	if finished.err == nil || finished.job.status != "failed" || !lease.nonInterruptible || !lease.closed {
		t.Fatalf("finished=%#v lease=%#v", finished, lease)
	}
	page, err := artifacts.List(context.Background(), ports.InventoryQuery{ProjectID: project.ProjectID()})
	if err != nil || len(page.Items) != 1 || page.Items[0].BackupID != backupResult.BackupID {
		t.Fatalf("cancellation boundary artifacts=%#v err=%v", page.Items, err)
	}
}

type sharedJobRecord struct {
	status           string
	cancelGeneration int64
}

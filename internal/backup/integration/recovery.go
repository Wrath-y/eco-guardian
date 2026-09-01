package integration

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zouyi/eco-guardian/internal/backup/application"
	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/project"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

// RestoreStartupRecovery owns restore recovery before ProjectManager opens a
// recent project. An unresolved journal deliberately keeps the exact project
// lock held so ordinary startup cannot open uncertain database bytes.
type RestoreStartupRecovery struct {
	Journal         ports.RestoreJournalStore
	Locker          project.Locker
	Registry        *domain.Registry
	MigrationBackup store.MigrationBackup
	Replacement     ports.DatabaseReplacement
	Verifier        ports.SnapshotVerifier
	Recent          RecentProjectRecords

	mu   sync.Mutex
	held []project.Lock
}

func (recovery *RestoreStartupRecovery) Recover(ctx context.Context) error {
	if recovery == nil || recovery.Journal == nil || recovery.Locker == nil || recovery.Registry == nil || recovery.Replacement == nil || recovery.Verifier == nil {
		return application.ErrRestoreRecoveryRequired
	}
	journals, err := recovery.Journal.List(ctx)
	if err != nil {
		return errors.Join(application.ErrRestoreRecoveryRequired, err)
	}
	var recoveryErr error
	for index := range journals {
		journal := journals[index]
		lock, lockErr := recovery.Locker.Acquire(journal.TargetPath)
		if lockErr != nil {
			recoveryErr = errors.Join(recoveryErr, lockErr)
			continue
		}
		if journalErr := recovery.recoverOne(ctx, &journal); journalErr != nil {
			recovery.hold(lock)
			recoveryErr = errors.Join(recoveryErr, journalErr)
			continue
		}
		if releaseErr := lock.Release(); releaseErr != nil {
			recoveryErr = errors.Join(recoveryErr, releaseErr)
		}
	}
	if recoveryErr != nil {
		return errors.Join(application.ErrRestoreRecoveryRequired, recoveryErr)
	}
	return nil
}

func (recovery *RestoreStartupRecovery) hold(lock project.Lock) {
	recovery.mu.Lock()
	recovery.held = append(recovery.held, lock)
	recovery.mu.Unlock()
}

func (recovery *RestoreStartupRecovery) recoverOne(ctx context.Context, journal *ports.RestoreJournal) error {
	if journal.TargetMode == backupdomain.RestoreEmptySelection {
		return recovery.recoverEmpty(ctx, journal)
	}
	if journal.TargetMode != backupdomain.RestoreActive {
		return application.ErrRestoreRecoveryRequired
	}
	if journal.Phase == backupdomain.RestoreRecoveryRequired {
		return application.ErrRestoreRecoveryRequired
	}
	original, originalPresent, originalValid, err := recovery.observeOriginal(ctx, *journal)
	if err != nil {
		return err
	}
	// Once reopen was durably journaled, reconciliation writes legitimately
	// change project.db beyond the installed artifact hash. Prove the database
	// by normal open/runtime checks and the original Job identity instead of
	// misclassifying those system writes as artifact tampering.
	if journal.Phase.AtOrAfter(backupdomain.RestoreReopened) {
		err = recovery.completeInstalled(ctx, journal, original, originalPresent && originalValid, false)
		if err != nil && journal.Phase != backupdomain.RestoreSucceeded && originalPresent && originalValid {
			return recovery.rollbackOriginal(ctx, journal, original, true)
		}
		return err
	}
	installedPresent, installedValid := recovery.observeInstalled(ctx, *journal)
	decision := backupdomain.DecideRestoreRecovery(journal.Phase, backupdomain.RestoreFileObservation{
		OriginalPresent:   originalPresent,
		OriginalValid:     originalValid,
		OriginalOpenable:  originalValid,
		InstalledPresent:  installedPresent,
		InstalledValid:    installedValid,
		InstalledExpected: installedValid,
		InstalledOpenable: installedValid,
	})
	if journal.Phase == backupdomain.RestoreRolledBack {
		decision = backupdomain.RecoverUseOriginal
	}
	switch decision {
	case backupdomain.RecoverUseInstalled:
		if completeErr := recovery.completeInstalled(ctx, journal, original, originalPresent && originalValid, true); completeErr != nil {
			if originalPresent && originalValid {
				if rollbackErr := recovery.rollbackOriginal(ctx, journal, original, true); rollbackErr != nil {
					return errors.Join(completeErr, rollbackErr)
				}
				return nil
			}
			return completeErr
		}
		return nil
	case backupdomain.RecoverUseOriginal:
		return recovery.rollbackOriginal(ctx, journal, original, originalPresent && originalValid)
	default:
		// Before the original was durably parked, project.db is itself the
		// original recovery candidate. Prove it by opening the exact project.
		if !journal.Phase.AtOrAfter(backupdomain.RestoreOriginalParked) && !journal.Phase.Terminal() {
			return recovery.rollbackOriginal(ctx, journal, ports.RestoreFileEvidence{}, false)
		}
		return application.ErrRestoreRecoveryRequired
	}
}

func (recovery *RestoreStartupRecovery) recoverEmpty(ctx context.Context, journal *ports.RestoreJournal) error {
	if !journal.RestorePreNotApplicable && journal.Phase.AtOrAfter(backupdomain.RestorePreBackup) || journal.RestorePreResult != nil || !journal.OriginalNotApplicable && journal.Phase.AtOrAfter(backupdomain.RestoreOriginalParked) {
		return application.ErrRestoreRecoveryRequired
	}
	if journal.Phase == backupdomain.RestoreRecoveryRequired {
		return application.ErrRestoreRecoveryRequired
	}
	if journal.Phase == backupdomain.RestoreRolledBack {
		return nil
	}
	if journal.Phase.AtOrAfter(backupdomain.RestoreInstalled) {
		present, valid := recovery.observeInstalled(ctx, *journal)
		if !present || !valid {
			return application.ErrRestoreRecoveryRequired
		}
		return recovery.completeInstalled(ctx, journal, ports.RestoreFileEvidence{}, false, !journal.Phase.AtOrAfter(backupdomain.RestoreReopened))
	}
	if _, err := os.Lstat(filepath.Join(journal.TargetPath, "project.db")); !errors.Is(err, os.ErrNotExist) {
		return errors.Join(application.ErrRestoreRecoveryRequired, err)
	}
	if journal.StagedPath != "" {
		staged := ports.RestoreFileEvidence{Path: journal.StagedPath, Bytes: journal.DatabaseBytes, SHA256: journal.StagedIdentity}
		if err := recovery.Replacement.DiscardStaged(ctx, journal.TargetPath, journal.Job.ID, staged); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	journal.LastEventOrdinal++
	journal.Events = append(journal.Events, sharedjob.Event{JobID: journal.Job.ID, Ordinal: journal.LastEventOrdinal, Phase: "recovered_rollback", Progress: 100, SafeError: "RESTORE_ROLLED_BACK", CreatedAt: now})
	if !journal.Job.Status.Terminal() {
		journal.Job.Status, journal.Job.UpdatedAt = sharedjob.Failed, now
	}
	return recovery.advanceJournal(ctx, journal, backupdomain.RestoreRolledBack)
}

func (recovery *RestoreStartupRecovery) observeInstalled(ctx context.Context, journal ports.RestoreJournal) (bool, bool) {
	mainPath := filepath.Join(journal.TargetPath, "project.db")
	info, err := os.Lstat(mainPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, false
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return err == nil, false
	}
	if journal.Phase.AtOrAfter(backupdomain.RestoreInstalled) && (journal.InstalledPath != mainPath || journal.InstalledIdentity != journal.DatabaseHash) {
		return true, false
	}
	_, err = recovery.Replacement.VerifyInstalled(ctx, journal.TargetPath, journal.ProjectID, journal.SchemaVersion, journal.DatabaseBytes, journal.DatabaseHash)
	return true, err == nil
}

func (recovery *RestoreStartupRecovery) observeOriginal(ctx context.Context, journal ports.RestoreJournal) (ports.RestoreFileEvidence, bool, bool, error) {
	expected := filepath.Join(journal.TargetPath, ".eco-restore-"+string(journal.Job.ID)+".original")
	if journal.OriginalPath != "" && journal.OriginalPath != expected {
		return ports.RestoreFileEvidence{}, false, false, application.ErrRestoreRecoveryRequired
	}
	info, err := os.Lstat(expected)
	if errors.Is(err, os.ErrNotExist) {
		return ports.RestoreFileEvidence{}, false, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ports.RestoreFileEvidence{}, err == nil, false, errors.Join(application.ErrRestoreRecoveryRequired, err)
	}
	hash, size, err := hashFile(expected)
	if err != nil {
		return ports.RestoreFileEvidence{}, true, false, err
	}
	evidence := ports.RestoreFileEvidence{Path: expected, Bytes: size, SHA256: hash}
	if journal.OriginalIdentity == "" || hash != journal.OriginalIdentity {
		return evidence, true, false, nil
	}
	verified, verifyErr := recovery.Verifier.Verify(ctx, expected, journal.ProjectID, store.DBSchemaVersion())
	return evidence, true, verifyErr == nil && verified.Bytes == size && verified.SHA256 == hash, nil
}

func (recovery *RestoreStartupRecovery) completeInstalled(ctx context.Context, journal *ports.RestoreJournal, original ports.RestoreFileEvidence, cleanupOriginal, verifySnapshot bool) error {
	if verifySnapshot {
		if _, err := recovery.Replacement.VerifyInstalled(ctx, journal.TargetPath, journal.ProjectID, journal.SchemaVersion, journal.DatabaseBytes, journal.DatabaseHash); err != nil {
			return err
		}
	}
	opened, openedID, err := store.OpenWithMigrationBackup(ctx, journal.TargetPath, recovery.Registry, recovery.MigrationBackup)
	if err != nil {
		return err
	}
	defer opened.Close()
	if openedID != journal.ProjectID {
		return application.ErrRestoreRecoveryRequired
	}
	if err = opened.VerifyRestoreRuntime(ctx); err != nil {
		return err
	}
	if err = opened.ReconcileRestoreJob(ctx, journal.Job, journal.LastEventOrdinal, journal.Generation); err != nil {
		return err
	}
	if err = opened.ReconcileRestoredJobs(ctx, journal.Job.ID); err != nil {
		return err
	}
	if journal.TargetMode == backupdomain.RestoreEmptySelection {
		if !journal.RestorePreNotApplicable || journal.RestorePreResult != nil {
			return application.ErrRestoreRecoveryRequired
		}
		for _, event := range journal.Events {
			if _, _, err = opened.Append(ctx, event); err != nil {
				return err
			}
		}
	} else {
		if journal.RestorePreResult == nil {
			return application.ErrRestoreRecoveryRequired
		}
		if reconcileErr := opened.ReconcileRestoreBackup(ctx, journal.Job.ID, *journal.RestorePreResult); reconcileErr != nil {
			return errors.Join(application.ErrRestoreRecoveryRequired, reconcileErr)
		}
	}
	current, err := opened.GetJob(ctx, journal.Job.ID)
	if err != nil {
		return err
	}
	result := &sharedjob.Result{Type: "restore", ID: journal.Job.ID, URL: "/api/v1/restores/" + string(journal.Job.ID)}
	ordinal, err := latestOrdinal(ctx, opened, journal.Job.ID, journal.LastEventOrdinal)
	if err != nil {
		return err
	}
	if current.Status != sharedjob.Succeeded {
		if current.Status.Terminal() {
			return application.ErrRestoreRecoveryRequired
		}
		if err = opened.InvalidateAfterRestore(ctx, journal.ProjectID, journal.Job.ID, journal.Job.RequestHash); err != nil {
			return err
		}
		_ = opened.EnqueueFullRebuild(ctx, journal.ProjectID, journal.Job.ID, journal.Job.RequestHash)
		ordinal++
		if _, _, err = opened.Append(ctx, sharedjob.Event{JobID: current.ID, Ordinal: ordinal, Phase: "recovered_succeeded", Progress: 100, Warning: "Graph verification is pending", Result: result, CreatedAt: time.Now().UTC()}); err != nil {
			return err
		}
		if current.Status == sharedjob.Queued {
			current, _, err = opened.Transition(ctx, current.ID, sharedjob.Queued, sharedjob.Running, nil, current.CancelGeneration)
			if err != nil {
				return err
			}
		}
		current, changed, transitionErr := opened.Transition(ctx, current.ID, current.Status, sharedjob.Succeeded, result, current.CancelGeneration)
		if transitionErr != nil || !changed {
			return errors.Join(application.ErrRestoreRecoveryRequired, transitionErr)
		}
		journal.Job = current
	} else if current.Result == nil || *current.Result != *result {
		return application.ErrRestoreRecoveryRequired
	}
	journal.LastEventOrdinal = ordinal
	if err = recovery.advanceInstalledJournal(ctx, journal); err != nil {
		return err
	}
	if cleanupOriginal {
		if err = recovery.Replacement.Cleanup(ctx, journal.TargetPath, journal.Job.ID, original); err != nil {
			return err
		}
	}
	if journal.TargetMode == backupdomain.RestoreEmptySelection {
		if recovery.Recent == nil {
			return application.ErrRestoreRecoveryRequired
		}
		if err = recovery.Recent.Record(project.ProjectInfo{ID: journal.ProjectID, Name: filepath.Base(journal.TargetPath), Path: journal.TargetPath}); err != nil {
			return err
		}
	}
	return recovery.Journal.DeleteTerminal(ctx, journal.Job.ID, journal.Generation)
}

func (recovery *RestoreStartupRecovery) rollbackOriginal(ctx context.Context, journal *ports.RestoreJournal, original ports.RestoreFileEvidence, parked bool) error {
	if parked {
		if err := recovery.Replacement.Rollback(ctx, journal.TargetPath, journal.Job.ID, original); err != nil {
			return err
		}
	}
	opened, openedID, err := store.OpenWithMigrationBackup(ctx, journal.TargetPath, recovery.Registry, recovery.MigrationBackup)
	if err != nil {
		return err
	}
	defer opened.Close()
	if openedID != journal.ProjectID {
		return application.ErrRestoreRecoveryRequired
	}
	if err = opened.VerifyRestoreRuntime(ctx); err != nil {
		return err
	}
	if err = opened.ReconcileRestoreJob(ctx, journal.Job, journal.LastEventOrdinal, journal.Generation); err != nil {
		return err
	}
	if journal.Phase.AtOrAfter(backupdomain.RestorePreBackup) {
		if journal.RestorePreResult == nil {
			return application.ErrRestoreRecoveryRequired
		}
		if reconcileErr := opened.ReconcileRestoreBackup(ctx, journal.Job.ID, *journal.RestorePreResult); reconcileErr != nil {
			return errors.Join(application.ErrRestoreRecoveryRequired, reconcileErr)
		}
	}
	current, err := opened.GetJob(ctx, journal.Job.ID)
	if err != nil {
		return err
	}
	ordinal, err := latestOrdinal(ctx, opened, journal.Job.ID, journal.LastEventOrdinal)
	if err != nil {
		return err
	}
	if current.Status != sharedjob.Failed {
		if current.Status.Terminal() {
			return application.ErrRestoreRecoveryRequired
		}
		ordinal++
		if _, _, err = opened.Append(ctx, sharedjob.Event{JobID: current.ID, Ordinal: ordinal, Phase: "recovered_rollback", Progress: 100, SafeError: "RESTORE_ROLLED_BACK", CreatedAt: time.Now().UTC()}); err != nil {
			return err
		}
		current, changed, transitionErr := opened.Transition(ctx, current.ID, current.Status, sharedjob.Failed, nil, current.CancelGeneration)
		if transitionErr != nil || !changed {
			return errors.Join(application.ErrRestoreRecoveryRequired, transitionErr)
		}
		journal.Job = current
	}
	journal.LastEventOrdinal = ordinal
	if journal.Phase != backupdomain.RestoreRolledBack {
		if journal.Phase.Terminal() {
			return application.ErrRestoreRecoveryRequired
		}
		if err = recovery.advanceJournal(ctx, journal, backupdomain.RestoreRolledBack); err != nil {
			return err
		}
	}
	return recovery.Journal.DeleteTerminal(ctx, journal.Job.ID, journal.Generation)
}

func (recovery *RestoreStartupRecovery) advanceInstalledJournal(ctx context.Context, journal *ports.RestoreJournal) error {
	ordered := []backupdomain.RestorePhase{
		backupdomain.RestoreInstalled,
		backupdomain.RestoreVerified,
		backupdomain.RestoreReopened,
		backupdomain.RestoreReconciled,
		backupdomain.RestoreSucceeded,
	}
	if journal.Phase == backupdomain.RestoreSucceeded {
		return nil
	}
	start := -1
	for index, phase := range ordered {
		if journal.Phase == phase {
			start = index
			break
		}
	}
	if start < 0 {
		return application.ErrRestoreRecoveryRequired
	}
	for index := start + 1; index < len(ordered); index++ {
		if err := recovery.advanceJournal(ctx, journal, ordered[index]); err != nil {
			return err
		}
	}
	return nil
}

func (recovery *RestoreStartupRecovery) advanceJournal(ctx context.Context, journal *ports.RestoreJournal, phase backupdomain.RestorePhase) error {
	next := *journal
	next.Generation++
	next.Phase = phase
	changed, err := recovery.Journal.CompareAndSwap(context.WithoutCancel(ctx), journal.Generation, next)
	if err != nil || !changed {
		return errors.Join(application.ErrRestoreRecoveryRequired, err)
	}
	*journal = next
	return nil
}

func latestOrdinal(ctx context.Context, events sharedjob.EventStore, jobID domain.ID, floor int64) (int64, error) {
	values, err := events.ListEvents(ctx, jobID, 0)
	if err != nil {
		return 0, err
	}
	for _, event := range values {
		if event.Ordinal > floor {
			floor = event.Ordinal
		}
	}
	return floor, nil
}

func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	digest := sha256.New()
	size, copyErr := io.Copy(digest, file)
	closeErr := file.Close()
	if err = errors.Join(copyErr, closeErr); err != nil {
		return "", 0, err
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), size, nil
}

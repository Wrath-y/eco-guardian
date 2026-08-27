package backupdomain

import (
	"strings"
	"testing"
)

func TestRestorePhaseTransitionsAndRecoveryDecisionsAreDeterministic(t *testing.T) {
	ordered := []RestorePhase{RestorePreflighted, RestoreMaintenance, RestorePreBackup, RestoreConnectionsClosed, RestoreDatabaseStaged, RestoreOriginalParked, RestoreInstalled, RestoreVerified, RestoreReopened, RestoreReconciled, RestoreSucceeded}
	for index := 0; index < len(ordered)-1; index++ {
		if !ordered[index].CanTransitionTo(ordered[index+1]) || index+2 < len(ordered) && ordered[index].CanTransitionTo(ordered[index+2]) {
			t.Fatalf("illegal transition policy at %s", ordered[index])
		}
	}
	if got := DecideRestoreRecovery(RestoreInstalled, RestoreFileObservation{OriginalPresent: true, OriginalValid: true, OriginalOpenable: true, InstalledPresent: true, InstalledValid: true, InstalledExpected: true, InstalledOpenable: true}); got != RecoverUseInstalled {
		t.Fatalf("decision=%s", got)
	}
	if got := DecideRestoreRecovery(RestoreOriginalParked, RestoreFileObservation{OriginalPresent: true, OriginalValid: true, OriginalOpenable: true}); got != RecoverUseOriginal {
		t.Fatalf("decision=%s", got)
	}
	if got := DecideRestoreRecovery(RestoreInstalled, RestoreFileObservation{InstalledPresent: true, InstalledValid: true}); got != RecoverPreserveAll {
		t.Fatalf("unproven installed decision=%s", got)
	}
}

func TestRestoreRecoveryDecisionExhaustiveFileMatrix(t *testing.T) {
	phases := []RestorePhase{RestorePreflighted, RestoreMaintenance, RestorePreBackup, RestoreConnectionsClosed, RestoreDatabaseStaged, RestoreOriginalParked, RestoreInstalled, RestoreVerified, RestoreReopened, RestoreReconciled, RestoreSucceeded, RestoreRolledBack, RestoreRecoveryRequired}
	for _, phase := range phases {
		for mask := 0; mask < 1<<9; mask++ {
			observation := RestoreFileObservation{
				StagedPresent: mask&1 != 0, StagedValid: mask&2 != 0,
				OriginalPresent: mask&4 != 0, OriginalValid: mask&8 != 0, OriginalOpenable: mask&16 != 0,
				InstalledPresent: mask&32 != 0, InstalledValid: mask&64 != 0,
				InstalledExpected: mask&128 != 0, InstalledOpenable: mask&256 != 0,
			}
			want := RecoverPreserveAll
			installedProven := phase != RestoreRecoveryRequired && phase.AtOrAfter(RestoreInstalled) && observation.InstalledPresent && observation.InstalledValid && observation.InstalledExpected && observation.InstalledOpenable
			originalProven := phase != RestoreRecoveryRequired && observation.OriginalPresent && observation.OriginalValid && observation.OriginalOpenable
			if installedProven {
				want = RecoverUseInstalled
			} else if originalProven {
				want = RecoverUseOriginal
			}
			if got := DecideRestoreRecovery(phase, observation); got != want {
				t.Fatalf("phase=%s mask=%09b decision=%s want=%s", phase, mask, got, want)
			}
		}
	}
}

func TestRestoreCommandHashBindsConfirmationAndGeneration(t *testing.T) {
	projectID := testProjectID
	backupID := testBackupID
	command := RestoreCommand{Version: RestoreCommandVersion, ProjectID: projectID, BackupID: backupID, TargetMode: RestoreActive, PreflightGeneration: strings.Repeat("a", 64), Confirmation: "RESTORE"}
	first, err := command.Hash()
	if err != nil {
		t.Fatal(err)
	}
	changed := command
	changed.PreflightGeneration = strings.Repeat("b", 64)
	second, err := changed.Hash()
	if err != nil || first == second {
		t.Fatalf("first=%s second=%s err=%v", first, second, err)
	}
	if (RestoreCommand{Version: RestoreCommandVersion, ProjectID: projectID, BackupID: backupID, TargetMode: RestoreActive, PreflightGeneration: strings.Repeat("a", 64), Confirmation: "yes"}).Valid() {
		t.Fatal("weak confirmation accepted")
	}
}

func FuzzRestoreRecoveryDecisionIsDeterministicAndNeverInventsProof(f *testing.F) {
	f.Add(uint8(6), uint16(0x1fc))
	f.Add(uint8(5), uint16(0x01c))
	f.Add(uint8(12), uint16(0x1ff))
	allPhases := []RestorePhase{RestorePreflighted, RestoreMaintenance, RestorePreBackup, RestoreConnectionsClosed, RestoreDatabaseStaged, RestoreOriginalParked, RestoreInstalled, RestoreVerified, RestoreReopened, RestoreReconciled, RestoreSucceeded, RestoreRolledBack, RestoreRecoveryRequired, RestorePhase("unknown")}
	f.Fuzz(func(t *testing.T, phaseIndex uint8, mask uint16) {
		phase := allPhases[int(phaseIndex)%len(allPhases)]
		files := RestoreFileObservation{
			StagedPresent: mask&1 != 0, StagedValid: mask&2 != 0,
			OriginalPresent: mask&4 != 0, OriginalValid: mask&8 != 0, OriginalOpenable: mask&16 != 0,
			InstalledPresent: mask&32 != 0, InstalledValid: mask&64 != 0,
			InstalledExpected: mask&128 != 0, InstalledOpenable: mask&256 != 0,
		}
		first := DecideRestoreRecovery(phase, files)
		if second := DecideRestoreRecovery(phase, files); second != first {
			t.Fatalf("non-deterministic decision %s/%s", first, second)
		}
		switch first {
		case RecoverUseInstalled:
			if !phase.AtOrAfter(RestoreInstalled) || !files.InstalledPresent || !files.InstalledValid || !files.InstalledExpected || !files.InstalledOpenable {
				t.Fatalf("installed decision without complete proof: phase=%s files=%#v", phase, files)
			}
		case RecoverUseOriginal:
			if !files.OriginalPresent || !files.OriginalValid || !files.OriginalOpenable {
				t.Fatalf("original decision without complete proof: phase=%s files=%#v", phase, files)
			}
		case RecoverPreserveAll:
		default:
			t.Fatalf("unknown recovery decision %q", first)
		}
	})
}

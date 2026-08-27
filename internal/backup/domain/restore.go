package backupdomain

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
)

const (
	RestorePreflightVersion = "restore-preflight-v1"
	RestoreCommandVersion   = "restore-command-v1"
	RestoreJournalVersion   = "restore-journal-v1"
)

type RestoreTargetMode string

const (
	RestoreActive         RestoreTargetMode = "active"
	RestoreEmptySelection RestoreTargetMode = "empty_selection"
)

func (mode RestoreTargetMode) Valid() bool {
	return mode == RestoreActive || mode == RestoreEmptySelection
}

type RestoreConfirmation struct {
	RestorePreBackupRequired bool `json:"restore_pre_backup_required"`
	MaintenanceRequired      bool `json:"maintenance_required"`
	MigrationRequired        bool `json:"migration_required"`
	GraphPending             bool `json:"graph_pending"`
}

type RestorePreflightBody struct {
	Version                   string              `json:"version"`
	Backup                    InventoryRecord     `json:"backup"`
	TargetMode                RestoreTargetMode   `json:"target_mode"`
	TargetIdentity            string              `json:"target_identity"`
	TargetGeneration          string              `json:"target_generation"`
	Writable                  bool                `json:"writable"`
	FreeSpaceSufficient       bool                `json:"free_space_sufficient"`
	MaintenanceAvailable      bool                `json:"maintenance_available"`
	RegistryState             string              `json:"registry_state"`
	RegistryConfirmationToken string              `json:"registry_confirmation_token,omitempty"`
	Confirmation              RestoreConfirmation `json:"confirmation"`
	CreatedAt                 time.Time           `json:"created_at"`
	ExpiresAt                 time.Time           `json:"expires_at"`
}

type RestorePreflight struct {
	RestorePreflightBody
	Generation string `json:"generation"`
}

func NewRestorePreflight(body RestorePreflightBody) (RestorePreflight, error) {
	registryValid := body.RegistryState == "matched" && body.RegistryConfirmationToken == "" || body.RegistryState == "confirmed" && body.RegistryConfirmationToken == "" || body.RegistryState == "migration_confirmation_required" && len(body.RegistryConfirmationToken) >= 32 && len(body.RegistryConfirmationToken) <= 256
	if body.Version != RestorePreflightVersion || !body.Backup.Restorable() || !body.TargetMode.Valid() || !validHash(body.TargetIdentity) || !validHash(body.TargetGeneration) || !body.Writable || !body.FreeSpaceSufficient || !body.MaintenanceAvailable || !registryValid || !body.Confirmation.MaintenanceRequired || !body.Confirmation.GraphPending || !validTime(body.CreatedAt) || !validTime(body.ExpiresAt) || !body.ExpiresAt.After(body.CreatedAt) {
		return RestorePreflight{}, ErrInvalidBackup
	}
	encoded, err := domain.CanonicalJSON(body)
	if err != nil {
		return RestorePreflight{}, err
	}
	digest := sha256.Sum256(encoded)
	return RestorePreflight{RestorePreflightBody: body, Generation: fmt.Sprintf("%x", digest)}, nil
}

func (preflight RestorePreflight) Valid(now time.Time) bool {
	recreated, err := NewRestorePreflight(preflight.RestorePreflightBody)
	return err == nil && recreated.Generation == preflight.Generation && now.Before(preflight.ExpiresAt)
}

type RestoreCommand struct {
	Version             string            `json:"version"`
	ProjectID           domain.ID         `json:"project_uuid"`
	BackupID            domain.ID         `json:"backup_id"`
	TargetMode          RestoreTargetMode `json:"target_mode"`
	PreflightGeneration string            `json:"preflight_generation"`
	Confirmation        string            `json:"confirmation"`
}

func (command RestoreCommand) Valid() bool {
	return command.Version == RestoreCommandVersion && command.ProjectID.Valid() && command.BackupID.Valid() && command.TargetMode.Valid() && validHash(command.PreflightGeneration) && command.Confirmation == "RESTORE"
}

func (command RestoreCommand) CanonicalJSON() ([]byte, error) {
	if !command.Valid() {
		return nil, ErrInvalidBackup
	}
	return domain.CanonicalJSON(command)
}

func (command RestoreCommand) Hash() (string, error) {
	encoded, err := command.CanonicalJSON()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest), nil
}

type RestorePhase string

const (
	RestorePreflighted       RestorePhase = "preflighted"
	RestoreMaintenance       RestorePhase = "maintenance"
	RestorePreBackup         RestorePhase = "restore_pre_backup"
	RestoreConnectionsClosed RestorePhase = "connections_closed"
	RestoreDatabaseStaged    RestorePhase = "database_staged"
	RestoreOriginalParked    RestorePhase = "original_parked"
	RestoreInstalled         RestorePhase = "installed"
	RestoreVerified          RestorePhase = "verified"
	RestoreReopened          RestorePhase = "reopened"
	RestoreReconciled        RestorePhase = "reconciled"
	RestoreSucceeded         RestorePhase = "succeeded"
	RestoreRolledBack        RestorePhase = "rolled_back"
	RestoreRecoveryRequired  RestorePhase = "recovery_required"
)

var restorePhaseOrder = map[RestorePhase]int{
	RestorePreflighted: 1, RestoreMaintenance: 2, RestorePreBackup: 3, RestoreConnectionsClosed: 4,
	RestoreDatabaseStaged: 5, RestoreOriginalParked: 6, RestoreInstalled: 7, RestoreVerified: 8,
	RestoreReopened: 9, RestoreReconciled: 10, RestoreSucceeded: 11,
}

func (phase RestorePhase) Valid() bool {
	_, ordered := restorePhaseOrder[phase]
	return ordered || phase == RestoreRolledBack || phase == RestoreRecoveryRequired
}

func (phase RestorePhase) Terminal() bool {
	return phase == RestoreSucceeded || phase == RestoreRolledBack || phase == RestoreRecoveryRequired
}

// AtOrAfter compares only the ordered, non-terminal restore checkpoints.
// Recovery code uses it to avoid inferring a file replacement that was never
// durably journaled.
func (phase RestorePhase) AtOrAfter(checkpoint RestorePhase) bool {
	phaseOrder, phaseOK := restorePhaseOrder[phase]
	checkpointOrder, checkpointOK := restorePhaseOrder[checkpoint]
	return phaseOK && checkpointOK && phaseOrder >= checkpointOrder
}

func (phase RestorePhase) CanTransitionTo(next RestorePhase) bool {
	if !phase.Valid() || !next.Valid() || phase.Terminal() {
		return false
	}
	if next == RestoreRolledBack || next == RestoreRecoveryRequired {
		return true
	}
	return restorePhaseOrder[next] == restorePhaseOrder[phase]+1
}

type RestoreFileObservation struct {
	StagedPresent     bool
	StagedValid       bool
	OriginalPresent   bool
	OriginalValid     bool
	OriginalOpenable  bool
	InstalledPresent  bool
	InstalledValid    bool
	InstalledExpected bool
	InstalledOpenable bool
}

type RecoveryDecision string

const (
	RecoverUseInstalled RecoveryDecision = "complete_installed"
	RecoverUseOriginal  RecoveryDecision = "rollback_original"
	RecoverPreserveAll  RecoveryDecision = "recovery_required"
)

func DecideRestoreRecovery(phase RestorePhase, files RestoreFileObservation) RecoveryDecision {
	if !phase.Valid() || phase == RestoreRecoveryRequired {
		return RecoverPreserveAll
	}
	if files.InstalledPresent && files.InstalledValid && files.InstalledExpected && files.InstalledOpenable && restorePhaseOrder[phase] >= restorePhaseOrder[RestoreInstalled] {
		return RecoverUseInstalled
	}
	if files.OriginalPresent && files.OriginalValid && files.OriginalOpenable {
		return RecoverUseOriginal
	}
	return RecoverPreserveAll
}

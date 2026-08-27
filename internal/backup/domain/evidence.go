package backupdomain

import (
	"crypto/sha256"
	"fmt"

	"github.com/zouyi/eco-guardian/internal/domain"
)

const (
	BackupCapabilityID             = "project-backup"
	BackupGateID                   = "mandatory-consistent-backup"
	BackupGateContractVersion      = "backup-gate-v1"
	BackupImplementationVersion    = "sqlite-online-backup-v1"
	MandatoryFailureNonOverridable = "non_overridable"
)

type BackupCapabilityDescriptor struct {
	CapabilityID          string `json:"capability_id"`
	GateID                string `json:"gate_id"`
	ContractVersion       string `json:"contract_version"`
	ImplementationVersion string `json:"implementation_version"`
	FailureClassification string `json:"failure_classification"`
}

func CurrentBackupCapabilityDescriptor() BackupCapabilityDescriptor {
	return BackupCapabilityDescriptor{CapabilityID: BackupCapabilityID, GateID: BackupGateID, ContractVersion: BackupGateContractVersion, ImplementationVersion: BackupImplementationVersion, FailureClassification: MandatoryFailureNonOverridable}
}

func (descriptor BackupCapabilityDescriptor) Valid() bool {
	return descriptor == CurrentBackupCapabilityDescriptor()
}

// MandatoryEvidence is the canonical Gate envelope. Result remains the
// immutable artifact evidence used by inventory/audit; this envelope adds the
// capability and implementation identities required by mandatory callers.
type MandatoryEvidence struct {
	Descriptor BackupCapabilityDescriptor `json:"descriptor"`
	Result     Result                     `json:"result"`
}

func (evidence MandatoryEvidence) Matches(command Command) bool {
	return evidence.Descriptor.Valid() && evidence.Result.Valid() && command.Valid() && command.Purpose.Mandatory() &&
		evidence.Result.ProjectID == command.ProjectID && evidence.Result.Type == command.Purpose &&
		evidence.Result.Source.CallerJobID == command.CallerJobID && evidence.Result.Source.RequestHash == command.CallerHash
}

func (evidence MandatoryEvidence) CanonicalJSON() ([]byte, error) {
	if !evidence.Descriptor.Valid() || !evidence.Result.Valid() {
		return nil, ErrInvalidBackup
	}
	return domain.CanonicalJSON(evidence)
}

func (evidence MandatoryEvidence) Hash() (string, error) {
	encoded, err := evidence.CanonicalJSON()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest), nil
}

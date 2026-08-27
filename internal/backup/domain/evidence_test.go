package backupdomain

import (
	"strings"
	"testing"
)

func TestMandatoryEvidenceBindsCallerArtifactAndNonOverridableImplementation(t *testing.T) {
	manifest, err := NewManifest(manifestBody())
	if err != nil {
		t.Fatal(err)
	}
	callerID := manifest.BackupID
	requestHash := strings.Repeat("c", 64)
	command := Command{CommandVersion: CommandVersion, ProjectID: manifest.ProjectID, Purpose: Migration, CallerJobID: callerID, CallerHash: requestHash, Source: SourceIdentity{CallerJobID: callerID, RequestHash: requestHash}}
	result := Result{EvidenceVersion: EvidenceVersion, BackupID: manifest.BackupID, ProjectID: manifest.ProjectID, Type: Migration, Trigger: TriggerMigration, CreatedAt: manifest.CreatedAt, AppVersion: manifest.AppVersion, SchemaVersion: manifest.SchemaVersion, DBBytes: manifest.DBBytes, DBSHA256: manifest.DBSHA256, ManifestHash: manifest.ManifestHash, Integrity: "ok", Source: command.Source, ResultURL: "/api/v1/backups/" + string(manifest.BackupID)}
	evidence := MandatoryEvidence{Descriptor: CurrentBackupCapabilityDescriptor(), Result: result}
	first, err := evidence.Hash()
	second, secondErr := evidence.Hash()
	if err != nil || secondErr != nil || first != second || !evidence.Matches(command) || evidence.Descriptor.FailureClassification != MandatoryFailureNonOverridable {
		t.Fatalf("evidence=%#v hash=%q/%q err=%v/%v", evidence, first, second, err, secondErr)
	}
	changed := command
	changed.CallerHash = strings.Repeat("d", 64)
	if evidence.Matches(changed) {
		t.Fatal("changed caller request matched mandatory evidence")
	}
	changed = command
	changed.ProjectID = callerID
	if evidence.Matches(changed) {
		t.Fatal("changed project matched mandatory evidence")
	}
	changed = command
	changed.Purpose = Daily
	if evidence.Matches(changed) {
		t.Fatal("daily purpose satisfied mandatory evidence")
	}
	mutated := evidence
	mutated.Descriptor.FailureClassification = "overridable"
	if mutated.Matches(command) {
		t.Fatal("overridable failure classification was accepted")
	}
	for _, mutate := range []func(*MandatoryEvidence){
		func(value *MandatoryEvidence) { value.Descriptor.CapabilityID = "other" },
		func(value *MandatoryEvidence) { value.Descriptor.GateID = "other" },
		func(value *MandatoryEvidence) { value.Descriptor.ContractVersion = "other" },
		func(value *MandatoryEvidence) { value.Descriptor.ImplementationVersion = "other" },
		func(value *MandatoryEvidence) { value.Result.Source.CallerJobID = value.Result.ProjectID },
		func(value *MandatoryEvidence) { value.Result.Source.RequestHash = strings.Repeat("e", 64) },
	} {
		mutated = evidence
		mutate(&mutated)
		if mutated.Matches(command) {
			t.Fatal("stale or incompatible mandatory evidence was accepted")
		}
	}
}

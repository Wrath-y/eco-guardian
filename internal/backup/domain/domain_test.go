package backupdomain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
)

var (
	testBackupID  = domain.ID("018f0f3c-7b4a-7cc1-8f4b-1a2b3c4d5e6f")
	testProjectID = domain.ID("018f0f3c-7b4b-7cc1-8f4b-1a2b3c4d5e6f")
	testJobID     = domain.ID("018f0f3c-7b4c-7cc1-8f4b-1a2b3c4d5e6f")
)

func manifestBody() ManifestBody {
	return ManifestBody{
		ManifestVersion: ManifestSchemaVersion,
		BackupID:        testBackupID,
		ProjectID:       testProjectID,
		Type:            Manual,
		Trigger:         TriggerUser,
		CreatedAt:       time.Date(2026, 8, 26, 3, 4, 5, 600, time.UTC),
		AppVersion:      "1.2.3",
		SchemaVersion:   21,
		DBBytes:         4096,
		DBSHA256:        strings.Repeat("a", 64),
		Source:          SourceIdentity{},
		Extensions:      map[string]json.RawMessage{"z": json.RawMessage(`{"b":2,"a":1}`), "a": json.RawMessage(`true`)},
	}
}

func TestManifestCanonicalHashExcludesOwnHashAndIgnoresMapOrder(t *testing.T) {
	left, err := NewManifest(manifestBody())
	if err != nil {
		t.Fatal(err)
	}
	rightBody := manifestBody()
	rightBody.Extensions = map[string]json.RawMessage{"a": json.RawMessage(`true`), "z": json.RawMessage(`{"a":1,"b":2}`)}
	right, err := NewManifest(rightBody)
	if err != nil {
		t.Fatal(err)
	}
	leftJSON, leftErr := left.CanonicalJSON()
	rightJSON, rightErr := right.CanonicalJSON()
	if leftErr != nil || rightErr != nil || left.ManifestHash != right.ManifestHash || string(leftJSON) != string(rightJSON) {
		t.Fatalf("canonical manifests differ: %v %v\n%s\n%s", leftErr, rightErr, leftJSON, rightJSON)
	}
	left.ManifestHash = strings.Repeat("f", 64)
	if left.Valid() {
		t.Fatal("self hash mutation was accepted")
	}
}

func TestManifestStrictDecodeRoundTripAndRejectsMalformedInputs(t *testing.T) {
	manifest, err := NewManifest(manifestBody())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeManifest(encoded)
	if err != nil || !decoded.Valid() || decoded.ManifestHash != manifest.ManifestHash {
		t.Fatalf("decode=%#v err=%v", decoded, err)
	}
	for _, invalid := range [][]byte{
		encoded[:len(encoded)-1],
		[]byte(`{"manifest_version":"x","manifest_version":"y"}`),
		append(encoded[:len(encoded)-1], []byte(`,"unknown":true}`)...),
		[]byte(`{"db_bytes":1e999}`),
	} {
		if _, decodeErr := DecodeManifest(invalid); decodeErr == nil {
			t.Fatalf("invalid manifest accepted: %s", invalid)
		}
	}
	oversized := make([]byte, MaxManifestBytes+1)
	if _, err = DecodeManifest(oversized); err == nil {
		t.Fatal("oversized manifest accepted")
	}
}

func TestBackupCommandHashAndMandatoryIdentity(t *testing.T) {
	manual := Command{CommandVersion: CommandVersion, ProjectID: testProjectID, Purpose: Manual, ManualReason: "manual", Source: SourceIdentity{}}
	hash, err := manual.Hash()
	key, keyErr := manual.UniquenessKey()
	if err != nil || keyErr != nil || len(hash) != 64 || !strings.Contains(key, hash) {
		t.Fatalf("hash=%q key=%q err=%v/%v", hash, key, err, keyErr)
	}
	changed := manual
	changed.ManualReason = "before-edit"
	changedHash, _ := changed.Hash()
	if changedHash == hash {
		t.Fatal("changed command reused canonical hash")
	}
	mandatory := Command{CommandVersion: CommandVersion, ProjectID: testProjectID, Purpose: Release, CallerJobID: testJobID, CallerHash: strings.Repeat("b", 64), Source: SourceIdentity{CallerJobID: testJobID, RequestHash: strings.Repeat("b", 64)}}
	if !mandatory.Valid() {
		t.Fatal("valid mandatory command rejected")
	}
	mandatory.CallerHash = ""
	if mandatory.Valid() {
		t.Fatal("mandatory command without caller hash accepted")
	}
}

func TestDailyPolicyRequiresExplicitBoundWaiverOnlyForBusinessWrites(t *testing.T) {
	observation := DailyObservation{ProjectID: testProjectID, LocalDate: "2026-08-26", BusinessWrite: true}
	if state, err := ReduceDailyPolicy(observation); err != nil || state != DailyRequired {
		t.Fatalf("state=%s err=%v", state, err)
	}
	observation.FailedJobID = testJobID
	if state, _ := ReduceDailyPolicy(observation); state != DailyAwaitingWaiver {
		t.Fatalf("state=%s", state)
	}
	observation.Waiver = &DailyWaiver{Version: "daily-waiver-v1", ProjectID: testProjectID, LocalDate: observation.LocalDate, FailedJobID: testJobID, Confirmed: true, ConfirmedBy: "local-user", ConfirmedAt: time.Date(2026, 8, 26, 4, 0, 0, 0, time.UTC)}
	if state, _ := ReduceDailyPolicy(observation); state != DailyExplicitlyWaived {
		t.Fatalf("state=%s", state)
	}
	observation.BusinessWrite = false
	if state, _ := ReduceDailyPolicy(observation); state != DailySuccessful {
		t.Fatalf("system write state=%s", state)
	}
}

func TestRetentionKeepsManualRestorePreInvalidAndLeasedArtifacts(t *testing.T) {
	base := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	ids := []domain.ID{
		"018f0f3c-7b40-7cc1-8f4b-1a2b3c4d5e6f",
		"018f0f3c-7b41-7cc1-8f4b-1a2b3c4d5e6f",
		"018f0f3c-7b42-7cc1-8f4b-1a2b3c4d5e6f",
		"018f0f3c-7b43-7cc1-8f4b-1a2b3c4d5e6f",
		"018f0f3c-7b44-7cc1-8f4b-1a2b3c4d5e6f",
	}
	artifacts := []Artifact{
		{ID: ids[0], Type: Daily, CreatedAt: base, Validation: ValidationValid, Published: true},
		{ID: ids[1], Type: Daily, CreatedAt: base.Add(time.Hour), Validation: ValidationValid, Published: true},
		{ID: ids[2], Type: Manual, CreatedAt: base.Add(-time.Hour), Validation: ValidationValid, Published: true},
		{ID: ids[3], Type: Release, CreatedAt: base, Validation: ValidationValid, Published: true, Leased: true},
		{ID: ids[4], Type: Migration, CreatedAt: base.Add(time.Hour), Validation: ValidationDamaged, Published: true},
	}
	selected, err := SelectRetention(artifacts, 1, 1)
	if err != nil || len(selected) != 1 || selected[0] != ids[0] {
		t.Fatalf("selected=%v err=%v", selected, err)
	}
}

func TestRetentionUsesStableTieBreakAndSharedReleaseMigrationQuota(t *testing.T) {
	created := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	ids := []domain.ID{
		"018f0f3c-7b40-7cc1-8f4b-1a2b3c4d5e6f",
		"018f0f3c-7b41-7cc1-8f4b-1a2b3c4d5e6f",
		"018f0f3c-7b42-7cc1-8f4b-1a2b3c4d5e6f",
		"018f0f3c-7b43-7cc1-8f4b-1a2b3c4d5e6f",
		"018f0f3c-7b44-7cc1-8f4b-1a2b3c4d5e6f",
		"018f0f3c-7b45-7cc1-8f4b-1a2b3c4d5e6f",
		"018f0f3c-7b46-7cc1-8f4b-1a2b3c4d5e6f",
	}
	artifacts := []Artifact{
		{ID: ids[2], Type: Daily, CreatedAt: created, Validation: ValidationValid, Published: true},
		{ID: ids[0], Type: Daily, CreatedAt: created, Validation: ValidationValid, Published: true},
		{ID: ids[1], Type: Daily, CreatedAt: created, Validation: ValidationValid, Published: true},
		{ID: ids[5], Type: Release, CreatedAt: created, Validation: ValidationValid, Published: true},
		{ID: ids[3], Type: Migration, CreatedAt: created, Validation: ValidationValid, Published: true},
		{ID: ids[4], Type: Release, CreatedAt: created, Validation: ValidationValid, Published: true},
		{ID: ids[6], Type: RestorePre, CreatedAt: created.Add(-365 * 24 * time.Hour), Validation: ValidationValid, Published: true},
	}
	want := []domain.ID{ids[0], ids[1], ids[3], ids[4]}
	for rotation := 0; rotation < len(artifacts); rotation++ {
		shuffled := append(append([]Artifact(nil), artifacts[rotation:]...), artifacts[:rotation]...)
		selected, err := SelectRetention(shuffled, 1, 1)
		if err != nil || len(selected) != len(want) {
			t.Fatalf("rotation=%d selected=%v err=%v", rotation, selected, err)
		}
		for index := range want {
			if selected[index] != want[index] {
				t.Fatalf("rotation=%d selected=%v want=%v", rotation, selected, want)
			}
		}
	}
}

func FuzzDecodeManifestNeverAcceptsMutationWithoutValidHash(f *testing.F) {
	manifest, _ := NewManifest(manifestBody())
	encoded, _ := manifest.CanonicalJSON()
	f.Add(encoded)
	f.Add([]byte(`{"manifest_version":1}`))
	f.Fuzz(func(t *testing.T, input []byte) {
		decoded, err := DecodeManifest(input)
		if err == nil && !decoded.Valid() {
			t.Fatal("decoder accepted an invalid manifest")
		}
	})
}

func FuzzDailyPolicyDateAndCommandHashIsolation(f *testing.F) {
	f.Add(int16(0), "manual")
	f.Add(int16(365), "before release")
	f.Add(int16(-365), "备份")
	f.Fuzz(func(t *testing.T, dayOffset int16, reason string) {
		if reason == "" || reason != strings.TrimSpace(reason) || len(reason) > 120 {
			t.Skip()
		}
		date := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(dayOffset))
		localDate := date.Format("2006-01-02")
		waiver := DailyWaiver{Version: "daily-waiver-v1", ProjectID: testProjectID, LocalDate: localDate, FailedJobID: testJobID, Confirmed: true, ConfirmedBy: "local-user", ConfirmedAt: date.Add(time.Hour)}
		observation := DailyObservation{ProjectID: testProjectID, LocalDate: localDate, BusinessWrite: true, FailedJobID: testJobID, Waiver: &waiver}
		if state, err := ReduceDailyPolicy(observation); err != nil || state != DailyExplicitlyWaived {
			t.Fatalf("same-date waiver state=%s err=%v", state, err)
		}
		observation.LocalDate = date.AddDate(0, 0, 1).Format("2006-01-02")
		if state, err := ReduceDailyPolicy(observation); err != nil || state != DailyAwaitingWaiver {
			t.Fatalf("next-date waiver state=%s err=%v", state, err)
		}

		command := Command{CommandVersion: CommandVersion, ProjectID: testProjectID, Purpose: Manual, ManualReason: reason, Source: SourceIdentity{}}
		first, err := command.Hash()
		second, replayErr := command.Hash()
		if err != nil || replayErr != nil || first != second {
			t.Fatalf("non-deterministic command hash %q/%q err=%v/%v", first, second, err, replayErr)
		}
		changed := command
		changed.ManualReason += "!"
		if changed.Valid() {
			changedHash, changedErr := changed.Hash()
			if changedErr != nil || changedHash == first {
				t.Fatalf("changed command hash=%q original=%q err=%v", changedHash, first, changedErr)
			}
		}
	})
}

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	root "github.com/zouyi/eco-guardian"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/validation"
	versioningdiff "github.com/zouyi/eco-guardian/internal/versioning/diff"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

type fakeMigrationBackup struct {
	evidence BackupEvidence
	err      error
	calls    int
}

type policyCatalog map[string]bool

func (catalog policyCatalog) SupportsCapabilityContract(requirement versioningpolicy.CapabilityRequirement) bool {
	return catalog[requirement.CapabilityID+"/"+requirement.GateID+"/"+requirement.ContractVersion]
}

func (f *fakeMigrationBackup) Backup(context.Context, string, domain.ID) (BackupEvidence, error) {
	f.calls++
	return f.evidence, f.err
}

func newStore(t *testing.T) *Store {
	t.Helper()
	r, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := Create(context.Background(), t.TempDir(), r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func tagDraft(key string) domain.EntityDraft {
	return domain.EntityDraft{Key: key, Name: key, Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}}
}

func TestConnectionPragmasAndConstraints(t *testing.T) {
	s := newStore(t)
	for _, assertion := range []struct{ query, want string }{{"PRAGMA foreign_keys", "1"}, {"PRAGMA journal_mode", "wal"}, {"PRAGMA busy_timeout", "5000"}} {
		var got string
		if err := s.db.QueryRow(assertion.query).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != assertion.want {
			t.Fatalf("%s = %q, want %q", assertion.query, got, assertion.want)
		}
	}
	if _, _, err := s.Create(context.Background(), domain.KindTag, tagDraft("fire")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Create(context.Background(), domain.KindTag, tagDraft("fire")); !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("duplicate = %v", err)
	}
}

func TestVersioningSchemaDefinitionHasRequiredFactsAndForeignKeys(t *testing.T) {
	s := newStore(t)
	for _, table := range []string{"revision_metadata", "release_policies", "jobs", "job_events", "release_intents", "releases", "active_release_pointer"} {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil {
			t.Fatalf("missing %s: %v", table, err)
		}
	}
	for _, index := range []string{"release_policies_display_version_unique", "jobs_project_idempotency_unique", "release_intents_job_unique", "releases_intent_unique", "active_release_pointer_generation_unique"} {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&name); err != nil {
			t.Fatalf("missing %s: %v", index, err)
		}
	}
	var generation int
	if err := s.db.QueryRow(`SELECT generation FROM active_release_pointer WHERE singleton=1`).Scan(&generation); err != nil || generation != 0 {
		t.Fatalf("active pointer generation=%d err=%v", generation, err)
	}
}

func TestVersioningHistoryIsImmutableWhileStateMachinesCanAdvance(t *testing.T) {
	s := newStore(t)
	_, revision, err := s.Create(context.Background(), domain.KindTag, tagDraft("versioned"))
	if err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("a", 64)
	policyID, jobID, intentID, releaseID := mustID(t), mustID(t), mustID(t), mustID(t)
	if _, err = s.db.Exec(`INSERT INTO release_policies(id,display_version,canonical_body,canonical_hash,created_at) VALUES(?,?,?,?,?)`, policyID, 2, `{}`, hash, "2026-08-05T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO jobs(id,project_uuid,kind,revision_id,input_hash,idempotency_key,request_hash,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, jobID, s.ProjectID(), "release", revision.ID, hash, "key", hash, "queued", "2026-08-05T00:00:00Z", "2026-08-05T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO release_intents(id,job_id,candidate_revision_id,policy_id,gate_manifest,gate_manifest_hash,confirmations,request_hash,idempotency_key,phase,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, intentID, jobID, revision.ID, policyID, `{}`, hash, `[]`, hash, "key", "QUEUED", "2026-08-05T00:00:00Z", "2026-08-05T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO releases(id,revision_id,policy_id,intent_id,notes,gate_evidence,confirmations,created_at) VALUES(?,?,?,?,?,?,?,?)`, releaseID, revision.ID, policyID, intentID, "", `[]`, `[]`, "2026-08-05T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE revision_metadata SET name='mutated'`, `DELETE FROM revision_metadata`,
		`UPDATE release_policies SET canonical_body='mutated'`, `DELETE FROM release_policies`,
		`UPDATE releases SET notes='mutated'`, `DELETE FROM releases`,
	} {
		if _, err = s.db.Exec(statement); err == nil {
			t.Fatalf("immutable history accepted %q", statement)
		}
	}
	if result, err := s.db.Exec(`UPDATE jobs SET status='running',updated_at=? WHERE id=? AND status='queued'`, "2026-08-05T00:00:01Z", jobID); err != nil {
		t.Fatal(err)
	} else if rows, _ := result.RowsAffected(); rows != 1 {
		t.Fatalf("job CAS rows=%d", rows)
	}
	if _, err = s.db.Exec(`UPDATE jobs SET request_hash=? WHERE id=?`, strings.Repeat("b", 64), jobID); err == nil {
		t.Fatal("job identity mutation accepted")
	}
	if result, err := s.db.Exec(`UPDATE release_intents SET phase='RECHECKED',updated_at=? WHERE id=? AND phase='QUEUED'`, "2026-08-05T00:00:01Z", intentID); err != nil {
		t.Fatal(err)
	} else if rows, _ := result.RowsAffected(); rows != 1 {
		t.Fatalf("intent CAS rows=%d", rows)
	}
	if _, err = s.db.Exec(`UPDATE release_intents SET request_hash=? WHERE id=?`, strings.Repeat("b", 64), intentID); err == nil {
		t.Fatal("intent identity mutation accepted")
	}
	if _, err = s.db.Exec(`UPDATE active_release_pointer SET active_release_id=?,generation=1 WHERE singleton=1 AND generation=0`, releaseID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE active_release_pointer SET generation=0 WHERE singleton=1`); err == nil {
		t.Fatal("pointer generation regression accepted")
	}
}

func TestReleasePolicyRepositoryCreatesImmutableVersionsAndPagesStably(t *testing.T) {
	s := newStore(t)
	catalog := policyCatalog{"graph/projection/1": true, "risk/threshold/1": true}
	definition := versioningpolicy.Definition{
		Samples:     1000,
		ThresholdID: "threshold-v1",
		ThresholdOn: true,
		Scenes: []versioningpolicy.Scene{
			{ID: "required", Required: true, Metrics: []versioningpolicy.Metric{{ID: "damage", Required: true}}},
			{ID: "optional", Required: false, Metrics: []versioningpolicy.Metric{{ID: "healing", Required: false}}},
		},
		Capabilities: []versioningpolicy.CapabilityRequirement{{CapabilityID: "risk", GateID: "threshold", ContractVersion: "1"}, {CapabilityID: "graph", GateID: "projection", ContractVersion: "1"}},
	}
	first, err := s.CreatePolicy(context.Background(), definition, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if first.DisplayVersion != 2 || !first.Valid() {
		t.Fatalf("first=%#v", first)
	}
	secondDefinition := definition
	secondDefinition.Scenes = []versioningpolicy.Scene{definition.Scenes[1], definition.Scenes[0]}
	secondDefinition.Capabilities = []versioningpolicy.CapabilityRequirement{definition.Capabilities[1], definition.Capabilities[0]}
	second, err := s.CreatePolicy(context.Background(), secondDefinition, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if second.DisplayVersion != first.DisplayVersion+1 || second.CanonicalHash != first.CanonicalHash || second.ID == first.ID {
		t.Fatalf("second=%#v first=%#v", second, first)
	}
	stored, err := s.GetPolicy(context.Background(), first.ID)
	storedCanonical, canonicalErr := stored.CanonicalJSON()
	firstCanonical, firstCanonicalErr := first.CanonicalJSON()
	if err != nil || canonicalErr != nil || firstCanonicalErr != nil || stored.ID != first.ID || stored.DisplayVersion != first.DisplayVersion || stored.CanonicalHash != first.CanonicalHash || string(storedCanonical) != string(firstCanonical) {
		t.Fatalf("stored=%#v first=%#v err=%v canonical errors=%v/%v", stored, first, err, canonicalErr, firstCanonicalErr)
	}
	page, err := s.ListPolicies(context.Background(), "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != second.ID || page.NextCursor == "" {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	next, err := s.ListPolicies(context.Background(), page.NextCursor, 1)
	if err != nil || len(next.Items) != 1 || next.Items[0].ID != first.ID {
		t.Fatalf("next=%#v err=%v", next, err)
	}
	if _, err := s.db.Exec(`UPDATE release_policies SET canonical_body='{}' WHERE id=?`, first.ID); err == nil {
		t.Fatal("policy mutation accepted")
	}
}

func TestReleasePolicyRepositoryRejectsInvalidDefinitionsAndUnknownContracts(t *testing.T) {
	s := newStore(t)
	catalog := policyCatalog{"graph/projection/1": true}
	valid := versioningpolicy.Definition{Samples: 1, ThresholdID: "threshold", ThresholdOn: true, Scenes: []versioningpolicy.Scene{{ID: "scene", Required: true, Metrics: []versioningpolicy.Metric{{ID: "metric", Required: true}}}}, Capabilities: []versioningpolicy.CapabilityRequirement{{CapabilityID: "graph", GateID: "projection", ContractVersion: "1"}}}
	for _, invalid := range []versioningpolicy.Definition{
		{Samples: 0, ThresholdID: valid.ThresholdID, ThresholdOn: true, Scenes: valid.Scenes, Capabilities: valid.Capabilities},
		{Samples: valid.Samples, ThresholdID: "", ThresholdOn: true, Scenes: valid.Scenes, Capabilities: valid.Capabilities},
		{Samples: valid.Samples, ThresholdID: valid.ThresholdID, ThresholdOn: false, Scenes: valid.Scenes, Capabilities: valid.Capabilities},
		{Samples: valid.Samples, ThresholdID: valid.ThresholdID, ThresholdOn: true, Scenes: []versioningpolicy.Scene{{ID: "same", Metrics: valid.Scenes[0].Metrics}, {ID: "same", Metrics: valid.Scenes[0].Metrics}}, Capabilities: valid.Capabilities},
		{Samples: valid.Samples, ThresholdID: valid.ThresholdID, ThresholdOn: true, Scenes: []versioningpolicy.Scene{{ID: "scene", Metrics: []versioningpolicy.Metric{{ID: "same", Required: true}, {ID: "same", Required: true}}}}, Capabilities: valid.Capabilities},
	} {
		if _, err := s.CreatePolicy(context.Background(), invalid, catalog); !errors.Is(err, ErrReleasePolicyInvalid) {
			t.Fatalf("invalid=%#v err=%v", invalid, err)
		}
	}
	unknown := valid
	unknown.Capabilities = []versioningpolicy.CapabilityRequirement{{CapabilityID: "unknown", GateID: "missing", ContractVersion: "1"}}
	if _, err := s.CreatePolicy(context.Background(), unknown, catalog); !errors.Is(err, ErrReleasePolicyInvalid) {
		t.Fatalf("unknown contract err=%v", err)
	}
}

func TestPopulatedOlderProjectRequiresVerifiedBackupBeforeMigration(t *testing.T) {
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	initial, err := root.Assets.ReadFile("migrations/0001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(initial)); err != nil {
		t.Fatal(err)
	}
	projectID, revisionID := mustID(t), mustID(t)
	created := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = db.Exec(`INSERT INTO project_meta(id,db_schema_version,created_at) VALUES(?,?,?); INSERT INTO config_revisions(id,display_revision,config_hash,created_at) VALUES(?,?,?,?)`, projectID, 1, created, revisionID, 1, strings.Repeat("a", 64), created); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err = Open(dir, registry); !errors.Is(err, ErrMigrationBackupRequired) {
		t.Fatalf("missing backup err=%v", err)
	}
	db, err = sql.Open("sqlite", filepath.Join(dir, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	var before int
	if err = db.QueryRow(`SELECT db_schema_version FROM project_meta`).Scan(&before); err != nil || before != 1 {
		t.Fatalf("migration wrote before backup: version=%d err=%v", before, err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	backup := &fakeMigrationBackup{evidence: BackupEvidence{Online: true, IntegrityChecked: true, Checksum: strings.Repeat("b", 64)}}
	invalid := &fakeMigrationBackup{evidence: BackupEvidence{Online: true, IntegrityChecked: false, Checksum: strings.Repeat("b", 64)}}
	if _, _, err = OpenWithMigrationBackup(context.Background(), dir, registry, invalid); !errors.Is(err, ErrMigrationBackupRequired) {
		t.Fatalf("invalid integrity err=%v", err)
	}
	store, openedID, err := OpenWithMigrationBackup(context.Background(), dir, registry, backup)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if openedID != projectID || backup.calls != 1 {
		t.Fatalf("opened=%s backup calls=%d", openedID, backup.calls)
	}
	var after int
	if err = store.db.QueryRow(`SELECT db_schema_version FROM project_meta`).Scan(&after); err != nil || after != currentSchemaVersion {
		t.Fatalf("version=%d err=%v", after, err)
	}
}

func TestMigrationStepsRollbackAndReplayWithoutDuplicates(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), databaseName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	initial, err := root.Assets.ReadFile("migrations/0001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(initial)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO project_meta(id,db_schema_version,created_at) VALUES(?,?,?)`, mustID(t), 1, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	for _, failedStep := range []string{"validation-v2", "versioning-v3", "graph-sync-v5"} {
		tx, txErr := db.BeginTx(context.Background(), nil)
		if txErr != nil {
			t.Fatal(txErr)
		}
		txErr = applyMigrationSteps(context.Background(), tx, 1, func(step string) error {
			if step == failedStep {
				return errors.New("injected restart")
			}
			return nil
		})
		if txErr == nil {
			t.Fatalf("%s migration unexpectedly succeeded", failedStep)
		}
		_ = tx.Rollback()
		var version int
		if txErr = db.QueryRow(`SELECT db_schema_version FROM project_meta`).Scan(&version); txErr != nil || version != 1 {
			t.Fatalf("%s persisted version=%d err=%v", failedStep, version, txErr)
		}
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = applyMigrationSteps(context.Background(), tx, 1, nil); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = verifyMigrationSteps(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM schema_migration_steps`).Scan(&count); err != nil || count != 5 {
		t.Fatalf("migration steps=%d err=%v", count, err)
	}
}

func TestUnresolvedHistoricalRevisionAndCloseReopenEquivalence(t *testing.T) {
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s, projectID, err := Create(context.Background(), dir, registry)
	if err != nil {
		t.Fatal(err)
	}
	revisionID := mustID(t)
	if _, err = s.db.Exec(`INSERT INTO config_revisions(id,display_revision,config_hash,created_at) VALUES(?,?,?,?)`, revisionID, 1, strings.Repeat("c", 64), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = backfillRevisionMetadata(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err = s.db.QueryRow(`SELECT version_manifest FROM revision_metadata WHERE revision_id=?`, revisionID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var manifest versioningrevision.VersionManifest
	if err = json.Unmarshal([]byte(raw), &manifest); err != nil {
		t.Fatal(err)
	}
	for _, entry := range manifest.Entries {
		if entry.CapabilityID == "schema" && entry.State != versioningrevision.Unregistered {
			t.Fatalf("resolved unknown schema=%#v", entry)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, reopenedID, err := Open(dir, registry)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopenedID != projectID {
		t.Fatalf("project identity changed %s != %s", reopenedID, projectID)
	}
	var version int
	if err = reopened.db.QueryRow(`SELECT db_schema_version FROM project_meta`).Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatalf("reopen version=%d err=%v", version, err)
	}
}

func TestRevisionMetadataBackfillPreservesHistoricalFactsAndMarksUnavailableCapabilities(t *testing.T) {
	s := newStore(t)
	_, revision, err := s.Create(context.Background(), domain.KindTag, tagDraft("backfill"))
	if err != nil {
		t.Fatal(err)
	}
	var blobs, runs, revisions int
	if err = s.db.QueryRow(`SELECT count(*) FROM entity_blobs`).Scan(&blobs); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT count(*) FROM validation_runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		tx, txErr := s.db.BeginTx(context.Background(), nil)
		if txErr != nil {
			t.Fatal(txErr)
		}
		if txErr = backfillRevisionMetadata(context.Background(), tx); txErr != nil {
			t.Fatal(txErr)
		}
		if txErr = tx.Commit(); txErr != nil {
			t.Fatal(txErr)
		}
	}
	var raw, manifestHash string
	if err = s.db.QueryRow(`SELECT version_manifest,version_manifest_hash FROM revision_metadata WHERE revision_id=?`, revision.ID).Scan(&raw, &manifestHash); err != nil {
		t.Fatal(err)
	}
	var manifest versioningrevision.VersionManifest
	if err = json.Unmarshal([]byte(raw), &manifest); err != nil || !manifest.Valid() {
		t.Fatalf("manifest=%s err=%v", raw, err)
	}
	hash, err := manifest.Hash()
	if err != nil || hash != manifestHash {
		t.Fatalf("hash=%s stored=%s err=%v", hash, manifestHash, err)
	}
	found := map[string]versioningrevision.VersionEntry{}
	for _, entry := range manifest.Entries {
		found[entry.CapabilityID] = entry
	}
	if found["schema"].State != versioningrevision.Registered || found["simulation-engine"].State != versioningrevision.Unregistered {
		t.Fatalf("manifest=%#v", manifest)
	}
	var afterBlobs, afterRuns, afterRevisions, metadata int
	_ = s.db.QueryRow(`SELECT count(*) FROM entity_blobs`).Scan(&afterBlobs)
	_ = s.db.QueryRow(`SELECT count(*) FROM validation_runs`).Scan(&afterRuns)
	_ = s.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&afterRevisions)
	_ = s.db.QueryRow(`SELECT count(*) FROM revision_metadata`).Scan(&metadata)
	if blobs != afterBlobs || runs != afterRuns || revisions != afterRevisions || metadata != 1 || revision.ConfigHash == "" {
		t.Fatalf("history changed blobs %d/%d runs %d/%d revisions %d/%d metadata=%d", blobs, afterBlobs, runs, afterRuns, revisions, afterRevisions, metadata)
	}
}

func TestStarterReleasePolicyIsCanonicalAndSeededExactlyOnce(t *testing.T) {
	s := newStore(t)
	var count int
	var body, hash string
	if err := s.db.QueryRow(`SELECT count(*),min(canonical_body),min(canonical_hash) FROM release_policies`).Scan(&count, &body, &hash); err != nil {
		t.Fatal(err)
	}
	if count != 1 || len(hash) != 64 {
		t.Fatalf("starter policies=%d hash=%q", count, hash)
	}
	var policy map[string]any
	if err := json.Unmarshal([]byte(body), &policy); err != nil {
		t.Fatal(err)
	}
	if policy["samples"] != float64(1000) || policy["threshold_enabled"] != true {
		t.Fatalf("starter=%s", body)
	}
	scenes, ok := policy["scenes"].([]any)
	if !ok || len(scenes) != 4 {
		t.Fatalf("starter scenes=%#v", policy["scenes"])
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = seedStarterReleasePolicy(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT count(*) FROM release_policies`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("replayed policies=%d err=%v", count, err)
	}
}

func TestListHasStableCursorAndKindIsolation(t *testing.T) {
	s := newStore(t)
	for _, key := range []string{"alpha", "bravo", "charlie"} {
		if _, _, err := s.Create(context.Background(), domain.KindTag, tagDraft(key)); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.Create(context.Background(), domain.KindAttribute, domain.EntityDraft{Key: "alpha", Name: "alpha", Payload: map[string]json.RawMessage{"value_type": json.RawMessage(`"decimal"`), "dimension": json.RawMessage(`"d"`), "base_unit": json.RawMessage(`"u"`), "default": json.RawMessage(`"1"`)}}); err != nil {
		t.Fatal(err)
	}
	first, err := s.List(context.Background(), domain.KindTag, "", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.NextCursor == "" || first.Items[0].Key != "alpha" {
		t.Fatalf("first=%#v", first)
	}
	second, err := s.List(context.Background(), domain.KindTag, "", first.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].Key != "charlie" {
		t.Fatalf("second=%#v", second)
	}
	if _, err := s.List(context.Background(), domain.KindTag, "", "", 201); err == nil {
		t.Fatal("unbounded page accepted")
	}
}

func TestArchiveReferenceProtectionAndRevisionImmutability(t *testing.T) {
	s := newStore(t)
	tag, _, err := s.Create(context.Background(), domain.KindTag, tagDraft("fire"))
	if err != nil {
		t.Fatal(err)
	}
	character := domain.EntityDraft{Key: "hero", Name: "hero", Payload: map[string]json.RawMessage{"attribute_values": json.RawMessage(`[]`), "skill_ids": json.RawMessage(`[]`), "item_ids": json.RawMessage(`[]`), "rule_blocks": json.RawMessage(`[]`)}, TagIDs: []domain.ID{tag.ID}}
	if _, _, err := s.Create(context.Background(), domain.KindCharacter, character); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Delete(context.Background(), domain.KindTag, tag.ID, tag.EntityVersion); err == nil {
		t.Fatal("referenced archive accepted")
	} else {
		var referenced *ReferencedError
		if !errors.As(err, &referenced) || len(referenced.References) == 0 {
			t.Fatalf("archive error = %v", err)
		}
	}
	var revisionID string
	if err := s.db.QueryRow("SELECT id FROM config_revisions ORDER BY display_revision LIMIT 1").Scan(&revisionID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE config_revisions SET config_hash='mutated' WHERE id=?", revisionID); err == nil {
		t.Fatal("revision update accepted")
	}
	if _, err := s.db.Exec("DELETE FROM revision_entities WHERE revision_id=?", revisionID); err == nil {
		t.Fatal("manifest delete accepted")
	}
}

func TestProjectIdentitySurvivesMoveAndRejectsNewerSchema(t *testing.T) {
	r, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	original := filepath.Join(root, "one")
	moved := filepath.Join(root, "two")
	s, id, err := Create(context.Background(), original, r)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	reopened, movedID, err := Open(moved, r)
	if err != nil {
		t.Fatal(err)
	}
	if id != movedID {
		t.Fatalf("id changed: %s %s", id, movedID)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	newer := filepath.Join(root, "newer")
	if err := os.Mkdir(newer, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(newer, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE project_meta(id TEXT PRIMARY KEY, db_schema_version INTEGER NOT NULL, created_at TEXT NOT NULL); INSERT INTO project_meta VALUES(?,?,?)", id, currentSchemaVersion+1, "2026-08-03T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, _, err := Open(newer, r); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("newer schema = %v", err)
	}
}

func TestSaveRollbackAtEveryStage(t *testing.T) {
	for _, stage := range []string{"blob", "working", "indexes", "revision", "metadata", "derived"} {
		t.Run(stage, func(t *testing.T) {
			s := newStore(t)
			s.failStage = func(at string) error {
				if at == stage {
					return errors.New("injected " + stage)
				}
				return nil
			}
			if _, _, err := s.Create(context.Background(), domain.KindTag, tagDraft("fire")); err == nil {
				t.Fatal("save unexpectedly succeeded")
			}
			for _, table := range []string{"entity_blobs", "working_entities", "entity_references", "entity_tags", "config_revisions", "revision_entities", "revision_metadata", "compiled_ast", "formula_index", "revision_references", "validation_runs", "validation_issues"} {
				var count int
				if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("%s left %d rows", table, count)
				}
			}
		})
	}
}

func TestEachEntitySaveWritesCanonicalRevisionMetadataAndLocalValidation(t *testing.T) {
	s := newStore(t)
	entity, created, err := s.Create(context.Background(), domain.KindTag, tagDraft("fire"))
	if err != nil {
		t.Fatal(err)
	}
	entity, patched, err := s.Patch(context.Background(), domain.KindTag, entity.ID, entity.EntityVersion, domain.EntityPatch{"name": json.RawMessage(`"Flame"`)})
	if err != nil {
		t.Fatal(err)
	}
	_, archived, err := s.Delete(context.Background(), domain.KindTag, entity.ID, entity.EntityVersion)
	if err != nil {
		t.Fatal(err)
	}
	for _, revision := range []domain.RevisionSummary{created, patched, archived} {
		var raw, storedHash, scope, validationRaw string
		if err := s.db.QueryRow(`SELECT version_manifest,version_manifest_hash FROM revision_metadata WHERE revision_id=?`, revision.ID).Scan(&raw, &storedHash); err != nil {
			t.Fatalf("metadata for %s: %v", revision.ID, err)
		}
		if err := s.db.QueryRow(`SELECT scope,version_manifest FROM validation_runs WHERE source_revision_id=?`, revision.ID).Scan(&scope, &validationRaw); err != nil {
			t.Fatalf("LOCAL run for %s: %v", revision.ID, err)
		}
		if scope != "LOCAL" || revision.Validation == nil || revision.Validation.Scope != "LOCAL" {
			t.Fatalf("revision %s local summary=%#v scope=%s", revision.ID, revision.Validation, scope)
		}
		var manifest versioningrevision.VersionManifest
		if err := json.Unmarshal([]byte(raw), &manifest); err != nil || !manifest.Valid() {
			t.Fatalf("metadata manifest=%s err=%v", raw, err)
		}
		canonical, err := manifest.CanonicalJSON()
		if err != nil || string(canonical) != raw {
			t.Fatalf("metadata is not canonical: raw=%s canonical=%s err=%v", raw, canonical, err)
		}
		hash, err := manifest.Hash()
		if err != nil || hash != storedHash {
			t.Fatalf("metadata hash=%s stored=%s err=%v", hash, storedHash, err)
		}
		var localVersions validation.VersionManifest
		if err := json.Unmarshal([]byte(validationRaw), &localVersions); err != nil {
			t.Fatalf("local manifest=%s err=%v", validationRaw, err)
		}
		want, err := versionManifestFromValidation(localVersions)
		if err != nil {
			t.Fatal(err)
		}
		wantCanonical, err := want.CanonicalJSON()
		if err != nil || string(wantCanonical) != raw {
			t.Fatalf("metadata/local mismatch: metadata=%s local=%s err=%v", raw, wantCanonical, err)
		}
	}
	var metadataCount int
	if err := s.db.QueryRow(`SELECT count(*) FROM revision_metadata`).Scan(&metadataCount); err != nil || metadataCount != 3 {
		t.Fatalf("metadata rows=%d err=%v", metadataCount, err)
	}
}

func TestRevisionMetadataRepositoryReadsStableCursorHistory(t *testing.T) {
	s := newStore(t)
	entity, first, err := s.Create(context.Background(), domain.KindTag, tagDraft("fire"))
	if err != nil {
		t.Fatal(err)
	}
	revisions := []domain.RevisionSummary{first}
	for _, name := range []string{"Flame", "Inferno", "Wildfire", "Ember"} {
		var revision domain.RevisionSummary
		entity, revision, err = s.Patch(context.Background(), domain.KindTag, entity.ID, entity.EntityVersion, domain.EntityPatch{"name": json.RawMessage(`"` + name + `"`)})
		if err != nil {
			t.Fatal(err)
		}
		revisions = append(revisions, revision)
	}
	record, err := s.GetRevisionRecord(context.Background(), revisions[2].ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.DisplayRevision != revisions[2].DisplayRevision || record.Metadata.RevisionID != revisions[2].ID || record.Metadata.ConfigHash != revisions[2].ConfigHash || !record.Metadata.Manifest.Valid() {
		t.Fatalf("record=%#v", record)
	}
	if _, err := s.GetRevisionRecord(context.Background(), mustID(t)); !errors.Is(err, ErrRevisionNotFound) {
		t.Fatalf("missing revision error=%v", err)
	}
	if _, err := s.ListRevisionRecords(context.Background(), "not-a-cursor", 2); !errors.Is(err, ErrInvalidRevisionCursor) {
		t.Fatalf("invalid cursor error=%v", err)
	}
	if _, err := s.ListRevisionRecords(context.Background(), "", 201); err == nil {
		t.Fatal("limit above 200 accepted")
	}

	page, err := s.ListRevisionRecords(context.Background(), "", 2)
	if err != nil {
		t.Fatal(err)
	}
	seen := make([]domain.ID, 0, len(revisions))
	for pageNumber := 0; ; pageNumber++ {
		for index, item := range page.Items {
			want := revisions[len(revisions)-1-len(seen)]
			if item.DisplayRevision != want.DisplayRevision || item.Metadata.RevisionID != want.ID || item.Metadata.ConfigHash != want.ConfigHash {
				t.Fatalf("page=%d item=%d got=%#v want=%#v", pageNumber, index, item, want)
			}
			seen = append(seen, item.Metadata.RevisionID)
		}
		if page.NextCursor == "" {
			break
		}
		page, err = s.ListRevisionRecords(context.Background(), page.NextCursor, 2)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != len(revisions) {
		t.Fatalf("history ids=%v", seen)
	}
}

func TestCheckpointCreatesSameContentRevisionWithLocalValidationAndAudit(t *testing.T) {
	s := newStore(t)
	_, current, err := s.Create(context.Background(), domain.KindTag, tagDraft("checkpoint"))
	if err != nil {
		t.Fatal(err)
	}
	var blobsBefore int
	if err := s.db.QueryRow(`SELECT count(*) FROM entity_blobs`).Scan(&blobsBefore); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := s.CreateCheckpoint(context.Background(), current.ID, "Baseline", "same content checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	changes, err := versioningdiff.CompareRevisions(context.Background(), s, current.ID, checkpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("same-content checkpoint diff=%#v", changes)
	}
	if checkpoint.ID == current.ID || checkpoint.DisplayRevision != current.DisplayRevision+1 || checkpoint.ConfigHash != current.ConfigHash || checkpoint.Validation == nil || checkpoint.Validation.Scope != "LOCAL" {
		t.Fatalf("checkpoint=%#v current=%#v", checkpoint, current)
	}
	var blobsAfter, currentEntities, checkpointEntities, differingManifestRows int
	if err := s.db.QueryRow(`SELECT count(*) FROM entity_blobs`).Scan(&blobsAfter); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM revision_entities WHERE revision_id=?`, current.ID).Scan(&currentEntities); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM revision_entities WHERE revision_id=?`, checkpoint.ID).Scan(&checkpointEntities); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM revision_entities source WHERE source.revision_id=? AND NOT EXISTS (SELECT 1 FROM revision_entities checkpoint WHERE checkpoint.revision_id=? AND checkpoint.entity_id=source.entity_id AND checkpoint.entity_version=source.entity_version AND checkpoint.status=source.status AND checkpoint.blob_hash=source.blob_hash)`, current.ID, checkpoint.ID).Scan(&differingManifestRows); err != nil {
		t.Fatal(err)
	}
	if blobsAfter != blobsBefore || currentEntities != checkpointEntities || differingManifestRows != 0 {
		t.Fatalf("blobs %d/%d entities %d/%d differing=%d", blobsBefore, blobsAfter, currentEntities, checkpointEntities, differingManifestRows)
	}
	record, err := s.GetRevisionRecord(context.Background(), checkpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Metadata.Name != "Baseline" || record.Metadata.Description != "same content checkpoint" || record.Metadata.ParentRevisionID != current.ID {
		t.Fatalf("metadata=%#v", record.Metadata)
	}
	var scope string
	if err := s.db.QueryRow(`SELECT scope FROM validation_runs WHERE source_revision_id=?`, checkpoint.ID).Scan(&scope); err != nil || scope != "LOCAL" {
		t.Fatalf("scope=%s err=%v", scope, err)
	}
	var revisionsBefore int
	if err := s.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&revisionsBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCheckpoint(context.Background(), current.ID, "stale", ""); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale checkpoint error=%v", err)
	}
	if _, err := s.CreateCheckpoint(context.Background(), checkpoint.ID, strings.Repeat("x", 201), ""); !errors.Is(err, ErrInvalidCheckpoint) {
		t.Fatalf("invalid checkpoint error=%v", err)
	}
	var revisionsAfter int
	if err := s.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&revisionsAfter); err != nil || revisionsAfter != revisionsBefore {
		t.Fatalf("revisions %d/%d err=%v", revisionsBefore, revisionsAfter, err)
	}
}

func TestActiveBaselineDoesNotSubstituteCurrentRevisionWhenNoReleaseIsActive(t *testing.T) {
	s := newStore(t)
	_, current, err := s.Create(context.Background(), domain.KindTag, tagDraft("nobaseline"))
	if err != nil {
		t.Fatal(err)
	}
	baseline, found, err := s.ActiveBaseline(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if found || baseline != "" || current.ID == baseline {
		t.Fatalf("baseline=%q found=%t current=%q", baseline, found, current.ID)
	}
	comparison, err := versioningdiff.CompareAgainstActiveBaseline(context.Background(), s, s, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.BaselineState != versioningdiff.NoBaseline || comparison.BaseRevisionID != "" || comparison.Changes != nil {
		t.Fatalf("comparison=%#v", comparison)
	}
}

func TestRestoreReleaseCreatesForwardRevisionAndPreservesHistory(t *testing.T) {
	s := newStore(t)
	entity, source, err := s.Create(context.Background(), domain.KindTag, tagDraft("restore"))
	if err != nil {
		t.Fatal(err)
	}
	entity, current, err := s.Patch(context.Background(), domain.KindTag, entity.ID, entity.EntityVersion, domain.EntityPatch{"name": json.RawMessage(`"Changed"`)})
	if err != nil {
		t.Fatal(err)
	}
	var policyID domain.ID
	if err := s.db.QueryRow(`SELECT id FROM release_policies ORDER BY display_version LIMIT 1`).Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	jobID, intentID, releaseID := mustID(t), mustID(t), mustID(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = s.db.Exec(`INSERT INTO jobs(id,project_uuid,kind,revision_id,input_hash,idempotency_key,request_hash,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, jobID, s.ProjectID(), "release", source.ID, source.ConfigHash, "restore-fixture", source.ConfigHash, "succeeded", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO release_intents(id,job_id,candidate_revision_id,policy_id,gate_manifest,gate_manifest_hash,confirmations,request_hash,idempotency_key,phase,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, intentID, jobID, source.ID, policyID, `{}`, source.ConfigHash, `[]`, source.ConfigHash, "restore-fixture", "COMMITTED", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO releases(id,revision_id,policy_id,intent_id,notes,gate_evidence,confirmations,created_at) VALUES(?,?,?,?,?,?,?,?)`, releaseID, source.ID, policyID, intentID, "fixture", `[]`, `[]`, now); err != nil {
		t.Fatal(err)
	}
	restored, err := s.RestoreRelease(context.Background(), current.ID, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ID == source.ID || restored.ID == current.ID || restored.ConfigHash != source.ConfigHash || restored.Validation == nil || restored.Validation.Scope != "LOCAL" {
		t.Fatalf("restored=%#v source=%#v current=%#v", restored, source, current)
	}
	working, err := s.Get(context.Background(), domain.KindTag, entity.ID)
	if err != nil || working.Name != "restore" {
		t.Fatalf("working=%#v err=%v", working, err)
	}
	record, err := s.GetRevisionRecord(context.Background(), restored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Metadata.ParentRevisionID != current.ID || record.Metadata.SourceRevisionID != source.ID || record.Metadata.SourceReleaseID != releaseID {
		t.Fatalf("restore metadata=%#v", record.Metadata)
	}
	var sourceName string
	if err := s.db.QueryRow(`SELECT json_extract(b.json,'$.name') FROM revision_entities r JOIN entity_blobs b ON b.hash=r.blob_hash WHERE r.revision_id=? AND r.entity_id=?`, source.ID, entity.ID).Scan(&sourceName); err != nil || sourceName != "restore" {
		t.Fatalf("source history name=%q err=%v", sourceName, err)
	}
	if _, err := s.RestoreRelease(context.Background(), current.ID, releaseID); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale restore error=%v", err)
	}
	if _, err := s.RestoreRelease(context.Background(), restored.ID, mustID(t)); !errors.Is(err, ErrReleaseNotFound) {
		t.Fatalf("missing release error=%v", err)
	}
}

func TestCheckpointAndRestoreFaultsRollBackEveryStage(t *testing.T) {
	for _, stage := range []string{"checkpoint_revision", "checkpoint_metadata", "checkpoint_derived", "checkpoint_validation"} {
		t.Run(stage, func(t *testing.T) {
			s := newStore(t)
			_, current, err := s.Create(context.Background(), domain.KindTag, tagDraft("checkpointfault"))
			if err != nil {
				t.Fatal(err)
			}
			assertRollbackCounts(t, s, func() error {
				s.failStage = func(at string) error {
					if at == stage {
						return errors.New("injected " + stage)
					}
					return nil
				}
				_, err := s.CreateCheckpoint(context.Background(), current.ID, "fault", "")
				return err
			})
		})
	}
	for _, stage := range []string{"restore_working", "restore_revision", "restore_metadata", "restore_derived", "restore_validation"} {
		t.Run(stage, func(t *testing.T) {
			s, entity, source, current, releaseID := restoreReleaseFixture(t)
			assertRollbackCounts(t, s, func() error {
				s.failStage = func(at string) error {
					if at == stage {
						return errors.New("injected " + stage)
					}
					return nil
				}
				_, err := s.RestoreRelease(context.Background(), current.ID, releaseID)
				return err
			})
			working, err := s.Get(context.Background(), domain.KindTag, entity.ID)
			if err != nil || working.Name != "Changed" {
				t.Fatalf("working=%#v err=%v", working, err)
			}
			var sourceName string
			if err := s.db.QueryRow(`SELECT json_extract(b.json,'$.name') FROM revision_entities r JOIN entity_blobs b ON b.hash=r.blob_hash WHERE r.revision_id=? AND r.entity_id=?`, source.ID, entity.ID).Scan(&sourceName); err != nil || sourceName != "restore" {
				t.Fatalf("source history=%q err=%v", sourceName, err)
			}
		})
	}
}

func TestRevisionDetailMergesImmutableTimelineAndActivePointer(t *testing.T) {
	s, _, source, _, releaseID := restoreReleaseFixture(t)
	if _, err := s.RunValidation(context.Background(), validation.SourceRevision, source.ID, validation.ScopeFull); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE active_release_pointer SET active_release_id=?,generation=1 WHERE singleton=1 AND generation=0`, releaseID); err != nil {
		t.Fatal(err)
	}
	detail, err := s.GetRevisionDetail(context.Background(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Record.Metadata.RevisionID != source.ID || detail.ActiveReleaseID != releaseID || detail.PointerGeneration != 1 {
		t.Fatalf("detail=%#v", detail)
	}
	wantTypes := map[string]bool{"revision_created": false, "validation_completed": false, "release_queued": false, "release_intent": false, "release_completed": false, "active_pointer_changed": false}
	for index, event := range detail.Timeline {
		if event.RevisionID != source.ID {
			t.Fatalf("event revision=%#v", event)
		}
		if _, ok := wantTypes[event.Type]; ok {
			wantTypes[event.Type] = true
		}
		if index > 0 {
			previous := detail.Timeline[index-1]
			if event.OccurredAt.Before(previous.OccurredAt) || (event.OccurredAt.Equal(previous.OccurredAt) && (event.ID < previous.ID || (event.ID == previous.ID && event.Type < previous.Type))) {
				t.Fatalf("unstable timeline order: %#v then %#v", previous, event)
			}
		}
	}
	for eventType, found := range wantTypes {
		if !found {
			t.Fatalf("missing %s in %#v", eventType, detail.Timeline)
		}
	}
	again, err := s.GetRevisionDetail(context.Background(), source.ID)
	if err != nil || len(again.Timeline) != len(detail.Timeline) {
		t.Fatalf("repeat detail=%#v err=%v", again, err)
	}
	for index := range detail.Timeline {
		if again.Timeline[index] != detail.Timeline[index] {
			t.Fatalf("timeline changed index=%d before=%#v after=%#v", index, detail.Timeline[index], again.Timeline[index])
		}
	}
}

func TestCheckpointAndRestoreRejectStaleMissingAndCrossProjectSourcesWithoutWrites(t *testing.T) {
	foreign, _, _, _, foreignReleaseID := restoreReleaseFixture(t)
	local := newStore(t)
	entity, current, err := local.Create(context.Background(), domain.KindTag, tagDraft("local"))
	if err != nil {
		t.Fatal(err)
	}
	var before int
	if err := local.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := local.CreateCheckpoint(context.Background(), "", "missing", ""); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("missing checkpoint precondition=%v", err)
	}
	if _, err := local.RestoreRelease(context.Background(), current.ID, foreignReleaseID); !errors.Is(err, ErrReleaseNotFound) {
		t.Fatalf("cross-project release=%v", err)
	}
	_, next, err := local.Patch(context.Background(), domain.KindTag, entity.ID, entity.EntityVersion, domain.EntityPatch{"name": json.RawMessage(`"updated"`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := local.CreateCheckpoint(context.Background(), current.ID, "stale", ""); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale checkpoint=%v", err)
	}
	if _, err := local.RestoreRelease(context.Background(), current.ID, foreignReleaseID); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale restore=%v", err)
	}
	checkpoint, err := local.CreateCheckpoint(context.Background(), next.ID, "same-content", "")
	if err != nil || checkpoint.ConfigHash != next.ConfigHash {
		t.Fatalf("repeated content checkpoint=%#v err=%v", checkpoint, err)
	}
	var after int
	if err := local.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&after); err != nil || after != before+2 {
		t.Fatalf("revisions=%d want=%d err=%v", after, before+2, err)
	}
	_ = foreign.Close()
}

func TestConcurrentEntitySaveAgainstCheckpointAndRestorePreservesWorkingState(t *testing.T) {
	t.Run("checkpoint", func(t *testing.T) {
		s := newStore(t)
		entity, current, err := s.Create(context.Background(), domain.KindTag, tagDraft("racecheckpoint"))
		if err != nil {
			t.Fatal(err)
		}
		errs := runConcurrent(func() error {
			_, _, err := s.Patch(context.Background(), domain.KindTag, entity.ID, entity.EntityVersion, domain.EntityPatch{"name": json.RawMessage(`"changed"`)})
			return err
		}, func() error {
			_, err := s.CreateCheckpoint(context.Background(), current.ID, "race", "")
			return err
		})
		assertConcurrentResults(t, errs)
		if _, err := s.Get(context.Background(), domain.KindTag, entity.ID); err != nil {
			t.Fatalf("working state unreadable: %v", err)
		}
	})
	t.Run("restore", func(t *testing.T) {
		s, entity, _, current, releaseID := restoreReleaseFixture(t)
		errs := runConcurrent(func() error {
			_, _, err := s.Patch(context.Background(), domain.KindTag, entity.ID, entity.EntityVersion, domain.EntityPatch{"name": json.RawMessage(`"changed-again"`)})
			return err
		}, func() error {
			_, err := s.RestoreRelease(context.Background(), current.ID, releaseID)
			return err
		})
		assertConcurrentResults(t, errs)
		if _, err := s.Get(context.Background(), domain.KindTag, entity.ID); err != nil {
			t.Fatalf("working state unreadable: %v", err)
		}
	})
}

func TestCheckpointAndRestoreSurviveRestartAsImmutableByteEquivalentHistory(t *testing.T) {
	s, entity, source, current, releaseID := restoreReleaseFixture(t)
	checkpoint, err := s.CreateCheckpoint(context.Background(), current.ID, "Restart checkpoint", "persist this description")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := s.RestoreRelease(context.Background(), checkpoint.ID, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	checkpointBefore, err := s.GetRevisionRecord(context.Background(), checkpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	restoredBefore, err := s.GetRevisionRecord(context.Background(), restored.ID)
	if err != nil {
		t.Fatal(err)
	}
	dir, registry := filepath.Dir(s.path), s.registry
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, _, err := Open(dir, registry)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	checkpointAfter, err := reopened.GetRevisionRecord(context.Background(), checkpoint.ID)
	if err != nil || !reflect.DeepEqual(checkpointAfter, checkpointBefore) {
		t.Fatalf("checkpoint changed=%#v before=%#v err=%v", checkpointAfter, checkpointBefore, err)
	}
	restoredAfter, err := reopened.GetRevisionRecord(context.Background(), restored.ID)
	if err != nil || !reflect.DeepEqual(restoredAfter, restoredBefore) {
		t.Fatalf("restore changed=%#v before=%#v err=%v", restoredAfter, restoredBefore, err)
	}
	if checkpointAfter.Metadata.Name != "Restart checkpoint" || checkpointAfter.Metadata.Description != "persist this description" || restoredAfter.Metadata.SourceRevisionID != source.ID || restoredAfter.Metadata.SourceReleaseID != releaseID {
		t.Fatalf("checkpoint=%#v restored=%#v", checkpointAfter.Metadata, restoredAfter.Metadata)
	}
	working, err := reopened.Get(context.Background(), domain.KindTag, entity.ID)
	if err != nil || working.Name != "restore" {
		t.Fatalf("restored working=%#v err=%v", working, err)
	}
	if _, err := reopened.db.Exec(`UPDATE revision_metadata SET name='mutated' WHERE revision_id=?`, checkpoint.ID); err == nil {
		t.Fatal("restart accepted metadata mutation")
	}
	for _, record := range []versioningrevision.Record{checkpointAfter, restoredAfter} {
		for _, entry := range record.Metadata.Manifest.Entries {
			if entry.CapabilityID == "simulation-engine" && entry.State != versioningrevision.Unregistered {
				t.Fatalf("resolved unavailable capability=%#v", entry)
			}
		}
	}
}

func TestDiffManifestMaterializesOnlySortedImmutableRevisions(t *testing.T) {
	s := newStore(t)
	entity, first, err := s.Create(context.Background(), domain.KindTag, tagDraft("diffmanifest"))
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := s.Patch(context.Background(), domain.KindTag, entity.ID, entity.EntityVersion, domain.EntityPatch{"name": json.RawMessage(`"changed"`)})
	if err != nil {
		t.Fatal(err)
	}
	firstEntities, err := s.Materialize(context.Background(), first.ID)
	if err != nil || len(firstEntities) != 1 || firstEntities[0].EntityID != entity.ID || firstEntities[0].Kind != domain.KindTag {
		t.Fatalf("first manifest=%#v err=%v", firstEntities, err)
	}
	secondEntities, err := s.Materialize(context.Background(), second.ID)
	if err != nil || len(secondEntities) != 1 || string(firstEntities[0].JSON) == string(secondEntities[0].JSON) {
		t.Fatalf("second manifest=%#v err=%v", secondEntities, err)
	}
	if _, err := s.Materialize(context.Background(), mustID(t)); !errors.Is(err, ErrDiffRevisionInvalid) {
		t.Fatalf("missing diff revision=%v", err)
	}
	if _, err := s.Materialize(context.Background(), ""); !errors.Is(err, ErrDiffRevisionInvalid) {
		t.Fatalf("invalid diff revision=%v", err)
	}
}

func TestDiffManifestSegmentsAreBoundedAndDoNotWaitForSaveWriteLane(t *testing.T) {
	s := newStore(t)
	_, first, err := s.Create(context.Background(), domain.KindTag, tagDraft("segmentone"))
	if err != nil {
		t.Fatal(err)
	}
	_, current, err := s.Create(context.Background(), domain.KindTag, tagDraft("segmenttwo"))
	if err != nil {
		t.Fatal(err)
	}
	s.writes.Lock()
	result := make(chan struct {
		entities []versioningdiff.EntityBlob
		err      error
	}, 1)
	go func() {
		entities, readErr := s.MaterializeSegment(context.Background(), current.ID, "", 1)
		result <- struct {
			entities []versioningdiff.EntityBlob
			err      error
		}{entities, readErr}
	}()
	select {
	case read := <-result:
		if read.err != nil || len(read.entities) != 1 {
			t.Fatalf("bounded read=%#v err=%v", read.entities, read.err)
		}
	case <-time.After(time.Second):
		t.Fatal("read-only diff segment waited for save write lane")
	}
	s.writes.Unlock()
	firstPage, err := s.MaterializeSegment(context.Background(), current.ID, "", 1)
	if err != nil || len(firstPage) != 1 {
		t.Fatalf("first segment=%#v err=%v", firstPage, err)
	}
	secondPage, err := s.MaterializeSegment(context.Background(), current.ID, firstPage[0].EntityID, 1)
	if err != nil || len(secondPage) != 1 || secondPage[0].EntityID == firstPage[0].EntityID {
		t.Fatalf("second segment=%#v err=%v", secondPage, err)
	}
	if _, err := s.MaterializeSegment(context.Background(), first.ID, "", 1); err != nil {
		t.Fatal(err)
	}
}

func runConcurrent(operations ...func() error) []error {
	start := make(chan struct{})
	errs := make(chan error, len(operations))
	var group sync.WaitGroup
	for _, operation := range operations {
		group.Add(1)
		go func(operation func() error) {
			defer group.Done()
			<-start
			errs <- operation()
		}(operation)
	}
	close(start)
	group.Wait()
	close(errs)
	results := make([]error, 0, len(operations))
	for err := range errs {
		results = append(results, err)
	}
	return results
}

func assertConcurrentResults(t *testing.T, errs []error) {
	t.Helper()
	for _, err := range errs {
		if err != nil && !errors.Is(err, ErrRevisionConflict) {
			t.Fatalf("unexpected concurrent operation error: %v", err)
		}
	}
}

func assertRollbackCounts(t *testing.T, s *Store, operation func() error) {
	t.Helper()
	tables := []string{"working_entities", "entity_blobs", "entity_references", "entity_tags", "config_revisions", "revision_entities", "revision_metadata", "compiled_ast", "formula_index", "revision_references", "validation_runs", "validation_issues"}
	before := make(map[string]int, len(tables))
	for _, table := range tables {
		var count int
		if err := s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		before[table] = count
	}
	if err := operation(); err == nil {
		t.Fatal("operation unexpectedly succeeded")
	}
	for _, table := range tables {
		var after int
		if err := s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&after); err != nil || after != before[table] {
			t.Fatalf("%s=%d want=%d err=%v", table, after, before[table], err)
		}
	}
}

func restoreReleaseFixture(t *testing.T) (*Store, domain.Entity, domain.RevisionSummary, domain.RevisionSummary, domain.ID) {
	t.Helper()
	s := newStore(t)
	entity, source, err := s.Create(context.Background(), domain.KindTag, tagDraft("restore"))
	if err != nil {
		t.Fatal(err)
	}
	entity, current, err := s.Patch(context.Background(), domain.KindTag, entity.ID, entity.EntityVersion, domain.EntityPatch{"name": json.RawMessage(`"Changed"`)})
	if err != nil {
		t.Fatal(err)
	}
	var policyID domain.ID
	if err := s.db.QueryRow(`SELECT id FROM release_policies ORDER BY display_version LIMIT 1`).Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	jobID, intentID, releaseID := mustID(t), mustID(t), mustID(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = s.db.Exec(`INSERT INTO jobs(id,project_uuid,kind,revision_id,input_hash,idempotency_key,request_hash,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, jobID, s.ProjectID(), "release", source.ID, source.ConfigHash, "restore-fault-fixture", source.ConfigHash, "succeeded", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO release_intents(id,job_id,candidate_revision_id,policy_id,gate_manifest,gate_manifest_hash,confirmations,request_hash,idempotency_key,phase,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, intentID, jobID, source.ID, policyID, `{}`, source.ConfigHash, `[]`, source.ConfigHash, "restore-fault-fixture", "COMMITTED", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO releases(id,revision_id,policy_id,intent_id,notes,gate_evidence,confirmations,created_at) VALUES(?,?,?,?,?,?,?,?)`, releaseID, source.ID, policyID, intentID, "fixture", `[]`, `[]`, now); err != nil {
		t.Fatal(err)
	}
	return s, entity, source, current, releaseID
}

func TestPatchBaseValidationRejectsBeforeWriteStages(t *testing.T) {
	s := newStore(t)
	entity, _, err := s.Create(context.Background(), domain.KindTag, tagDraft("fire"))
	if err != nil {
		t.Fatal(err)
	}
	s.failStage = func(string) error { return errors.New("write transaction should not start") }
	_, _, err = s.Patch(context.Background(), domain.KindTag, entity.ID, entity.EntityVersion, domain.EntityPatch{"key": json.RawMessage(`"INVALID KEY"`)})
	var validation ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("patch error=%v", err)
	}
	got, err := s.Get(context.Background(), domain.KindTag, entity.ID)
	if err != nil || got.Key != "fire" || got.EntityVersion != entity.EntityVersion {
		t.Fatalf("entity changed=%#v err=%v", got, err)
	}
}

func TestLocalPreflightDetectsChangedManifestAndDependencies(t *testing.T) {
	s := newStore(t)
	tag, _, err := s.Create(context.Background(), domain.KindTag, tagDraft("fire"))
	if err != nil {
		t.Fatal(err)
	}
	output, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	prospective, err := domain.NewEntity(domain.KindCharacter, domain.EntityDraft{Key: "hero", Name: "Hero", TagIDs: []domain.ID{tag.ID}, Payload: map[string]json.RawMessage{
		"attribute_values": json.RawMessage(`[{"output_attribute_id":"` + string(output) + `","expression":"min(1, 2)"}]`),
		"skill_ids":        json.RawMessage(`[]`),
		"item_ids":         json.RawMessage(`[]`),
		"rule_blocks":      json.RawMessage(`[]`),
	}}, s.now())
	if err != nil {
		t.Fatal(err)
	}
	preflight, err := s.preflightLocal(context.Background(), prospective)
	if err != nil || preflight.workingHash == "" || len(preflight.dependencies) != 2 {
		t.Fatalf("preflight=%#v err=%v", preflight, err)
	}
	if _, _, err = s.Patch(context.Background(), domain.KindTag, tag.ID, tag.EntityVersion, domain.EntityPatch{"name": json.RawMessage(`"Flame"`)}); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fresh, err := s.recheckLocalPreflightTx(context.Background(), tx, preflight)
	if err != nil || fresh {
		t.Fatalf("recheck fresh=%v err=%v", fresh, err)
	}
}

func TestSemanticBlockCreatesLocalRevisionAndImmutableReport(t *testing.T) {
	s := newStore(t)
	missing, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	entity, revision, err := s.Create(context.Background(), domain.KindCharacter, domain.EntityDraft{Key: "hero", Name: "Hero", Payload: map[string]json.RawMessage{
		"attribute_values": json.RawMessage(`[]`), "skill_ids": json.RawMessage(`["` + string(missing) + `"]`), "item_ids": json.RawMessage(`[]`), "rule_blocks": json.RawMessage(`[]`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if revision.Validation == nil || revision.Validation.Scope != "LOCAL" || revision.Validation.Block != 1 || revision.Validation.RunID == "" {
		t.Fatalf("local summary=%#v", revision.Validation)
	}
	report, err := s.GetValidationReport(context.Background(), string(revision.Validation.RunID))
	if err != nil || report.Run.Scope != validation.ScopeLocal || report.Run.Source.RevisionID != revision.ID || len(report.Issues) != 1 || report.Issues[0].Code != "REFERENCE_NOT_FOUND" || report.Issues[0].EntityID != entity.ID {
		t.Fatalf("report=%#v err=%v", report, err)
	}
}

func TestBlockRevisionLocalReportSurvivesRestart(t *testing.T) {
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	store, _, err := Create(context.Background(), dir, registry)
	if err != nil {
		t.Fatal(err)
	}
	missing, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	_, revision, err := store.Create(context.Background(), domain.KindCharacter, domain.EntityDraft{Key: "hero", Name: "Hero", Payload: map[string]json.RawMessage{
		"attribute_values": json.RawMessage(`[]`), "skill_ids": json.RawMessage(`["` + string(missing) + `"]`), "item_ids": json.RawMessage(`[]`), "rule_blocks": json.RawMessage(`[]`),
	}})
	if err != nil || revision.Validation == nil || revision.Validation.Block != 1 {
		t.Fatalf("revision=%#v err=%v", revision, err)
	}
	runID := revision.Validation.RunID
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, _, err := Open(dir, registry)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered, err := reopened.GetValidationReport(context.Background(), string(runID))
	if err != nil || recovered.Run.Scope != validation.ScopeLocal || recovered.Run.Source.RevisionID != revision.ID || recovered.Run.Summary.Block != 1 || len(recovered.Issues) != 1 {
		t.Fatalf("recovered=%#v err=%v", recovered, err)
	}
}

func TestLocalValidationFailureRollsBackTheWholeSave(t *testing.T) {
	for _, stage := range []string{"validation_run", "validation_issue"} {
		t.Run(stage, func(t *testing.T) {
			s := newStore(t)
			s.failStage = func(at string) error {
				if at == stage {
					return errors.New("injected " + stage)
				}
				return nil
			}
			var err error
			if stage == "validation_run" {
				_, _, err = s.Create(context.Background(), domain.KindTag, tagDraft("fire"))
			} else {
				attribute, idErr := domain.NewID()
				if idErr != nil {
					t.Fatal(idErr)
				}
				_, _, err = s.Create(context.Background(), domain.KindCharacter, domain.EntityDraft{Key: "hero", Name: "Hero", Payload: map[string]json.RawMessage{
					"attribute_values": json.RawMessage(`[{"output_attribute_id":"` + string(attribute) + `","expression":"@"}]`), "skill_ids": json.RawMessage(`[]`), "item_ids": json.RawMessage(`[]`), "rule_blocks": json.RawMessage(`[]`),
				}})
			}
			if err == nil {
				t.Fatal("save unexpectedly succeeded")
			}
			for _, table := range []string{"entity_blobs", "working_entities", "config_revisions", "revision_entities", "revision_metadata", "validation_runs", "validation_issues"} {
				var count int
				if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("%s left %d rows", table, count)
				}
			}
		})
	}
}

func TestLocalSavePersistsRevisionDerivedIndexes(t *testing.T) {
	s := newStore(t)
	tag, _, err := s.Create(context.Background(), domain.KindTag, tagDraft("fire"))
	if err != nil {
		t.Fatal(err)
	}
	attribute, _, err := s.Create(context.Background(), domain.KindAttribute, domain.EntityDraft{Key: "power", Name: "Power", Payload: map[string]json.RawMessage{
		"value_type": json.RawMessage(`"decimal"`), "dimension": json.RawMessage(`"scalar"`), "base_unit": json.RawMessage(`"scalar"`), "default": json.RawMessage(`"0"`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, revision, err := s.Create(context.Background(), domain.KindCharacter, domain.EntityDraft{Key: "hero", Name: "Hero", TagIDs: []domain.ID{tag.ID}, Payload: map[string]json.RawMessage{
		"attribute_values": json.RawMessage(`[{"output_attribute_id":"` + string(attribute.ID) + `","expression":"min(1, 2)"}]`),
		"skill_ids":        json.RawMessage(`[]`),
		"item_ids":         json.RawMessage(`[]`),
		"rule_blocks":      json.RawMessage(`[]`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"compiled_ast": 1, "formula_index": 0, "revision_references": 2} {
		var got int
		if err := s.db.QueryRow("SELECT count(*) FROM "+table+" WHERE revision_id=?", revision.ID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s=%d want %d", table, got, want)
		}
	}
}

func TestDerivedIndexFailureRollsBackWholeSave(t *testing.T) {
	s := newStore(t)
	output, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	s.failStage = func(at string) error {
		if at == "derived" {
			return errors.New("injected derived")
		}
		return nil
	}
	_, _, err = s.Create(context.Background(), domain.KindCharacter, domain.EntityDraft{Key: "hero", Name: "Hero", Payload: map[string]json.RawMessage{
		"attribute_values": json.RawMessage(`[{"output_attribute_id":"` + string(output) + `","expression":"min(1, 2)"}]`),
		"skill_ids":        json.RawMessage(`[]`),
		"item_ids":         json.RawMessage(`[]`),
		"rule_blocks":      json.RawMessage(`[]`),
	}})
	if err == nil {
		t.Fatal("save unexpectedly succeeded")
	}
	for _, table := range []string{"entity_blobs", "working_entities", "config_revisions", "revision_entities", "compiled_ast", "formula_index", "revision_references", "validation_runs", "validation_issues"} {
		var count int
		if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s left %d rows", table, count)
		}
	}
}

func TestInvalidFormulaSavesBlockWithoutCompiledAST(t *testing.T) {
	s := newStore(t)
	output, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	_, revision, err := s.Create(context.Background(), domain.KindCharacter, domain.EntityDraft{Key: "hero", Name: "Hero", Payload: map[string]json.RawMessage{
		"attribute_values": json.RawMessage(`[{"output_attribute_id":"` + string(output) + `","expression":"@"}]`),
		"skill_ids":        json.RawMessage(`[]`),
		"item_ids":         json.RawMessage(`[]`),
		"rule_blocks":      json.RawMessage(`[]`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if revision.Validation == nil || revision.Validation.Block == 0 {
		t.Fatalf("expected local BLOCK summary, got %#v", revision.Validation)
	}
	var astCount, refCount int
	if err := s.db.QueryRow("SELECT count(*) FROM compiled_ast WHERE revision_id=?", revision.ID).Scan(&astCount); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT count(*) FROM revision_references WHERE revision_id=?", revision.ID).Scan(&refCount); err != nil {
		t.Fatal(err)
	}
	if astCount != 0 || refCount != 1 {
		t.Fatalf("compiled_ast=%d revision_references=%d", astCount, refCount)
	}
}

func TestLocalSaveDefersGlobalRuleSafetyToFullValidation(t *testing.T) {
	s := newStore(t)
	_, revision, err := s.Create(context.Background(), domain.KindEffect, domain.EntityDraft{Key: "burn", Name: "Burn", Payload: map[string]json.RawMessage{
		"duration":       json.RawMessage(`"0"`),
		"modifiers":      json.RawMessage(`[]`),
		"trigger_blocks": json.RawMessage(`[]`),
		"stack_rule":     json.RawMessage(`{"operation":"Add","max_stacks":"0","refresh_policy":"refresh"}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if revision.Validation == nil || revision.Validation.Block != 0 {
		t.Fatalf("LOCAL must not run global stack safety: %#v", revision.Validation)
	}
	report, err := s.RunValidation(context.Background(), validation.SourceRevision, revision.ID, validation.ScopeFull)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, issue := range report.Issues {
		codes[issue.Code] = true
	}
	if !codes["STACK_PRIORITY_MISSING"] || !codes["STACK_UNBOUNDED"] {
		t.Fatalf("FULL issues=%#v", report.Issues)
	}
}

func TestDerivedIndexesRebuildFromStoredBlob(t *testing.T) {
	s := newStore(t)
	tag, _, err := s.Create(context.Background(), domain.KindTag, tagDraft("fire"))
	if err != nil {
		t.Fatal(err)
	}
	draft := domain.EntityDraft{Key: "hero", Name: "hero", TagIDs: []domain.ID{tag.ID}, Payload: map[string]json.RawMessage{"attribute_values": json.RawMessage(`[]`), "skill_ids": json.RawMessage(`[]`), "item_ids": json.RawMessage(`[]`), "rule_blocks": json.RawMessage(`[]`)}}
	entity, _, err := s.Create(context.Background(), domain.KindCharacter, draft)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := s.db.QueryRow(`SELECT b.json FROM working_entities w JOIN entity_blobs b ON b.hash=w.blob_hash WHERE w.id=?`, entity.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var restored domain.Entity
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	refs, tags := domain.ExtractIndexes(restored)
	for table, want := range map[string]int{"entity_references": len(refs), "entity_tags": len(tags)} {
		var got int
		if err := s.db.QueryRow("SELECT count(*) FROM "+table+" WHERE "+map[string]string{"entity_references": "source_entity_id", "entity_tags": "entity_id"}[table]+"=?", entity.ID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s=%d want %d", table, got, want)
		}
	}
}

func TestCreatePatchListAndReopen(t *testing.T) {
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	store, projectID, err := Create(context.Background(), dir, registry)
	if err != nil {
		t.Fatal(err)
	}
	draft := domain.EntityDraft{Key: "fire", Name: "Fire", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}}
	entity, first, err := store.Create(context.Background(), domain.KindTag, draft)
	if err != nil {
		t.Fatal(err)
	}
	if first.DisplayRevision != 1 || !entity.ID.Valid() {
		t.Fatalf("create = %#v %#v", entity, first)
	}
	page, err := store.List(context.Background(), domain.KindTag, "fire", "", 50)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("list = %#v %v", page, err)
	}
	next, second, err := store.Patch(context.Background(), domain.KindTag, entity.ID, entity.EntityVersion, domain.EntityPatch{"name": json.RawMessage(`"Flame"`)})
	if err != nil {
		t.Fatal(err)
	}
	if next.EntityVersion != 2 || second.DisplayRevision != 2 {
		t.Fatalf("patch = %#v %#v", next, second)
	}
	if _, _, err := store.Patch(context.Background(), domain.KindTag, entity.ID, 1, domain.EntityPatch{"name": json.RawMessage(`"stale"`)}); err != ErrRevisionConflict {
		t.Fatalf("stale patch = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, gotID, err := Open(dir, registry)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if gotID != projectID {
		t.Fatalf("project identity changed: %s %s", projectID, gotID)
	}
	got, err := reopened.Get(context.Background(), domain.KindTag, entity.ID)
	if err != nil || got.Name != "Flame" {
		t.Fatalf("reopened get = %#v %v", got, err)
	}
}

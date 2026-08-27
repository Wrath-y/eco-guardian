package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/validation"
)

// TestCapacityFixture exercises the pagination path against the v1 planning
// bound without paying the intentionally expensive per-save revision cost while
// loading the fixture. CRUD itself remains covered through the normal public
// repository APIs below and in store_test.go.
func TestCapacityFixture(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seedCapacityFixture(t, s, 10_000)
	if _, _, err := s.Create(ctx, domain.KindTag, tagDraft("typical_save")); err != nil {
		t.Fatal(err)
	}
	times := make([]time.Duration, 0, 25)
	for range 25 {
		started := time.Now()
		page, err := s.List(ctx, domain.KindTag, "", "", 200)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 200 || len(page.Items) > 200 || page.NextCursor == "" {
			t.Fatalf("unexpected page: %d/%q", len(page.Items), page.NextCursor)
		}
		times = append(times, time.Since(started))
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	p95 := times[(len(times)*95+99)/100-1]
	if p95 >= 200*time.Millisecond {
		t.Fatalf("10k fixture list p95=%s, want <200ms", p95)
	}
}

// TestValidationCapacityFixtures keeps the two published planning profiles
// reproducible from generated data: 2,000 entities/20,000 references is the
// typical FULL-validation target, while 10,000/100,000 verifies that source
// materialization and reference walking remain bounded without a revision
// save. RunValidation itself materializes through a read-only transaction, so
// its CPU analysis never holds the Store's serialized writer lock.
func TestValidationCapacityFixtures(t *testing.T) {
	t.Run("typical_2000_entities_20000_relations", func(t *testing.T) {
		s := newStore(t)
		seedReferenceFixture(t, s, 2_000, 10)
		started := time.Now()
		report, err := s.RunValidation(context.Background(), validation.SourceWorking, "", validation.ScopeFull)
		elapsed := time.Since(started)
		if err != nil || len(report.Issues) != 0 {
			t.Fatalf("FULL report=%#v err=%v", report.Run, err)
		}
		if elapsed >= 5*time.Second {
			t.Fatalf("typical FULL validation=%s, want <5s", elapsed)
		}
		started = time.Now()
		if _, _, err = s.Create(context.Background(), domain.KindTag, tagDraft("typical_local_save")); err != nil {
			t.Fatal(err)
		}
		if elapsed = time.Since(started); elapsed >= 5*time.Second {
			t.Fatalf("typical LOCAL save=%s, want <5s", elapsed)
		}
	})
	t.Run("upper_10000_entities_100000_relations", func(t *testing.T) {
		s := newStore(t)
		seedReferenceFixture(t, s, 10_000, 10)
		snapshot, err := s.MaterializeValidationSource(context.Background(), validation.SourceWorking, "")
		if err != nil || len(snapshot.Entities) != 10_000 {
			t.Fatalf("materialized=%d err=%v", len(snapshot.Entities), err)
		}
		references, _ := validation.WalkKnownSchema(snapshot.Entities)
		if len(references) != 100_000 {
			t.Fatalf("references=%d, want 100000", len(references))
		}
	})
}

func TestRevisionHistoryCapacityUsesBoundedPagesAtTwoHundredRevisions(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	entity, _, err := store.Create(ctx, domain.KindTag, tagDraft("revision_capacity"))
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index < 200; index++ {
		name, marshalErr := json.Marshal(fmt.Sprintf("Revision %03d", index+1))
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		entity, _, err = store.Patch(ctx, domain.KindTag, entity.ID, entity.EntityVersion, domain.EntityPatch{"name": name})
		if err != nil {
			t.Fatalf("revision %d: %v", index+1, err)
		}
	}
	seen := map[domain.ID]bool{}
	cursor := ""
	for {
		page, listErr := store.ListRevisionRecords(ctx, cursor, 50)
		if listErr != nil || len(page.Items) == 0 || len(page.Items) > 50 {
			t.Fatalf("page items=%d cursor=%q err=%v", len(page.Items), cursor, listErr)
		}
		for _, record := range page.Items {
			if seen[record.Metadata.RevisionID] {
				t.Fatalf("duplicate revision across pages: %s", record.Metadata.RevisionID)
			}
			seen[record.Metadata.RevisionID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 200 {
		t.Fatalf("revision history count=%d want=200", len(seen))
	}
}

func seedCapacityFixture(t *testing.T, s *Store, count int) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	blob, err := tx.Prepare(`INSERT INTO entity_blobs(hash,json) VALUES(?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer blob.Close()
	working, err := tx.Prepare(`INSERT INTO working_entities(id,kind,entity_key,schema_version,entity_version,blob_hash,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer working.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for i := range count {
		entity, err := domain.NewEntity(domain.KindTag, tagDraft(fmt.Sprintf("fixture_%05d", i)), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(entity)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(raw)
		hash := hex.EncodeToString(digest[:])
		if _, err = blob.Exec(hash, raw); err != nil {
			t.Fatal(err)
		}
		if _, err = working.Exec(entity.ID, entity.Kind, entity.Key, 1, 1, hash, domain.StatusActive, now, now); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func seedReferenceFixture(t *testing.T, s *Store, entityCount, referencesPerEntity int) {
	t.Helper()
	entities := make([]domain.Entity, entityCount)
	for index := range entities {
		entity, err := domain.NewEntity(domain.KindTag, tagDraft(fmt.Sprintf("relation_%05d", index)), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		entities[index] = entity
	}
	for index := range entities {
		references := make([]domain.ID, referencesPerEntity)
		for offset := range references {
			references[offset] = entities[(index+offset+1)%len(entities)].ID
		}
		raw, err := json.Marshal(references)
		if err != nil {
			t.Fatal(err)
		}
		entities[index].Payload["parent_tag_ids"] = raw
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	blob, err := tx.Prepare(`INSERT INTO entity_blobs(hash,json) VALUES(?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer blob.Close()
	working, err := tx.Prepare(`INSERT INTO working_entities(id,kind,entity_key,schema_version,entity_version,blob_hash,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer working.Close()
	for _, entity := range entities {
		raw, err := json.Marshal(entity)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(raw)
		hash := hex.EncodeToString(digest[:])
		if _, err = blob.Exec(hash, raw); err != nil {
			t.Fatal(err)
		}
		if _, err = working.Exec(entity.ID, entity.Kind, entity.Key, entity.SchemaVersion, entity.EntityVersion, hash, entity.Status, entity.CreatedAt.Format(time.RFC3339Nano), entity.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

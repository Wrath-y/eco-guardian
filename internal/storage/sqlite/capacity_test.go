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

package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	aiaudit "github.com/zouyi/eco-guardian/internal/ai/audit"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiorchestration "github.com/zouyi/eco-guardian/internal/ai/orchestration"
	"github.com/zouyi/eco-guardian/internal/domain"
)

// TestAIDesignRunCapacityFixture exercises the durable upper-bound policy with
// realistic large canonical events. The rejected append must remain atomic.
func TestAIDesignRunCapacityFixture(t *testing.T) {
	store := newStore(t)
	entity, revision, err := store.Create(context.Background(), domain.KindTag, tagDraft("aicapacity"))
	if err != nil {
		t.Fatal(err)
	}
	input := sqliteAIInput(t, store, entity, revision)
	inputHash, _ := aicontract.HashAIDesignInputV1(input)
	controller := aiorchestration.AIJobController{Jobs: store}
	state, _, err := controller.Admit(context.Background(), input, "ai-capacity")
	if err != nil {
		t.Fatal(err)
	}
	attemptID := aicontract.AttemptID("018f9e40-0000-7000-8000-0000000002a0")
	attempt, err := aiorchestration.NewAttemptRecord(state.Job.ID, aiorchestration.AttemptInitial, 1, 0, "", "", sqliteAttemptManifest(inputHash, aicontract.Hash(strings.Repeat("b", 64)), attemptID))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.InsertAttempt(context.Background(), attempt); err != nil {
		t.Fatal(err)
	}

	trail := aiaudit.Trail{Repository: store}
	redactor := aiaudit.NewRedactor()
	payload := strings.Repeat("x", 55_000)
	started := time.Now()
	committed := 0
	for ordinal := 1; ordinal <= 512; ordinal++ {
		draft, draftErr := aiaudit.NewEventDraft(redactor, ordinal, attemptID, aiaudit.EventToolResult, map[string]any{"ordinal": ordinal, "payload": payload}, nil)
		if draftErr != nil {
			t.Fatal(draftErr)
		}
		if _, _, appendErr := trail.Append(context.Background(), draft); errors.Is(appendErr, ErrAIRunCapacity) {
			break
		} else if appendErr != nil {
			t.Fatal(appendErr)
		}
		committed++
	}
	if committed < 100 || committed >= 512 {
		t.Fatalf("capacity committed=%d", committed)
	}
	var rows, bytes int
	if err = store.db.QueryRow(`SELECT count(*),COALESCE(sum(length(canonical_event)),0) FROM ai_audit_events WHERE attempt_id=?`, attemptID).Scan(&rows, &bytes); err != nil {
		t.Fatal(err)
	}
	if rows != committed || bytes >= MaxAIRunStoredBytesV1 {
		t.Fatalf("rows=%d committed=%d bytes=%d limit=%d", rows, committed, bytes, MaxAIRunStoredBytesV1)
	}
	t.Logf("ai-capacity events=%d canonical_event_bytes=%d elapsed=%s retention=project_history", committed, bytes, time.Since(started))
}

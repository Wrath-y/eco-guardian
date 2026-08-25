package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiorchestration "github.com/zouyi/eco-guardian/internal/ai/orchestration"
	"github.com/zouyi/eco-guardian/internal/domain"
)

func TestSQLiteAIDeterministicCheckpointIsReplayableAndOnlyAdvances(t *testing.T) {
	store := newStore(t)
	entity, revision, err := store.Create(context.Background(), domain.KindTag, tagDraft("aicheckpoint"))
	if err != nil {
		t.Fatal(err)
	}
	input := sqliteAIInput(t, store, entity, revision)
	inputHash, err := aicontract.HashAIDesignInputV1(input)
	if err != nil {
		t.Fatal(err)
	}
	controller := aiorchestration.AIJobController{Jobs: store}
	state, _, err := controller.Admit(context.Background(), input, "ai-checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	state, _, err = controller.Claim(context.Background(), state, "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	hash := func(value string) aicontract.Hash { return aicontract.Hash(strings.Repeat(value, 64)) }
	checkpoint := aiorchestration.DeterministicCheckpoint{
		DeterministicRecoveryIdentity: aiorchestration.DeterministicRecoveryIdentity{
			JobID: state.Job.ID, Phase: aiorchestration.PhaseInputPinned, InputHash: inputHash,
			EvidenceManifestHash: hash("b"), DependencyHash: hash("c"), CancelGeneration: state.Job.CancelGeneration,
		},
		OutputHash: hash("d"),
	}
	if replay, err := store.SaveDeterministicCheckpoint(context.Background(), checkpoint); err != nil || replay {
		t.Fatalf("save replay=%v err=%v", replay, err)
	}
	if replay, err := store.SaveDeterministicCheckpoint(context.Background(), checkpoint); err != nil || !replay {
		t.Fatalf("replay=%v err=%v", replay, err)
	}
	if loaded, found, err := store.LoadDeterministicCheckpoint(context.Background(), state.Job.ID); err != nil || !found || loaded != checkpoint {
		t.Fatalf("loaded=%#v found=%v err=%v", loaded, found, err)
	}

	conflict := checkpoint
	conflict.OutputHash = hash("e")
	if _, err := store.SaveDeterministicCheckpoint(context.Background(), conflict); !errors.Is(err, aiorchestration.ErrAIRecoveryConflict) {
		t.Fatalf("same phase conflict err=%v", err)
	}
	state, _, err = controller.Advance(context.Background(), state, aiorchestration.PhaseEvidencePinned)
	if err != nil {
		t.Fatal(err)
	}
	advanced := checkpoint
	advanced.Phase = aiorchestration.PhaseEvidencePinned
	advanced.OutputHash = hash("e")
	if replay, err := store.SaveDeterministicCheckpoint(context.Background(), advanced); err != nil || replay {
		t.Fatalf("advance replay=%v err=%v", replay, err)
	}
	if _, err := store.SaveDeterministicCheckpoint(context.Background(), checkpoint); !errors.Is(err, aiorchestration.ErrAIRecoveryConflict) {
		t.Fatalf("regression err=%v", err)
	}

	if _, err := store.db.Exec(`UPDATE ai_design_runs SET input_hash=? WHERE job_id=?`, hash("f"), state.Job.ID); err == nil {
		t.Fatal("mutable run identity was accepted")
	}
	if _, err := store.db.Exec(`UPDATE ai_design_runs SET phase=? WHERE job_id=?`, aiorchestration.PhaseInputPinned, state.Job.ID); err == nil {
		t.Fatal("run phase regression was accepted")
	}
	if _, err := store.db.Exec(`DELETE FROM ai_design_runs WHERE job_id=?`, state.Job.ID); err == nil {
		t.Fatal("run deletion was accepted")
	}
}

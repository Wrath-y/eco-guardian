package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"testing"

	aiapplication "github.com/zouyi/eco-guardian/internal/ai/application"
	aiaudit "github.com/zouyi/eco-guardian/internal/ai/audit"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiorchestration "github.com/zouyi/eco-guardian/internal/ai/orchestration"
	aipersistence "github.com/zouyi/eco-guardian/internal/ai/persistence"
	"github.com/zouyi/eco-guardian/internal/ai/retrieval"
	"github.com/zouyi/eco-guardian/internal/domain"
)

type aiDecisionFixture struct {
	store     *Store
	entity    domain.Entity
	revision  domain.RevisionSummary
	candidate aiorchestration.Candidate
}

func newAIDecisionFixture(t *testing.T, key string) aiDecisionFixture {
	return newAIDecisionFixtureWithCandidate(t, key, nil)
}

func newAIDecisionFixtureWithCandidate(t *testing.T, key string, mutate func(*aiorchestration.Candidate)) aiDecisionFixture {
	t.Helper()
	ctx := context.Background()
	store := newStore(t)
	entity, revision, err := store.Create(ctx, domain.KindTag, tagDraft(key))
	if err != nil {
		t.Fatal(err)
	}
	input := sqliteAIInput(t, store, entity, revision)
	candidate := sealAIDecisionFixture(t, store, key, input, aipersistence.PatchAcceptable, func(pinned retrieval.PinnedEvidence) aiorchestration.Candidate {
		candidate := sqliteCandidate(t, input, entity, pinned)
		if mutate != nil {
			mutate(&candidate)
		}
		return candidate
	})
	return aiDecisionFixture{store: store, entity: entity, revision: revision, candidate: candidate}
}

func newAIDecisionFixtureWithAcceptability(t *testing.T, key string, acceptability aipersistence.PatchAcceptability) aiDecisionFixture {
	t.Helper()
	ctx := context.Background()
	store := newStore(t)
	entity, revision, err := store.Create(ctx, domain.KindTag, tagDraft(key))
	if err != nil {
		t.Fatal(err)
	}
	input := sqliteAIInput(t, store, entity, revision)
	candidate := sealAIDecisionFixture(t, store, key, input, acceptability, func(pinned retrieval.PinnedEvidence) aiorchestration.Candidate {
		return sqliteCandidate(t, input, entity, pinned)
	})
	return aiDecisionFixture{store: store, entity: entity, revision: revision, candidate: candidate}
}

func sealAIDecisionFixture(t *testing.T, store *Store, key string, input aicontract.AIDesignInputV1, acceptability aipersistence.PatchAcceptability, build func(retrieval.PinnedEvidence) aiorchestration.Candidate) aiorchestration.Candidate {
	t.Helper()
	ctx := context.Background()
	inputHash, _ := aicontract.HashAIDesignInputV1(input)
	controller := aiorchestration.AIJobController{Jobs: store}
	state, _, err := controller.Admit(ctx, input, "decision-"+key)
	if err != nil {
		t.Fatal(err)
	}
	pinned := sqlitePinnedEvidence(t, input, "decision evidence")
	batch, err := aipersistence.NewEvidenceBatch(state.Job.ID, "", pinned, aiaudit.NewRedactor())
	if err != nil {
		t.Fatal(err)
	}
	pinned = batch.Evidence()
	if _, err = store.InsertEvidenceBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	attemptID := aicontract.AttemptID("018f9e40-0000-7000-8000-000000000291")
	attempt, err := aiorchestration.NewAttemptRecord(state.Job.ID, aiorchestration.AttemptInitial, 1, 0, "", "", sqliteAttemptManifest(inputHash, pinned.ManifestHash, attemptID))
	if err != nil {
		t.Fatal(err)
	}
	ledger := aiorchestration.AttemptLedger{Repository: store}
	if _, _, err = ledger.Create(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	candidate := build(pinned)
	record := aipersistence.DraftPatchRecord{JobID: state.Job.ID, AttemptID: attemptID, InputHash: inputHash, Candidate: candidate, ValidationHash: aiDecisionHash('c'), Preview: sqlitePreview(inputHash, acceptability == aipersistence.PatchAcceptable), PreviewIssues: []string{}, Acceptability: acceptability}
	if _, err = store.InsertDraftPatch(ctx, record); err != nil {
		t.Fatal(err)
	}
	receipt, err := aiorchestration.NewTerminalResponseReceipt(state.Job.ID, attemptID, candidate.RawAudit)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = ledger.RecordResponse(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	outcome, err := aiorchestration.NewAttemptOutcomeReceipt(state.Job.ID, attemptID, aicontract.OutcomeSucceeded, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = ledger.Complete(ctx, outcome); err != nil {
		t.Fatal(err)
	}
	seal, err := aiorchestration.NewAttemptPatchSeal(state.Job.ID, attemptID, candidate.Patch.ID, candidate.Patch.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = ledger.SealPatch(ctx, seal); err != nil {
		t.Fatal(err)
	}
	state, _, err = controller.Claim(ctx, state, "decision-worker")
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []aiorchestration.JobPhase{aiorchestration.PhaseEvidencePinned, aiorchestration.PhaseProviderToolLoop, aiorchestration.PhaseDeterministicPreview, aiorchestration.PhasePatchSealed} {
		state, _, err = controller.Advance(ctx, state, phase)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err = controller.SealPatch(ctx, state, candidate.Patch.ID); err != nil {
		t.Fatal(err)
	}
	return candidate
}

func aiDecisionHash(value byte) aicontract.Hash {
	buffer := make([]byte, 64)
	for index := range buffer {
		buffer[index] = value
	}
	return aicontract.Hash(buffer)
}

func newMultiEntityAIDecisionFixture(t *testing.T) (aiDecisionFixture, domain.Entity) {
	t.Helper()
	ctx := context.Background()
	store := newStore(t)
	first, _, err := store.Create(ctx, domain.KindTag, tagDraft("multi_first"))
	if err != nil {
		t.Fatal(err)
	}
	second, revision, err := store.Create(ctx, domain.KindTag, tagDraft("multi_second"))
	if err != nil {
		t.Fatal(err)
	}
	input := sqliteAIInput(t, store, first, revision)
	input.AllowedTargets = append(input.AllowedTargets, aicontract.AllowedTarget{EntityID: aicontract.EntityID(second.ID), Kind: string(second.Kind), ExpectedEntityVersion: second.EntityVersion, Paths: []aicontract.AllowedPath{{Path: "/payload/category", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}}}})
	sort.Slice(input.AllowedTargets, func(i, j int) bool { return input.AllowedTargets[i].EntityID < input.AllowedTargets[j].EntityID })
	candidate := sealAIDecisionFixture(t, store, "multi", input, aipersistence.PatchAcceptable, func(pinned retrieval.PinnedEvidence) aiorchestration.Candidate {
		single := input
		single.AllowedTargets = []aicontract.AllowedTarget{input.AllowedTargets[0]}
		baseEntity := first
		other := second
		if single.AllowedTargets[0].EntityID == aicontract.EntityID(second.ID) {
			baseEntity, other = second, first
		}
		value := sqliteCandidate(t, single, baseEntity, pinned)
		target := value.Patch.Targets[0]
		target.EntityID = aicontract.EntityID(other.ID)
		target.Kind = string(other.Kind)
		target.ExpectedEntityVersion = other.EntityVersion
		value.Patch.Targets = append(value.Patch.Targets, target)
		sort.Slice(value.Patch.Targets, func(i, j int) bool { return value.Patch.Targets[i].EntityID < value.Patch.Targets[j].EntityID })
		value.Patch.Hash = aiDecisionHash('0')
		value.Patch.Hash, _ = aicontract.HashDraftPatch(value.Patch)
		value.PatchCanonical, _ = aicontract.CanonicalDraftPatch(value.Patch)
		value.Diff.Changes = append(value.Diff.Changes, aicontract.DraftDiffChange{EntityID: aicontract.EntityID(other.ID), Path: "/payload/category", Ordinal: 1, Kind: aicontract.OperationReplace, Original: json.RawMessage(`"element"`), Canonical: json.RawMessage(`"mechanic"`)})
		sort.Slice(value.Diff.Changes, func(i, j int) bool { return value.Diff.Changes[i].EntityID < value.Diff.Changes[j].EntityID })
		value.Diff.PatchHash = value.Patch.Hash
		value.DiffCanonical, _ = aicontract.CanonicalDraftDiff(value.Diff)
		value.DiffHash, _ = aicontract.HashDraftDiff(value.Diff)
		return value
	})
	return aiDecisionFixture{store: store, entity: first, revision: revision, candidate: candidate}, second
}

func (fixture aiDecisionFixture) acceptCommand(key string) aiapplication.AcceptCommand {
	targets := make([]aiapplication.TargetPrecondition, len(fixture.candidate.Patch.Targets))
	for index, target := range fixture.candidate.Patch.Targets {
		targets[index] = aiapplication.TargetPrecondition{EntityID: target.EntityID, ExpectedEntityVersion: target.ExpectedEntityVersion}
	}
	return aiapplication.AcceptCommand{PatchID: fixture.candidate.Patch.ID, PatchHash: fixture.candidate.Patch.Hash, BaseRevisionID: fixture.revision.ID, Targets: targets, IdempotencyKey: key, Actor: aiapplication.LocalDecisionActor}
}

func TestSQLiteAcceptDraftPatchUpdatesMultipleEntitiesInExactlyOneRevision(t *testing.T) {
	fixture, second := newMultiEntityAIDecisionFixture(t)
	var before int
	_ = fixture.store.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&before)
	decision, replay, err := (aiapplication.DecisionService{Repository: fixture.store}).Accept(context.Background(), fixture.acceptCommand("multi-accept"))
	if err != nil || replay || !decision.AcceptedRevisionID.Valid() {
		t.Fatalf("decision=%#v replay=%v err=%v", decision, replay, err)
	}
	for _, original := range []domain.Entity{fixture.entity, second} {
		updated, getErr := fixture.store.Get(context.Background(), original.Kind, original.ID)
		var category string
		_ = json.Unmarshal(updated.Payload["category"], &category)
		if getErr != nil || updated.EntityVersion != original.EntityVersion+1 || category != "mechanic" {
			t.Fatalf("id=%s updated=%#v category=%q err=%v", original.ID, updated, category, getErr)
		}
	}
	var after, revisionEntities int
	_ = fixture.store.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&after)
	_ = fixture.store.db.QueryRow(`SELECT count(*) FROM revision_entities WHERE revision_id=? AND entity_version=2`, decision.AcceptedRevisionID).Scan(&revisionEntities)
	if after != before+1 || revisionEntities != 2 {
		t.Fatalf("revisions=%d->%d changed revision entities=%d", before, after, revisionEntities)
	}
}

func TestSQLiteAcceptDraftPatchCreatesOneRevisionAndIsIdempotent(t *testing.T) {
	fixture := newAIDecisionFixture(t, "accept")
	ctx := context.Background()
	observerCalls := 0
	fixture.store.RegisterRevisionObserver(func(context.Context, domain.RevisionSummary) { observerCalls++ })
	var revisionsBefore, releasesBefore int
	if err := fixture.store.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&revisionsBefore); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.db.QueryRow(`SELECT count(*) FROM releases`).Scan(&releasesBefore); err != nil {
		t.Fatal(err)
	}
	service := aiapplication.DecisionService{Repository: fixture.store}
	decision, replay, err := service.Accept(ctx, fixture.acceptCommand("accept-once"))
	if err != nil || replay || !decision.AcceptedRevisionID.Valid() || observerCalls != 1 {
		t.Fatalf("decision=%#v replay=%v observer=%d err=%v", decision, replay, observerCalls, err)
	}
	updated, err := fixture.store.Get(ctx, fixture.entity.Kind, fixture.entity.ID)
	if err != nil {
		t.Fatal(err)
	}
	var category string
	if err = json.Unmarshal(updated.Payload["category"], &category); err != nil || category != "mechanic" || updated.EntityVersion != fixture.entity.EntityVersion+1 {
		t.Fatalf("updated=%#v category=%q err=%v", updated, category, err)
	}
	var revisionsAfter, releasesAfter int
	if err = fixture.store.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&revisionsAfter); err != nil {
		t.Fatal(err)
	}
	if err = fixture.store.db.QueryRow(`SELECT count(*) FROM releases`).Scan(&releasesAfter); err != nil {
		t.Fatal(err)
	}
	if revisionsAfter != revisionsBefore+1 || releasesAfter != releasesBefore {
		t.Fatalf("revisions %d->%d releases %d->%d", revisionsBefore, revisionsAfter, releasesBefore, releasesAfter)
	}
	replayed, replay, err := service.Accept(ctx, fixture.acceptCommand("accept-once"))
	if err != nil || !replay || replayed != decision || observerCalls != 1 {
		t.Fatalf("replayed=%#v replay=%v observer=%d err=%v", replayed, replay, observerCalls, err)
	}
	if _, _, err = service.Accept(ctx, fixture.acceptCommand("different-key")); !errors.Is(err, aiapplication.ErrDecisionConflict) {
		t.Fatalf("second decision err=%v", err)
	}
}

func TestSQLiteAcceptedRevisionSurvivesPostCommitHandoffFailureAndRetry(t *testing.T) {
	fixture := newAIDecisionFixture(t, "handoff")
	ctx := context.Background()
	attempts := 0
	var completed domain.ID
	fixture.store.RegisterRevisionObserver(func(_ context.Context, revision domain.RevisionSummary) {
		attempts++
		if attempts > 1 {
			completed = revision.ID
		}
	})
	var before int
	_ = fixture.store.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&before)
	decision, replay, err := (aiapplication.DecisionService{Repository: fixture.store}).Accept(ctx, fixture.acceptCommand("handoff-accept"))
	if err != nil || replay || attempts != 1 || completed != "" {
		t.Fatalf("decision=%#v replay=%v attempts=%d completed=%s err=%v", decision, replay, attempts, completed, err)
	}
	// Downstream handoffs own their retry. Replaying that post-commit seam uses
	// the immutable accepted revision and must not replay the accept command.
	fixture.store.notifyRevisionCommitted(ctx, domain.RevisionSummary{ID: decision.AcceptedRevisionID})
	var after int
	_ = fixture.store.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&after)
	if attempts != 2 || completed != decision.AcceptedRevisionID || after != before+1 {
		t.Fatalf("attempts=%d completed=%s revisions=%d->%d", attempts, completed, before, after)
	}
	if _, replay, err = (aiapplication.DecisionService{Repository: fixture.store}).Accept(ctx, fixture.acceptCommand("handoff-accept")); err != nil || !replay || attempts != 2 {
		t.Fatalf("accept replay=%v attempts=%d err=%v", replay, attempts, err)
	}
}

func TestSQLiteDiscardDraftPatchIsTerminalWithoutRevision(t *testing.T) {
	fixture := newAIDecisionFixture(t, "discard")
	ctx := context.Background()
	var before int
	_ = fixture.store.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&before)
	command := aiapplication.DiscardCommand{PatchID: fixture.candidate.Patch.ID, PatchHash: fixture.candidate.Patch.Hash, Reason: "Designer declined the proposal.", IdempotencyKey: "discard-once", Actor: aiapplication.LocalDecisionActor}
	service := aiapplication.DecisionService{Repository: fixture.store}
	decision, replay, err := service.Discard(ctx, command)
	if err != nil || replay || decision.Kind != aicontract.DecisionDiscarded || decision.Reason != command.Reason {
		t.Fatalf("decision=%#v replay=%v err=%v", decision, replay, err)
	}
	var after int
	_ = fixture.store.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&after)
	if after != before {
		t.Fatalf("discard created revision: %d -> %d", before, after)
	}
	if replayed, replayedFlag, replayErr := service.Discard(ctx, command); replayErr != nil || !replayedFlag || replayed != decision {
		t.Fatalf("replayed=%#v replay=%v err=%v", replayed, replayedFlag, replayErr)
	}
	if _, _, err = service.Accept(ctx, fixture.acceptCommand("accept-after-discard")); !errors.Is(err, aiapplication.ErrDecisionConflict) {
		t.Fatalf("accept after discard err=%v", err)
	}
}

func TestSQLiteAcceptDraftPatchRejectsNonAcceptableAndCanceledGeneration(t *testing.T) {
	for _, acceptability := range []aipersistence.PatchAcceptability{aipersistence.PatchPending, aipersistence.PatchFailed} {
		t.Run(string(acceptability), func(t *testing.T) {
			fixture := newAIDecisionFixtureWithAcceptability(t, "notready", acceptability)
			if _, _, err := (aiapplication.DecisionService{Repository: fixture.store}).Accept(context.Background(), fixture.acceptCommand("not-ready")); !errors.Is(err, aiapplication.ErrDecisionNotAcceptable) {
				t.Fatalf("acceptability=%s err=%v", acceptability, err)
			}
		})
	}
	fixture := newAIDecisionFixture(t, "canceled")
	if _, err := fixture.store.db.Exec(`UPDATE jobs SET cancel_generation=1,cancel_requested_at=updated_at WHERE id=(SELECT job_id FROM ai_draft_patches WHERE id=?)`, fixture.candidate.Patch.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (aiapplication.DecisionService{Repository: fixture.store}).Accept(context.Background(), fixture.acceptCommand("canceled-accept")); !errors.Is(err, aiapplication.ErrDecisionCanceled) {
		t.Fatalf("canceled err=%v", err)
	}
}

func TestSQLiteAcceptDraftPatchRejectsStaleBaseWithoutPartialWrites(t *testing.T) {
	fixture := newAIDecisionFixture(t, "stale")
	if _, _, err := fixture.store.Create(context.Background(), domain.KindTag, tagDraft("newer")); err != nil {
		t.Fatal(err)
	}
	var revisionsBefore int
	_ = fixture.store.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&revisionsBefore)
	if _, _, err := (aiapplication.DecisionService{Repository: fixture.store}).Accept(context.Background(), fixture.acceptCommand("stale-accept")); !errors.Is(err, aiapplication.ErrDecisionStale) {
		t.Fatalf("stale err=%v", err)
	}
	var revisionsAfter, decisions int
	_ = fixture.store.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&revisionsAfter)
	_ = fixture.store.db.QueryRow(`SELECT count(*) FROM ai_patch_decisions`).Scan(&decisions)
	if revisionsAfter != revisionsBefore || decisions != 0 {
		t.Fatalf("partial write revisions=%d->%d decisions=%d", revisionsBefore, revisionsAfter, decisions)
	}
}

func TestSQLiteAcceptDraftPatchRejectsOneMismatchedTargetPrecondition(t *testing.T) {
	fixture, _ := newMultiEntityAIDecisionFixture(t)
	command := fixture.acceptCommand("stale-target")
	command.Targets[1].ExpectedEntityVersion++
	var revisionsBefore int
	_ = fixture.store.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&revisionsBefore)
	if _, _, err := (aiapplication.DecisionService{Repository: fixture.store}).Accept(context.Background(), command); !errors.Is(err, aiapplication.ErrDecisionStale) {
		t.Fatalf("target precondition err=%v", err)
	}
	var revisionsAfter, decisions int
	_ = fixture.store.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&revisionsAfter)
	_ = fixture.store.db.QueryRow(`SELECT count(*) FROM ai_patch_decisions`).Scan(&decisions)
	if revisionsAfter != revisionsBefore || decisions != 0 {
		t.Fatalf("partial write revisions=%d->%d decisions=%d", revisionsBefore, revisionsAfter, decisions)
	}
}

func TestSQLiteAcceptDraftPatchRevalidatesStoredAllowlist(t *testing.T) {
	fixture := newAIDecisionFixtureWithCandidate(t, "scope", func(candidate *aiorchestration.Candidate) {
		operation := &candidate.Patch.Targets[0].Operations[0]
		operation.Path = "/name"
		operation.Value = json.RawMessage(`"changed-by-ai"`)
		candidate.Patch.Hash = aiDecisionHash('0')
		candidate.Patch.Hash, _ = aicontract.HashDraftPatch(candidate.Patch)
		candidate.PatchCanonical, _ = aicontract.CanonicalDraftPatch(candidate.Patch)
		candidate.Diff = aicontract.DraftDiff{PatchHash: candidate.Patch.Hash, Changes: []aicontract.DraftDiffChange{{EntityID: candidate.Patch.Targets[0].EntityID, Path: "/name", Ordinal: 1, Kind: aicontract.OperationReplace, Original: json.RawMessage(`"scope"`), Canonical: json.RawMessage(`"changed-by-ai"`)}}}
		candidate.DiffCanonical, _ = aicontract.CanonicalDraftDiff(candidate.Diff)
		candidate.DiffHash, _ = aicontract.HashDraftDiff(candidate.Diff)
	})
	var revisionsBefore int
	_ = fixture.store.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&revisionsBefore)
	if _, _, err := (aiapplication.DecisionService{Repository: fixture.store}).Accept(context.Background(), fixture.acceptCommand("invalid-scope")); !errors.Is(err, aiapplication.ErrDecisionValidation) {
		t.Fatalf("validation err=%v", err)
	}
	var revisionsAfter, decisions int
	_ = fixture.store.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&revisionsAfter)
	_ = fixture.store.db.QueryRow(`SELECT count(*) FROM ai_patch_decisions`).Scan(&decisions)
	if revisionsAfter != revisionsBefore || decisions != 0 {
		t.Fatalf("partial write revisions=%d->%d decisions=%d", revisionsBefore, revisionsAfter, decisions)
	}
}

func TestSQLiteAcceptDraftPatchRollsBackEveryInjectedTransactionFailure(t *testing.T) {
	for _, stage := range []string{"ai-accept-working", "ai-accept-revision", "ai-accept-decision", "ai-accept-before-commit"} {
		t.Run(stage, func(t *testing.T) {
			fixture := newAIDecisionFixture(t, "fault")
			var revisionsBefore int
			_ = fixture.store.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&revisionsBefore)
			injected := errors.New("injected accept crash")
			fixture.store.failStage = func(observed string) error {
				if observed == stage {
					return injected
				}
				return nil
			}
			if _, _, err := (aiapplication.DecisionService{Repository: fixture.store}).Accept(context.Background(), fixture.acceptCommand("fault-"+stage)); !errors.Is(err, injected) {
				t.Fatalf("stage=%s err=%v", stage, err)
			}
			fixture.store.failStage = nil
			current, err := fixture.store.Get(context.Background(), fixture.entity.Kind, fixture.entity.ID)
			if err != nil {
				t.Fatal(err)
			}
			var revisionsAfter, decisions int
			_ = fixture.store.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&revisionsAfter)
			_ = fixture.store.db.QueryRow(`SELECT count(*) FROM ai_patch_decisions`).Scan(&decisions)
			if current.EntityVersion != fixture.entity.EntityVersion || revisionsAfter != revisionsBefore || decisions != 0 {
				t.Fatalf("stage=%s entity_version=%d revisions=%d->%d decisions=%d", stage, current.EntityVersion, revisionsBefore, revisionsAfter, decisions)
			}
		})
	}
}

func TestSQLiteAcceptDraftPatchSerializesAgainstManualEdit(t *testing.T) {
	fixture := newAIDecisionFixture(t, "manualrace")
	ctx := context.Background()
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	var acceptErr, editErr error
	go func() {
		defer wait.Done()
		<-start
		_, _, acceptErr = (aiapplication.DecisionService{Repository: fixture.store}).Accept(ctx, fixture.acceptCommand("manual-race"))
	}()
	go func() {
		defer wait.Done()
		<-start
		_, _, editErr = fixture.store.Patch(ctx, fixture.entity.Kind, fixture.entity.ID, fixture.entity.EntityVersion, domain.EntityPatch{"payload": json.RawMessage(`{"category":"manual","parent_tag_ids":[]}`)})
	}()
	close(start)
	wait.Wait()
	if (acceptErr == nil) == (editErr == nil) {
		t.Fatalf("accept err=%v manual err=%v; exactly one writer must win", acceptErr, editErr)
	}
	if acceptErr != nil && !errors.Is(acceptErr, aiapplication.ErrDecisionStale) || editErr != nil && !errors.Is(editErr, ErrRevisionConflict) {
		t.Fatalf("accept err=%v manual err=%v", acceptErr, editErr)
	}
	current, err := fixture.store.Get(ctx, fixture.entity.Kind, fixture.entity.ID)
	if err != nil || current.EntityVersion != fixture.entity.EntityVersion+1 {
		t.Fatalf("current=%#v err=%v", current, err)
	}
}

func TestSQLiteAcceptAndDiscardAreMutuallyExclusiveUnderRace(t *testing.T) {
	fixture := newAIDecisionFixture(t, "decisionrace")
	ctx := context.Background()
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	var acceptErr, discardErr error
	go func() {
		defer wait.Done()
		<-start
		_, _, acceptErr = (aiapplication.DecisionService{Repository: fixture.store}).Accept(ctx, fixture.acceptCommand("race-accept"))
	}()
	go func() {
		defer wait.Done()
		<-start
		_, _, discardErr = (aiapplication.DecisionService{Repository: fixture.store}).Discard(ctx, aiapplication.DiscardCommand{PatchID: fixture.candidate.Patch.ID, PatchHash: fixture.candidate.Patch.Hash, IdempotencyKey: "race-discard", Actor: aiapplication.LocalDecisionActor})
	}()
	close(start)
	wait.Wait()
	if (acceptErr == nil) == (discardErr == nil) {
		t.Fatalf("accept err=%v discard err=%v; exactly one decision must win", acceptErr, discardErr)
	}
	if acceptErr != nil && !errors.Is(acceptErr, aiapplication.ErrDecisionConflict) || discardErr != nil && !errors.Is(discardErr, aiapplication.ErrDecisionConflict) {
		t.Fatalf("accept err=%v discard err=%v", acceptErr, discardErr)
	}
	var decisions int
	_ = fixture.store.db.QueryRow(`SELECT count(*) FROM ai_patch_decisions`).Scan(&decisions)
	if decisions != 1 {
		t.Fatalf("decisions=%d", decisions)
	}
}

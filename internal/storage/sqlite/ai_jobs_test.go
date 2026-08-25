package sqlite

import (
	"context"
	"strings"
	"testing"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiorchestration "github.com/zouyi/eco-guardian/internal/ai/orchestration"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

func sqliteAIInput(t *testing.T, store *Store, entity domain.Entity, revision domain.RevisionSummary) aicontract.AIDesignInputV1 {
	t.Helper()
	hash := aicontract.Hash(strings.Repeat("a", 64))
	fixture := aicontract.V1Fixture()
	registries, err := aicontract.NewV1RegistrySet()
	if err != nil {
		t.Fatal(err)
	}
	budget, err := registries.ResolvedLimits()
	if err != nil {
		t.Fatal(err)
	}
	return aicontract.AIDesignInputV1{
		Schema: fixture.PatchSchema.Identity,
		Base: aicontract.FrozenBaseIdentity{
			ProjectID: aicontract.ProjectID(store.ProjectID()), ConfigRevisionID: aicontract.RevisionID(revision.ID), ConfigHash: aicontract.Hash(revision.ConfigHash),
			VersionManifestHash: hash, MaterializationHash: hash, GraphNamespace: string(store.ProjectID()), GraphSnapshot: string(revision.ID), GraphContentHash: hash,
		},
		Baseline: aicontract.BaselineIdentity{Kind: aicontract.BaselineNone},
		Goals:    []aicontract.Goal{{ID: "balance", Description: "Balance the pinned revision."}},
		AllowedTargets: []aicontract.AllowedTarget{{
			EntityID: aicontract.EntityID(entity.ID), Kind: string(entity.Kind), ExpectedEntityVersion: entity.EntityVersion,
			Paths: []aicontract.AllowedPath{{Path: "/payload/category", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}}},
		}},
		Scenes: []string{"single-target-30s"}, Budget: budget,
		RequiredVersions: []aicontract.VersionIdentity{{ID: "validation", Version: "v1", Hash: hash}},
	}
}

func TestAIDesignSchemaMigrationCreatesBoundedLinkedTables(t *testing.T) {
	store := newStore(t)
	for _, table := range []string{
		"ai_design_runs", "ai_attempts", "ai_attempt_responses", "ai_attempt_outcomes", "ai_evidence_manifests", "ai_evidence_refs", "ai_blobs", "ai_draft_patches",
		"ai_attempt_patch_seals", "ai_tool_calls", "ai_audit_events", "ai_job_event_details", "ai_patch_decisions",
	} {
		var name string
		if err := store.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil {
			t.Fatalf("missing %s: %v", table, err)
		}
	}
	var version int
	if err := store.db.QueryRow(`SELECT db_schema_version FROM project_meta WHERE id=?`, store.ProjectID()).Scan(&version); err != nil || version != 18 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	var steps int
	if err := store.db.QueryRow(`SELECT count(*) FROM schema_migration_steps WHERE step_id='ai-design-persistence-v17' AND checksum IS NOT NULL`).Scan(&steps); err != nil || steps != 1 {
		t.Fatalf("migration steps=%d err=%v", steps, err)
	}
}

func TestSQLiteAIJobRepositoryAdmitsTransitionsAndSealsOneSharedJob(t *testing.T) {
	store := newStore(t)
	entity, revision, err := store.Create(context.Background(), domain.KindTag, tagDraft("aijobstore"))
	if err != nil {
		t.Fatal(err)
	}
	input := sqliteAIInput(t, store, entity, revision)
	controller := aiorchestration.AIJobController{Jobs: store}
	state, replay, err := controller.Admit(context.Background(), input, "sqlite-ai-job")
	if err != nil || replay || state.Job.Status != sharedjob.Queued || state.Phase != aiorchestration.PhaseInputPinned {
		t.Fatalf("state=%#v replay=%v err=%v", state, replay, err)
	}
	replayed, replay, err := controller.Admit(context.Background(), input, "sqlite-ai-job")
	if err != nil || !replay || replayed.Job.ID != state.Job.ID {
		t.Fatalf("replayed=%#v replay=%v err=%v", replayed, replay, err)
	}
	var jobs, runs int
	if err = store.db.QueryRow(`SELECT (SELECT count(*) FROM jobs WHERE id=?),(SELECT count(*) FROM ai_design_runs WHERE job_id=? AND length(canonical_input)>0)`, state.Job.ID, state.Job.ID).Scan(&jobs, &runs); err != nil || jobs != 1 || runs != 1 {
		t.Fatalf("jobs=%d runs=%d err=%v", jobs, runs, err)
	}
	state, _, err = controller.Claim(context.Background(), state, "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []aiorchestration.JobPhase{aiorchestration.PhaseEvidencePinned, aiorchestration.PhaseProviderToolLoop, aiorchestration.PhaseDeterministicPreview, aiorchestration.PhasePatchSealed} {
		state, _, err = controller.Advance(context.Background(), state, phase)
		if err != nil {
			t.Fatalf("phase=%s err=%v", phase, err)
		}
	}
	patchID := aicontract.PatchID("018f9e40-0000-7000-8000-000000000299")
	state, _, err = controller.SealPatch(context.Background(), state, patchID)
	if err != nil || state.Job.Status != sharedjob.Succeeded || state.Job.Result == nil || state.Job.Result.ID != domain.ID(patchID) || state.Owner != "" {
		t.Fatalf("sealed=%#v err=%v", state, err)
	}
	loaded, err := store.GetAIJob(context.Background(), state.Job.ID)
	if err != nil || loaded.Job.Status != sharedjob.Succeeded || loaded.Phase != aiorchestration.PhasePatchSealed {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
}

func TestSQLiteAIJobCancellationIsGenerationCheckedAndIdempotent(t *testing.T) {
	store := newStore(t)
	entity, revision, err := store.Create(context.Background(), domain.KindTag, tagDraft("aijobcancel"))
	if err != nil {
		t.Fatal(err)
	}
	controller := aiorchestration.AIJobController{Jobs: store}
	coordinator := aiorchestration.CancellationCoordinator{Controller: controller, Signals: aiorchestration.NewInvocationCancellationRegistry()}
	queued, _, err := controller.Admit(context.Background(), sqliteAIInput(t, store, entity, revision), "sqlite-ai-cancel-queued")
	if err != nil {
		t.Fatal(err)
	}
	canceled, replay, err := coordinator.Cancel(context.Background(), queued.Job.ID)
	if err != nil || replay || canceled.Job.Status != sharedjob.Canceled || canceled.Job.CancelGeneration != 1 {
		t.Fatalf("canceled=%#v replay=%v err=%v", canceled, replay, err)
	}
	if replayed, replay, err := coordinator.Cancel(context.Background(), queued.Job.ID); err != nil || !replay || replayed.Job.CancelGeneration != 1 {
		t.Fatalf("replayed=%#v replay=%v err=%v", replayed, replay, err)
	}

	running, _, err := controller.Admit(context.Background(), sqliteAIInput(t, store, entity, revision), "sqlite-ai-cancel-running")
	if err != nil {
		t.Fatal(err)
	}
	running, _, err = controller.Claim(context.Background(), running, "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := coordinator.BeginCalls(context.Background(), running)
	if err != nil {
		t.Fatal(err)
	}
	requested, _, err := coordinator.Cancel(context.Background(), running.Job.ID)
	if err != nil || requested.Job.Status != sharedjob.Running || requested.Job.CancelGeneration != 1 {
		t.Fatalf("requested=%#v err=%v", requested, err)
	}
	settled, _, err := coordinator.SettleCancellation(context.Background(), lease, false)
	if err != nil || settled.Job.Status != sharedjob.Interrupted || settled.Owner != "" {
		t.Fatalf("settled=%#v err=%v", settled, err)
	}
}

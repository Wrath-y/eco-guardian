package orchestration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

type aiJobRepositoryFake struct {
	state AIJobState
}

func (repository *aiJobRepositoryFake) AdmitAIJob(_ context.Context, admission AIJobAdmission) (AIJobState, bool, error) {
	if repository.state.Job.ID.Valid() {
		return repository.state, true, nil
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	id, _ := domain.NewID()
	repository.state = AIJobState{Job: sharedjob.Record{ID: id, ProjectID: admission.Request.ProjectID, Kind: admission.Request.Kind, RevisionID: admission.Request.RevisionID, InputHash: admission.Request.InputHash, IdempotencyKey: admission.Request.IdempotencyKey, RequestHash: admission.Request.RequestHash, Status: sharedjob.Queued, CreatedAt: now, UpdatedAt: now}, Phase: admission.Phase}
	return repository.state, false, nil
}

func (repository *aiJobRepositoryFake) GetAIJob(_ context.Context, jobID domain.ID) (AIJobState, error) {
	if repository.state.Job.ID != jobID {
		return AIJobState{}, ErrAIJobTransition
	}
	return repository.state, nil
}

func (repository *aiJobRepositoryFake) RequestAIJobCancellation(_ context.Context, jobID domain.ID) (AIJobState, bool, error) {
	if repository.state.Job.ID != jobID {
		return AIJobState{}, false, ErrAIJobTransition
	}
	if repository.state.Job.CancelGeneration > 0 {
		return repository.state, true, nil
	}
	now := repository.state.Job.UpdatedAt.Add(time.Second)
	repository.state.Job.CancelGeneration++
	repository.state.Job.CancelRequestedAt = &now
	repository.state.Job.UpdatedAt = now
	if repository.state.Job.Status == sharedjob.Queued {
		repository.state.Job.Status = sharedjob.Canceled
	}
	return repository.state, false, nil
}

func (repository *aiJobRepositoryFake) TransitionAIJob(_ context.Context, transition AIJobTransition) (AIJobState, bool, error) {
	current := repository.state
	if transition.JobID != current.Job.ID || transition.ExpectedStatus != current.Job.Status || transition.ExpectedPhase != current.Phase || transition.ExpectedOwner != current.Owner || transition.ObservedCancelGeneration != current.Job.CancelGeneration {
		return AIJobState{}, false, ErrAIJobTransition
	}
	if current.Job.Status == transition.NextStatus && current.Phase == transition.NextPhase && current.Owner == transition.NextOwner && sameJobResult(current.Job.Result, transition.Result) {
		return current, true, nil
	}
	current.Job.Status = transition.NextStatus
	current.Job.Result = cloneJobResult(transition.Result)
	current.Job.UpdatedAt = current.Job.UpdatedAt.Add(time.Second)
	current.Phase, current.Owner = transition.NextPhase, transition.NextOwner
	repository.state = current
	return current, false, nil
}

func TestAIJobControllerUsesOrderedOwnedGenerationCheckedTransitions(t *testing.T) {
	repository := &aiJobRepositoryFake{}
	controller := AIJobController{Jobs: repository}
	input := aiJobInput(t)
	state, replay, err := controller.Admit(context.Background(), input, "ai-design-1")
	if err != nil || replay || state.Job.Status != sharedjob.Queued || state.Phase != PhaseInputPinned || state.Owner != "" || state.Job.RevisionID != domain.ID(input.Base.ConfigRevisionID) {
		t.Fatalf("admission=%#v replay=%v err=%v", state, replay, err)
	}
	if replayed, replay, err := controller.Admit(context.Background(), input, "ai-design-1"); err != nil || !replay || replayed.Job.ID != state.Job.ID {
		t.Fatalf("replay=%#v replayed=%v err=%v", replayed, replay, err)
	}
	state, _, err = controller.Claim(context.Background(), state, "worker-1")
	if err != nil || state.Job.Status != sharedjob.Running || state.Owner != "worker-1" {
		t.Fatalf("claim=%#v err=%v", state, err)
	}
	for _, phase := range []JobPhase{PhaseEvidencePinned, PhaseProviderToolLoop, PhaseDeterministicPreview, PhasePatchSealed} {
		state, _, err = controller.Advance(context.Background(), state, phase)
		if err != nil || state.Phase != phase || state.Owner != "worker-1" || phase.Progress() <= 0 {
			t.Fatalf("phase=%s state=%#v err=%v", phase, state, err)
		}
	}
	patchID := aicontract.PatchID("018f9e40-0000-7000-8000-000000000299")
	state, _, err = controller.SealPatch(context.Background(), state, patchID)
	if err != nil || state.Job.Status != sharedjob.Succeeded || state.Owner != "" || state.Job.Result == nil || state.Job.Result.ID != domain.ID(patchID) || state.Job.Result.URL != "/api/v1/draft-patches/"+string(patchID) {
		t.Fatalf("sealed=%#v err=%v", state, err)
	}
}

func TestAIJobControllerRejectsSkippedPhaseWrongOwnerAndCancelGeneration(t *testing.T) {
	repository := &aiJobRepositoryFake{}
	controller := AIJobController{Jobs: repository}
	state, _, _ := controller.Admit(context.Background(), aiJobInput(t), "ai-design-2")
	state, _, _ = controller.Claim(context.Background(), state, "worker-1")
	if _, _, err := controller.Advance(context.Background(), state, PhaseDeterministicPreview); !errors.Is(err, ErrAIJobTransition) {
		t.Fatalf("skipped phase err=%v", err)
	}
	wrongOwner := state
	wrongOwner.Owner = "worker-2"
	if _, _, err := controller.Advance(context.Background(), wrongOwner, PhaseEvidencePinned); !errors.Is(err, ErrAIJobTransition) {
		t.Fatalf("wrong owner err=%v", err)
	}
	stale := state
	repository.state.Job.CancelGeneration = 1
	now := repository.state.Job.UpdatedAt.Add(time.Second)
	repository.state.Job.CancelRequestedAt = &now
	if _, _, err := controller.Advance(context.Background(), stale, PhaseEvidencePinned); !errors.Is(err, ErrAIJobTransition) {
		t.Fatalf("stale generation err=%v", err)
	}
	if _, _, err := controller.SealPatch(context.Background(), state, "018f9e40-0000-7000-8000-000000000299"); !errors.Is(err, ErrAIJobTransition) {
		t.Fatalf("early seal err=%v", err)
	}
}

func TestAIJobStateRequiresCanonicalTerminalPatchLink(t *testing.T) {
	repository := &aiJobRepositoryFake{}
	controller := AIJobController{Jobs: repository}
	state, _, _ := controller.Admit(context.Background(), aiJobInput(t), "ai-design-3")
	state.Job.Status = sharedjob.Succeeded
	state.Phase = PhasePatchSealed
	state.Job.Result = &sharedjob.Result{Type: DraftPatchResultType, ID: "018f9e40-0000-7000-8000-000000000299", URL: "/wrong"}
	if state.Valid() {
		t.Fatal("noncanonical terminal result link was accepted")
	}
}

func aiJobInput(t *testing.T) aicontract.AIDesignInputV1 {
	t.Helper()
	hash := aicontract.Hash(strings.Repeat("a", 64))
	fixture := aicontract.V1Fixture()
	return aicontract.AIDesignInputV1{
		Schema:   fixture.PatchSchema.Identity,
		Base:     aicontract.FrozenBaseIdentity{ProjectID: "018f9e40-0000-7000-8000-000000000201", ConfigRevisionID: "018f9e40-0000-7000-8000-000000000202", ConfigHash: hash, VersionManifestHash: hash, MaterializationHash: hash, GraphNamespace: "018f9e40-0000-7000-8000-000000000201", GraphSnapshot: "018f9e40-0000-7000-8000-000000000202", GraphContentHash: hash},
		Baseline: aicontract.BaselineIdentity{Kind: aicontract.BaselineNone}, Goals: []aicontract.Goal{{ID: "balance", Description: "Balance the fixed scene."}},
		AllowedTargets: []aicontract.AllowedTarget{{EntityID: "018f9e40-0000-7000-8000-000000000210", Kind: "skill", ExpectedEntityVersion: 1, Paths: []aicontract.AllowedPath{{Path: "/payload/cooldown", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}}}}},
		Scenes:         []string{"scene"}, Budget: aicontract.Budget{Policy: fixture.Budget.Identity, BudgetLimits: fixture.Budget.Limits}, RequiredVersions: []aicontract.VersionIdentity{{ID: "validation", Version: "v1", Hash: hash}},
	}
}

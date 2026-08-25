package orchestration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	aiaudit "github.com/zouyi/eco-guardian/internal/ai/audit"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

const (
	AIJobKind            sharedjob.Kind = "ai_design"
	DraftPatchResultType                = "draft_patch"
)

var (
	ErrAIJobInvalid    = errors.New("AI design Job is invalid")
	ErrAIJobTransition = errors.New("AI design Job transition is invalid")
)

type JobPhase string

const (
	PhaseInputPinned          JobPhase = "input_pinned"
	PhaseEvidencePinned       JobPhase = "evidence_pinned"
	PhaseProviderToolLoop     JobPhase = "provider/tool_loop"
	PhaseDeterministicPreview JobPhase = "deterministic_preview"
	PhasePatchSealed          JobPhase = "patch_sealed"
)

func (phase JobPhase) Valid() bool { return phase.Order() >= 0 }

func (phase JobPhase) Order() int {
	switch phase {
	case PhaseInputPinned:
		return 0
	case PhaseEvidencePinned:
		return 1
	case PhaseProviderToolLoop:
		return 2
	case PhaseDeterministicPreview:
		return 3
	case PhasePatchSealed:
		return 4
	default:
		return -1
	}
}

func (phase JobPhase) Progress() int {
	switch phase {
	case PhaseInputPinned:
		return 10
	case PhaseEvidencePinned:
		return 25
	case PhaseProviderToolLoop:
		return 50
	case PhaseDeterministicPreview:
		return 80
	case PhasePatchSealed:
		return 100
	default:
		return 0
	}
}

func nextJobPhase(current, next JobPhase) bool {
	return current.Valid() && next.Valid() && next.Order() == current.Order()+1
}

type AIJobState struct {
	Job   sharedjob.Record `json:"job"`
	Phase JobPhase         `json:"phase"`
	Owner string           `json:"owner,omitempty"`
}

func (state AIJobState) Valid() bool {
	if !state.Job.Valid() || state.Job.Kind != AIJobKind || !state.Phase.Valid() || !validOwner(state.Owner, true) {
		return false
	}
	if state.Job.Status == sharedjob.Queued {
		return state.Phase == PhaseInputPinned && state.Owner == "" && state.Job.Result == nil
	}
	if state.Job.Status == sharedjob.Running {
		return state.Owner != "" && state.Job.Result == nil
	}
	if state.Job.Status == sharedjob.Succeeded {
		return state.Phase == PhasePatchSealed && state.Owner == "" && validPatchResult(state.Job.Result)
	}
	return state.Owner == "" && state.Job.Result == nil
}

type AIJobAdmission struct {
	Request        sharedjob.Request
	Phase          JobPhase
	CanonicalInput []byte
}

func (admission AIJobAdmission) Valid() bool {
	if !admission.Request.Valid() || admission.Request.Kind != AIJobKind || admission.Phase != PhaseInputPinned || len(admission.CanonicalInput) == 0 || len(admission.CanonicalInput) > 262144 || !json.Valid(admission.CanonicalInput) {
		return false
	}
	var input aicontract.AIDesignInputV1
	if err := json.Unmarshal(admission.CanonicalInput, &input); err != nil || !input.Valid() {
		return false
	}
	canonical, err := aicontract.CanonicalAIDesignInputV1(input)
	if err != nil || !bytes.Equal(canonical, admission.CanonicalInput) {
		return false
	}
	hash, err := aicontract.HashAIDesignInputV1(input)
	return err == nil && string(hash) == admission.Request.InputHash && domain.ID(input.Base.ProjectID) == admission.Request.ProjectID && domain.ID(input.Base.ConfigRevisionID) == admission.Request.RevisionID
}

type AIJobTransition struct {
	JobID                      domain.ID
	ExpectedStatus, NextStatus sharedjob.Status
	ExpectedPhase, NextPhase   JobPhase
	ExpectedOwner, NextOwner   string
	ObservedCancelGeneration   int64
	Result                     *sharedjob.Result
}

func (transition AIJobTransition) Valid() bool {
	if !transition.JobID.Valid() || !transition.ExpectedStatus.Valid() || !transition.NextStatus.Valid() || !transition.ExpectedPhase.Valid() || !transition.NextPhase.Valid() || !validOwner(transition.ExpectedOwner, true) || !validOwner(transition.NextOwner, true) || transition.ObservedCancelGeneration < 0 || (transition.Result != nil && !transition.Result.Valid()) {
		return false
	}
	return true
}

// AIJobRepository must apply admission and transitions atomically. Transition
// implementations compare every expected field plus cancel generation before
// changing state, so duplicate workers cannot advance or seal stale work.
type AIJobRepository interface {
	AdmitAIJob(context.Context, AIJobAdmission) (AIJobState, bool, error)
	GetAIJob(context.Context, domain.ID) (AIJobState, error)
	RequestAIJobCancellation(context.Context, domain.ID) (AIJobState, bool, error)
	TransitionAIJob(context.Context, AIJobTransition) (AIJobState, bool, error)
}

type AIJobController struct {
	Jobs     AIJobRepository
	Redactor aiaudit.Redactor
}

func (controller AIJobController) Admit(ctx context.Context, input aicontract.AIDesignInputV1, idempotencyKey string) (AIJobState, bool, error) {
	if ctx == nil || controller.Jobs == nil || !input.Valid() {
		return AIJobState{}, false, ErrAIJobInvalid
	}
	inputHash, err := aicontract.HashAIDesignInputV1(input)
	if err != nil {
		return AIJobState{}, false, ErrAIJobInvalid
	}
	request := sharedjob.Request{
		ProjectID: domain.ID(input.Base.ProjectID), Kind: AIJobKind, RevisionID: domain.ID(input.Base.ConfigRevisionID),
		InputHash: string(inputHash), IdempotencyKey: idempotencyKey, RequestHash: string(inputHash),
	}
	if !request.Valid() {
		return AIJobState{}, false, ErrAIJobInvalid
	}
	canonical, err := controller.Redactor.SafeCanonicalInput(input)
	if err != nil {
		return AIJobState{}, false, ErrAIJobInvalid
	}
	state, replay, err := controller.Jobs.AdmitAIJob(ctx, AIJobAdmission{Request: request, Phase: PhaseInputPinned, CanonicalInput: canonical})
	if err != nil {
		return AIJobState{}, false, err
	}
	if !state.Valid() || !state.Job.Request().Equivalent(request) {
		return AIJobState{}, false, ErrAIJobInvalid
	}
	return state, replay, nil
}

func (controller AIJobController) Claim(ctx context.Context, current AIJobState, owner string) (AIJobState, bool, error) {
	if !current.Valid() || current.Job.Status != sharedjob.Queued || !validOwner(owner, false) {
		return AIJobState{}, false, ErrAIJobTransition
	}
	return controller.transition(ctx, current, sharedjob.Running, current.Phase, owner, nil)
}

func (controller AIJobController) Advance(ctx context.Context, current AIJobState, next JobPhase) (AIJobState, bool, error) {
	if !current.Valid() || current.Job.Status != sharedjob.Running || !nextJobPhase(current.Phase, next) {
		return AIJobState{}, false, ErrAIJobTransition
	}
	return controller.transition(ctx, current, sharedjob.Running, next, current.Owner, nil)
}

func (controller AIJobController) SealPatch(ctx context.Context, current AIJobState, patchID aicontract.PatchID) (AIJobState, bool, error) {
	if !current.Valid() || current.Job.Status != sharedjob.Running || current.Phase != PhasePatchSealed || !patchID.Valid() {
		return AIJobState{}, false, ErrAIJobTransition
	}
	result := &sharedjob.Result{Type: DraftPatchResultType, ID: domain.ID(patchID), URL: "/api/v1/draft-patches/" + string(patchID)}
	if !result.Valid() {
		return AIJobState{}, false, ErrAIJobTransition
	}
	return controller.transition(ctx, current, sharedjob.Succeeded, current.Phase, "", result)
}

func (controller AIJobController) RequestCancellation(ctx context.Context, jobID domain.ID) (AIJobState, bool, error) {
	if ctx == nil || controller.Jobs == nil || !jobID.Valid() {
		return AIJobState{}, false, ErrAIJobTransition
	}
	state, replay, err := controller.Jobs.RequestAIJobCancellation(ctx, jobID)
	if err != nil {
		return AIJobState{}, false, err
	}
	if !state.Valid() || state.Job.ID != jobID || state.Job.CancelGeneration < 1 || state.Job.CancelRequestedAt == nil {
		return AIJobState{}, false, ErrAIJobTransition
	}
	if state.Job.Status != sharedjob.Running && state.Job.Status != sharedjob.Canceled && state.Job.Status != sharedjob.Interrupted {
		return AIJobState{}, false, ErrAIJobTransition
	}
	return state, replay, nil
}

func (controller AIJobController) SettleCancellation(ctx context.Context, current AIJobState, remoteStopped bool) (AIJobState, bool, error) {
	if !current.Valid() || current.Job.CancelGeneration < 1 || current.Job.CancelRequestedAt == nil {
		return AIJobState{}, false, ErrAIJobTransition
	}
	next := sharedjob.Interrupted
	if remoteStopped {
		next = sharedjob.Canceled
	}
	if current.Job.Status == next && current.Owner == "" && current.Job.Result == nil {
		return current, true, nil
	}
	if current.Job.Status != sharedjob.Running {
		return AIJobState{}, false, ErrAIJobTransition
	}
	return controller.transition(ctx, current, next, current.Phase, "", nil)
}

func (controller AIJobController) transition(ctx context.Context, current AIJobState, nextStatus sharedjob.Status, nextPhase JobPhase, nextOwner string, result *sharedjob.Result) (AIJobState, bool, error) {
	if ctx == nil || controller.Jobs == nil || !current.Valid() {
		return AIJobState{}, false, ErrAIJobTransition
	}
	transition := AIJobTransition{
		JobID: current.Job.ID, ExpectedStatus: current.Job.Status, NextStatus: nextStatus,
		ExpectedPhase: current.Phase, NextPhase: nextPhase, ExpectedOwner: current.Owner, NextOwner: nextOwner,
		ObservedCancelGeneration: current.Job.CancelGeneration, Result: cloneJobResult(result),
	}
	if !transition.Valid() {
		return AIJobState{}, false, ErrAIJobTransition
	}
	next, replay, err := controller.Jobs.TransitionAIJob(ctx, transition)
	if err != nil {
		return AIJobState{}, false, err
	}
	if !next.Valid() || next.Job.ID != current.Job.ID || next.Job.CancelGeneration != current.Job.CancelGeneration || next.Job.Status != nextStatus || next.Phase != nextPhase || next.Owner != nextOwner || !sameJobResult(next.Job.Result, result) {
		return AIJobState{}, false, ErrAIJobTransition
	}
	return next, replay, nil
}

func validOwner(value string, optional bool) bool {
	if value == "" {
		return optional
	}
	return value == strings.TrimSpace(value) && utf8.ValidString(value) && len(value) <= 128 && !strings.ContainsAny(value, "\x00\r\n")
}

func validPatchResult(result *sharedjob.Result) bool {
	return result != nil && result.Valid() && result.Type == DraftPatchResultType && result.URL == "/api/v1/draft-patches/"+string(result.ID)
}

func cloneJobResult(value *sharedjob.Result) *sharedjob.Result {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func sameJobResult(left, right *sharedjob.Result) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

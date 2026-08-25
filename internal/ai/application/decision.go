package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	"github.com/zouyi/eco-guardian/internal/domain"
)

const LocalDecisionActor = "local-user"

var (
	ErrDecisionInvalid       = errors.New("AI Patch decision command is invalid")
	ErrDecisionNotFound      = errors.New("AI Patch was not found")
	ErrDecisionNotAcceptable = errors.New("AI Patch is not acceptable")
	ErrDecisionStale         = errors.New("AI Patch is stale")
	ErrDecisionConflict      = errors.New("AI Patch already has a conflicting decision")
	ErrDecisionCanceled      = errors.New("AI Patch generation was canceled")
	ErrDecisionValidation    = errors.New("AI Patch failed server revalidation")
)

type TargetPrecondition struct {
	EntityID              aicontract.EntityID `json:"entity_id"`
	ExpectedEntityVersion int64               `json:"expected_entity_version"`
}

func (precondition TargetPrecondition) Valid() bool {
	return precondition.EntityID.Valid() && domain.ID(precondition.EntityID).Valid() && precondition.ExpectedEntityVersion > 0
}

type AcceptCommand struct {
	PatchID        aicontract.PatchID   `json:"patch_id"`
	PatchHash      aicontract.Hash      `json:"patch_hash"`
	BaseRevisionID domain.ID            `json:"base_revision_id"`
	Targets        []TargetPrecondition `json:"targets"`
	IdempotencyKey string               `json:"-"`
	Actor          string               `json:"-"`
}

func (command AcceptCommand) Valid() bool {
	if !command.PatchID.Valid() || !domain.ID(command.PatchID).Valid() || !command.PatchHash.Valid() || !command.BaseRevisionID.Valid() || len(command.Targets) == 0 || len(command.Targets) > 200 || !validDecisionKey(command.IdempotencyKey) || command.Actor != LocalDecisionActor {
		return false
	}
	for index, target := range command.Targets {
		if !target.Valid() || index > 0 && target.EntityID <= command.Targets[index-1].EntityID {
			return false
		}
	}
	return true
}

func (command AcceptCommand) RequestHash() (aicontract.Hash, error) {
	if !command.Valid() {
		return "", ErrDecisionInvalid
	}
	return decisionHash("eco-guardian.ai-accept-request/v1", struct {
		PatchID        aicontract.PatchID   `json:"patch_id"`
		PatchHash      aicontract.Hash      `json:"patch_hash"`
		BaseRevisionID domain.ID            `json:"base_revision_id"`
		Targets        []TargetPrecondition `json:"targets"`
	}{command.PatchID, command.PatchHash, command.BaseRevisionID, command.Targets})
}

func (command AcceptCommand) ExpectedVersionsHash() (aicontract.Hash, error) {
	if !command.Valid() {
		return "", ErrDecisionInvalid
	}
	return decisionHash("eco-guardian.ai-expected-versions/v1", command.Targets)
}

func EmptyExpectedVersionsHash() aicontract.Hash {
	hash, _ := decisionHash("eco-guardian.ai-expected-versions/v1", []TargetPrecondition{})
	return hash
}

type DiscardCommand struct {
	PatchID        aicontract.PatchID `json:"patch_id"`
	PatchHash      aicontract.Hash    `json:"patch_hash"`
	Reason         string             `json:"reason,omitempty"`
	IdempotencyKey string             `json:"-"`
	Actor          string             `json:"-"`
}

func (command DiscardCommand) Valid() bool {
	return command.PatchID.Valid() && domain.ID(command.PatchID).Valid() && command.PatchHash.Valid() && validDecisionKey(command.IdempotencyKey) && command.Actor == LocalDecisionActor && command.Reason == strings.TrimSpace(command.Reason) && utf8.ValidString(command.Reason) && len(command.Reason) <= 2_000
}

func (command DiscardCommand) RequestHash() (aicontract.Hash, error) {
	if !command.Valid() {
		return "", ErrDecisionInvalid
	}
	return decisionHash("eco-guardian.ai-discard-request/v1", struct {
		PatchID   aicontract.PatchID `json:"patch_id"`
		PatchHash aicontract.Hash    `json:"patch_hash"`
		Reason    string             `json:"reason,omitempty"`
	}{command.PatchID, command.PatchHash, command.Reason})
}

type PatchDecision struct {
	ID                 aicontract.DecisionID        `json:"id"`
	PatchID            aicontract.PatchID           `json:"patch_id"`
	Kind               aicontract.HumanDecisionKind `json:"kind"`
	Actor              string                       `json:"actor"`
	RequestHash        aicontract.Hash              `json:"request_hash"`
	ResultHash         aicontract.Hash              `json:"result_hash"`
	AcceptedRevisionID domain.ID                    `json:"accepted_revision_id,omitempty"`
	Reason             string                       `json:"reason,omitempty"`
	CreatedAt          time.Time                    `json:"created_at"`
}

func (decision PatchDecision) Valid() bool {
	if !decision.ID.Valid() || !domain.ID(decision.ID).Valid() || !decision.PatchID.Valid() || !domain.ID(decision.PatchID).Valid() || !decision.Kind.Valid() || decision.Actor != LocalDecisionActor || !decision.RequestHash.Valid() || !decision.ResultHash.Valid() || decision.CreatedAt.IsZero() || decision.Reason != strings.TrimSpace(decision.Reason) || len(decision.Reason) > 2_000 {
		return false
	}
	if !(decision.Kind == aicontract.DecisionAccepted && decision.AcceptedRevisionID.Valid() && decision.Reason == "" || decision.Kind == aicontract.DecisionDiscarded && decision.AcceptedRevisionID == "") {
		return false
	}
	want, err := decisionResultHash(decision.PatchID, decision.Kind, decision.Actor, decision.RequestHash, decision.AcceptedRevisionID, decision.Reason)
	return err == nil && decision.ResultHash == want
}

func NewPatchDecision(id aicontract.DecisionID, patchID aicontract.PatchID, kind aicontract.HumanDecisionKind, actor string, requestHash aicontract.Hash, acceptedRevisionID domain.ID, reason string, createdAt time.Time) (PatchDecision, error) {
	resultHash, err := decisionResultHash(patchID, kind, actor, requestHash, acceptedRevisionID, reason)
	if err != nil {
		return PatchDecision{}, err
	}
	decision := PatchDecision{ID: id, PatchID: patchID, Kind: kind, Actor: actor, RequestHash: requestHash, ResultHash: resultHash, AcceptedRevisionID: acceptedRevisionID, Reason: reason, CreatedAt: createdAt.UTC()}
	if !decision.Valid() {
		return PatchDecision{}, ErrDecisionInvalid
	}
	return decision, nil
}

type DecisionRepository interface {
	AcceptDraftPatch(context.Context, AcceptCommand) (PatchDecision, bool, error)
	DiscardDraftPatch(context.Context, DiscardCommand) (PatchDecision, bool, error)
}

type DecisionService struct{ Repository DecisionRepository }

func (service DecisionService) Accept(ctx context.Context, command AcceptCommand) (PatchDecision, bool, error) {
	if ctx == nil || service.Repository == nil || !command.Valid() {
		return PatchDecision{}, false, ErrDecisionInvalid
	}
	decision, replay, err := service.Repository.AcceptDraftPatch(ctx, command)
	if err != nil {
		return PatchDecision{}, false, err
	}
	requestHash, _ := command.RequestHash()
	if !decision.Valid() || decision.PatchID != command.PatchID || decision.Kind != aicontract.DecisionAccepted || decision.RequestHash != requestHash {
		return PatchDecision{}, false, ErrDecisionConflict
	}
	return decision, replay, nil
}

func (service DecisionService) Discard(ctx context.Context, command DiscardCommand) (PatchDecision, bool, error) {
	if ctx == nil || service.Repository == nil || !command.Valid() {
		return PatchDecision{}, false, ErrDecisionInvalid
	}
	decision, replay, err := service.Repository.DiscardDraftPatch(ctx, command)
	if err != nil {
		return PatchDecision{}, false, err
	}
	requestHash, _ := command.RequestHash()
	if !decision.Valid() || decision.PatchID != command.PatchID || decision.Kind != aicontract.DecisionDiscarded || decision.RequestHash != requestHash {
		return PatchDecision{}, false, ErrDecisionConflict
	}
	return decision, replay, nil
}

func validDecisionKey(value string) bool {
	return value == strings.TrimSpace(value) && utf8.ValidString(value) && len(value) > 0 && len(value) <= 256
}

func decisionHash(domainName string, value any) (aicontract.Hash, error) {
	canonical, err := domain.CanonicalJSON(value)
	if err != nil {
		return "", ErrDecisionInvalid
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(domainName))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(canonical)
	return aicontract.Hash(hex.EncodeToString(hash.Sum(nil))), nil
}

func decisionResultHash(patchID aicontract.PatchID, kind aicontract.HumanDecisionKind, actor string, requestHash aicontract.Hash, acceptedRevisionID domain.ID, reason string) (aicontract.Hash, error) {
	return decisionHash("eco-guardian.ai-patch-decision/v1", struct {
		PatchID            aicontract.PatchID           `json:"patch_id"`
		Kind               aicontract.HumanDecisionKind `json:"kind"`
		Actor              string                       `json:"actor"`
		RequestHash        aicontract.Hash              `json:"request_hash"`
		AcceptedRevisionID domain.ID                    `json:"accepted_revision_id,omitempty"`
		Reason             string                       `json:"reason,omitempty"`
	}{patchID, kind, actor, requestHash, acceptedRevisionID, reason})
}

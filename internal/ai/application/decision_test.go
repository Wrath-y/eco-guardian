package application

import (
	"errors"
	"strings"
	"testing"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
)

func TestAcceptCommandPinsOrderedPreconditionsAndStableHashes(t *testing.T) {
	hash := aicontract.Hash(strings.Repeat("a", 64))
	command := AcceptCommand{
		PatchID: "018f9e40-0000-7000-8000-000000000201", PatchHash: hash, BaseRevisionID: "018f9e40-0000-7000-8000-000000000202",
		Targets:        []TargetPrecondition{{EntityID: "018f9e40-0000-7000-8000-000000000210", ExpectedEntityVersion: 1}, {EntityID: "018f9e40-0000-7000-8000-000000000211", ExpectedEntityVersion: 2}},
		IdempotencyKey: "accept-1", Actor: LocalDecisionActor,
	}
	requestHash, err := command.RequestHash()
	versionsHash, versionsErr := command.ExpectedVersionsHash()
	if err != nil || versionsErr != nil || !requestHash.Valid() || !versionsHash.Valid() || requestHash == versionsHash {
		t.Fatalf("request=%s versions=%s errors=%v/%v", requestHash, versionsHash, err, versionsErr)
	}
	reversed := command
	reversed.Targets = []TargetPrecondition{command.Targets[1], command.Targets[0]}
	if reversed.Valid() {
		t.Fatal("unordered target preconditions were accepted")
	}
	changedKey := command
	changedKey.IdempotencyKey = "accept-2"
	if changedHash, err := changedKey.RequestHash(); err != nil || changedHash != requestHash {
		t.Fatalf("idempotency key changed semantic request hash=%s err=%v", changedHash, err)
	}
}

func TestDiscardCommandPinsReasonAndRejectsInvalidBoundary(t *testing.T) {
	hash := aicontract.Hash(strings.Repeat("a", 64))
	command := DiscardCommand{PatchID: "018f9e40-0000-7000-8000-000000000201", PatchHash: hash, Reason: "Not suitable.", IdempotencyKey: "discard-1", Actor: LocalDecisionActor}
	if requestHash, err := command.RequestHash(); err != nil || !requestHash.Valid() {
		t.Fatalf("hash=%s err=%v", requestHash, err)
	}
	command.Reason = " trailing "
	if _, err := command.RequestHash(); !errors.Is(err, ErrDecisionInvalid) {
		t.Fatalf("invalid reason err=%v", err)
	}
}

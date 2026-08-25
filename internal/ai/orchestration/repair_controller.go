package orchestration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sync"

	aiaudit "github.com/zouyi/eco-guardian/internal/ai/audit"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aipatch "github.com/zouyi/eco-guardian/internal/ai/patch"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
)

var (
	ErrRepairControllerInvalid = errors.New("AI repair controller is invalid")
	ErrRepairNotAllowed        = errors.New("AI failure is not eligible for format repair")
	ErrRepairExhausted         = errors.New("AI format repair budget is exhausted")
	ErrRepairClosed            = errors.New("AI repair lineage is already terminal")
	ErrRepairPersistence       = errors.New("AI repair transition could not be persisted")
)

type RepairDiagnostic struct {
	Classification RepairReason `json:"classification"`
	Code           string       `json:"code"`
}

type RepairInstruction struct {
	Round            int                  `json:"round"`
	ParentAttemptID  aicontract.AttemptID `json:"parent_attempt_id"`
	Diagnostic       RepairDiagnostic     `json:"diagnostic"`
	RedactedResponse json.RawMessage      `json:"redacted_response"`
}

type RepairAttemptRecord struct {
	AttemptID        aicontract.AttemptID
	ParentAttemptID  aicontract.AttemptID
	Ordinal          int
	RepairRound      int
	Manifest         aiprovider.AttemptManifest
	Outcome          aicontract.AttemptOutcome
	FailureReason    RepairReason
	OriginalBodyHash aicontract.Hash
	StoredBodyHash   aicontract.Hash
}

type RepairTransition struct {
	Terminal RepairAttemptRecord
	Next     *RepairAttemptRecord
}

type RepairTransitionStore interface {
	CommitRepairTransition(context.Context, RepairTransition) error
}

type RepairPlan struct {
	Manifest    aiprovider.AttemptManifest
	Instruction RepairInstruction
	Canonical   []byte
}

type RepairController struct {
	mu sync.Mutex

	store     RepairTransitionStore
	current   aiprovider.AttemptManifest
	parent    aicontract.AttemptID
	ordinal   int
	round     int
	maxRounds int
	closed    bool
	seen      map[aicontract.AttemptID]struct{}
}

func NewRepairController(input aicontract.AIDesignInputV1, initial aiprovider.AttemptManifest, store RepairTransitionStore) (*RepairController, error) {
	inputHash, err := aicontract.HashAIDesignInputV1(input)
	if err != nil || store == nil || !initial.Valid() || inputHash != initial.InputHash {
		return nil, ErrRepairControllerInvalid
	}
	maxRounds := input.Budget.MaxFormatRepairs
	if maxRounds > aicontract.V1MaxFormatRepairs {
		maxRounds = aicontract.V1MaxFormatRepairs
	}
	if providerRounds := input.Budget.MaxProviderTurns - 1; maxRounds > providerRounds {
		maxRounds = providerRounds
	}
	return &RepairController{
		store: store, current: cloneRepairManifest(initial), ordinal: 1, maxRounds: maxRounds,
		seen: map[aicontract.AttemptID]struct{}{initial.AttemptID: {}},
	}, nil
}

// HandleFailure commits the current attempt's immutable terminal outcome. A
// repairable failure may also create exactly one linked running attempt. No
// mutable goal, constraint, evidence, tool, or path input is accepted here.
func (c *RepairController) HandleFailure(
	ctx context.Context,
	failure error,
	response aiaudit.SealedProviderResponse,
	nextAttemptID aicontract.AttemptID,
) (RepairPlan, error) {
	if c == nil || ctx == nil {
		return RepairPlan{}, ErrRepairControllerInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return RepairPlan{}, ErrRepairClosed
	}
	if err := validateRepairResponse(response, c.current.StructuredResponseSchema); err != nil {
		return RepairPlan{}, err
	}
	decision := ClassifyRepair(failure)
	terminal := RepairAttemptRecord{
		AttemptID: c.current.AttemptID, ParentAttemptID: c.parent, Ordinal: c.ordinal, RepairRound: c.round,
		Manifest: cloneRepairManifest(c.current), Outcome: aicontract.OutcomeFailed, FailureReason: decision.Reason,
		OriginalBodyHash: response.OriginalBodyHash, StoredBodyHash: response.StoredBodyHash,
	}
	if !decision.Repairable {
		if err := c.store.CommitRepairTransition(ctx, RepairTransition{Terminal: terminal}); err != nil {
			return RepairPlan{}, ErrRepairPersistence
		}
		c.closed = true
		return RepairPlan{}, ErrRepairNotAllowed
	}
	if c.round >= c.maxRounds {
		if err := c.store.CommitRepairTransition(ctx, RepairTransition{Terminal: terminal}); err != nil {
			return RepairPlan{}, ErrRepairPersistence
		}
		c.closed = true
		return RepairPlan{}, ErrRepairExhausted
	}
	if _, duplicate := c.seen[nextAttemptID]; !nextAttemptID.Valid() || duplicate {
		return RepairPlan{}, ErrRepairControllerInvalid
	}
	nextManifest := cloneRepairManifest(c.current)
	nextManifest.AttemptID = nextAttemptID
	if !sameFrozenRepairManifest(c.current, nextManifest) || !nextManifest.Valid() {
		return RepairPlan{}, ErrRepairControllerInvalid
	}
	instruction := RepairInstruction{
		Round: c.round + 1, ParentAttemptID: c.current.AttemptID,
		Diagnostic:       RepairDiagnostic{Classification: decision.Reason, Code: stableFailureCode(failure, decision)},
		RedactedResponse: append(json.RawMessage(nil), response.StoredBody...),
	}
	canonical, err := json.Marshal(instruction)
	if err != nil || len(canonical) > aicontract.V1MaxOutputBytes {
		return RepairPlan{}, ErrRepairControllerInvalid
	}
	next := &RepairAttemptRecord{
		AttemptID: nextAttemptID, ParentAttemptID: c.current.AttemptID, Ordinal: c.ordinal + 1, RepairRound: c.round + 1,
		Manifest: cloneRepairManifest(nextManifest), Outcome: aicontract.OutcomeRunning,
	}
	transition := RepairTransition{Terminal: terminal, Next: cloneRepairRecord(next)}
	if err := c.store.CommitRepairTransition(ctx, transition); err != nil {
		return RepairPlan{}, ErrRepairPersistence
	}
	c.parent = c.current.AttemptID
	c.current = cloneRepairManifest(nextManifest)
	c.ordinal++
	c.round++
	c.seen[nextAttemptID] = struct{}{}
	return RepairPlan{Manifest: cloneRepairManifest(nextManifest), Instruction: cloneRepairInstruction(instruction), Canonical: append([]byte(nil), canonical...)}, nil
}

func validateRepairResponse(value aiaudit.SealedProviderResponse, schema aicontract.VersionIdentity) error {
	if value.Schema != schema || !value.OriginalBodyHash.Valid() || !value.StoredBodyHash.Valid() || len(value.StoredBody) == 0 || len(value.StoredBody) > aicontract.V1MaxOutputBytes || !json.Valid(value.StoredBody) {
		return ErrRepairControllerInvalid
	}
	hash := sha256.Sum256(value.StoredBody)
	if hex.EncodeToString(hash[:]) != string(value.StoredBodyHash) {
		return ErrRepairControllerInvalid
	}
	return nil
}

func stableFailureCode(err error, decision RepairDecision) string {
	var patchError *aipatch.DecodeError
	if errors.As(err, &patchError) && patchError.Code != "" {
		return string(patchError.Code)
	}
	return string(decision.Reason)
}

func sameFrozenRepairManifest(left, right aiprovider.AttemptManifest) bool {
	left.AttemptID, right.AttemptID = "", ""
	if len(left.Tools) != len(right.Tools) {
		return false
	}
	for index := range left.Tools {
		if left.Tools[index] != right.Tools[index] {
			return false
		}
	}
	return reflect.DeepEqual(left, right)
}

func cloneRepairManifest(value aiprovider.AttemptManifest) aiprovider.AttemptManifest {
	value.Tools = append([]aicontract.VersionIdentity(nil), value.Tools...)
	return value
}

func cloneRepairInstruction(value RepairInstruction) RepairInstruction {
	value.RedactedResponse = append(json.RawMessage(nil), value.RedactedResponse...)
	return value
}

func cloneRepairRecord(value *RepairAttemptRecord) *RepairAttemptRecord {
	if value == nil {
		return nil
	}
	result := *value
	result.Manifest = cloneRepairManifest(value.Manifest)
	return &result
}

package application

import (
	"context"
	"errors"
	"sort"
	"time"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	"github.com/zouyi/eco-guardian/internal/ai/retrieval"
	"github.com/zouyi/eco-guardian/internal/domain"
)

var (
	ErrReviewInvalid  = errors.New("AI DraftPatch review resource is invalid")
	ErrReviewNotFound = errors.New("AI DraftPatch review resource was not found")
)

type AttemptReadProjection struct {
	ID              aicontract.AttemptID       `json:"id"`
	Ordinal         int                        `json:"ordinal"`
	ParentAttemptID aicontract.AttemptID       `json:"parent_attempt_id,omitempty"`
	Stage           aicontract.AttemptStage    `json:"stage"`
	Outcome         aicontract.AttemptOutcome  `json:"outcome"`
	Manifest        aicontract.VersionIdentity `json:"manifest"`
	RepairRound     int                        `json:"repair_round"`
}

func (value AttemptReadProjection) Valid() bool {
	return value.ID.Valid() && (value.ParentAttemptID == "" || value.ParentAttemptID.Valid()) && value.ParentAttemptID != value.ID && value.Ordinal > 0 && value.Stage.Valid() && value.Outcome.Valid() && value.Manifest.Valid() && value.RepairRound >= 0 && value.RepairRound <= 3
}

type DraftPatchReviewResource struct {
	Patch             aicontract.DraftPatchV1    `json:"patch"`
	JobID             domain.ID                  `json:"job_id"`
	Input             aicontract.AIDesignInputV1 `json:"input"`
	InputHash         aicontract.Hash            `json:"input_hash"`
	Attempts          []AttemptReadProjection    `json:"attempts"`
	Preview           *aicontract.Preview        `json:"preview,omitempty"`
	PreviewIssues     []string                   `json:"preview_issues"`
	RetrievalEvidence []retrieval.EvidenceRefV1  `json:"retrieval_evidence"`
	Freshness         aicontract.Freshness       `json:"freshness"`
	Decision          *PatchDecision             `json:"decision,omitempty"`
	Formal            FormalAnalysisLinks        `json:"formal"`
	CreatedAt         time.Time                  `json:"created_at"`
}

func (value DraftPatchReviewResource) Valid() bool {
	if !value.Patch.Valid() || !value.JobID.Valid() || !value.Input.Valid() || !value.InputHash.Valid() || value.Patch.Base != value.Input.Base || value.CreatedAt.IsZero() || len(value.Attempts) == 0 || value.PreviewIssues == nil || !value.Freshness.Valid() {
		return false
	}
	inputHash, err := aicontract.HashAIDesignInputV1(value.Input)
	if err != nil || inputHash != value.InputHash {
		return false
	}
	for index, attempt := range value.Attempts {
		if !attempt.Valid() || index > 0 && attempt.Ordinal <= value.Attempts[index-1].Ordinal {
			return false
		}
	}
	if value.Preview != nil {
		if !value.Preview.Valid() || value.Preview.InputHash != value.InputHash {
			return false
		}
	}
	if !sort.StringsAreSorted(value.PreviewIssues) {
		return false
	}
	for index, issue := range value.PreviewIssues {
		if issue == "" || index > 0 && issue == value.PreviewIssues[index-1] {
			return false
		}
	}
	for _, evidence := range value.RetrievalEvidence {
		if !evidence.ID.Valid() || !evidence.RecordHash.Valid() {
			return false
		}
	}
	if value.Decision != nil && (!value.Decision.Valid() || value.Decision.PatchID != value.Patch.ID) {
		return false
	}
	return true
}

type ReviewRepository interface {
	ReadDraftPatchReview(context.Context, aicontract.PatchID) (DraftPatchReviewResource, error)
}

type ReviewService struct{ Repository ReviewRepository }

func (service ReviewService) Read(ctx context.Context, patchID aicontract.PatchID) (DraftPatchReviewResource, error) {
	if ctx == nil || service.Repository == nil || !patchID.Valid() || !domain.ID(patchID).Valid() {
		return DraftPatchReviewResource{}, ErrReviewInvalid
	}
	resource, err := service.Repository.ReadDraftPatchReview(ctx, patchID)
	if err != nil {
		return DraftPatchReviewResource{}, err
	}
	if !resource.Valid() || resource.Patch.ID != patchID {
		return DraftPatchReviewResource{}, ErrReviewInvalid
	}
	return resource, nil
}

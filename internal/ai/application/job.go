package application

import (
	"context"
	"errors"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiorchestration "github.com/zouyi/eco-guardian/internal/ai/orchestration"
	"github.com/zouyi/eco-guardian/internal/domain"
)

var ErrDesignJobInvalid = errors.New("AI design Job request is invalid")

type DesignJobIntent struct {
	BaseRevisionID  aicontract.RevisionID
	Goals           []aicontract.Goal
	Metrics         []aicontract.MetricGoal
	Constraints     []aicontract.Constraint
	AllowedTargets  []aicontract.AllowedTarget
	Scenes          []string
	RequestedBudget *aicontract.Budget
}

type DesignJobService struct {
	ProjectID domain.ID
	Admission aiorchestration.Admission
	Jobs      aiorchestration.AIJobStore
}

func (service DesignJobService) Submit(ctx context.Context, intent DesignJobIntent, idempotencyKey string) (aiorchestration.AdmissionResult, error) {
	if ctx == nil || !service.ProjectID.Valid() || service.Jobs == nil || service.Admission.Snapshots == nil {
		return aiorchestration.AdmissionResult{}, ErrDesignJobInvalid
	}
	return service.Admission.Submit(ctx, service.Jobs, aiorchestration.AdmissionRequest{
		ProjectID: aicontract.ProjectID(service.ProjectID), BaseRevisionID: intent.BaseRevisionID,
		Goals: intent.Goals, Metrics: intent.Metrics, Constraints: intent.Constraints, AllowedTargets: intent.AllowedTargets,
		Scenes: intent.Scenes, RequestedBudget: intent.RequestedBudget,
	}, idempotencyKey)
}

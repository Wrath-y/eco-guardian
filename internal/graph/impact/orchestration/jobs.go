package orchestration

import (
	"context"
	"errors"
	"fmt"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/impact"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

const JobKind sharedjob.Kind = "impact_analysis"

var ErrSubmitInvalid = errors.New("invalid impact submission")

type Submitter struct {
	Jobs    impact.JobStore
	Events  impact.EventStore
	Reports impact.ReportStore
	Clock   impact.Clock
}

func (s Submitter) Submit(ctx context.Context, input impact.Input, inputHash, idempotencyKey, intent string) (sharedjob.Record, bool, error) {
	if s.Jobs == nil || s.Reports == nil || s.Clock == nil || !impact.ValidHash(inputHash) || (intent != "manual" && intent != "automatic") {
		return sharedjob.Record{}, false, ErrSubmitInvalid
	}
	if intent == "automatic" {
		idempotencyKey = fmt.Sprintf("impact:auto:%s:%s", input.Target.RevisionID, inputHash)
	}
	request := sharedjob.Request{ProjectID: input.ProjectID, Kind: JobKind, RevisionID: input.Target.RevisionID, InputHash: inputHash, IdempotencyKey: idempotencyKey, RequestHash: inputHash}
	job, replay, err := s.Jobs.CreateOrGet(ctx, request)
	if err != nil || replay {
		return job, replay, err
	}
	if cached, found, cacheErr := s.Reports.FindByInputHash(ctx, inputHash); cacheErr != nil {
		return sharedjob.Record{}, false, cacheErr
	} else if found {
		running, _, transitionErr := s.Jobs.Transition(ctx, job.ID, sharedjob.Queued, sharedjob.Running, nil, 0)
		if transitionErr != nil {
			return sharedjob.Record{}, false, transitionErr
		}
		result := sharedjob.Result{Type: "impact_analysis", ID: cached.ID, URL: "/api/v1/impact-analyses/" + string(cached.ID)}
		succeeded, _, transitionErr := s.Jobs.Transition(ctx, job.ID, sharedjob.Running, sharedjob.Succeeded, &result, running.CancelGeneration)
		return succeeded, false, transitionErr
	}
	if _, _, err = s.Reports.CreateOrGetStaging(ctx, job.ID, input, inputHash); err != nil {
		return sharedjob.Record{}, false, err
	}
	return job, false, nil
}

func AutomaticKey(input impact.Input, inputHash string) string {
	return fmt.Sprintf("impact:auto:%s:%s", input.Target.RevisionID, inputHash)
}

func ResultLink(reportID domain.ID) *sharedjob.Result {
	return &sharedjob.Result{Type: "impact_analysis", ID: reportID, URL: "/api/v1/impact-analyses/" + string(reportID)}
}

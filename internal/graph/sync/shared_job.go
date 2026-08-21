package sync

import (
	"fmt"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

const SharedJobKind sharedjob.Kind = "graph_sync"

func (j GraphJob) SharedRecord() (sharedjob.Record, error) {
	var result *sharedjob.Result
	if j.Result != nil {
		result = &sharedjob.Result{Type: j.Result.Type, ID: j.Result.ID, URL: j.Result.URL}
	}
	record := sharedjob.Record{ID: j.ID, ProjectID: j.ProjectID, Kind: SharedJobKind, RevisionID: j.RevisionID, InputHash: j.InputHash, IdempotencyKey: j.IdempotencyKey, RequestHash: j.RequestHash, Status: sharedjob.Status(j.Status), Result: result, CancelGeneration: j.CancelGeneration, CancelRequestedAt: j.CancelRequestedAt, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt}
	if !record.Valid() {
		return sharedjob.Record{}, fmt.Errorf("invalid graph job for shared adapter")
	}
	return record, nil
}

// GraphJobFromSharedRecord restores the shared fields while leaving retry and
// evidence in Graph's own durable extension columns.
func GraphJobFromSharedRecord(record sharedjob.Record, retryOfJobID domain.ID, evidence string) (GraphJob, error) {
	if !record.Valid() || record.Kind != SharedJobKind || !record.RevisionID.Valid() {
		return GraphJob{}, fmt.Errorf("invalid shared graph job")
	}
	var result *GraphJobResult
	if record.Result != nil {
		result = &GraphJobResult{Type: record.Result.Type, ID: record.Result.ID, URL: record.Result.URL}
	}
	job := GraphJob{ID: record.ID, RetryOfJobID: retryOfJobID, ProjectID: record.ProjectID, RevisionID: record.RevisionID, InputHash: record.InputHash, IdempotencyKey: record.IdempotencyKey, RequestHash: record.RequestHash, Evidence: evidence, Status: JobStatus(record.Status), Result: result, CancelGeneration: record.CancelGeneration, CancelRequestedAt: record.CancelRequestedAt, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
	if !job.Valid() {
		return GraphJob{}, fmt.Errorf("invalid adapted graph job")
	}
	return job, nil
}

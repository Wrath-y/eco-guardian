package release

import (
	"fmt"

	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

const SharedJobKind sharedjob.Kind = "release"

func (j Job) SharedRecord() (sharedjob.Record, error) {
	var result *sharedjob.Result
	if j.Result != nil {
		result = &sharedjob.Result{Type: j.Result.Type, ID: j.Result.ID, URL: j.Result.URL}
	}
	record := sharedjob.Record{ID: j.ID, ProjectID: j.ProjectID, Kind: SharedJobKind, RevisionID: j.RevisionID, InputHash: j.InputHash, IdempotencyKey: j.IdempotencyKey, RequestHash: j.RequestHash, Status: sharedjob.Status(j.Status), Result: result, CancelGeneration: j.CancelGeneration, CancelRequestedAt: j.CancelRequestedAt, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt}
	if !record.Valid() {
		return sharedjob.Record{}, fmt.Errorf("invalid release job for shared adapter")
	}
	return record, nil
}

func JobFromSharedRecord(record sharedjob.Record) (Job, error) {
	if !record.Valid() || record.Kind != SharedJobKind || !record.RevisionID.Valid() {
		return Job{}, fmt.Errorf("invalid shared release job")
	}
	var result *JobResult
	if record.Result != nil {
		result = &JobResult{Type: record.Result.Type, ID: record.Result.ID, URL: record.Result.URL}
	}
	job := Job{ID: record.ID, ProjectID: record.ProjectID, RevisionID: record.RevisionID, InputHash: record.InputHash, IdempotencyKey: record.IdempotencyKey, RequestHash: record.RequestHash, Status: JobStatus(record.Status), Result: result, CancelGeneration: record.CancelGeneration, CancelRequestedAt: record.CancelRequestedAt, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
	if !job.Valid() {
		return Job{}, fmt.Errorf("invalid adapted release job")
	}
	return job, nil
}

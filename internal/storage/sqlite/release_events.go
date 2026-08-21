package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	versioningrelease "github.com/zouyi/eco-guardian/internal/versioning/release"
)

var ErrReleaseJobEventConflict = errors.New("release job event ordinal conflicts with different content")

var _ versioningrelease.JobEventRepository = (*Store)(nil)

// AppendReleaseJobEvent records a monotonically numbered event. Retrying the
// exact same persisted event returns it as a deduplicated replay; any changed
// payload at that ordinal is rejected rather than overwriting audit history.
func (s *Store) AppendReleaseJobEvent(ctx context.Context, event versioningrelease.Event) (versioningrelease.Event, bool, error) {
	if !event.Valid() {
		return versioningrelease.Event{}, false, ErrReleaseJobInvalid
	}
	var result *sharedjob.Result
	if event.Result != nil {
		result = &sharedjob.Result{Type: event.Result.Type, ID: event.Result.ID, URL: event.Result.URL}
	}
	stored, replay, err := s.Append(ctx, sharedjob.Event{JobID: event.JobID, Ordinal: event.Ordinal, Phase: event.Phase, Progress: event.Progress, Warning: event.Warning, SafeError: event.Error, Result: result, CreatedAt: event.CreatedAt})
	if errors.Is(err, ErrJobEvent) {
		return versioningrelease.Event{}, false, ErrReleaseJobEventConflict
	}
	if errors.Is(err, ErrJobNotFound) {
		return versioningrelease.Event{}, false, ErrReleaseJobNotFound
	}
	if err != nil {
		return versioningrelease.Event{}, false, err
	}
	return releaseEventFromShared(stored), replay, nil
}

func (s *Store) ListReleaseJobEvents(ctx context.Context, jobID domain.ID, after int64) ([]versioningrelease.Event, error) {
	if !jobID.Valid() || after < 0 {
		return nil, ErrReleaseJobInvalid
	}
	stored, err := s.ListEvents(ctx, jobID, after)
	if err != nil {
		return nil, err
	}
	events := make([]versioningrelease.Event, 0, len(stored))
	for _, event := range stored {
		events = append(events, releaseEventFromShared(event))
	}
	return events, nil
}

func releaseEventFromShared(event sharedjob.Event) versioningrelease.Event {
	result := (*versioningrelease.JobResult)(nil)
	if event.Result != nil {
		result = &versioningrelease.JobResult{Type: event.Result.Type, ID: event.Result.ID, URL: event.Result.URL}
	}
	return versioningrelease.Event{JobID: event.JobID, Ordinal: event.Ordinal, Phase: event.Phase, Progress: event.Progress, Warning: event.Warning, Error: event.SafeError, Result: result, CreatedAt: event.CreatedAt}
}

func findReleaseJobEvent(ctx context.Context, tx *sql.Tx, projectID, jobID domain.ID, ordinal int64) (versioningrelease.Event, bool, error) {
	event, err := scanReleaseJobEvent(tx.QueryRowContext(ctx, `SELECT e.job_id,e.event_ordinal,e.phase,e.progress,e.warning,e.error,e.result_type,e.result_id,e.result_url,e.created_at FROM job_events e JOIN jobs j ON j.id=e.job_id WHERE e.job_id=? AND j.project_uuid=? AND e.event_ordinal=?`, jobID, projectID, ordinal))
	if errors.Is(err, sql.ErrNoRows) {
		return versioningrelease.Event{}, false, nil
	}
	return event, err == nil, err
}

type releaseEventScanner interface{ Scan(...any) error }

func scanReleaseJobEvent(scanner releaseEventScanner) (versioningrelease.Event, error) {
	var jobID, phase, createdRaw string
	var ordinal int64
	var progress int
	var warning, eventError, resultType, resultID, resultURL sql.NullString
	if err := scanner.Scan(&jobID, &ordinal, &phase, &progress, &warning, &eventError, &resultType, &resultID, &resultURL, &createdRaw); err != nil {
		return versioningrelease.Event{}, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdRaw)
	if err != nil {
		return versioningrelease.Event{}, err
	}
	event := versioningrelease.Event{JobID: domain.ID(jobID), Ordinal: ordinal, Phase: phase, Progress: progress, Warning: warning.String, Error: eventError.String, CreatedAt: createdAt}
	if resultType.Valid || resultID.Valid || resultURL.Valid {
		if !resultType.Valid || !resultID.Valid || !resultURL.Valid {
			return versioningrelease.Event{}, errors.New("partial stored release job event result")
		}
		event.Result = &versioningrelease.JobResult{Type: resultType.String, ID: domain.ID(resultID.String), URL: resultURL.String}
	}
	if !event.Valid() {
		return versioningrelease.Event{}, errors.New("invalid stored release job event")
	}
	return event, nil
}

func sameReleaseJobEvent(left, right versioningrelease.Event) bool {
	if left.JobID != right.JobID || left.Ordinal != right.Ordinal || left.Phase != right.Phase || left.Progress != right.Progress || left.Warning != right.Warning || left.Error != right.Error || !left.CreatedAt.Equal(right.CreatedAt) {
		return false
	}
	if left.Result == nil || right.Result == nil {
		return left.Result == nil && right.Result == nil
	}
	return *left.Result == *right.Result
}

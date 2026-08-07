package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
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
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return versioningrelease.Event{}, false, err
	}
	defer tx.Rollback()
	if existing, found, err := findReleaseJobEvent(ctx, tx, s.projectID, event.JobID, event.Ordinal); err != nil {
		return versioningrelease.Event{}, false, err
	} else if found {
		if !sameReleaseJobEvent(existing, event) {
			return versioningrelease.Event{}, false, ErrReleaseJobEventConflict
		}
		return existing, true, tx.Commit()
	}
	var resultType, resultID, resultURL any
	if event.Result != nil {
		resultType, resultID, resultURL = event.Result.Type, event.Result.ID, event.Result.URL
	}
	write, err := tx.ExecContext(ctx, `INSERT INTO job_events(job_id,event_ordinal,phase,progress,warning,error,result_type,result_id,result_url,created_at) SELECT ?,?,?,?,?,?,?,?,?,? WHERE EXISTS (SELECT 1 FROM jobs WHERE id=? AND project_uuid=?)`, event.JobID, event.Ordinal, event.Phase, event.Progress, nullString(event.Warning), nullString(event.Error), resultType, resultID, resultURL, event.CreatedAt.UTC().Format(time.RFC3339Nano), event.JobID, s.projectID)
	if err != nil {
		return versioningrelease.Event{}, false, err
	}
	if rows, rowsErr := write.RowsAffected(); rowsErr != nil {
		return versioningrelease.Event{}, false, rowsErr
	} else if rows != 1 {
		return versioningrelease.Event{}, false, ErrReleaseJobNotFound
	}
	if err = tx.Commit(); err != nil {
		return versioningrelease.Event{}, false, err
	}
	return event, false, nil
}

func (s *Store) ListReleaseJobEvents(ctx context.Context, jobID domain.ID, after int64) ([]versioningrelease.Event, error) {
	if !jobID.Valid() || after < 0 {
		return nil, ErrReleaseJobInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.job_id,e.event_ordinal,e.phase,e.progress,e.warning,e.error,e.result_type,e.result_id,e.result_url,e.created_at FROM job_events e JOIN jobs j ON j.id=e.job_id WHERE e.job_id=? AND j.project_uuid=? AND e.event_ordinal>? ORDER BY e.event_ordinal`, jobID, s.projectID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]versioningrelease.Event, 0)
	for rows.Next() {
		event, err := scanReleaseJobEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
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

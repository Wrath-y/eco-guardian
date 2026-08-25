package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	aiorchestration "github.com/zouyi/eco-guardian/internal/ai/orchestration"
	"github.com/zouyi/eco-guardian/internal/domain"
)

var _ aiorchestration.AIJobEventRepository = (*Store)(nil)

func (s *Store) CommitAIJobEvent(ctx context.Context, draft aiorchestration.AIJobEventDraft) (aiorchestration.AIJobEvent, bool, error) {
	if ctx == nil || !draft.Valid() {
		return aiorchestration.AIJobEvent{}, false, aiorchestration.ErrAIJobEventInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return aiorchestration.AIJobEvent{}, false, err
	}
	defer tx.Rollback()
	if existing, found, findErr := findAIJobEventByKey(ctx, tx, draft.JobID, draft.EventKey, s.projectID); findErr != nil {
		return aiorchestration.AIJobEvent{}, false, findErr
	} else if found {
		if !equalAIPersistence(existing.AIJobEventDraft, draft) {
			return aiorchestration.AIJobEvent{}, false, aiorchestration.ErrAIJobEventInvalid
		}
		return existing, true, tx.Commit()
	}
	state, err := loadAIJobState(ctx, tx, draft.JobID, s.projectID)
	if err != nil || draft.Phase.Order() > state.Phase.Order() {
		return aiorchestration.AIJobEvent{}, false, aiorchestration.ErrAIJobEventInvalid
	}
	var ordinal int64
	var progress int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(event_ordinal),0),COALESCE(MAX(progress),0) FROM job_events WHERE job_id=?`, draft.JobID).Scan(&ordinal, &progress); err != nil {
		return aiorchestration.AIJobEvent{}, false, err
	}
	if draft.Progress < progress {
		return aiorchestration.AIJobEvent{}, false, aiorchestration.ErrAIJobEventInvalid
	}
	event := aiorchestration.AIJobEvent{AIJobEventDraft: draft, Ordinal: ordinal + 1, CreatedAt: s.now().UTC()}
	if !event.Valid() {
		return aiorchestration.AIJobEvent{}, false, aiorchestration.ErrAIJobEventInvalid
	}
	canonical, err := domain.CanonicalJSON(event)
	if err != nil || len(canonical) > aiorchestration.MaxAIJobEventBytesV1 {
		return aiorchestration.AIJobEvent{}, false, aiorchestration.ErrAIJobEventInvalid
	}
	digest := sha256.Sum256(canonical)
	if err = ensureAIRunCapacity(ctx, tx, draft.JobID, s.projectID, len(canonical)); err != nil {
		return aiorchestration.AIJobEvent{}, false, err
	}
	var resultType, resultID, resultURL any
	if event.Result != nil {
		resultType, resultID, resultURL = event.Result.Type, event.Result.ID, event.Result.URL
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO job_events(job_id,event_ordinal,phase,progress,warning,error,result_type,result_id,result_url,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, event.JobID, event.Ordinal, event.Phase, event.Progress, nullableAIText(event.WarningCode), nullableAIText(event.SafeErrorCode), resultType, resultID, resultURL, formatAIJobTime(event.CreatedAt)); err != nil {
		return aiorchestration.AIJobEvent{}, false, err
	}
	var toolID, toolVersion, toolHash any
	if event.Tool != nil {
		toolID, toolVersion, toolHash = event.Tool.ID, event.Tool.Version, event.Tool.Hash
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ai_job_event_details(job_id,event_ordinal,event_key,event_kind,attempt_id,warning_code,warning_ref,tool_id,tool_version,tool_hash,repair_count,outcome,canonical_event,event_hash) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, event.JobID, event.Ordinal, event.EventKey, event.Kind, nullableAttemptID(event.AttemptID), nullableAIText(event.WarningCode), nullableAIText(event.WarningRef), toolID, toolVersion, toolHash, nullableAIInt(event.RepairCount), nullableAIText(string(event.Outcome)), canonical, hex.EncodeToString(digest[:]))
	if err != nil {
		return aiorchestration.AIJobEvent{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return aiorchestration.AIJobEvent{}, false, err
	}
	return event, false, nil
}

func (s *Store) ListAIJobEvents(ctx context.Context, jobID domain.ID, after int64) ([]aiorchestration.AIJobEvent, error) {
	if ctx == nil || !jobID.Valid() || after < 0 {
		return nil, aiorchestration.ErrAIJobEventReplay
	}
	rows, err := s.db.QueryContext(ctx, `SELECT d.canonical_event FROM ai_job_event_details d JOIN ai_design_runs r ON r.job_id=d.job_id WHERE d.job_id=? AND r.project_uuid=? AND d.event_ordinal>? ORDER BY d.event_ordinal`, jobID, s.projectID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []aiorchestration.AIJobEvent{}
	for rows.Next() {
		var canonical []byte
		if err = rows.Scan(&canonical); err != nil {
			return nil, err
		}
		var event aiorchestration.AIJobEvent
		if err = json.Unmarshal(canonical, &event); err != nil || !event.Valid() || event.JobID != jobID {
			return nil, aiorchestration.ErrAIJobEventReplay
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) GetAIJobState(ctx context.Context, jobID domain.ID) (aiorchestration.AIJobState, error) {
	return s.GetAIJob(ctx, jobID)
}

func findAIJobEventByKey(ctx context.Context, tx *sql.Tx, jobID domain.ID, eventKey string, projectID domain.ID) (aiorchestration.AIJobEvent, bool, error) {
	var canonical []byte
	err := tx.QueryRowContext(ctx, `SELECT d.canonical_event FROM ai_job_event_details d JOIN ai_design_runs r ON r.job_id=d.job_id WHERE d.job_id=? AND d.event_key=? AND r.project_uuid=?`, jobID, eventKey, projectID).Scan(&canonical)
	if errors.Is(err, sql.ErrNoRows) {
		return aiorchestration.AIJobEvent{}, false, nil
	}
	if err != nil {
		return aiorchestration.AIJobEvent{}, false, err
	}
	var event aiorchestration.AIJobEvent
	if err = json.Unmarshal(canonical, &event); err != nil || !event.Valid() {
		return aiorchestration.AIJobEvent{}, false, aiorchestration.ErrAIJobEventInvalid
	}
	return event, true, nil
}

func nullableAIInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

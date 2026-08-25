package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	aiaudit "github.com/zouyi/eco-guardian/internal/ai/audit"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	"github.com/zouyi/eco-guardian/internal/domain"
)

var _ aiaudit.EventRepository = (*Store)(nil)

func (s *Store) AppendAuditEvent(ctx context.Context, draft aiaudit.EventDraft) (aiaudit.Event, bool, error) {
	if ctx == nil || !draft.Valid() {
		return aiaudit.Event{}, false, aiaudit.ErrAuditEventInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return aiaudit.Event{}, false, err
	}
	defer tx.Rollback()

	if existing, found, findErr := findAIAuditEvent(ctx, tx, draft.AttemptID, draft.Ordinal, s.projectID); findErr != nil {
		return aiaudit.Event{}, false, findErr
	} else if found {
		if !auditEventMatchesDraft(existing, draft) {
			return aiaudit.Event{}, false, aiaudit.ErrAuditEventConflict
		}
		return existing, true, tx.Commit()
	}
	if !aiAttemptExists(ctx, tx, draft.AttemptID, s.projectID) {
		return aiaudit.Event{}, false, aiaudit.ErrAuditEventConflict
	}
	var lastOrdinal sql.NullInt64
	var previous sql.NullString
	if err = tx.QueryRowContext(ctx, `SELECT max(ordinal) FROM ai_audit_events WHERE attempt_id=?`, draft.AttemptID).Scan(&lastOrdinal); err != nil {
		return aiaudit.Event{}, false, err
	}
	if lastOrdinal.Valid {
		if draft.Ordinal != int(lastOrdinal.Int64)+1 {
			return aiaudit.Event{}, false, aiaudit.ErrAuditEventConflict
		}
		if err = tx.QueryRowContext(ctx, `SELECT event_hash FROM ai_audit_events WHERE attempt_id=? AND ordinal=?`, draft.AttemptID, lastOrdinal.Int64).Scan(&previous); err != nil {
			return aiaudit.Event{}, false, err
		}
	} else if draft.Ordinal != 1 {
		return aiaudit.Event{}, false, aiaudit.ErrAuditEventConflict
	}
	event, err := aiaudit.BuildEvent(aicontract.Hash(previous.String), draft)
	if err != nil {
		return aiaudit.Event{}, false, err
	}
	canonical, err := aiaudit.CanonicalEvent(event)
	if err != nil {
		return aiaudit.Event{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ai_audit_events(attempt_id,ordinal,event_kind,canonical_event,event_hash,created_at) VALUES(?,?,?,?,?,?)`, draft.AttemptID, draft.Ordinal, draft.Kind, canonical, event.ChainHash, formatAIJobTime(s.now().UTC())); err != nil {
		return aiaudit.Event{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return aiaudit.Event{}, false, err
	}
	return event, false, nil
}

func (s *Store) ListAuditEvents(ctx context.Context, attemptID aicontract.AttemptID) ([]aiaudit.Event, error) {
	if ctx == nil || !attemptID.Valid() {
		return nil, aiaudit.ErrAuditEventInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.canonical_event,e.event_hash FROM ai_audit_events e JOIN ai_attempts a ON a.attempt_id=e.attempt_id JOIN ai_design_runs r ON r.job_id=a.job_id WHERE e.attempt_id=? AND r.project_uuid=? ORDER BY e.ordinal`, attemptID, s.projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []aiaudit.Event{}
	var previous aicontract.Hash
	for rows.Next() {
		var canonical []byte
		var storedHash aicontract.Hash
		if err = rows.Scan(&canonical, &storedHash); err != nil {
			return nil, err
		}
		var event aiaudit.Event
		if err = json.Unmarshal(canonical, &event); err != nil || !event.Valid() || event.Record.Ordinal != len(result)+1 || event.Record.AttemptID != attemptID || event.PreviousHash != previous || event.ChainHash != storedHash {
			return nil, aiaudit.ErrAuditEventInvalid
		}
		wantCanonical, canonicalErr := aiaudit.CanonicalEvent(event)
		if canonicalErr != nil || !bytes.Equal(wantCanonical, canonical) {
			return nil, aiaudit.ErrAuditEventInvalid
		}
		result = append(result, event)
		previous = event.ChainHash
	}
	return result, rows.Err()
}

func findAIAuditEvent(ctx context.Context, tx *sql.Tx, attemptID aicontract.AttemptID, ordinal int, projectID domain.ID) (aiaudit.Event, bool, error) {
	var canonical []byte
	var eventKind string
	var eventHash aicontract.Hash
	err := tx.QueryRowContext(ctx, `SELECT e.event_kind,e.canonical_event,e.event_hash FROM ai_audit_events e JOIN ai_attempts a ON a.attempt_id=e.attempt_id JOIN ai_design_runs r ON r.job_id=a.job_id WHERE e.attempt_id=? AND e.ordinal=? AND r.project_uuid=?`, attemptID, ordinal, projectID).Scan(&eventKind, &canonical, &eventHash)
	if errors.Is(err, sql.ErrNoRows) {
		return aiaudit.Event{}, false, nil
	}
	if err != nil {
		return aiaudit.Event{}, false, err
	}
	var event aiaudit.Event
	if err = json.Unmarshal(canonical, &event); err != nil || !event.Valid() || event.Record.EventType != eventKind || event.ChainHash != eventHash {
		return aiaudit.Event{}, false, aiaudit.ErrAuditEventInvalid
	}
	wantCanonical, canonicalErr := aiaudit.CanonicalEvent(event)
	if canonicalErr != nil || !bytes.Equal(wantCanonical, canonical) {
		return aiaudit.Event{}, false, aiaudit.ErrAuditEventInvalid
	}
	return event, true, nil
}

func auditEventMatchesDraft(event aiaudit.Event, draft aiaudit.EventDraft) bool {
	return event.Record.Ordinal == draft.Ordinal && event.Record.AttemptID == draft.AttemptID && event.Record.EventType == string(draft.Kind) && bytes.Equal(event.Payload, draft.Payload) && equalAIPersistence(event.Record.Versions, draft.Versions)
}

func aiAttemptExists(ctx context.Context, tx *sql.Tx, attemptID aicontract.AttemptID, projectID domain.ID) bool {
	var count int
	return tx.QueryRowContext(ctx, `SELECT count(*) FROM ai_attempts a JOIN ai_design_runs r ON r.job_id=a.job_id WHERE a.attempt_id=? AND r.project_uuid=?`, attemptID, projectID).Scan(&count) == nil && count == 1
}

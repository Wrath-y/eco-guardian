package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioningrelease "github.com/zouyi/eco-guardian/internal/versioning/release"
)

var ErrReleaseCursor = errors.New("release history cursor is invalid")

type releaseHistoryCursor struct {
	CreatedAt time.Time `json:"c"`
	ID        domain.ID `json:"i"`
}

func encodeReleaseHistoryCursor(cursor releaseHistoryCursor) string {
	body, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeReleaseHistoryCursor(value string) (releaseHistoryCursor, error) {
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return releaseHistoryCursor{}, ErrReleaseCursor
	}
	var cursor releaseHistoryCursor
	if err := json.Unmarshal(body, &cursor); err != nil || !cursor.ID.Valid() || cursor.CreatedAt.IsZero() {
		return releaseHistoryCursor{}, ErrReleaseCursor
	}
	return cursor, nil
}

// ListReleases returns the append-only release audit in a stable order. It is
// intentionally separate from GetRelease, whose narrow shape is retained for
// command validation ports.
func (s *Store) ListReleases(ctx context.Context, after string, limit int) (versioningrelease.ReadPage, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return versioningrelease.ReadPage{}, ErrReleaseCursor
	}
	query := releaseReadSelect
	args := make([]any, 0, 3)
	if after != "" {
		cursor, err := decodeReleaseHistoryCursor(after)
		if err != nil {
			return versioningrelease.ReadPage{}, err
		}
		query += ` WHERE created_at < ? OR (created_at = ? AND id < ?)`
		args = append(args, cursor.CreatedAt.UTC().Format(time.RFC3339Nano), cursor.CreatedAt.UTC().Format(time.RFC3339Nano), cursor.ID)
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return versioningrelease.ReadPage{}, err
	}
	defer rows.Close()
	page := versioningrelease.ReadPage{Items: make([]versioningrelease.ReadRecord, 0, limit)}
	for rows.Next() {
		record, err := scanReleaseReadRecord(rows)
		if err != nil {
			return versioningrelease.ReadPage{}, err
		}
		page.Items = append(page.Items, record)
	}
	if err := rows.Err(); err != nil {
		return versioningrelease.ReadPage{}, err
	}
	if len(page.Items) > limit {
		last := page.Items[limit-1]
		page.Items = page.Items[:limit]
		page.NextCursor = encodeReleaseHistoryCursor(releaseHistoryCursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	return page, nil
}

func (s *Store) GetReleaseRecord(ctx context.Context, releaseID domain.ID) (versioningrelease.ReadRecord, error) {
	if !releaseID.Valid() {
		return versioningrelease.ReadRecord{}, ErrReleaseNotFound
	}
	record, err := scanReleaseReadRecord(s.db.QueryRowContext(ctx, releaseReadSelect+` WHERE id=?`, releaseID))
	if errors.Is(err, sql.ErrNoRows) {
		return versioningrelease.ReadRecord{}, ErrReleaseNotFound
	}
	return record, err
}

const releaseReadSelect = `SELECT id,revision_id,policy_id,baseline_release_id,intent_id,notes,gate_evidence,confirmations,override_audit,created_at FROM releases`

type releaseReadScanner interface{ Scan(...any) error }

func scanReleaseReadRecord(scanner releaseReadScanner) (versioningrelease.ReadRecord, error) {
	var id, revisionID, policyID, intentID, notes, evidence, confirmations, createdRaw string
	var baseline, override sql.NullString
	if err := scanner.Scan(&id, &revisionID, &policyID, &baseline, &intentID, &notes, &evidence, &confirmations, &override, &createdRaw); err != nil {
		return versioningrelease.ReadRecord{}, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdRaw)
	if err != nil {
		return versioningrelease.ReadRecord{}, err
	}
	var decodedConfirmations []versioningrelease.Confirmation
	if json.Unmarshal([]byte(confirmations), &decodedConfirmations) != nil || !json.Valid([]byte(evidence)) {
		return versioningrelease.ReadRecord{}, ErrReleaseNotFound
	}
	var audit *versioningrelease.OverrideAudit
	if override.Valid && json.Unmarshal([]byte(override.String), &audit) != nil {
		return versioningrelease.ReadRecord{}, ErrReleaseNotFound
	}
	record := versioningrelease.ReadRecord{
		Release:           versioningrelease.Release{ID: domain.ID(id), RevisionID: domain.ID(revisionID), PolicyID: domain.ID(policyID), IntentID: domain.ID(intentID), CreatedAt: createdAt},
		BaselineReleaseID: domain.ID(baseline.String), Notes: notes, GateEvidence: json.RawMessage(evidence), Confirmations: decodedConfirmations, Override: audit,
	}
	if !record.Release.Valid() {
		return versioningrelease.ReadRecord{}, ErrReleaseNotFound
	}
	return record, nil
}

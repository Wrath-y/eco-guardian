package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

var (
	ErrRevisionNotFound      = errors.New("revision not found")
	ErrInvalidRevisionCursor = errors.New("invalid revision cursor")
)

type revisionHistoryCursor struct {
	DisplayRevision int64     `json:"d"`
	RevisionID      domain.ID `json:"i"`
}

func encodeRevisionHistoryCursor(cursor revisionHistoryCursor) string {
	body, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeRevisionHistoryCursor(raw string) (revisionHistoryCursor, error) {
	body, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return revisionHistoryCursor{}, ErrInvalidRevisionCursor
	}
	var cursor revisionHistoryCursor
	if err := json.Unmarshal(body, &cursor); err != nil || cursor.DisplayRevision < 1 || !cursor.RevisionID.Valid() {
		return revisionHistoryCursor{}, ErrInvalidRevisionCursor
	}
	return cursor, nil
}

// GetRevisionRecord materializes one immutable metadata row and verifies its
// canonical manifest/hash before returning it to a caller.
func (s *Store) GetRevisionRecord(ctx context.Context, revisionID domain.ID) (versioningrevision.Record, error) {
	if !revisionID.Valid() {
		return versioningrevision.Record{}, ErrRevisionNotFound
	}
	row := s.db.QueryRowContext(ctx, revisionMetadataSelect+` WHERE r.id=?`, revisionID)
	record, err := scanRevisionRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return versioningrevision.Record{}, ErrRevisionNotFound
	}
	return record, err
}

// ListRevisionRecords returns newest-first immutable history with an opaque,
// stable display-revision/ID cursor. It never reads working state.
func (s *Store) ListRevisionRecords(ctx context.Context, after string, limit int) (versioningrevision.HistoryPage, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return versioningrevision.HistoryPage{}, errors.New("limit must be 1..200")
	}
	query := revisionMetadataSelect
	args := make([]any, 0, 4)
	if after != "" {
		cursor, err := decodeRevisionHistoryCursor(after)
		if err != nil {
			return versioningrevision.HistoryPage{}, err
		}
		query += ` WHERE r.display_revision < ? OR (r.display_revision = ? AND r.id < ?)`
		args = append(args, cursor.DisplayRevision, cursor.DisplayRevision, cursor.RevisionID)
	}
	query += ` ORDER BY r.display_revision DESC,r.id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return versioningrevision.HistoryPage{}, err
	}
	defer rows.Close()
	page := versioningrevision.HistoryPage{Items: make([]versioningrevision.Record, 0, limit)}
	for rows.Next() {
		record, err := scanRevisionRecord(rows)
		if err != nil {
			return versioningrevision.HistoryPage{}, err
		}
		page.Items = append(page.Items, record)
	}
	if err := rows.Err(); err != nil {
		return versioningrevision.HistoryPage{}, err
	}
	if len(page.Items) > limit {
		last := page.Items[limit-1]
		page.Items = page.Items[:limit]
		page.NextCursor = encodeRevisionHistoryCursor(revisionHistoryCursor{DisplayRevision: last.DisplayRevision, RevisionID: last.Metadata.RevisionID})
	}
	return page, nil
}

// GetRevisionDetail merges immutable revision facts with durable release
// history. It deliberately does not materialize a mutable candidate/status
// column on config_revisions.
func (s *Store) GetRevisionDetail(ctx context.Context, revisionID domain.ID) (versioningrevision.Detail, error) {
	record, err := s.GetRevisionRecord(ctx, revisionID)
	if err != nil {
		return versioningrevision.Detail{}, err
	}
	detail := versioningrevision.Detail{Record: record, Timeline: make([]versioningrevision.TimelineEvent, 0)}
	rows, err := s.db.QueryContext(ctx, revisionTimelineQuery, revisionID, revisionID, revisionID, revisionID, revisionID)
	if err != nil {
		return versioningrevision.Detail{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, occurredAt, kind, eventRevisionID, subjectID, status string
		if err := rows.Scan(&id, &occurredAt, &kind, &eventRevisionID, &subjectID, &status); err != nil {
			return versioningrevision.Detail{}, err
		}
		occurred, err := time.Parse(time.RFC3339Nano, occurredAt)
		if err != nil {
			return versioningrevision.Detail{}, fmt.Errorf("invalid revision timeline timestamp: %w", err)
		}
		detail.Timeline = append(detail.Timeline, versioningrevision.TimelineEvent{ID: id, OccurredAt: occurred, Type: kind, RevisionID: domain.ID(eventRevisionID), SubjectID: domain.ID(subjectID), Status: status})
	}
	if err := rows.Err(); err != nil {
		return versioningrevision.Detail{}, err
	}
	var activeReleaseID sql.NullString
	var generation int64
	var activeRevisionID sql.NullString
	var activeCreatedAt sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT p.active_release_id,p.generation,r.revision_id,r.created_at FROM active_release_pointer p LEFT JOIN releases r ON r.id=p.active_release_id WHERE p.singleton=1`).Scan(&activeReleaseID, &generation, &activeRevisionID, &activeCreatedAt)
	if err != nil {
		return versioningrevision.Detail{}, err
	}
	detail.PointerGeneration = generation
	if activeReleaseID.Valid {
		detail.ActiveReleaseID = domain.ID(activeReleaseID.String)
	}
	if activeRevisionID.Valid && domain.ID(activeRevisionID.String) == revisionID {
		occurred, parseErr := time.Parse(time.RFC3339Nano, activeCreatedAt.String)
		if parseErr != nil {
			return versioningrevision.Detail{}, fmt.Errorf("invalid active pointer timestamp: %w", parseErr)
		}
		detail.Timeline = append(detail.Timeline, versioningrevision.TimelineEvent{ID: "active-pointer:" + activeReleaseID.String, OccurredAt: occurred, Type: "active_pointer_changed", RevisionID: revisionID, SubjectID: domain.ID(activeReleaseID.String), Status: fmt.Sprintf("generation=%d", generation)})
	}
	sortTimeline(detail.Timeline)
	return detail, nil
}

const revisionTimelineQuery = `
SELECT id,created_at,'revision_created',id,'','' FROM config_revisions WHERE id=?
UNION ALL
SELECT id,created_at,'validation_completed',source_revision_id,id,status FROM validation_runs WHERE source_revision_id=? AND scope='FULL'
UNION ALL
SELECT id,created_at,'release_queued',revision_id,id,status FROM jobs WHERE revision_id=?
UNION ALL
SELECT i.id,i.created_at,'release_intent',i.candidate_revision_id,i.job_id,i.phase FROM release_intents i WHERE i.candidate_revision_id=?
UNION ALL
SELECT id,created_at,'release_completed',revision_id,id,'published' FROM releases WHERE revision_id=?
ORDER BY 2,1,3`

func sortTimeline(events []versioningrevision.TimelineEvent) {
	sort.Slice(events, func(i, j int) bool {
		if !events[i].OccurredAt.Equal(events[j].OccurredAt) {
			return events[i].OccurredAt.Before(events[j].OccurredAt)
		}
		if events[i].ID != events[j].ID {
			return events[i].ID < events[j].ID
		}
		return events[i].Type < events[j].Type
	})
}

const revisionMetadataSelect = `SELECT r.id,r.display_revision,r.config_hash,m.name,m.description,
	m.parent_revision_id,m.source_revision_id,m.source_release_id,m.version_manifest,
	m.version_manifest_hash,m.created_at
	FROM config_revisions r JOIN revision_metadata m ON m.revision_id=r.id`

type revisionRecordScanner interface{ Scan(...any) error }

func scanRevisionRecord(scanner revisionRecordScanner) (versioningrevision.Record, error) {
	var (
		id, configHash, rawManifest, manifestHash, createdAt           string
		displayRevision                                                int64
		name, description, parentID, sourceRevisionID, sourceReleaseID sql.NullString
	)
	if err := scanner.Scan(&id, &displayRevision, &configHash, &name, &description, &parentID, &sourceRevisionID, &sourceReleaseID, &rawManifest, &manifestHash, &createdAt); err != nil {
		return versioningrevision.Record{}, err
	}
	created, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return versioningrevision.Record{}, fmt.Errorf("invalid revision metadata timestamp: %w", err)
	}
	var manifest versioningrevision.VersionManifest
	if err := json.Unmarshal([]byte(rawManifest), &manifest); err != nil {
		return versioningrevision.Record{}, fmt.Errorf("invalid revision metadata manifest: %w", err)
	}
	canonical, err := manifest.CanonicalJSON()
	if err != nil {
		return versioningrevision.Record{}, fmt.Errorf("canonicalize revision metadata manifest: %w", err)
	}
	if string(canonical) != rawManifest {
		return versioningrevision.Record{}, errors.New("revision metadata manifest is not canonical")
	}
	actualHash, err := manifest.Hash()
	if err != nil {
		return versioningrevision.Record{}, fmt.Errorf("hash revision metadata manifest: %w", err)
	}
	if actualHash != manifestHash {
		return versioningrevision.Record{}, errors.New("revision metadata manifest hash mismatch")
	}
	record := versioningrevision.Record{DisplayRevision: displayRevision, Metadata: versioningrevision.Metadata{
		RevisionID:       domain.ID(id),
		ConfigHash:       configHash,
		Name:             name.String,
		Description:      description.String,
		ParentRevisionID: domain.ID(parentID.String),
		SourceRevisionID: domain.ID(sourceRevisionID.String),
		SourceReleaseID:  domain.ID(sourceReleaseID.String),
		Manifest:         manifest,
		ManifestHash:     manifestHash,
		CreatedAt:        created,
	}}
	if !record.Valid() {
		return versioningrevision.Record{}, errors.New("invalid revision metadata record")
	}
	return record, nil
}

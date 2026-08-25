package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
	"github.com/zouyi/eco-guardian/internal/risk/threshold"
)

var _ threshold.Repository = (*Store)(nil)

const thresholdVersionSelect = `SELECT project_uuid,id,display_version,origin,enabled,canonical_body,body_hash,created_by,created_at FROM threshold_versions`

func (s *Store) GetExactEnabled(ctx context.Context, projectID domain.ID, identity riskcontract.Identity) (threshold.Version, bool, error) {
	if projectID != s.projectID || !identity.Valid() {
		return threshold.Version{}, false, threshold.ErrThresholdInvalid
	}
	displayVersion, err := strconv.ParseInt(identity.Version, 10, 64)
	if err != nil || displayVersion < 1 {
		return threshold.Version{}, false, threshold.ErrThresholdInvalid
	}
	version, err := scanThresholdVersion(s.db.QueryRowContext(ctx, thresholdVersionSelect+` WHERE project_uuid=? AND id=? AND display_version=? AND body_hash=? AND enabled=1`, projectID, identity.ID, displayVersion, identity.Hash))
	if errors.Is(err, sql.ErrNoRows) {
		return threshold.Version{}, false, nil
	}
	if err != nil {
		return threshold.Version{}, false, err
	}
	return version, true, nil
}

func (s *Store) GetThresholdVersion(ctx context.Context, projectID, id domain.ID) (threshold.Version, error) {
	if projectID != s.projectID || !id.Valid() {
		return threshold.Version{}, threshold.ErrThresholdInvalid
	}
	version, err := scanThresholdVersion(s.db.QueryRowContext(ctx, thresholdVersionSelect+` WHERE project_uuid=? AND id=? AND display_version>0`, projectID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return threshold.Version{}, ErrNotFound
	}
	return version, err
}

func (s *Store) CreateEnabled(ctx context.Context, request threshold.CreateRequest) (threshold.Version, bool, error) {
	if !request.Valid() || request.ProjectID != s.projectID {
		return threshold.Version{}, false, threshold.ErrThresholdInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return threshold.Version{}, false, err
	}
	defer tx.Rollback()

	existing, err := scanThresholdVersion(tx.QueryRowContext(ctx, thresholdVersionSelect+` WHERE project_uuid=? AND activation_key=?`, request.ProjectID, request.IdempotencyKey))
	if err == nil {
		var requestHash string
		if err = tx.QueryRowContext(ctx, `SELECT activation_request_hash FROM threshold_versions WHERE id=?`, existing.ID).Scan(&requestHash); err != nil {
			return threshold.Version{}, false, err
		}
		if requestHash != request.RequestHash || existing.BodyHash != request.BodyHash || !existing.Enabled {
			return threshold.Version{}, false, threshold.ErrIdempotencyConflict
		}
		if err = tx.Commit(); err != nil {
			return threshold.Version{}, false, err
		}
		return existing, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return threshold.Version{}, false, err
	}

	var displayVersion int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(display_version),0)+1 FROM threshold_versions WHERE project_uuid=?`, request.ProjectID).Scan(&displayVersion); err != nil {
		return threshold.Version{}, false, err
	}
	version, err := threshold.NewVersion(request.ProjectID, request.ProposedID, displayVersion, request.Origin, true, request.Body, request.CreatedBy, request.CreatedAt)
	if err != nil || version.BodyHash != request.BodyHash {
		return threshold.Version{}, false, threshold.ErrThresholdInvalid
	}
	body, err := version.Body.CanonicalJSON()
	if err != nil {
		return threshold.Version{}, false, threshold.ErrThresholdInvalid
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO threshold_versions(id,project_uuid,display_version,origin,enabled,schema_version,canonical_body,body_hash,created_by,created_at,activation_key,activation_request_hash) VALUES(?,?,?,?,1,?,?,?,?,?,?,?)`, version.ID, version.ProjectID, version.DisplayVersion, version.Origin, threshold.SchemaVersionV1, string(body), version.BodyHash, version.CreatedBy, version.CreatedAt.Format(time.RFC3339Nano), request.IdempotencyKey, request.RequestHash)
	if err != nil {
		return threshold.Version{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return threshold.Version{}, false, err
	}
	return version, false, nil
}

type thresholdVersionScanner interface{ Scan(...any) error }

func scanThresholdVersion(scanner thresholdVersionScanner) (threshold.Version, error) {
	var version threshold.Version
	var origin, bodyRaw, createdAt string
	var enabled int
	if err := scanner.Scan(&version.ProjectID, &version.ID, &version.DisplayVersion, &origin, &enabled, &bodyRaw, &version.BodyHash, &version.CreatedBy, &createdAt); err != nil {
		return threshold.Version{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(bodyRaw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&version.Body); err != nil {
		return threshold.Version{}, fmt.Errorf("decode threshold body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return threshold.Version{}, errors.New("decode threshold body: trailing content")
	}
	version.Origin = threshold.Origin(origin)
	version.Enabled = enabled == 1
	parsedAt, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return threshold.Version{}, err
	}
	version.CreatedAt = parsedAt
	if !version.Valid() {
		return threshold.Version{}, threshold.ErrThresholdInvalid
	}
	return version, nil
}

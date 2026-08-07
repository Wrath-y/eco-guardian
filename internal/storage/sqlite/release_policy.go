package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioning "github.com/zouyi/eco-guardian/internal/versioning"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
)

var (
	ErrReleasePolicyNotFound = errors.New("release policy not found")
	ErrReleasePolicyInvalid  = errors.New("release policy is invalid")
	ErrReleasePolicyCursor   = errors.New("release policy cursor is invalid")
)

type releasePolicyCursor struct {
	DisplayVersion int64     `json:"d"`
	ID             domain.ID `json:"i"`
}

func encodeReleasePolicyCursor(cursor releasePolicyCursor) string {
	body, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeReleasePolicyCursor(value string) (releasePolicyCursor, error) {
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return releasePolicyCursor{}, ErrReleasePolicyCursor
	}
	var cursor releasePolicyCursor
	if err := json.Unmarshal(body, &cursor); err != nil || cursor.DisplayVersion < 1 || !cursor.ID.Valid() {
		return releasePolicyCursor{}, ErrReleasePolicyCursor
	}
	return cursor, nil
}

// CreatePolicy allocates a new immutable policy version. A policy body may
// only name contracts supplied by the composition root's catalog.
func (s *Store) CreatePolicy(ctx context.Context, definition versioningpolicy.Definition, catalog versioningpolicy.ContractCatalog) (versioningpolicy.ReleasePolicy, error) {
	if err := definition.ValidateContracts(catalog); err != nil {
		return versioningpolicy.ReleasePolicy{}, fmt.Errorf("%w: %v", ErrReleasePolicyInvalid, err)
	}
	body, err := definition.CanonicalJSON()
	if err != nil {
		return versioningpolicy.ReleasePolicy{}, err
	}
	hash := versioning.SHA256(body)
	id, err := domain.NewID()
	if err != nil {
		return versioningpolicy.ReleasePolicy{}, err
	}
	createdAt := s.now().UTC()
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return versioningpolicy.ReleasePolicy{}, err
	}
	defer tx.Rollback()
	var displayVersion int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(display_version),0)+1 FROM release_policies`).Scan(&displayVersion); err != nil {
		return versioningpolicy.ReleasePolicy{}, err
	}
	policy := versioningpolicy.ReleasePolicy{Definition: definition, ID: id, DisplayVersion: displayVersion, CanonicalHash: hash, CreatedAt: createdAt}
	if !policy.Valid() {
		return versioningpolicy.ReleasePolicy{}, ErrReleasePolicyInvalid
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO release_policies(id,display_version,canonical_body,canonical_hash,created_at) VALUES(?,?,?,?,?)`, id, displayVersion, string(body), hash, createdAt.Format(time.RFC3339Nano)); err != nil {
		return versioningpolicy.ReleasePolicy{}, err
	}
	if err = tx.Commit(); err != nil {
		return versioningpolicy.ReleasePolicy{}, err
	}
	return policy, nil
}

func (s *Store) GetPolicy(ctx context.Context, id domain.ID) (versioningpolicy.ReleasePolicy, error) {
	if !id.Valid() {
		return versioningpolicy.ReleasePolicy{}, ErrReleasePolicyNotFound
	}
	return scanReleasePolicy(s.db.QueryRowContext(ctx, `SELECT id,display_version,canonical_body,canonical_hash,created_at FROM release_policies WHERE id=?`, id))
}

func (s *Store) ListPolicies(ctx context.Context, after string, limit int) (versioningpolicy.Page, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return versioningpolicy.Page{}, ErrReleasePolicyCursor
	}
	query := `SELECT id,display_version,canonical_body,canonical_hash,created_at FROM release_policies`
	args := []any{}
	if after != "" {
		cursor, err := decodeReleasePolicyCursor(after)
		if err != nil {
			return versioningpolicy.Page{}, err
		}
		query += ` WHERE display_version < ? OR (display_version = ? AND id < ?)`
		args = append(args, cursor.DisplayVersion, cursor.DisplayVersion, cursor.ID)
	}
	query += ` ORDER BY display_version DESC,id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return versioningpolicy.Page{}, err
	}
	defer rows.Close()
	page := versioningpolicy.Page{Items: make([]versioningpolicy.ReleasePolicy, 0, limit)}
	for rows.Next() {
		policy, err := scanReleasePolicy(rows)
		if err != nil {
			return versioningpolicy.Page{}, err
		}
		page.Items = append(page.Items, policy)
	}
	if err := rows.Err(); err != nil {
		return versioningpolicy.Page{}, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = encodeReleasePolicyCursor(releasePolicyCursor{DisplayVersion: last.DisplayVersion, ID: last.ID})
	}
	return page, nil
}

type releasePolicyScanner interface{ Scan(...any) error }

func scanReleasePolicy(scanner releasePolicyScanner) (versioningpolicy.ReleasePolicy, error) {
	var id domain.ID
	var displayVersion int64
	var body, hash, createdRaw string
	if err := scanner.Scan(&id, &displayVersion, &body, &hash, &createdRaw); errors.Is(err, sql.ErrNoRows) {
		return versioningpolicy.ReleasePolicy{}, ErrReleasePolicyNotFound
	} else if err != nil {
		return versioningpolicy.ReleasePolicy{}, err
	}
	var definition versioningpolicy.Definition
	if err := json.Unmarshal([]byte(body), &definition); err != nil {
		return versioningpolicy.ReleasePolicy{}, fmt.Errorf("decode release policy: %w", err)
	}
	canonical, err := definition.CanonicalJSON()
	if err != nil || string(canonical) != body || versioning.SHA256(canonical) != hash {
		return versioningpolicy.ReleasePolicy{}, ErrReleasePolicyInvalid
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdRaw)
	if err != nil {
		return versioningpolicy.ReleasePolicy{}, ErrReleasePolicyInvalid
	}
	policy := versioningpolicy.ReleasePolicy{Definition: definition, ID: id, DisplayVersion: displayVersion, CanonicalHash: hash, CreatedAt: createdAt}
	if !policy.Valid() {
		return versioningpolicy.ReleasePolicy{}, ErrReleasePolicyInvalid
	}
	return policy, nil
}

package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	root "github.com/zouyi/eco-guardian"
	"github.com/zouyi/eco-guardian/internal/domain"
	_ "modernc.org/sqlite"
)

const databaseName = "project.db"
const currentSchemaVersion = 2

func DBSchemaVersion() int { return currentSchemaVersion }

var (
	ErrProjectInvalid        = errors.New("invalid or unsupported project database")
	ErrDuplicateKey          = errors.New("duplicate active kind/key")
	ErrNotFound              = errors.New("entity not found")
	ErrRevisionConflict      = errors.New("entity revision conflict")
	errLocalPreflightChanged = errors.New("local validation preflight changed")
)

type ReferencedError struct{ References []domain.Reference }

func (e *ReferencedError) Error() string { return "entity is referenced" }

type ValidationError struct{ Issues []domain.FieldIssue }

func (e ValidationError) Error() string { return "entity validation failed" }

type Store struct {
	db        *sql.DB
	registry  *domain.Registry
	writes    sync.Mutex
	now       func() time.Time
	projectID domain.ID
	failStage func(string) error // test-only transaction fault injector
}

func Create(ctx context.Context, dir string, registry *domain.Registry) (*Store, domain.ID, error) {
	path := filepath.Join(dir, databaseName)
	if _, err := os.Stat(path); err == nil {
		return nil, "", fmt.Errorf("%w: database already exists", ErrProjectInvalid)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", err
	}
	s, err := open(path, registry)
	if err != nil {
		return nil, "", err
	}
	migration, err := root.Assets.ReadFile("migrations/0001_initial.sql")
	if err != nil {
		s.Close()
		return nil, "", err
	}
	id, err := domain.NewID()
	if err != nil {
		s.Close()
		return nil, "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.Close()
		return nil, "", err
	}
	_, err = tx.ExecContext(ctx, string(migration))
	if err == nil {
		_, err = tx.ExecContext(ctx, "INSERT INTO project_meta(id,db_schema_version,created_at) VALUES(?,?,?)", id, 1, s.now().UTC().Format(time.RFC3339Nano))
	}
	if err == nil {
		err = applyMigrationV2(ctx, tx)
	}
	if err != nil {
		tx.Rollback()
		s.Close()
		return nil, "", err
	}
	if err = tx.Commit(); err != nil {
		s.Close()
		return nil, "", err
	}
	s.projectID = id
	return s, id, nil
}

func Open(dir string, registry *domain.Registry) (*Store, domain.ID, error) {
	s, err := open(filepath.Join(dir, databaseName), registry)
	if err != nil {
		return nil, "", err
	}
	var id domain.ID
	var version int
	if err = s.db.QueryRow("SELECT id,db_schema_version FROM project_meta").Scan(&id, &version); err != nil || !id.Valid() {
		s.Close()
		return nil, "", fmt.Errorf("%w: %v", ErrProjectInvalid, err)
	}
	if version == 1 {
		if err = upgradeV2(context.Background(), s.db); err != nil {
			s.Close()
			return nil, "", fmt.Errorf("%w: %v", ErrProjectInvalid, err)
		}
		version = 2
	}
	if version != currentSchemaVersion {
		s.Close()
		return nil, "", fmt.Errorf("%w: unsupported schema version %d", ErrProjectInvalid, version)
	}
	s.projectID = id
	return s, id, nil
}

func upgradeV2(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = applyMigrationV2(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}
func applyMigrationV2(ctx context.Context, tx *sql.Tx) error {
	migration, err := root.Assets.ReadFile("migrations/0002_validation.sql")
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, string(migration))
	return err
}
func open(path string, registry *domain.Registry) (*Store, error) {
	if registry == nil {
		return nil, errors.New("registry is required")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	for _, p := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000"} {
		if _, err = db.Exec(p); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db: db, registry: registry, now: time.Now}, nil
}
func (s *Store) Close() error         { return s.db.Close() }
func (s *Store) ProjectID() domain.ID { return s.projectID }

func (s *Store) Get(ctx context.Context, kind domain.EntityKind, id domain.ID) (domain.Entity, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT b.json FROM working_entities w JOIN entity_blobs b ON b.hash=w.blob_hash WHERE w.id=? AND w.kind=?`, id, kind).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Entity{}, ErrNotFound
	}
	if err != nil {
		return domain.Entity{}, err
	}
	var e domain.Entity
	return e, json.Unmarshal(raw, &e)
}

type Page struct {
	Items      []domain.Entity
	NextCursor string
}
type cursor struct {
	Key string    `json:"k"`
	ID  domain.ID `json:"i"`
}

func encodeCursor(c cursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}
func decodeCursor(v string) (cursor, error) {
	var c cursor
	b, e := base64.RawURLEncoding.DecodeString(v)
	if e != nil {
		return c, e
	}
	return c, json.Unmarshal(b, &c)
}
func (s *Store) List(ctx context.Context, kind domain.EntityKind, query, after string, limit int) (Page, error) {
	if !kind.Valid() {
		return Page{}, errors.New("unsupported kind")
	}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return Page{}, errors.New("limit must be 1..200")
	}
	c := cursor{}
	var err error
	if after != "" {
		c, err = decodeCursor(after)
		if err != nil {
			return Page{}, errors.New("invalid cursor")
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT b.json FROM working_entities w JOIN entity_blobs b ON b.hash=w.blob_hash WHERE w.kind=? AND w.status='active' AND (?='' OR lower(w.entity_key) LIKE lower(?) OR lower(json_extract(b.json,'$.name')) LIKE lower(?)) AND (w.entity_key>? OR (w.entity_key=? AND w.id>?)) ORDER BY w.entity_key,w.id LIMIT ?`, kind, query, "%"+query+"%", "%"+query+"%", c.Key, c.Key, c.ID, limit+1)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	p := Page{Items: make([]domain.Entity, 0, limit)}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return Page{}, err
		}
		var e domain.Entity
		if err := json.Unmarshal(raw, &e); err != nil {
			return Page{}, err
		}
		p.Items = append(p.Items, e)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	if len(p.Items) > limit {
		last := p.Items[limit-1]
		p.Items = p.Items[:limit]
		p.NextCursor = encodeCursor(cursor{last.Key, last.ID})
	}
	return p, nil
}

func (s *Store) Create(ctx context.Context, kind domain.EntityKind, d domain.EntityDraft) (domain.Entity, domain.RevisionSummary, error) {
	e, err := domain.NewEntity(kind, d, s.now())
	if err != nil {
		return domain.Entity{}, domain.RevisionSummary{}, err
	}
	e, err = s.validateProspective(e)
	if err != nil {
		return domain.Entity{}, domain.RevisionSummary{}, err
	}
	return s.save(ctx, e, true)
}
func (s *Store) Patch(ctx context.Context, kind domain.EntityKind, id domain.ID, version int64, p domain.EntityPatch) (domain.Entity, domain.RevisionSummary, error) {
	// Compose and validate outside the serialized write transaction. The same
	// entity version is rechecked inside it before any write takes place.
	current, e := s.Get(ctx, kind, id)
	if e != nil {
		return domain.Entity{}, domain.RevisionSummary{}, e
	}
	if current.EntityVersion != version {
		return domain.Entity{}, domain.RevisionSummary{}, ErrRevisionConflict
	}
	next, e := current.ApplyPatch(p, s.now())
	if e != nil {
		return domain.Entity{}, domain.RevisionSummary{}, e
	}
	next, e = s.validateProspective(next)
	if e != nil {
		return domain.Entity{}, domain.RevisionSummary{}, e
	}
	for attempt := 0; attempt < 2; attempt++ {
		preflight, preflightErr := s.preflightLocal(ctx, next)
		if preflightErr != nil {
			return domain.Entity{}, domain.RevisionSummary{}, preflightErr
		}
		s.writes.Lock()
		tx, txErr := s.db.BeginTx(ctx, nil)
		if txErr != nil {
			s.writes.Unlock()
			return domain.Entity{}, domain.RevisionSummary{}, txErr
		}
		current, txErr = s.getTx(ctx, tx, kind, id)
		if txErr == nil && current.EntityVersion != version {
			txErr = ErrRevisionConflict
		}
		if txErr == nil {
			var currentInput bool
			currentInput, txErr = s.recheckLocalPreflightTx(ctx, tx, preflight)
			if txErr == nil && !currentInput {
				txErr = errLocalPreflightChanged
			}
		}
		if txErr == nil {
			txErr = s.recheckActiveKeyTx(ctx, tx, next)
		}
		var revision domain.RevisionSummary
		if txErr == nil {
			revision, txErr = s.saveTx(ctx, tx, next, false)
		}
		if txErr == nil {
			txErr = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		s.writes.Unlock()
		if errors.Is(txErr, errLocalPreflightChanged) && attempt == 0 {
			continue
		}
		return next, revision, txErr
	}
	return domain.Entity{}, domain.RevisionSummary{}, ErrRevisionConflict
}

func (s *Store) validateProspective(entity domain.Entity) (domain.Entity, error) {
	normalized, err := domain.NormalizeNumericEntity(entity)
	if err != nil {
		return domain.Entity{}, ValidationError{[]domain.FieldIssue{{Path: "payload", Message: err.Error()}}}
	}
	if issues := s.registry.Validate(normalized); len(issues) > 0 {
		return domain.Entity{}, ValidationError{issues}
	}
	return normalized, nil
}
func (s *Store) Delete(ctx context.Context, kind domain.EntityKind, id domain.ID, version int64) (domain.Entity, domain.RevisionSummary, error) {
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return domain.Entity{}, domain.RevisionSummary{}, e
	}
	defer tx.Rollback()
	entity, e := s.getTx(ctx, tx, kind, id)
	if e != nil {
		return domain.Entity{}, domain.RevisionSummary{}, e
	}
	if entity.EntityVersion != version {
		return domain.Entity{}, domain.RevisionSummary{}, ErrRevisionConflict
	}
	rows, e := tx.QueryContext(ctx, `SELECT r.source_entity_id,r.field_path,r.ordinal,r.expected_kind,r.target_entity_id FROM entity_references r JOIN working_entities w ON w.id=r.source_entity_id WHERE r.target_entity_id=? AND w.status='active'`, id)
	if e != nil {
		return domain.Entity{}, domain.RevisionSummary{}, e
	}
	refs := []domain.Reference{}
	for rows.Next() {
		var ref domain.Reference
		if e = rows.Scan(&ref.SourceID, &ref.FieldPath, &ref.Ordinal, &ref.ExpectedKind, &ref.TargetID); e != nil {
			rows.Close()
			return domain.Entity{}, domain.RevisionSummary{}, e
		}
		refs = append(refs, ref)
	}
	rows.Close()
	if len(refs) > 0 {
		return domain.Entity{}, domain.RevisionSummary{}, &ReferencedError{refs}
	}
	entity.Status = domain.StatusArchived
	entity.EntityVersion++
	entity.UpdatedAt = s.now().UTC()
	r, e := s.saveTx(ctx, tx, entity, false)
	if e == nil {
		e = tx.Commit()
	}
	return entity, r, e
}

func (s *Store) save(ctx context.Context, e domain.Entity, create bool) (domain.Entity, domain.RevisionSummary, error) {
	for attempt := 0; attempt < 2; attempt++ {
		preflight, err := s.preflightLocal(ctx, e)
		if err != nil {
			return domain.Entity{}, domain.RevisionSummary{}, err
		}
		s.writes.Lock()
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			s.writes.Unlock()
			return domain.Entity{}, domain.RevisionSummary{}, err
		}
		currentInput, err := s.recheckLocalPreflightTx(ctx, tx, preflight)
		if err == nil && !currentInput {
			err = errLocalPreflightChanged
		}
		if err == nil {
			err = s.recheckActiveKeyTx(ctx, tx, e)
		}
		var revision domain.RevisionSummary
		if err == nil {
			revision, err = s.saveTx(ctx, tx, e, create)
		}
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		s.writes.Unlock()
		if errors.Is(err, errLocalPreflightChanged) && attempt == 0 {
			continue
		}
		return e, revision, err
	}
	return domain.Entity{}, domain.RevisionSummary{}, ErrRevisionConflict
}

func (s *Store) recheckActiveKeyTx(ctx context.Context, tx *sql.Tx, entity domain.Entity) error {
	if entity.Status != domain.StatusActive {
		return nil
	}
	var existing domain.ID
	err := tx.QueryRowContext(ctx, `SELECT id FROM working_entities WHERE kind=? AND entity_key=? AND status='active' AND id<>? LIMIT 1`, entity.Kind, entity.Key, entity.ID).Scan(&existing)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrDuplicateKey
}
func (s *Store) getTx(ctx context.Context, tx *sql.Tx, kind domain.EntityKind, id domain.ID) (domain.Entity, error) {
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT b.json FROM working_entities w JOIN entity_blobs b ON b.hash=w.blob_hash WHERE w.id=? AND w.kind=?`, id, kind).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Entity{}, ErrNotFound
	}
	var e domain.Entity
	if err != nil {
		return e, err
	}
	return e, json.Unmarshal(raw, &e)
}
func (s *Store) saveTx(ctx context.Context, tx *sql.Tx, e domain.Entity, create bool) (domain.RevisionSummary, error) {
	hash, blob, err := domain.BlobHash(e)
	if err != nil {
		return domain.RevisionSummary{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO entity_blobs(hash,json) VALUES(?,?)", hash, blob); err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = s.inject("blob"); err != nil {
		return domain.RevisionSummary{}, err
	}
	if create {
		_, err = tx.ExecContext(ctx, `INSERT INTO working_entities(id,kind,entity_key,schema_version,entity_version,blob_hash,status,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?)`, e.ID, e.Kind, e.Key, e.SchemaVersion, e.EntityVersion, hash, e.Status, e.CreatedAt.Format(time.RFC3339Nano), e.UpdatedAt.Format(time.RFC3339Nano))
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return domain.RevisionSummary{}, ErrDuplicateKey
			}
			return domain.RevisionSummary{}, err
		}
	} else {
		result, x := tx.ExecContext(ctx, `UPDATE working_entities SET entity_key=?,schema_version=?,entity_version=?,blob_hash=?,status=?,updated_at=? WHERE id=?`, e.Key, e.SchemaVersion, e.EntityVersion, hash, e.Status, e.UpdatedAt.Format(time.RFC3339Nano), e.ID)
		if x != nil {
			return domain.RevisionSummary{}, x
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return domain.RevisionSummary{}, ErrNotFound
		}
	}
	if err = s.inject("working"); err != nil {
		return domain.RevisionSummary{}, err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM entity_references WHERE source_entity_id=?", e.ID); err != nil {
		return domain.RevisionSummary{}, err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM entity_tags WHERE entity_id=?", e.ID); err != nil {
		return domain.RevisionSummary{}, err
	}
	refs, tags := domain.ExtractIndexes(e)
	for _, v := range refs {
		if _, err = tx.ExecContext(ctx, "INSERT INTO entity_references(source_entity_id,field_path,ordinal,expected_kind,target_entity_id) VALUES(?,?,?,?,?)", v.SourceID, v.FieldPath, v.Ordinal, v.ExpectedKind, v.TargetID); err != nil {
			return domain.RevisionSummary{}, err
		}
	}
	for _, v := range tags {
		if _, err = tx.ExecContext(ctx, "INSERT INTO entity_tags(entity_id,tag_id) VALUES(?,?)", v.EntityID, v.TagID); err != nil {
			return domain.RevisionSummary{}, err
		}
	}
	if err = s.inject("indexes"); err != nil {
		return domain.RevisionSummary{}, err
	}
	revision, err := s.writeRevision(ctx, tx)
	if err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = s.persistRevisionDerived(ctx, tx, revision.ID); err != nil {
		return domain.RevisionSummary{}, err
	}
	if err = s.inject("derived"); err != nil {
		return domain.RevisionSummary{}, err
	}
	summary, err := s.persistLocalValidation(ctx, tx, e, revision)
	if err != nil {
		return domain.RevisionSummary{}, err
	}
	revision.Validation = &summary
	return revision, nil
}

func (s *Store) inject(stage string) error {
	if s.failStage == nil {
		return nil
	}
	return s.failStage(stage)
}
func (s *Store) writeRevision(ctx context.Context, tx *sql.Tx) (domain.RevisionSummary, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id,entity_version,status,blob_hash FROM working_entities ORDER BY id")
	if err != nil {
		return domain.RevisionSummary{}, err
	}
	defer rows.Close()
	type m struct {
		id domain.ID
		v  int64
		st domain.EntityStatus
		h  string
	}
	all := []m{}
	in := strings.Builder{}
	for rows.Next() {
		var x m
		if err = rows.Scan(&x.id, &x.v, &x.st, &x.h); err != nil {
			return domain.RevisionSummary{}, err
		}
		all = append(all, x)
		fmt.Fprintf(&in, "%s:%d:%s:%s\n", x.id, x.v, x.st, x.h)
	}
	if err = rows.Err(); err != nil {
		return domain.RevisionSummary{}, err
	}
	sum := sha256.Sum256([]byte(in.String()))
	id, err := domain.NewID()
	if err != nil {
		return domain.RevisionSummary{}, err
	}
	var display int64
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(display_revision),0)+1 FROM config_revisions").Scan(&display); err != nil {
		return domain.RevisionSummary{}, err
	}
	now := s.now().UTC()
	h := fmt.Sprintf("%x", sum)
	if _, err = tx.ExecContext(ctx, "INSERT INTO config_revisions(id,display_revision,config_hash,created_at)VALUES(?,?,?,?)", id, display, h, now.Format(time.RFC3339Nano)); err != nil {
		return domain.RevisionSummary{}, err
	}
	for _, x := range all {
		if _, err = tx.ExecContext(ctx, "INSERT INTO revision_entities(revision_id,entity_id,entity_version,status,blob_hash)VALUES(?,?,?,?,?)", id, x.id, x.v, x.st, x.h); err != nil {
			return domain.RevisionSummary{}, err
		}
	}
	if err = s.inject("revision"); err != nil {
		return domain.RevisionSummary{}, err
	}
	return domain.RevisionSummary{ID: id, DisplayRevision: display, ConfigHash: h, CreatedAt: now}, nil
}

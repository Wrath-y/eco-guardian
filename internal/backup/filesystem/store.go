// Package filesystem implements immutable backup artifacts beneath one
// server-managed root. Every operation resolves trusted relative components
// and rechecks containment and file type immediately before mutation.
package filesystem

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
)

var (
	ErrPathSecurity      = errors.New("backup path security validation failed")
	ErrArtifactNotFound  = errors.New("backup artifact not found")
	ErrArtifactDamaged   = errors.New("backup artifact is damaged")
	ErrArtifactPublished = errors.New("backup artifact is already published")
)

const (
	databaseFile     = "project.db"
	manifestFile     = "manifest.json"
	startupOrphanAge = 24 * time.Hour
)

type Store struct {
	root      string
	maxSchema int
	mu        sync.Mutex
	leases    map[domain.ID]int
	cache     map[string]inventoryCache
}

type inventoryCache struct {
	manifestSize int64
	manifestTime int64
	databaseSize int64
	databaseTime int64
	record       backupdomain.InventoryRecord
}

var _ ports.BackupArtifactStore = (*Store)(nil)
var _ ports.ManagedBackupInventory = (*Store)(nil)

func NewStore(root string, maxSchema int) (*Store, error) {
	if root == "" || !filepath.IsAbs(root) || maxSchema < 1 {
		return nil, ErrPathSecurity
	}
	clean := filepath.Clean(root)
	if clean != root {
		return nil, ErrPathSecurity
	}
	if err := os.MkdirAll(clean, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(clean, 0o700); err != nil {
		return nil, err
	}
	canonical, err := secureExistingDirectory(clean)
	if err != nil {
		return nil, ErrPathSecurity
	}
	store := &Store{root: canonical, maxSchema: maxSchema, leases: map[domain.ID]int{}, cache: map[string]inventoryCache{}}
	if err = store.cleanupStartup(context.Background(), time.Now().UTC().Add(-startupOrphanAge)); err != nil {
		return nil, err
	}
	return store, nil
}

type staging struct {
	store     *Store
	projectID domain.ID
	id        domain.ID
	path      string
	published bool
	closed    bool
}

func (value *staging) ID() domain.ID        { return value.id }
func (value *staging) DatabasePath() string { return filepath.Join(value.path, databaseFile) }

func (value *staging) WriteManifest(ctx context.Context, encoded []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if value.closed || value.published || !value.store.secureOwnedDirectory(value.projectID, value.path) {
		return ErrPathSecurity
	}
	if _, err := backupdomain.DecodeManifest(encoded); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(value.path, manifestFile), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(encoded); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

func (value *staging) Flush(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if value.closed || value.published || !value.store.secureOwnedDirectory(value.projectID, value.path) {
		return ErrPathSecurity
	}
	for _, name := range []string{databaseFile, manifestFile} {
		file, err := os.OpenFile(filepath.Join(value.path, name), os.O_RDONLY, 0)
		if err != nil {
			return err
		}
		if err = file.Sync(); err != nil {
			file.Close()
			return err
		}
		if err = file.Close(); err != nil {
			return err
		}
	}
	directory, err := os.Open(value.path)
	if err != nil {
		return err
	}
	err = directory.Sync()
	closeErr := directory.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func (value *staging) Close() error {
	value.closed = true
	return nil
}

func (store *Store) CreateStaging(ctx context.Context, projectID, backupID domain.ID) (ports.StagingArtifact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !projectID.Valid() || !backupID.Valid() {
		return nil, ErrPathSecurity
	}
	projectRoot, err := store.ensureProjectRoot(projectID)
	if err != nil {
		return nil, err
	}
	stagingRoot := filepath.Join(projectRoot, ".staging")
	path := filepath.Join(stagingRoot, string(backupID))
	if err = os.Mkdir(path, 0o700); err != nil {
		return nil, err
	}
	return &staging{store: store, projectID: projectID, id: backupID, path: path}, nil
}

func (store *Store) Publish(ctx context.Context, artifact ports.StagingArtifact, manifest backupdomain.Manifest) (backupdomain.Result, error) {
	if err := ctx.Err(); err != nil {
		return backupdomain.Result{}, err
	}
	value, ok := artifact.(*staging)
	if !ok || value.store != store || value.published || value.closed || value.id != manifest.BackupID || value.projectID != manifest.ProjectID || !manifest.Valid() || !store.secureOwnedDirectory(value.projectID, value.path) {
		return backupdomain.Result{}, ErrPathSecurity
	}
	if err := validateRegular(filepath.Join(value.path, databaseFile)); err != nil {
		return backupdomain.Result{}, err
	}
	if err := validateRegular(filepath.Join(value.path, manifestFile)); err != nil {
		return backupdomain.Result{}, err
	}
	encoded, err := os.ReadFile(filepath.Join(value.path, manifestFile))
	if err != nil {
		return backupdomain.Result{}, err
	}
	stored, err := backupdomain.DecodeManifest(encoded)
	if err != nil || stored.ManifestHash != manifest.ManifestHash || stored.BackupID != manifest.BackupID {
		return backupdomain.Result{}, ErrArtifactDamaged
	}
	name := finalName(manifest)
	projectRoot, err := store.projectRoot(value.projectID)
	if err != nil {
		return backupdomain.Result{}, err
	}
	destination := filepath.Join(projectRoot, name)
	if !contained(projectRoot, destination) {
		return backupdomain.Result{}, ErrPathSecurity
	}
	if err = os.Rename(value.path, destination); err != nil {
		return backupdomain.Result{}, err
	}
	value.published = true
	return resultFromManifest(manifest), nil
}

func (store *Store) Discard(ctx context.Context, artifact ports.StagingArtifact) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	value, ok := artifact.(*staging)
	if !ok || value.store != store || value.published || !store.secureOwnedDirectory(value.projectID, value.path) || filepath.Base(value.path) != string(value.id) {
		return ErrPathSecurity
	}
	value.closed = true
	return os.RemoveAll(value.path)
}

func (store *Store) List(ctx context.Context, query ports.InventoryQuery) (ports.InventoryPage, error) {
	if err := ctx.Err(); err != nil {
		return ports.InventoryPage{}, err
	}
	if !query.ProjectID.Valid() || query.Limit < 0 || query.Limit > 200 {
		return ports.InventoryPage{}, ErrPathSecurity
	}
	limit := query.Limit
	if limit == 0 {
		limit = 50
	}
	projectRoot, err := store.projectRoot(query.ProjectID)
	if errors.Is(err, os.ErrNotExist) {
		return ports.InventoryPage{Items: []backupdomain.InventoryRecord{}}, nil
	}
	if err != nil {
		return ports.InventoryPage{}, err
	}
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return ports.InventoryPage{}, err
	}
	records := []backupdomain.InventoryRecord{}
	for _, entry := range entries {
		if err = ctx.Err(); err != nil {
			return ports.InventoryPage{}, err
		}
		if entry.Name() == ".staging" || entry.Name() == ".trash" {
			continue
		}
		parsed, parseErr := parseFinalName(entry.Name())
		if parseErr != nil || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		record, recordErr := store.inspect(query.ProjectID, projectRoot, entry.Name(), parsed)
		if recordErr == nil {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(left, right int) bool {
		if !records[left].CreatedAt.Equal(records[right].CreatedAt) {
			return records[left].CreatedAt.After(records[right].CreatedAt)
		}
		return records[left].BackupID > records[right].BackupID
	})
	start := 0
	if query.After != "" {
		cursor, decodeErr := base64.RawURLEncoding.DecodeString(query.After)
		if decodeErr != nil {
			return ports.InventoryPage{}, ErrPathSecurity
		}
		for index := range records {
			if string(records[index].BackupID) == string(cursor) {
				start = index + 1
				break
			}
		}
	}
	end := min(start+limit, len(records))
	page := ports.InventoryPage{Items: append([]backupdomain.InventoryRecord(nil), records[start:end]...)}
	if end < len(records) && end > start {
		page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(records[end-1].BackupID))
	}
	return page, nil
}

type lease struct {
	store *Store
	id    domain.ID
	path  string
	once  sync.Once
}

func (value *lease) DatabasePath() string { return value.path }

func (value *lease) Release() error {
	value.once.Do(func() {
		value.store.mu.Lock()
		value.store.leases[value.id]--
		if value.store.leases[value.id] <= 0 {
			delete(value.store.leases, value.id)
		}
		value.store.mu.Unlock()
	})
	return nil
}

func (store *Store) Acquire(ctx context.Context, projectID, backupID domain.ID) (backupdomain.InventoryRecord, ports.ArtifactLease, error) {
	projectRoot, err := store.projectRoot(projectID)
	if err != nil {
		return backupdomain.InventoryRecord{}, nil, err
	}
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return backupdomain.InventoryRecord{}, nil, err
	}
	for _, entry := range entries {
		parsed, parseErr := parseFinalName(entry.Name())
		if parseErr != nil || parsed.id != backupID || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		record, inspectErr := store.inspect(projectID, projectRoot, entry.Name(), parsed)
		if inspectErr != nil || !record.Restorable() {
			return backupdomain.InventoryRecord{}, nil, ErrArtifactDamaged
		}
		store.mu.Lock()
		store.leases[backupID]++
		store.mu.Unlock()
		return record, &lease{store: store, id: backupID, path: filepath.Join(projectRoot, entry.Name(), databaseFile)}, nil
	}
	return backupdomain.InventoryRecord{}, nil, ErrArtifactNotFound
}

// Resolve searches only direct UUIDv7 project directories beneath the
// configured managed root. A backup identity must resolve exactly once.
func (store *Store) Resolve(ctx context.Context, backupID domain.ID) (backupdomain.InventoryRecord, ports.ArtifactLease, error) {
	if err := ctx.Err(); err != nil {
		return backupdomain.InventoryRecord{}, nil, err
	}
	if store == nil || !backupID.Valid() {
		return backupdomain.InventoryRecord{}, nil, ErrPathSecurity
	}
	entries, err := os.ReadDir(store.root)
	if err != nil {
		return backupdomain.InventoryRecord{}, nil, err
	}
	var record backupdomain.InventoryRecord
	var selected ports.ArtifactLease
	for _, entry := range entries {
		projectID := domain.ID(entry.Name())
		if !projectID.Valid() || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		candidate, lease, acquireErr := store.Acquire(ctx, projectID, backupID)
		if errors.Is(acquireErr, ErrArtifactNotFound) {
			continue
		}
		if acquireErr != nil {
			if selected != nil {
				_ = selected.Release()
			}
			return backupdomain.InventoryRecord{}, nil, acquireErr
		}
		if selected != nil {
			_ = selected.Release()
			_ = lease.Release()
			return backupdomain.InventoryRecord{}, nil, ErrArtifactDamaged
		}
		record, selected = candidate, lease
	}
	if selected == nil {
		return backupdomain.InventoryRecord{}, nil, ErrArtifactNotFound
	}
	return record, selected, nil
}

func (store *Store) ListAll(ctx context.Context, after string, limit int) (ports.InventoryPage, error) {
	if store == nil || limit < 1 || limit > 200 {
		return ports.InventoryPage{}, ErrPathSecurity
	}
	entries, err := os.ReadDir(store.root)
	if err != nil {
		return ports.InventoryPage{}, err
	}
	all := make([]backupdomain.InventoryRecord, 0)
	for _, entry := range entries {
		projectID := domain.ID(entry.Name())
		if !projectID.Valid() || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		page, listErr := store.List(ctx, ports.InventoryQuery{ProjectID: projectID, Limit: 200})
		if listErr != nil {
			continue
		}
		all = append(all, page.Items...)
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].CreatedAt.After(all[j].CreatedAt)
		}
		return all[i].BackupID > all[j].BackupID
	})
	start := 0
	if after != "" {
		decoded, decodeErr := base64.RawURLEncoding.DecodeString(after)
		if decodeErr != nil {
			return ports.InventoryPage{}, ErrPathSecurity
		}
		for i := range all {
			if string(all[i].BackupID) == string(decoded) {
				start = i + 1
				break
			}
		}
	}
	if start >= len(all) {
		return ports.InventoryPage{Items: []backupdomain.InventoryRecord{}}, nil
	}
	end := min(start+limit, len(all))
	page := ports.InventoryPage{Items: all[start:end]}
	if end < len(all) {
		page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(all[end-1].BackupID))
	}
	return page, nil
}

func (store *Store) TrashAndDelete(ctx context.Context, projectID, backupID domain.ID, manifestHash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	leased := store.leases[backupID] > 0
	store.mu.Unlock()
	if leased {
		return ErrArtifactPublished
	}
	projectRoot, err := store.projectRoot(projectID)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		parsed, parseErr := parseFinalName(entry.Name())
		if parseErr != nil || parsed.id != backupID || parsed.hash != manifestHash {
			continue
		}
		record, inspectErr := store.inspect(projectID, projectRoot, entry.Name(), parsed)
		if inspectErr != nil || record.ManifestHash != manifestHash {
			return ErrArtifactDamaged
		}
		source := filepath.Join(projectRoot, entry.Name())
		trashRoot := filepath.Join(projectRoot, ".trash")
		if !store.secureOwnedDirectory(projectID, trashRoot) {
			return ErrPathSecurity
		}
		trashID, idErr := domain.NewID()
		if idErr != nil {
			return idErr
		}
		destination := filepath.Join(trashRoot, string(trashID)+"_"+string(backupID)+"_"+manifestHash)
		if err = os.Rename(source, destination); err != nil {
			return err
		}
		if !store.secureOwnedDirectory(projectID, destination) {
			return ErrPathSecurity
		}
		return os.RemoveAll(destination)
	}
	return ErrArtifactNotFound
}

type parsedName struct {
	created time.Time
	kind    backupdomain.Type
	id      domain.ID
	hash    string
}

func finalName(manifest backupdomain.Manifest) string {
	return manifest.CreatedAt.UTC().Format("20060102T150405.000000000Z") + "_" + string(manifest.Type) + "_" + string(manifest.BackupID) + "_" + manifest.ManifestHash + ".ecobackup"
}

func parseFinalName(name string) (parsedName, error) {
	if !strings.HasSuffix(name, ".ecobackup") {
		return parsedName{}, ErrPathSecurity
	}
	parts := strings.Split(strings.TrimSuffix(name, ".ecobackup"), "_")
	if len(parts) != 4 {
		return parsedName{}, ErrPathSecurity
	}
	const timestampLayout = "20060102T150405.000000000Z"
	created, err := time.Parse(timestampLayout, parts[0])
	parsed := parsedName{created: created.UTC(), kind: backupdomain.Type(parts[1]), id: domain.ID(parts[2]), hash: parts[3]}
	if err != nil || parsed.created.Format(timestampLayout) != parts[0] || !parsed.kind.Valid() || !parsed.id.Valid() || len(parsed.hash) != 64 || strings.Trim(parsed.hash, "0123456789abcdef") != "" {
		return parsedName{}, ErrPathSecurity
	}
	return parsed, nil
}

func (store *Store) inspect(projectID domain.ID, projectRoot, name string, parsed parsedName) (backupdomain.InventoryRecord, error) {
	artifactRoot := filepath.Join(projectRoot, name)
	if !contained(projectRoot, artifactRoot) || !store.secureOwnedDirectory(projectID, artifactRoot) {
		return backupdomain.InventoryRecord{}, ErrPathSecurity
	}
	manifestPath := filepath.Join(artifactRoot, manifestFile)
	databasePath := filepath.Join(artifactRoot, databaseFile)
	manifestInfo, err := os.Lstat(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		return incompleteRecord(projectID, parsed), nil
	}
	if err != nil || !manifestInfo.Mode().IsRegular() || manifestInfo.Mode()&os.ModeSymlink != 0 {
		return backupdomain.InventoryRecord{}, ErrPathSecurity
	}
	file, err := os.Open(manifestPath)
	if err != nil {
		return backupdomain.InventoryRecord{}, err
	}
	encoded, readErr := io.ReadAll(io.LimitReader(file, backupdomain.MaxManifestBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(encoded) > backupdomain.MaxManifestBytes {
		return backupdomain.InventoryRecord{}, ErrArtifactDamaged
	}
	manifest, err := backupdomain.DecodeManifest(encoded)
	if err != nil || manifest.ProjectID != projectID || manifest.BackupID != parsed.id || manifest.ManifestHash != parsed.hash || manifest.Type != parsed.kind || !manifest.CreatedAt.Equal(parsed.created) {
		return incompleteRecord(projectID, parsed), nil
	}
	databaseInfo, err := os.Lstat(databasePath)
	if errors.Is(err, os.ErrNotExist) {
		return inventoryFromManifest(manifest, backupdomain.ValidationMissing, store.maxSchema), nil
	}
	if err != nil || !databaseInfo.Mode().IsRegular() || databaseInfo.Mode()&os.ModeSymlink != 0 {
		return backupdomain.InventoryRecord{}, ErrPathSecurity
	}
	if cached, ok := store.cachedRecord(artifactRoot, manifestInfo, databaseInfo); ok {
		return cached, nil
	}
	database, err := os.Open(databasePath)
	if err != nil {
		return backupdomain.InventoryRecord{}, err
	}
	hash := sha256.New()
	bytesRead, hashErr := io.Copy(hash, database)
	closeErr = database.Close()
	actualHash := fmt.Sprintf("%x", hash.Sum(nil))
	validation := backupdomain.ValidationValid
	if hashErr != nil || closeErr != nil || bytesRead != manifest.DBBytes || actualHash != manifest.DBSHA256 {
		validation = backupdomain.ValidationDamaged
	}
	record := inventoryFromManifest(manifest, validation, store.maxSchema)
	store.storeCachedRecord(artifactRoot, manifestInfo, databaseInfo, record)
	return record, nil
}

func incompleteRecord(projectID domain.ID, parsed parsedName) backupdomain.InventoryRecord {
	return backupdomain.InventoryRecord{BackupID: parsed.id, ProjectID: projectID, Type: parsed.kind, CreatedAt: parsed.created, ManifestHash: parsed.hash, Validation: backupdomain.ValidationIncomplete, Compatibility: backupdomain.CompatibilityUnknown, Retention: retentionDescription(parsed.kind), ResultURL: "/api/v1/backups/" + string(parsed.id)}
}

func inventoryFromManifest(manifest backupdomain.Manifest, validation backupdomain.ValidationState, maxSchema int) backupdomain.InventoryRecord {
	compatibility := backupdomain.CompatibilityCurrent
	if manifest.SchemaVersion < maxSchema {
		compatibility = backupdomain.CompatibilityOlder
	} else if manifest.SchemaVersion > maxSchema {
		compatibility = backupdomain.CompatibilityNewer
	}
	return backupdomain.InventoryRecord{BackupID: manifest.BackupID, ProjectID: manifest.ProjectID, Type: manifest.Type, CreatedAt: manifest.CreatedAt, AppVersion: manifest.AppVersion, SchemaVersion: manifest.SchemaVersion, DBBytes: manifest.DBBytes, DBSHA256: manifest.DBSHA256, ManifestHash: manifest.ManifestHash, Validation: validation, Compatibility: compatibility, Source: manifest.Source, Retention: retentionDescription(manifest.Type), ResultURL: "/api/v1/backups/" + string(manifest.BackupID)}
}

func (store *Store) cachedRecord(path string, manifest, database os.FileInfo) (backupdomain.InventoryRecord, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, ok := store.cache[path]
	return value.record, ok && value.manifestSize == manifest.Size() && value.manifestTime == manifest.ModTime().UnixNano() && value.databaseSize == database.Size() && value.databaseTime == database.ModTime().UnixNano()
}

func (store *Store) storeCachedRecord(path string, manifest, database os.FileInfo, record backupdomain.InventoryRecord) {
	store.mu.Lock()
	store.cache[path] = inventoryCache{manifestSize: manifest.Size(), manifestTime: manifest.ModTime().UnixNano(), databaseSize: database.Size(), databaseTime: database.ModTime().UnixNano(), record: record}
	store.mu.Unlock()
}

func resultFromManifest(manifest backupdomain.Manifest) backupdomain.Result {
	return backupdomain.Result{EvidenceVersion: backupdomain.EvidenceVersion, BackupID: manifest.BackupID, ProjectID: manifest.ProjectID, Type: manifest.Type, Trigger: manifest.Trigger, CreatedAt: manifest.CreatedAt, AppVersion: manifest.AppVersion, SchemaVersion: manifest.SchemaVersion, DBBytes: manifest.DBBytes, DBSHA256: manifest.DBSHA256, ManifestHash: manifest.ManifestHash, Integrity: "ok", Source: manifest.Source, ResultURL: "/api/v1/backups/" + string(manifest.BackupID)}
}

func retentionDescription(kind backupdomain.Type) string {
	switch kind {
	case backupdomain.Daily:
		return "daily count retention"
	case backupdomain.Release, backupdomain.Migration:
		return "shared release/migration count retention"
	default:
		return "not automatically pruned"
	}
}

func (store *Store) ensureProjectRoot(projectID domain.ID) (string, error) {
	path := filepath.Join(store.root, string(projectID))
	if !contained(store.root, path) {
		return "", ErrPathSecurity
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return "", err
	}
	projectRoot, err := store.projectRoot(projectID)
	if err != nil {
		return "", err
	}
	for _, name := range []string{".staging", ".trash"} {
		directory := filepath.Join(projectRoot, name)
		if err = os.MkdirAll(directory, 0o700); err != nil {
			return "", err
		}
		if err = os.Chmod(directory, 0o700); err != nil || !store.secureOwnedDirectory(projectID, directory) {
			return "", ErrPathSecurity
		}
	}
	return projectRoot, nil
}

// cleanupStartup removes only old, structurally owned work directories. Fresh
// work and any lookalike containing an unexpected entry are preserved.
func (store *Store) cleanupStartup(ctx context.Context, cutoff time.Time) error {
	projects, err := os.ReadDir(store.root)
	if err != nil {
		return err
	}
	for _, projectEntry := range projects {
		if err = ctx.Err(); err != nil {
			return err
		}
		projectID := domain.ID(projectEntry.Name())
		if !projectID.Valid() || !projectEntry.IsDir() || projectEntry.Type()&os.ModeSymlink != 0 {
			continue
		}
		projectRoot, rootErr := store.projectRoot(projectID)
		if rootErr != nil {
			continue
		}
		for _, area := range []string{".staging", ".trash"} {
			areaRoot := filepath.Join(projectRoot, area)
			entries, readErr := os.ReadDir(areaRoot)
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			if readErr != nil || !store.secureOwnedDirectory(projectID, areaRoot) {
				return errors.Join(ErrPathSecurity, readErr)
			}
			for _, entry := range entries {
				path := filepath.Join(areaRoot, entry.Name())
				info, statErr := entry.Info()
				if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.ModTime().After(cutoff) {
					continue
				}
				owned := area == ".staging" && store.ownedStaging(projectID, path, entry.Name()) || area == ".trash" && store.ownedTrash(projectID, path, entry.Name())
				if owned {
					if removeErr := os.RemoveAll(path); removeErr != nil {
						return removeErr
					}
				}
			}
		}
	}
	return nil
}

func (store *Store) ownedStaging(projectID domain.ID, path, name string) bool {
	backupID := domain.ID(name)
	if !backupID.Valid() || !store.secureOwnedDirectory(projectID, path) {
		return false
	}
	store.mu.Lock()
	leased := store.leases[backupID] > 0
	store.mu.Unlock()
	if leased {
		return false
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) > 2 {
		return false
	}
	for _, entry := range entries {
		if entry.Name() != databaseFile && entry.Name() != manifestFile || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || validateRegular(filepath.Join(path, entry.Name())) != nil {
			return false
		}
	}
	return true
}

func (store *Store) ownedTrash(projectID domain.ID, path, name string) bool {
	parts := strings.Split(name, "_")
	if len(parts) != 3 || !domain.ID(parts[0]).Valid() || !domain.ID(parts[1]).Valid() || len(parts[2]) != 64 || strings.Trim(parts[2], "0123456789abcdef") != "" || !store.secureOwnedDirectory(projectID, path) {
		return false
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 2 {
		return false
	}
	manifestBytes, err := os.ReadFile(filepath.Join(path, manifestFile))
	manifest, decodeErr := backupdomain.DecodeManifest(manifestBytes)
	return err == nil && decodeErr == nil && manifest.ProjectID == projectID && manifest.BackupID == domain.ID(parts[1]) && manifest.ManifestHash == parts[2] && validateRegular(filepath.Join(path, databaseFile)) == nil && validateRegular(filepath.Join(path, manifestFile)) == nil
}

func (store *Store) projectRoot(projectID domain.ID) (string, error) {
	if !projectID.Valid() {
		return "", ErrPathSecurity
	}
	path := filepath.Join(store.root, string(projectID))
	canonical, err := secureExistingDirectory(path)
	if err != nil {
		return "", err
	}
	if canonical != path || !contained(store.root, canonical) {
		return "", ErrPathSecurity
	}
	return canonical, nil
}

func (store *Store) secureOwnedDirectory(projectID domain.ID, path string) bool {
	projectRoot, err := store.projectRoot(projectID)
	if err != nil || !contained(projectRoot, path) {
		return false
	}
	canonical, err := secureExistingDirectory(path)
	return err == nil && canonical == path && contained(projectRoot, canonical)
}

func secureExistingDirectory(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", ErrPathSecurity
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(filepath.Clean(canonical))
	if err != nil {
		return "", err
	}
	return absolute, nil
}

func validateRegular(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return ErrPathSecurity
	}
	return validatePlatformFile(path)
}

func contained(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && !filepath.IsAbs(relative)
}

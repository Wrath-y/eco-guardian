// Package restorejournal persists the minimal crash-recovery envelope outside
// project.db, which may itself be replaced during restore.
package restorejournal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
)

var (
	ErrJournalInvalid  = errors.New("restore journal is invalid")
	ErrJournalConflict = errors.New("restore journal generation conflicts")
)

const maxJournalBytes = 64 << 10

type Store struct {
	root string
	mu   sync.Mutex
}

var _ ports.RestoreJournalStore = (*Store)(nil)

func New(root string) (*Store, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, ErrJournalInvalid
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, err
	}
	if err := restrictPath(root, true); err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrJournalInvalid
	}
	return &Store{root: root}, nil
}

func (store *Store) Load(ctx context.Context, jobID domain.ID) (ports.RestoreJournal, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.RestoreJournal{}, false, err
	}
	if store == nil || !jobID.Valid() {
		return ports.RestoreJournal{}, false, ErrJournalInvalid
	}
	return store.load(jobID)
}

func (store *Store) List(ctx context.Context) ([]ports.RestoreJournal, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if store == nil {
		return nil, ErrJournalInvalid
	}
	entries, err := os.ReadDir(store.root)
	if err != nil {
		return nil, err
	}
	result := []ports.RestoreJournal{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || filepath.Ext(name) != ".json" {
			continue
		}
		jobID := domain.ID(strings.TrimSuffix(name, ".json"))
		if !jobID.Valid() || name != string(jobID)+".json" {
			return nil, ErrJournalInvalid
		}
		journal, found, loadErr := store.load(jobID)
		if loadErr != nil || !found {
			return nil, errors.Join(ErrJournalInvalid, loadErr)
		}
		result = append(result, journal)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Job.ID < result[j].Job.ID })
	return result, nil
}

func (store *Store) load(jobID domain.ID) (ports.RestoreJournal, bool, error) {
	path := filepath.Join(store.root, string(jobID)+".json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return ports.RestoreJournal{}, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 2 || info.Size() > maxJournalBytes || info.Mode().Perm()&0o077 != 0 {
		return ports.RestoreJournal{}, false, ErrJournalInvalid
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return ports.RestoreJournal{}, false, err
	}
	journal, err := decode(encoded)
	return journal, err == nil, err
}

func (store *Store) CompareAndSwap(ctx context.Context, expected int64, next ports.RestoreJournal) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if store == nil || expected < 0 || !valid(next) || next.Generation != expected+1 {
		return false, ErrJournalInvalid
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	current, found, err := store.load(next.Job.ID)
	if err != nil {
		return false, err
	}
	if (!found && expected != 0) || found && current.Generation != expected {
		return false, ErrJournalConflict
	}
	if found && (!current.Phase.CanTransitionTo(next.Phase) || current.ProjectID != next.ProjectID || current.BackupID != next.BackupID || current.ManifestHash != next.ManifestHash || current.DatabaseHash != next.DatabaseHash || current.DatabaseBytes != next.DatabaseBytes || current.SchemaVersion != next.SchemaVersion || current.CommandHash != next.CommandHash || current.PreflightGeneration != next.PreflightGeneration || current.TargetIdentity != next.TargetIdentity || current.TargetGeneration != next.TargetGeneration || current.TargetPath != next.TargetPath || current.TargetMode != next.TargetMode || !restorePreTransitionValid(current, next) || !originalTransitionValid(current, next) || !jobAndEventsTransitionValid(current, next)) {
		return false, ErrJournalConflict
	}
	encoded, err := encode(next)
	if err != nil {
		return false, err
	}
	temporary, err := os.CreateTemp(store.root, ".restore-journal-*")
	if err != nil {
		return false, err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err = temporary.Chmod(0o600); err == nil {
		err = restrictPath(temporaryPath, false)
	}
	if err == nil {
		_, err = temporary.Write(encoded)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return false, err
	}
	path := filepath.Join(store.root, string(next.Job.ID)+".json")
	if err = atomicReplace(temporaryPath, path); err != nil {
		return false, err
	}
	committed = true
	directory, err := os.Open(store.root)
	if err == nil {
		err = directory.Sync()
		_ = directory.Close()
	}
	return true, err
}

func (store *Store) DeleteTerminal(ctx context.Context, jobID domain.ID, generation int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	journal, found, err := store.load(jobID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if journal.Generation != generation || !journal.Phase.Terminal() {
		return ErrJournalConflict
	}
	return os.Remove(filepath.Join(store.root, string(jobID)+".json"))
}

func valid(value ports.RestoreJournal) bool {
	if value.Version != backupdomain.RestoreJournalVersion || !value.Job.Valid() || value.Job.Kind != "restore" || value.Generation < 1 || value.LastEventOrdinal < 0 || !value.Phase.Valid() || value.ProjectID != value.Job.ProjectID || !value.BackupID.Valid() || !validHash(value.ManifestHash) || !validHash(value.DatabaseHash) || value.DatabaseBytes < 1 || value.SchemaVersion < 1 || !validHash(value.CommandHash) || !validHash(value.PreflightGeneration) || !value.TargetMode.Valid() || !validHash(value.TargetIdentity) || !validHash(value.TargetGeneration) || value.TargetPath == "" {
		return false
	}
	for _, path := range []string{value.TargetPath, value.StagedPath, value.OriginalPath, value.InstalledPath} {
		if path != "" && (!filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, '\x00')) {
			return false
		}
	}
	for _, identity := range []string{value.OriginalIdentity, value.StagedIdentity, value.InstalledIdentity} {
		if identity != "" && !validHash(identity) {
			return false
		}
	}
	if value.Phase.AtOrAfter(backupdomain.RestoreDatabaseStaged) && (value.StagedPath == "" || value.StagedIdentity == "") {
		return false
	}
	if value.Phase.AtOrAfter(backupdomain.RestoreOriginalParked) {
		if value.TargetMode == backupdomain.RestoreEmptySelection {
			if !value.OriginalNotApplicable || value.OriginalPath != "" || value.OriginalIdentity != "" {
				return false
			}
		} else if value.OriginalNotApplicable || value.OriginalPath == "" || value.OriginalIdentity == "" {
			return false
		}
	}
	if value.Phase.AtOrAfter(backupdomain.RestoreInstalled) && (value.InstalledPath == "" || value.InstalledIdentity == "") {
		return false
	}
	requiresRestorePre := value.Phase.AtOrAfter(backupdomain.RestorePreBackup) || value.Phase == backupdomain.RestoreSucceeded
	if value.RestorePreResult != nil {
		result := value.RestorePreResult
		if value.Phase == backupdomain.RestorePreflighted || value.Phase == backupdomain.RestoreMaintenance || !result.Valid() || result.ProjectID != value.ProjectID || result.Type != backupdomain.RestorePre || result.Source.CallerJobID != value.Job.ID || result.Source.RequestHash != value.Job.RequestHash {
			return false
		}
	} else if requiresRestorePre {
		if value.TargetMode != backupdomain.RestoreEmptySelection || !value.RestorePreNotApplicable {
			return false
		}
	}
	if value.RestorePreResult != nil && value.RestorePreNotApplicable || value.RestorePreNotApplicable && value.TargetMode != backupdomain.RestoreEmptySelection {
		return false
	}
	last := int64(0)
	for _, event := range value.Events {
		if !event.Valid() || event.JobID != value.Job.ID || event.Ordinal <= last {
			return false
		}
		last = event.Ordinal
	}
	if len(value.Events) > 0 && last > value.LastEventOrdinal {
		return false
	}
	return true
}

func sameRestorePreResult(current, next *backupdomain.Result) bool {
	if current == nil || next == nil {
		return current == nil && next == nil
	}
	return *current == *next
}

func restorePreTransitionValid(current, next ports.RestoreJournal) bool {
	if current.RestorePreResult == nil && !current.RestorePreNotApplicable && current.Phase == backupdomain.RestoreMaintenance && next.Phase == backupdomain.RestorePreBackup {
		return (next.RestorePreResult != nil) != next.RestorePreNotApplicable
	}
	return sameRestorePreResult(current.RestorePreResult, next.RestorePreResult) && current.RestorePreNotApplicable == next.RestorePreNotApplicable
}

func originalTransitionValid(current, next ports.RestoreJournal) bool {
	if current.Phase == backupdomain.RestoreDatabaseStaged && next.Phase == backupdomain.RestoreOriginalParked && next.TargetMode == backupdomain.RestoreEmptySelection {
		return !current.OriginalNotApplicable && next.OriginalNotApplicable
	}
	return current.OriginalNotApplicable == next.OriginalNotApplicable
}

func jobAndEventsTransitionValid(current, next ports.RestoreJournal) bool {
	if !current.Job.Request().Equivalent(next.Job.Request()) || current.Job.ID != next.Job.ID || current.Job.CreatedAt != next.Job.CreatedAt || next.Job.UpdatedAt.Before(current.Job.UpdatedAt) {
		return false
	}
	if current.Job.Status != next.Job.Status && !current.Job.Status.CanTransitionTo(next.Job.Status) {
		return false
	}
	if current.Job.CancelGeneration > next.Job.CancelGeneration || len(next.Events) < len(current.Events) {
		return false
	}
	for index := range current.Events {
		if current.Events[index] != next.Events[index] {
			return false
		}
	}
	return true
}

func encode(value ports.RestoreJournal) ([]byte, error) {
	if !valid(value) {
		return nil, ErrJournalInvalid
	}
	encoded, err := domain.CanonicalJSON(value)
	if err != nil || len(encoded) > maxJournalBytes {
		return nil, ErrJournalInvalid
	}
	return encoded, nil
}

func decode(encoded []byte) (ports.RestoreJournal, error) {
	if len(encoded) < 2 || len(encoded) > maxJournalBytes {
		return ports.RestoreJournal{}, ErrJournalInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var value ports.RestoreJournal
	if err := decoder.Decode(&value); err != nil {
		return ports.RestoreJournal{}, ErrJournalInvalid
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF || !valid(value) {
		return ports.RestoreJournal{}, ErrJournalInvalid
	}
	canonical, err := encode(value)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return ports.RestoreJournal{}, ErrJournalInvalid
	}
	return value, nil
}

func validHash(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
}

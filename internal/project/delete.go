package project

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
	_ "modernc.org/sqlite"
)

// deleteRecentProject removes only Eco Guardian's database files. The selected
// directory may also contain user files, which must never be removed with it.
func deleteRecentProject(ctx context.Context, info ProjectInfo, locker Locker, recent RecentProjects) error {
	directory := filepath.Clean(info.Path)
	if !filepath.IsAbs(info.Path) || filepath.Dir(directory) == directory || locker == nil {
		return ErrDeleteUnsafe
	}
	beforeDirectory, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return recent.Remove(info.ID)
	}
	if err != nil {
		return err
	}
	if !beforeDirectory.IsDir() || beforeDirectory.Mode()&os.ModeSymlink != 0 {
		return ErrDeleteUnsafe
	}
	lock, err := locker.Acquire(directory)
	if err != nil {
		return err
	}
	released := false
	defer func() {
		if !released {
			_ = lock.Release()
		}
	}()
	currentDirectory, err := os.Lstat(directory)
	if err != nil || !os.SameFile(beforeDirectory, currentDirectory) {
		return ErrDeleteUnsafe
	}

	databasePath := filepath.Join(directory, "project.db")
	beforeDatabase, err := os.Lstat(databasePath)
	missingDatabase := errors.Is(err, os.ErrNotExist)
	if err != nil && !missingDatabase {
		return err
	}
	if !missingDatabase {
		if !beforeDatabase.Mode().IsRegular() || beforeDatabase.Mode()&os.ModeSymlink != 0 {
			return ErrDeleteUnsafe
		}
		actualID, readErr := readProjectID(ctx, databasePath)
		if readErr != nil || actualID != info.ID {
			return fmt.Errorf("%w: database identity does not match recent project", ErrDeleteUnsafe)
		}
		currentDatabase, statErr := os.Lstat(databasePath)
		if statErr != nil || !os.SameFile(beforeDatabase, currentDatabase) {
			return ErrDeleteUnsafe
		}
	}

	sidecars := []string{databasePath + "-wal", databasePath + "-shm", databasePath + "-journal"}
	for _, path := range sidecars {
		entry, statErr := os.Lstat(path)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return statErr
		}
		if !entry.Mode().IsRegular() || entry.Mode()&os.ModeSymlink != 0 {
			return ErrDeleteUnsafe
		}
	}
	if !missingDatabase {
		if err := os.Remove(databasePath); err != nil {
			return err
		}
	}
	for _, path := range sidecars {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := recent.Remove(info.ID); err != nil {
		return err
	}
	released = true
	if err := lock.Release(); err != nil {
		return err
	}
	// Remove a dedicated project directory if it is now empty. A shared
	// directory and all files unrelated to the project remain untouched.
	_ = os.Remove(directory)
	return nil
}

func readProjectID(ctx context.Context, path string) (domain.ID, error) {
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := (&url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=ro"}).String()
	database, err := sql.Open("sqlite", uri)
	if err != nil {
		return "", err
	}
	defer database.Close()
	var id domain.ID
	if err := database.QueryRowContext(ctx, "SELECT id FROM project_meta").Scan(&id); err != nil {
		return "", err
	}
	if !id.Valid() {
		return "", ErrDeleteUnsafe
	}
	return id, nil
}

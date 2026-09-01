//go:build !windows

// Package securefs provides platform-specific private-file validation and
// durability operations for server-managed data.
package securefs

import (
	"errors"
	"os"
)

var ErrNotPrivate = errors.New("path is not private to the current user")

func Restrict(path string, directory bool) error {
	mode := os.FileMode(0o600)
	if directory {
		mode = 0o700
	}
	return os.Chmod(path, mode)
}

func ValidatePrivate(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || directory != info.IsDir() || !directory && !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return ErrNotPrivate
	}
	return nil
}

func SyncFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	err = file.Sync()
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

func SyncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	err = directory.Sync()
	if closeErr := directory.Close(); err == nil {
		err = closeErr
	}
	return err
}

// Package restorefs owns exact-target same-directory database replacement.
package restorefs

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
)

var (
	ErrReplacementInvalid  = errors.New("restore database replacement input is invalid")
	ErrReplacementConflict = errors.New("restore database replacement conflicts with target state")
)

type Verifier interface {
	Verify(context.Context, string, domain.ID, int) (ports.VerifiedSnapshot, error)
}

type Replacement struct{ Verifier Verifier }

var _ ports.DatabaseReplacement = Replacement{}

func (replacement Replacement) Stage(ctx context.Context, sourcePath, targetDirectory string, jobID, projectID domain.ID, schema int, expectedBytes int64, expectedHash string) (ports.RestoreFileEvidence, error) {
	if replacement.Verifier == nil || !jobID.Valid() || !projectID.Valid() || schema < 1 || expectedBytes < 1 || !validHash(expectedHash) {
		return ports.RestoreFileEvidence{}, ErrReplacementInvalid
	}
	target, err := secureDirectory(targetDirectory)
	if err != nil {
		return ports.RestoreFileEvidence{}, err
	}
	if err = validateRegular(sourcePath); err != nil {
		return ports.RestoreFileEvidence{}, err
	}
	staged := filepath.Join(target, ".eco-restore-"+string(jobID)+".staging")
	if !contained(target, staged) {
		return ports.RestoreFileEvidence{}, ErrReplacementInvalid
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return ports.RestoreFileEvidence{}, err
	}
	defer source.Close()
	destination, err := os.OpenFile(staged, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return ports.RestoreFileEvidence{}, err
	}
	complete := false
	defer func() {
		_ = destination.Close()
		if !complete {
			_ = os.Remove(staged)
		}
	}()
	digest := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(destination, digest), source)
	if copyErr == nil {
		copyErr = destination.Sync()
	}
	if closeErr := destination.Close(); copyErr == nil {
		copyErr = closeErr
	}
	actualHash := fmt.Sprintf("%x", digest.Sum(nil))
	if copyErr != nil || written != expectedBytes || actualHash != expectedHash {
		return ports.RestoreFileEvidence{}, ErrReplacementConflict
	}
	verified, err := replacement.Verifier.Verify(ctx, staged, projectID, schema)
	if err != nil || verified.Bytes != expectedBytes || verified.SHA256 != expectedHash {
		return ports.RestoreFileEvidence{}, errors.Join(ErrReplacementConflict, err)
	}
	if err = syncDirectory(target); err != nil {
		return ports.RestoreFileEvidence{}, err
	}
	complete = true
	return ports.RestoreFileEvidence{Path: staged, Bytes: written, SHA256: actualHash}, nil
}

func (replacement Replacement) ParkOriginal(ctx context.Context, targetDirectory string, jobID domain.ID) (ports.RestoreFileEvidence, error) {
	if err := ctx.Err(); err != nil {
		return ports.RestoreFileEvidence{}, err
	}
	target, err := secureDirectory(targetDirectory)
	if err != nil || !jobID.Valid() {
		return ports.RestoreFileEvidence{}, ErrReplacementInvalid
	}
	mainPath := filepath.Join(target, "project.db")
	if err = validateRegular(mainPath); err != nil {
		return ports.RestoreFileEvidence{}, err
	}
	for _, sidecar := range []string{mainPath + "-wal", mainPath + "-shm"} {
		if info, sidecarErr := os.Lstat(sidecar); sidecarErr == nil {
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return ports.RestoreFileEvidence{}, ErrReplacementConflict
			}
			if removeErr := os.Remove(sidecar); removeErr != nil {
				return ports.RestoreFileEvidence{}, removeErr
			}
		} else if !errors.Is(sidecarErr, os.ErrNotExist) {
			return ports.RestoreFileEvidence{}, sidecarErr
		}
	}
	originalHash, originalBytes, err := hashRegular(mainPath)
	if err != nil {
		return ports.RestoreFileEvidence{}, err
	}
	originalPath := filepath.Join(target, ".eco-restore-"+string(jobID)+".original")
	if _, err = os.Lstat(originalPath); !errors.Is(err, os.ErrNotExist) {
		return ports.RestoreFileEvidence{}, ErrReplacementConflict
	}
	if err = renameExact(mainPath, originalPath); err != nil {
		return ports.RestoreFileEvidence{}, err
	}
	if err = syncDirectory(target); err != nil {
		return ports.RestoreFileEvidence{}, err
	}
	return ports.RestoreFileEvidence{Path: originalPath, Bytes: originalBytes, SHA256: originalHash}, nil
}

func (replacement Replacement) Install(ctx context.Context, targetDirectory string, jobID domain.ID, staged ports.RestoreFileEvidence) (ports.RestoreFileEvidence, error) {
	return replacement.install(ctx, targetDirectory, jobID, staged, true)
}

func (replacement Replacement) InstallEmpty(ctx context.Context, targetDirectory string, jobID domain.ID, staged ports.RestoreFileEvidence) (ports.RestoreFileEvidence, error) {
	return replacement.install(ctx, targetDirectory, jobID, staged, false)
}

func (replacement Replacement) install(ctx context.Context, targetDirectory string, jobID domain.ID, staged ports.RestoreFileEvidence, requireOriginal bool) (ports.RestoreFileEvidence, error) {
	if err := ctx.Err(); err != nil {
		return ports.RestoreFileEvidence{}, err
	}
	target, err := secureDirectory(targetDirectory)
	if err != nil || !jobID.Valid() || !validHash(staged.SHA256) || staged.Bytes < 1 || filepath.Dir(staged.Path) != target || filepath.Base(staged.Path) != ".eco-restore-"+string(jobID)+".staging" {
		return ports.RestoreFileEvidence{}, ErrReplacementInvalid
	}
	mainPath := filepath.Join(target, "project.db")
	if _, statErr := os.Lstat(mainPath); !errors.Is(statErr, os.ErrNotExist) {
		return ports.RestoreFileEvidence{}, ErrReplacementConflict
	}
	originalPath := filepath.Join(target, ".eco-restore-"+string(jobID)+".original")
	if requireOriginal {
		if err = validateRegular(originalPath); err != nil {
			return ports.RestoreFileEvidence{}, err
		}
	} else if _, statErr := os.Lstat(originalPath); !errors.Is(statErr, os.ErrNotExist) {
		return ports.RestoreFileEvidence{}, ErrReplacementConflict
	}
	stagedHash, stagedBytes, err := hashRegular(staged.Path)
	if err != nil || stagedHash != staged.SHA256 || stagedBytes != staged.Bytes {
		return ports.RestoreFileEvidence{}, errors.Join(ErrReplacementConflict, err)
	}
	if err = renameExact(staged.Path, mainPath); err != nil {
		return ports.RestoreFileEvidence{}, err
	}
	if err = syncDirectory(target); err != nil {
		return ports.RestoreFileEvidence{}, err
	}
	return ports.RestoreFileEvidence{Path: mainPath, Bytes: staged.Bytes, SHA256: staged.SHA256}, nil
}

func (replacement Replacement) DiscardStaged(ctx context.Context, targetDirectory string, jobID domain.ID, staged ports.RestoreFileEvidence) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := secureDirectory(targetDirectory)
	expected := filepath.Join(target, ".eco-restore-"+string(jobID)+".staging")
	if err != nil || !jobID.Valid() || staged.Path != expected || !validHash(staged.SHA256) || staged.Bytes < 1 {
		return ErrReplacementInvalid
	}
	hash, size, err := hashRegular(staged.Path)
	if err != nil || hash != staged.SHA256 || size != staged.Bytes {
		return errors.Join(ErrReplacementConflict, err)
	}
	if err = os.Remove(staged.Path); err != nil {
		return err
	}
	return syncDirectory(target)
}

func (replacement Replacement) VerifyInstalled(ctx context.Context, targetDirectory string, projectID domain.ID, schema int, expectedBytes int64, expectedHash string) (ports.RestoreFileEvidence, error) {
	target, err := secureDirectory(targetDirectory)
	if err != nil {
		return ports.RestoreFileEvidence{}, err
	}
	path := filepath.Join(target, "project.db")
	verified, err := replacement.Verifier.Verify(ctx, path, projectID, schema)
	if err != nil || verified.Bytes != expectedBytes || verified.SHA256 != expectedHash {
		return ports.RestoreFileEvidence{}, errors.Join(ErrReplacementConflict, err)
	}
	for _, sidecar := range []string{path + "-wal", path + "-shm"} {
		if _, sidecarErr := os.Lstat(sidecar); !errors.Is(sidecarErr, os.ErrNotExist) {
			return ports.RestoreFileEvidence{}, ErrReplacementConflict
		}
	}
	return ports.RestoreFileEvidence{Path: path, Bytes: verified.Bytes, SHA256: verified.SHA256}, nil
}

func (replacement Replacement) Rollback(ctx context.Context, targetDirectory string, jobID domain.ID, original ports.RestoreFileEvidence) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := secureDirectory(targetDirectory)
	if err != nil || !jobID.Valid() || original.Path != filepath.Join(target, ".eco-restore-"+string(jobID)+".original") || !validHash(original.SHA256) {
		return ErrReplacementInvalid
	}
	hash, size, err := hashRegular(original.Path)
	if err != nil || hash != original.SHA256 || size != original.Bytes {
		return ErrReplacementConflict
	}
	mainPath := filepath.Join(target, "project.db")
	if _, statErr := os.Lstat(mainPath); statErr == nil {
		quarantine := filepath.Join(target, ".eco-restore-"+string(jobID)+".failed-installed")
		if err = renameExact(mainPath, quarantine); err != nil {
			return err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	if err = renameExact(original.Path, mainPath); err != nil {
		return err
	}
	return syncDirectory(target)
}

func (replacement Replacement) Cleanup(ctx context.Context, targetDirectory string, jobID domain.ID, original ports.RestoreFileEvidence) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := secureDirectory(targetDirectory)
	if err != nil || original.Path != filepath.Join(target, ".eco-restore-"+string(jobID)+".original") {
		return ErrReplacementInvalid
	}
	hash, size, err := hashRegular(original.Path)
	if err != nil || hash != original.SHA256 || size != original.Bytes {
		return ErrReplacementConflict
	}
	if err = os.Remove(original.Path); err != nil {
		return err
	}
	return syncDirectory(target)
}

func secureDirectory(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, '\x00') {
		return "", ErrReplacementInvalid
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", ErrReplacementInvalid
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Clean(resolved))
}

func validateRegular(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ErrReplacementInvalid
	}
	return nil
}

func hashRegular(path string) (string, int64, error) {
	if err := validateRegular(path); err != nil {
		return "", 0, err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	digest := sha256.New()
	size, hashErr := io.Copy(digest, file)
	closeErr := file.Close()
	return fmt.Sprintf("%x", digest.Sum(nil)), size, errors.Join(hashErr, closeErr)
}

func validHash(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
}

func contained(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && !filepath.IsAbs(relative)
}

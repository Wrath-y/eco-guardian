package filesystem

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"

	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/platform/securefs"
)

var (
	ErrRootUnwritable    = errors.New("backup root is not writable")
	ErrSpaceInsufficient = errors.New("backup storage has insufficient space")
)

type Probe struct{}

var _ ports.SpaceProbe = Probe{}

func (Probe) RequireWritable(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clean := filepath.Clean(path)
	info, err := os.Lstat(clean)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrRootUnwritable
	}
	canonical, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return ErrRootUnwritable
	}
	canonical, err = filepath.Abs(filepath.Clean(canonical))
	if err != nil || canonical == "" {
		return ErrRootUnwritable
	}
	id, err := domain.NewID()
	if err != nil {
		return ErrRootUnwritable
	}
	probePath := filepath.Join(canonical, ".write-probe-"+string(id))
	if !contained(canonical, probePath) {
		return ErrRootUnwritable
	}
	file, err := os.OpenFile(probePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return ErrRootUnwritable
	}
	if err = securefs.Restrict(probePath, false); err == nil {
		_, err = file.Write([]byte{0})
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = securefs.ValidatePrivate(probePath, false)
	}
	if err == nil {
		err = securefs.SyncFile(probePath)
	}
	if err == nil {
		err = securefs.SyncDirectory(canonical)
	}
	removeErr := os.Remove(probePath)
	if err == nil && removeErr == nil {
		err = securefs.SyncDirectory(canonical)
	}
	if err != nil || removeErr != nil {
		return ErrRootUnwritable
	}
	return nil
}

func (Probe) RequireAvailable(ctx context.Context, path string, required int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if required < 1 {
		return ErrSpaceInsufficient
	}
	margin := required / 10
	if margin > math.MaxInt64-required {
		return ErrSpaceInsufficient
	}
	available, err := availableBytes(path)
	if err != nil || available < required+margin {
		return ErrSpaceInsufficient
	}
	return nil
}

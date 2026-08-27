package filesystem

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"

	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
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
	canonical, err := secureExistingDirectory(path)
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
	if _, err = file.Write([]byte{0}); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	removeErr := os.Remove(probePath)
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

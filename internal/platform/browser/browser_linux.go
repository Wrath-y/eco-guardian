//go:build linux

package browser

import (
	"context"
	"errors"
	"os/exec"
)

// OpenBrowser uses the freedesktop launcher without a shell. Headless Linux
// hosts can disable auto-open and continue serving the loopback application.
func (Default) OpenBrowser(ctx context.Context, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateURL(value); err != nil {
		return err
	}
	launcher, err := exec.LookPath("xdg-open")
	if err != nil {
		return errors.Join(ErrUnsupported, err)
	}
	if err = exec.CommandContext(ctx, launcher, value).Run(); err != nil {
		return err
	}
	return ctx.Err()
}

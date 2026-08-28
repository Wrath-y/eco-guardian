//go:build darwin

package browser

import (
	"context"
	"os/exec"
)

// OpenBrowser delegates to LaunchServices through the macOS `open` command.
// The URL is passed as one argument after canonical validation; no shell is
// involved.
func (Default) OpenBrowser(ctx context.Context, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateURL(value); err != nil {
		return err
	}
	if err := exec.CommandContext(ctx, "/usr/bin/open", value).Run(); err != nil {
		return err
	}
	return ctx.Err()
}

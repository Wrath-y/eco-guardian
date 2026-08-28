//go:build darwin

package project

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
)

// NativeDirectorySelector opens the macOS folder chooser through
// LaunchServices' AppleScript bridge. The prompt is supplied as an argv value
// rather than interpolated into the script, so titles cannot inject code.
type NativeDirectorySelector struct{ Title string }

func (s NativeDirectorySelector) SelectDirectory(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	title := s.Title
	if title == "" {
		title = "Select Eco Guardian project folder"
	}
	script := `on run argv
set selectedFolder to choose folder with prompt (item 1 of argv)
return POSIX path of selectedFolder
end run`
	output, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script, title).Output()
	if err != nil {
		return "", err
	}
	path := strings.TrimSuffix(strings.TrimSuffix(string(output), "\n"), "\r")
	if path == "" {
		return "", errors.New("directory selection returned an empty path")
	}
	return filepath.Abs(filepath.Clean(path))
}

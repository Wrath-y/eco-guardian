//go:build linux

package project

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
)

// NativeDirectorySelector uses an installed freedesktop dialog helper. The
// title is passed as one argv value, never interpreted by a shell.
type NativeDirectorySelector struct{ Title string }

func (s NativeDirectorySelector) SelectDirectory(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	title := s.Title
	if title == "" {
		title = "Select Eco Guardian project folder"
	}
	var command *exec.Cmd
	if launcher, err := exec.LookPath("zenity"); err == nil {
		command = exec.CommandContext(ctx, launcher, "--file-selection", "--directory", "--title="+title)
	} else if launcher, err := exec.LookPath("kdialog"); err == nil {
		command = exec.CommandContext(ctx, launcher, "--getexistingdirectory", ".", "--title", title)
	} else {
		return "", errors.New("no Linux directory chooser is installed")
	}
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	path := strings.TrimSuffix(strings.TrimSuffix(string(output), "\n"), "\r")
	if path == "" {
		return "", errors.New("directory selection returned an empty path")
	}
	return filepath.Abs(filepath.Clean(path))
}

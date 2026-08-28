//go:build !darwin

package project

import (
	"context"
	"path/filepath"

	"github.com/sqweek/dialog"
)

// NativeDirectorySelector opens the OS-provided directory dialog on macOS and
// Windows. The selected path stays process-local in the token store.
type NativeDirectorySelector struct{ Title string }

func (s NativeDirectorySelector) SelectDirectory(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	title := s.Title
	if title == "" {
		title = "Select Eco Guardian project folder"
	}
	path, err := dialog.Directory().Title(title).Browse()
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Clean(path))
}

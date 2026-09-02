//go:build !windows && !darwin && !linux

package project

import (
	"context"
	"errors"
)

type NativeDirectorySelector struct{ Title string }

func (NativeDirectorySelector) SelectDirectory(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", errors.New("native directory selection is unsupported")
}

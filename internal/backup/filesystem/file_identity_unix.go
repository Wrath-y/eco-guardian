//go:build !windows

package filesystem

import (
	"os"
	"syscall"
)

func validatePlatformFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		return ErrPathSecurity
	}
	return nil
}

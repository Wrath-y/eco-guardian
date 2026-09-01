//go:build !windows

package filesystem

import "os"

func renameDurable(source, destination string) error {
	return os.Rename(source, destination)
}

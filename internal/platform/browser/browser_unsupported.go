//go:build !windows

package browser

import "context"

func (Default) OpenBrowser(ctx context.Context, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateURL(value); err != nil {
		return err
	}
	return ErrUnsupported
}

//go:build !windows

package credential

import (
	"context"

	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
)

type WindowsStore struct{}

func NewWindowsStore() *WindowsStore { return &WindowsStore{} }

func (*WindowsStore) Put(context.Context, string, []byte) error { return ErrUnsupported }
func (*WindowsStore) Get(context.Context, string) ([]byte, error) {
	return nil, aiprovider.ErrCredentialNotFound
}
func (*WindowsStore) Delete(context.Context, string) error { return ErrUnsupported }

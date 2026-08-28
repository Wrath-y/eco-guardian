//go:build darwin && !cgo

package credential

import (
	"context"

	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
)

// A macOS build without cgo cannot link the Security framework. Keep the
// environment fallback available and fail closed for persistent credentials.
type DarwinStore struct{}

func NewDarwinStore() *DarwinStore  { return &DarwinStore{} }
func NewDefaultStore() *DarwinStore { return NewDarwinStore() }
func NewWindowsStore() *DarwinStore { return NewDarwinStore() }

func (*DarwinStore) Put(context.Context, string, []byte) error { return ErrUnsupported }
func (*DarwinStore) Get(context.Context, string) ([]byte, error) {
	return nil, aiprovider.ErrCredentialNotFound
}
func (*DarwinStore) Delete(context.Context, string) error { return ErrUnsupported }

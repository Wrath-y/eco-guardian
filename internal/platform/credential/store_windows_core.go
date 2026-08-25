package credential

import (
	"context"
	"errors"

	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
)

var errWindowsCredentialNotFound = errors.New("Windows credential not found")

// windowsCredentialAPI is the synchronous policy boundary around advapi32.
// Keeping target/value validation and memory ownership here lets the adapter
// behavior run under deterministic tests on every development platform while
// store_windows.go remains the minimal Windows syscall binding.
type windowsCredentialAPI interface {
	Put(target string, value []byte) error
	Get(target string) ([]byte, error)
	Delete(target string) error
}

type WindowsStore struct{ api windowsCredentialAPI }

func newWindowsStore(api windowsCredentialAPI) *WindowsStore { return &WindowsStore{api: api} }

func (store *WindowsStore) Put(ctx context.Context, provider string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := targetName(provider)
	if err != nil || len(value) == 0 || len(value) > aiprovider.MaxCredentialBytes || store == nil || store.api == nil {
		if store == nil || store.api == nil {
			return ErrUnsupported
		}
		return aiprovider.ErrCredentialInvalid
	}
	copyValue := append([]byte(nil), value...)
	defer clear(copyValue)
	return store.api.Put(target, copyValue)
}

func (store *WindowsStore) Get(ctx context.Context, provider string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target, err := targetName(provider)
	if err != nil {
		return nil, aiprovider.ErrCredentialInvalid
	}
	if store == nil || store.api == nil {
		return nil, aiprovider.ErrCredentialNotFound
	}
	raw, err := store.api.Get(target)
	if errors.Is(err, errWindowsCredentialNotFound) {
		return nil, aiprovider.ErrCredentialNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > aiprovider.MaxCredentialBytes {
		clear(raw)
		return nil, aiprovider.ErrCredentialNotFound
	}
	value := append([]byte(nil), raw...)
	clear(raw)
	return value, nil
}

func (store *WindowsStore) Delete(ctx context.Context, provider string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := targetName(provider)
	if err != nil {
		return aiprovider.ErrCredentialInvalid
	}
	if store == nil || store.api == nil {
		return ErrUnsupported
	}
	if err = store.api.Delete(target); errors.Is(err, errWindowsCredentialNotFound) {
		return aiprovider.ErrCredentialNotFound
	}
	return err
}

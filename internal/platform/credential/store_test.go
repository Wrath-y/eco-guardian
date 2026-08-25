package credential

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
)

type fakeWindowsCredentialAPI struct {
	values       map[string][]byte
	putTarget    string
	putBuffer    []byte
	getTarget    string
	getBuffer    []byte
	deleteTarget string
	putErr       error
	getErr       error
	deleteErr    error
}

func (fake *fakeWindowsCredentialAPI) Put(target string, value []byte) error {
	fake.putTarget, fake.putBuffer = target, value
	if fake.putErr == nil {
		if fake.values == nil {
			fake.values = map[string][]byte{}
		}
		fake.values[target] = append([]byte(nil), value...)
	}
	return fake.putErr
}

func (fake *fakeWindowsCredentialAPI) Get(target string) ([]byte, error) {
	fake.getTarget = target
	if fake.getErr != nil {
		return nil, fake.getErr
	}
	value, found := fake.values[target]
	if !found {
		return nil, errWindowsCredentialNotFound
	}
	fake.getBuffer = append([]byte(nil), value...)
	return fake.getBuffer, nil
}

func (fake *fakeWindowsCredentialAPI) Delete(target string) error {
	fake.deleteTarget = target
	if fake.deleteErr != nil {
		return fake.deleteErr
	}
	if _, found := fake.values[target]; !found {
		return errWindowsCredentialNotFound
	}
	delete(fake.values, target)
	return nil
}

func TestCredentialTargetIsStableAndRejectsPathInjection(t *testing.T) {
	target, err := targetName("openai-compatible")
	if err != nil || target != "EcoGuardian/AI/openai-compatible" {
		t.Fatalf("target=%q err=%v", target, err)
	}
	for _, provider := range []string{"", "../secret", "bad\\name", "bad\nname"} {
		if _, err := targetName(provider); err == nil {
			t.Fatalf("provider %q was accepted", provider)
		}
	}
}

func TestWindowsCredentialStorePutGetDeletePolicyAndMemoryOwnership(t *testing.T) {
	fake := &fakeWindowsCredentialAPI{}
	store := newWindowsStore(fake)
	original := []byte("credential-canary")
	if err := store.Put(context.Background(), aiprovider.OpenAICompatibleProvider, original); err != nil {
		t.Fatal(err)
	}
	if fake.putTarget != "EcoGuardian/AI/openai-compatible" || !bytes.Equal(original, []byte("credential-canary")) || !allZero(fake.putBuffer) {
		t.Fatalf("put target=%q original=%q cleared=%v", fake.putTarget, original, allZero(fake.putBuffer))
	}
	value, err := store.Get(context.Background(), aiprovider.OpenAICompatibleProvider)
	if err != nil || !bytes.Equal(value, original) || fake.getTarget != fake.putTarget || !allZero(fake.getBuffer) {
		t.Fatalf("get target=%q value=%q buffer_cleared=%v err=%v", fake.getTarget, value, allZero(fake.getBuffer), err)
	}
	value[0] = 'X'
	second, err := store.Get(context.Background(), aiprovider.OpenAICompatibleProvider)
	if err != nil || !bytes.Equal(second, original) {
		t.Fatalf("returned credential aliases native storage: %q err=%v", second, err)
	}
	if err = store.Delete(context.Background(), aiprovider.OpenAICompatibleProvider); err != nil || fake.deleteTarget != fake.putTarget {
		t.Fatalf("delete target=%q err=%v", fake.deleteTarget, err)
	}
	if _, err = store.Get(context.Background(), aiprovider.OpenAICompatibleProvider); !errors.Is(err, aiprovider.ErrCredentialNotFound) {
		t.Fatalf("missing get error=%v", err)
	}
	if err = store.Delete(context.Background(), aiprovider.OpenAICompatibleProvider); !errors.Is(err, aiprovider.ErrCredentialNotFound) {
		t.Fatalf("missing delete error=%v", err)
	}
}

func TestWindowsCredentialStoreRejectsInvalidInputAndMapsNativeErrors(t *testing.T) {
	fake := &fakeWindowsCredentialAPI{}
	store := newWindowsStore(fake)
	for _, test := range []struct {
		provider string
		value    []byte
	}{
		{"", []byte("value")},
		{"../provider", []byte("value")},
		{aiprovider.OpenAICompatibleProvider, nil},
		{aiprovider.OpenAICompatibleProvider, []byte(strings.Repeat("x", aiprovider.MaxCredentialBytes+1))},
	} {
		if err := store.Put(context.Background(), test.provider, test.value); !errors.Is(err, aiprovider.ErrCredentialInvalid) {
			t.Errorf("provider=%q bytes=%d err=%v", test.provider, len(test.value), err)
		}
	}
	if fake.putTarget != "" {
		t.Fatalf("invalid input reached native API: %q", fake.putTarget)
	}
	nativeErr := errors.New("advapi32 failure")
	fake.putErr = nativeErr
	if err := store.Put(context.Background(), aiprovider.OpenAICompatibleProvider, []byte("value")); !errors.Is(err, nativeErr) {
		t.Fatalf("put native error=%v", err)
	}
	fake.getErr = nativeErr
	if _, err := store.Get(context.Background(), aiprovider.OpenAICompatibleProvider); !errors.Is(err, nativeErr) {
		t.Fatalf("get native error=%v", err)
	}
	fake.deleteErr = nativeErr
	if err := store.Delete(context.Background(), aiprovider.OpenAICompatibleProvider); !errors.Is(err, nativeErr) {
		t.Fatalf("delete native error=%v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Put(canceled, aiprovider.OpenAICompatibleProvider, []byte("value")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled put error=%v", err)
	}
}

func TestUnsupportedWindowsCredentialStorePreservesPublicContract(t *testing.T) {
	store := newWindowsStore(nil)
	if err := store.Put(context.Background(), aiprovider.OpenAICompatibleProvider, []byte("value")); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported put error=%v", err)
	}
	if _, err := store.Get(context.Background(), aiprovider.OpenAICompatibleProvider); !errors.Is(err, aiprovider.ErrCredentialNotFound) {
		t.Fatalf("unsupported get error=%v", err)
	}
	if err := store.Delete(context.Background(), aiprovider.OpenAICompatibleProvider); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported delete error=%v", err)
	}
}

func allZero(value []byte) bool {
	for _, current := range value {
		if current != 0 {
			return false
		}
	}
	return true
}

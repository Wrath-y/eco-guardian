package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
)

const (
	OpenAICompatibleProvider = "openai-compatible"
	OpenAIAPIKeyEnvironment  = "ECO_GUARDIAN_OPENAI_API_KEY"
	MaxCredentialBytes       = 2560
)

var (
	ErrCredentialNotFound = errors.New("provider credential not found")
	ErrCredentialInvalid  = errors.New("provider credential is invalid")
	ErrCredentialStore    = errors.New("provider credential store failed")
)

type CredentialStore interface {
	Put(context.Context, string, []byte) error
	Get(context.Context, string) ([]byte, error)
	Delete(context.Context, string) error
}

type Environment interface {
	LookupEnv(string) (string, bool)
}

// OSEnvironment reads process environment values without persisting them.
type OSEnvironment struct{}

func (OSEnvironment) LookupEnv(name string) (string, bool) { return os.LookupEnv(name) }

type CredentialSource string

const (
	CredentialManager     CredentialSource = "credential_manager"
	CredentialEnvironment CredentialSource = "environment"
)

type Secret struct {
	value  []byte
	source CredentialSource
}

func (s Secret) Source() CredentialSource { return s.source }
func (s Secret) Present() bool            { return len(s.value) != 0 }
func (s Secret) Reveal() []byte           { return append([]byte(nil), s.value...) }
func (s Secret) String() string           { return "[REDACTED]" }
func (s Secret) GoString() string         { return "provider.Secret{[REDACTED]}" }

type CredentialResolver struct {
	Store       CredentialStore
	Environment Environment
}

func (r CredentialResolver) Resolve(ctx context.Context, provider string) (Secret, error) {
	if provider != OpenAICompatibleProvider {
		return Secret{}, ErrCredentialInvalid
	}
	if r.Store != nil {
		value, err := r.Store.Get(ctx, provider)
		if err == nil {
			if err := validateCredential(value); err != nil {
				return Secret{}, err
			}
			return Secret{value: append([]byte(nil), value...), source: CredentialManager}, nil
		}
		if !errors.Is(err, ErrCredentialNotFound) {
			return Secret{}, fmt.Errorf("%w: %v", ErrCredentialStore, err)
		}
	}
	if r.Environment != nil {
		if value, ok := r.Environment.LookupEnv(environmentName(provider)); ok && value != "" {
			bytes := []byte(value)
			if err := validateCredential(bytes); err != nil {
				return Secret{}, err
			}
			return Secret{value: append([]byte(nil), bytes...), source: CredentialEnvironment}, nil
		}
	}
	return Secret{}, ErrCredentialNotFound
}

func (r CredentialResolver) Put(ctx context.Context, provider string, value []byte) error {
	if provider != OpenAICompatibleProvider || r.Store == nil {
		return ErrCredentialInvalid
	}
	if err := validateCredential(value); err != nil {
		return err
	}
	copyValue := append([]byte(nil), value...)
	defer zero(copyValue)
	if err := r.Store.Put(ctx, provider, copyValue); err != nil {
		return fmt.Errorf("%w: %v", ErrCredentialStore, err)
	}
	return nil
}

func (r CredentialResolver) Delete(ctx context.Context, provider string) error {
	if provider != OpenAICompatibleProvider || r.Store == nil {
		return ErrCredentialInvalid
	}
	if err := r.Store.Delete(ctx, provider); err != nil && !errors.Is(err, ErrCredentialNotFound) {
		return fmt.Errorf("%w: %v", ErrCredentialStore, err)
	}
	return nil
}

func environmentName(provider string) string {
	if provider == OpenAICompatibleProvider {
		return OpenAIAPIKeyEnvironment
	}
	return ""
}

func validateCredential(value []byte) error {
	if len(value) == 0 || len(value) > MaxCredentialBytes {
		return ErrCredentialInvalid
	}
	for _, current := range value {
		if current == 0 || current == '\r' || current == '\n' {
			return ErrCredentialInvalid
		}
	}
	return nil
}

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

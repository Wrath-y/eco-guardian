package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
)

type memoryCredentialStore struct{ values map[string][]byte }

func (s *memoryCredentialStore) Put(_ context.Context, provider string, value []byte) error {
	if s.values == nil {
		s.values = map[string][]byte{}
	}
	s.values[provider] = append([]byte(nil), value...)
	return nil
}
func (s *memoryCredentialStore) Get(_ context.Context, provider string) ([]byte, error) {
	value, ok := s.values[provider]
	if !ok {
		return nil, ErrCredentialNotFound
	}
	return append([]byte(nil), value...), nil
}
func (s *memoryCredentialStore) Delete(_ context.Context, provider string) error {
	if _, ok := s.values[provider]; !ok {
		return ErrCredentialNotFound
	}
	delete(s.values, provider)
	return nil
}

type environmentMap map[string]string

func (e environmentMap) LookupEnv(name string) (string, bool) { value, ok := e[name]; return value, ok }

func TestCredentialManagerPrecedesNonPersistentEnvironmentFallback(t *testing.T) {
	store := &memoryCredentialStore{}
	resolver := CredentialResolver{Store: store, Environment: environmentMap{OpenAIAPIKeyEnvironment: "environment-canary"}}
	secret, err := resolver.Resolve(context.Background(), OpenAICompatibleProvider)
	if err != nil || secret.Source() != CredentialEnvironment || string(secret.Reveal()) != "environment-canary" {
		t.Fatalf("environment fallback=%#v err=%v", secret, err)
	}
	if err := resolver.Put(context.Background(), OpenAICompatibleProvider, []byte("manager-canary")); err != nil {
		t.Fatal(err)
	}
	secret, err = resolver.Resolve(context.Background(), OpenAICompatibleProvider)
	if err != nil || secret.Source() != CredentialManager || string(secret.Reveal()) != "manager-canary" {
		t.Fatalf("manager precedence=%#v err=%v", secret, err)
	}
	if err := resolver.Delete(context.Background(), OpenAICompatibleProvider); err != nil {
		t.Fatal(err)
	}
	secret, err = resolver.Resolve(context.Background(), OpenAICompatibleProvider)
	if err != nil || secret.Source() != CredentialEnvironment {
		t.Fatalf("fallback after delete=%#v err=%v", secret, err)
	}
}

func TestCredentialValuesAreBoundedAndRedactedByFormatting(t *testing.T) {
	resolver := CredentialResolver{Store: &memoryCredentialStore{}}
	for _, value := range [][]byte{nil, []byte("line\nbreak"), make([]byte, MaxCredentialBytes+1)} {
		if err := resolver.Put(context.Background(), OpenAICompatibleProvider, value); !errors.Is(err, ErrCredentialInvalid) {
			t.Fatalf("invalid credential error=%v", err)
		}
	}
	secret := Secret{value: []byte("format-canary"), source: CredentialManager}
	if got := fmt.Sprintf("%v %#v", secret, secret); got != "[REDACTED] provider.Secret{[REDACTED]}" {
		t.Fatalf("secret formatting leaked: %s", got)
	}
}

func TestCredentialPrecedenceFixture(t *testing.T) {
	raw, err := os.ReadFile("../../../api/fixtures/ai-credential-precedence.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Provider              string   `json:"provider"`
		EnvironmentVariable   string   `json:"environment_variable"`
		EnvironmentPersistent bool     `json:"environment_persistent"`
		Precedence            []string `json:"precedence"`
		Cases                 []struct {
			ManagerPresent     bool    `json:"credential_manager_present"`
			EnvironmentPresent bool    `json:"environment_present"`
			ResolvedSource     *string `json:"resolved_source"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Provider != OpenAICompatibleProvider || fixture.EnvironmentVariable != OpenAIAPIKeyEnvironment || fixture.EnvironmentPersistent || fmt.Sprint(fixture.Precedence) != "[credential_manager environment]" {
		t.Fatalf("invalid credential precedence fixture: %#v", fixture)
	}
	for _, test := range fixture.Cases {
		store := &memoryCredentialStore{}
		if test.ManagerPresent {
			store.values = map[string][]byte{OpenAICompatibleProvider: []byte("manager-fixture")}
		}
		environment := environmentMap{}
		if test.EnvironmentPresent {
			environment[OpenAIAPIKeyEnvironment] = "environment-fixture"
		}
		secret, resolveErr := (CredentialResolver{Store: store, Environment: environment}).Resolve(context.Background(), fixture.Provider)
		if test.ResolvedSource == nil {
			if !errors.Is(resolveErr, ErrCredentialNotFound) {
				t.Fatalf("missing fixture credential error=%v", resolveErr)
			}
			continue
		}
		if resolveErr != nil || string(secret.Source()) != *test.ResolvedSource {
			t.Fatalf("fixture source=%q want=%q err=%v", secret.Source(), *test.ResolvedSource, resolveErr)
		}
	}
}

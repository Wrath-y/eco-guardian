package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
)

type resolutionEnvironment map[string]string

func (e resolutionEnvironment) LookupEnv(name string) (string, bool) {
	value, ok := e[name]
	return value, ok
}

type changingConfigSource struct {
	configuration aiprovider.Configuration
	credential    aiprovider.Secret
	calls         int
}

func (s *changingConfigSource) Resolve(context.Context) (aiprovider.Configuration, aiprovider.Secret, error) {
	s.calls++
	return s.configuration, s.credential, nil
}

type manifestStoreFake struct {
	values   []aiprovider.AttemptManifest
	err      error
	onInsert func()
}

func (s *manifestStoreFake) Insert(_ context.Context, manifest aiprovider.AttemptManifest) error {
	if s.err != nil {
		return s.err
	}
	s.values = append(s.values, manifest)
	if s.onInsert != nil {
		s.onInsert()
	}
	return nil
}

func resolutionSecret(t *testing.T) aiprovider.Secret {
	t.Helper()
	secret, err := (aiprovider.CredentialResolver{Environment: resolutionEnvironment{
		aiprovider.OpenAIAPIKeyEnvironment: "resolution-secret-canary",
	}}).Resolve(context.Background(), aiprovider.OpenAICompatibleProvider)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func resolutionInput(id string) AttemptResolutionInput {
	return AttemptResolutionInput{
		AttemptID: aicontract.AttemptID(id), InputHash: aicontract.Hash(strings.Repeat("b", 64)),
		EvidenceManifestHash: aicontract.Hash(strings.Repeat("c", 64)), CancelGeneration: 4,
	}
}

func TestAttemptConfigurationIsResolvedOnceAndPersistedBeforeInvocation(t *testing.T) {
	source := &changingConfigSource{configuration: aiprovider.Configuration{
		Enabled: true, Endpoint: "http://127.0.0.1:11434/v1", Model: "model-a", Timeout: time.Second,
	}, credential: resolutionSecret(t)}
	store := &manifestStoreFake{}
	store.onInsert = func() {
		source.configuration.Endpoint = "http://127.0.0.1:22434/v1"
		source.configuration.Model = "model-b"
	}
	resolver := AttemptResolver{Source: source, Store: store}
	invoked := 0
	var first ResolvedAttempt
	if err := resolver.ResolveAndInvoke(context.Background(), resolutionInput("attempt-1"), func(resolved ResolvedAttempt) error {
		invoked++
		first = resolved
		if len(store.values) != 1 {
			t.Fatal("attempt invoked before its manifest was persisted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if source.calls != 1 || invoked != 1 || first.Configuration.Model != "model-a" || first.Configuration.Endpoint != "http://127.0.0.1:11434/v1" || first.Manifest.Model.ID != "model-a" {
		t.Fatalf("first resolution=%#v source_calls=%d invoked=%d", first, source.calls, invoked)
	}
	if err := resolver.ResolveAndInvoke(context.Background(), resolutionInput("attempt-2"), func(resolved ResolvedAttempt) error {
		if resolved.Configuration.Model != "model-b" || resolved.Manifest.Model.ID != "model-b" || resolved.Manifest.Provider.Hash == first.Manifest.Provider.Hash {
			t.Fatalf("later attempt did not observe changed settings: %#v", resolved)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if source.calls != 2 || len(store.values) != 2 {
		t.Fatalf("source_calls=%d manifests=%d", source.calls, len(store.values))
	}
	encoded, err := json.Marshal(store.values)
	if err != nil || strings.Contains(string(encoded), "resolution-secret-canary") || strings.Contains(strings.ToLower(string(encoded)), "credential") {
		t.Fatalf("persisted manifest leaked credential: %s err=%v", encoded, err)
	}
}

func TestAttemptPersistenceFailurePreventsInvocation(t *testing.T) {
	source := &changingConfigSource{configuration: aiprovider.Configuration{
		Enabled: true, Endpoint: "http://127.0.0.1:11434/v1", Model: "model-a", Timeout: time.Second,
	}, credential: resolutionSecret(t)}
	resolver := AttemptResolver{Source: source, Store: &manifestStoreFake{err: errors.New("storage failed")}}
	invoked := false
	err := resolver.ResolveAndInvoke(context.Background(), resolutionInput("attempt-1"), func(ResolvedAttempt) error {
		invoked = true
		return nil
	})
	if !errors.Is(err, ErrAttemptPersistence) || invoked {
		t.Fatalf("err=%v invoked=%v", err, invoked)
	}
}

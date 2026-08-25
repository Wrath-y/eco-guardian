package orchestration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
)

var (
	ErrAttemptConfiguration = errors.New("AI attempt configuration is invalid")
	ErrAttemptPersistence   = errors.New("AI attempt manifest could not be persisted")
)

type AttemptConfigSource interface {
	Resolve(context.Context) (aiprovider.Configuration, aiprovider.Secret, error)
}

type AttemptManifestStore interface {
	Insert(context.Context, aiprovider.AttemptManifest) error
}

type AttemptResolutionInput struct {
	AttemptID            aicontract.AttemptID
	InputHash            aicontract.Hash
	EvidenceManifestHash aicontract.Hash
	CancelGeneration     uint64
}

func (v AttemptResolutionInput) Valid() bool {
	return v.AttemptID.Valid() && v.InputHash.Valid() && v.EvidenceManifestHash.Valid()
}

type ResolvedAttempt struct {
	Configuration aiprovider.Configuration
	Credential    aiprovider.Secret
	Manifest      aiprovider.AttemptManifest
	Prompt        aicontract.PromptManifest
	PatchSchema   aicontract.SchemaManifest
	Tools         []aicontract.ToolManifest
	Budget        aicontract.BudgetPolicyManifest
	Orchestrator  aicontract.OrchestratorManifest
}

type AttemptResolver struct {
	Source AttemptConfigSource
	Store  AttemptManifestStore
}

// ResolveAndInvoke snapshots configuration exactly once, persists only safe
// identities, and invokes the callback only after persistence succeeds.
func (r AttemptResolver) ResolveAndInvoke(ctx context.Context, input AttemptResolutionInput, invoke func(ResolvedAttempt) error) error {
	if r.Source == nil || r.Store == nil || invoke == nil || !input.Valid() {
		return ErrAttemptConfiguration
	}
	configuration, credential, err := r.Source.Resolve(ctx)
	if err != nil || !configuration.Enabled || !credential.Present() || strings.TrimSpace(configuration.Model) == "" || configuration.Timeout <= 0 {
		return ErrAttemptConfiguration
	}
	classification, err := aiprovider.ValidateEndpoint(configuration.Endpoint, configuration.AllowCloud)
	if err != nil {
		return ErrAttemptConfiguration
	}
	fixture := aicontract.V1Fixture()
	providerIdentity := configuredIdentity("openai-compatible", "v1", string(classification), configuration.Endpoint)
	modelIdentity := configuredIdentity(configuration.Model, "configured", string(providerIdentity.Hash), configuration.Model)
	toolIdentities := make([]aicontract.VersionIdentity, len(fixture.Tools))
	for index, tool := range fixture.Tools {
		toolIdentities[index] = tool.Identity
	}
	manifest := aiprovider.AttemptManifest{
		AttemptID: input.AttemptID, Provider: providerIdentity, Model: modelIdentity,
		EndpointClassification: classification, Prompt: fixture.Prompt.Identity, StructuredResponseSchema: fixture.PatchSchema.Identity,
		Tools: toolIdentities, Orchestrator: fixture.Orchestrator.Identity, Budget: fixture.Budget.Identity,
		InputHash: input.InputHash, EvidenceManifestHash: input.EvidenceManifestHash, CancelGeneration: input.CancelGeneration,
	}
	if !manifest.Valid() {
		return ErrAttemptConfiguration
	}
	if err := r.Store.Insert(ctx, manifest); err != nil {
		return ErrAttemptPersistence
	}
	resolved := ResolvedAttempt{
		Configuration: configuration, Credential: credential, Manifest: cloneAttemptManifest(manifest),
		Prompt: fixture.Prompt, PatchSchema: cloneSchema(fixture.PatchSchema), Tools: cloneTools(fixture.Tools),
		Budget: fixture.Budget, Orchestrator: fixture.Orchestrator,
	}
	return invoke(resolved)
}

func configuredIdentity(id, version string, fields ...string) aicontract.VersionIdentity {
	hash := sha256.New()
	_, _ = hash.Write([]byte("eco-guardian.ai-provider-config/v1\x00"))
	for _, field := range fields {
		_, _ = hash.Write([]byte{byte(len(field) >> 24), byte(len(field) >> 16), byte(len(field) >> 8), byte(len(field))})
		_, _ = hash.Write([]byte(field))
	}
	return aicontract.VersionIdentity{ID: id, Version: version, Hash: aicontract.Hash(hex.EncodeToString(hash.Sum(nil)))}
}

func cloneAttemptManifest(value aiprovider.AttemptManifest) aiprovider.AttemptManifest {
	value.Tools = append([]aicontract.VersionIdentity(nil), value.Tools...)
	return value
}

func cloneSchema(value aicontract.SchemaManifest) aicontract.SchemaManifest {
	value.Schema = append([]byte(nil), value.Schema...)
	return value
}

func cloneTools(values []aicontract.ToolManifest) []aicontract.ToolManifest {
	result := append([]aicontract.ToolManifest(nil), values...)
	for index := range result {
		result[index].RequiredIdentities = append([]string(nil), result[index].RequiredIdentities...)
	}
	return result
}

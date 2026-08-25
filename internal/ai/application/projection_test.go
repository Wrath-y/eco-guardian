package application

import (
	"context"
	"reflect"
	"testing"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
)

type projectionRepositoryFake struct{ stored StoredPatchProjection }

func (fake *projectionRepositoryFake) ReadStoredPatchProjection(context.Context, aicontract.PatchID) (StoredPatchProjection, error) {
	return fake.stored, nil
}

type capabilitySourceFake struct{ capability aiprovider.Capability }

func (fake *capabilitySourceFake) CurrentAICapability(context.Context) aiprovider.Capability {
	return fake.capability
}

func TestProjectionServiceRefreshesProviderWithoutMutatingHistoricalProjection(t *testing.T) {
	patchID := aicontract.PatchID("018f9e40-0000-7000-8000-000000000299")
	repository := &projectionRepositoryFake{stored: StoredPatchProjection{
		PatchID: patchID, JobID: "018f9e40-0000-7000-8000-000000000201", BaseRevisionID: "018f9e40-0000-7000-8000-000000000202",
		Freshness: aicontract.Freshness{State: aicontract.FreshnessFresh},
	}}
	capabilities := &capabilitySourceFake{capability: aiprovider.Capability{State: aiprovider.CapabilityUnavailable, Enabled: true, Reasons: []string{aiprovider.ReasonToolCallsUnsupported, aiprovider.ReasonProviderUnavailable}}}
	service := ProjectionService{Repository: repository, Capabilities: capabilities}
	first, err := service.Read(context.Background(), patchID)
	if err != nil || first.Provider.State != aiprovider.CapabilityUnavailable || first.Provider.Reasons[0] != aiprovider.ReasonProviderUnavailable {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	classification := aiprovider.EndpointLoopback
	capabilities.capability = aiprovider.Capability{State: aiprovider.CapabilityAvailable, Enabled: true, EndpointClassification: &classification, CredentialPresent: true, StructuredOutput: true, ToolCalls: true, Streaming: true}
	second, err := service.Read(context.Background(), patchID)
	if err != nil || second.Provider.State != aiprovider.CapabilityAvailable || !reflect.DeepEqual(second.Stored, first.Stored) || repository.stored.Freshness.State != aicontract.FreshnessFresh {
		t.Fatalf("second=%#v err=%v", second, err)
	}
}

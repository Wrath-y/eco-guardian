package application

import (
	"context"
	"errors"
	"sort"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	"github.com/zouyi/eco-guardian/internal/domain"
)

var (
	ErrProjectionInvalid     = errors.New("AI DraftPatch read projection is invalid")
	ErrProjectionNotFound    = errors.New("AI DraftPatch was not found")
	ErrProjectionUnavailable = errors.New("AI DraftPatch projection is unavailable")
)

type FormalAnalysisLinks struct {
	Validation string `json:"formal_validation,omitempty"`
	Graph      string `json:"formal_graph,omitempty"`
	Simulation string `json:"formal_simulation,omitempty"`
	Risk       string `json:"formal_risk,omitempty"`
}

type StoredPatchProjection struct {
	PatchID            aicontract.PatchID   `json:"patch_id"`
	JobID              domain.ID            `json:"job_id"`
	BaseRevisionID     domain.ID            `json:"base_revision_id"`
	AcceptedRevisionID domain.ID            `json:"accepted_revision_id,omitempty"`
	Freshness          aicontract.Freshness `json:"freshness"`
	Formal             FormalAnalysisLinks  `json:"formal"`
}

func (projection StoredPatchProjection) Valid() bool {
	if !projection.PatchID.Valid() || !domain.ID(projection.PatchID).Valid() || !projection.JobID.Valid() || !projection.BaseRevisionID.Valid() || !projection.Freshness.Valid() {
		return false
	}
	if projection.AcceptedRevisionID == "" {
		return projection.Formal == (FormalAnalysisLinks{})
	}
	return projection.AcceptedRevisionID.Valid()
}

type ProviderCapabilityProjection struct {
	State                  aiprovider.CapabilityState         `json:"state"`
	Enabled                bool                               `json:"enabled"`
	EndpointClassification *aiprovider.EndpointClassification `json:"endpoint_classification,omitempty"`
	CredentialPresent      bool                               `json:"credential_present"`
	StructuredOutput       bool                               `json:"structured_output"`
	ToolCalls              bool                               `json:"tool_calls"`
	Streaming              bool                               `json:"streaming"`
	Reasons                []string                           `json:"reasons"`
}

func (projection ProviderCapabilityProjection) Valid() bool {
	if projection.State != aiprovider.CapabilityUnconfigured && projection.State != aiprovider.CapabilityAvailable && projection.State != aiprovider.CapabilityDegraded && projection.State != aiprovider.CapabilityUnavailable {
		return false
	}
	if projection.EndpointClassification != nil && *projection.EndpointClassification != aiprovider.EndpointLoopback && *projection.EndpointClassification != aiprovider.EndpointCloud {
		return false
	}
	return sort.StringsAreSorted(projection.Reasons)
}

type DraftPatchReadProjection struct {
	Stored   StoredPatchProjection        `json:"stored"`
	Provider ProviderCapabilityProjection `json:"provider"`
}

type ProjectionRepository interface {
	ReadStoredPatchProjection(context.Context, aicontract.PatchID) (StoredPatchProjection, error)
}

type CapabilitySource interface {
	CurrentAICapability(context.Context) aiprovider.Capability
}

type ProjectionService struct {
	Repository   ProjectionRepository
	Capabilities CapabilitySource
}

func (service ProjectionService) Read(ctx context.Context, patchID aicontract.PatchID) (DraftPatchReadProjection, error) {
	if ctx == nil || service.Repository == nil || service.Capabilities == nil || !patchID.Valid() {
		return DraftPatchReadProjection{}, ErrProjectionInvalid
	}
	stored, err := service.Repository.ReadStoredPatchProjection(ctx, patchID)
	if err != nil {
		return DraftPatchReadProjection{}, err
	}
	capability := service.Capabilities.CurrentAICapability(ctx)
	provider := ProviderCapabilityProjection{
		State: capability.State, Enabled: capability.Enabled, EndpointClassification: cloneClassification(capability.EndpointClassification),
		CredentialPresent: capability.CredentialPresent, StructuredOutput: capability.StructuredOutput,
		ToolCalls: capability.ToolCalls, Streaming: capability.Streaming, Reasons: append([]string(nil), capability.Reasons...),
	}
	sort.Strings(provider.Reasons)
	projection := DraftPatchReadProjection{Stored: stored, Provider: provider}
	if !projection.Stored.Valid() || !projection.Provider.Valid() || projection.Stored.PatchID != patchID {
		return DraftPatchReadProjection{}, ErrProjectionInvalid
	}
	return projection, nil
}

func cloneClassification(value *aiprovider.EndpointClassification) *aiprovider.EndpointClassification {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

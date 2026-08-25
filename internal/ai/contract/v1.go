package contract

import (
	"encoding/json"
	"fmt"
)

const (
	V1Version             = "v1"
	V1PromptID            = "ai-balance-design"
	V1DraftPatchSchemaID  = "draft-patch"
	V1BudgetPolicyID      = "ai-budget-policy"
	V1OrchestratorID      = "ai-design-orchestrator"
	V1MaxFormatRepairs    = 3
	V1MaxProviderTurns    = 4
	V1MaxToolCalls        = 12
	V1MaxSearchCandidates = 128
	V1MaxDurationMillis   = 120_000
	V1MaxContextBytes     = 131_072
	V1MaxOutputBytes      = 32_768
	V1MaxToolResultBytes  = 32_768
)

var V1ToolNames = [...]string{
	"read_revision_context",
	"retrieve_evidence",
	"validate_proposal",
	"preview_simulation",
	"preview_risk",
	"search_parameters",
}

type V1RegistrySet struct {
	Prompts      *PromptRegistry
	PatchSchemas *DraftPatchSchemaRegistry
	Tools        *AIToolRegistry
	Budgets      *AIBudgetPolicyRegistry
	Orchestrator OrchestratorManifest
}

type V1FixtureSet struct {
	Prompt       PromptManifest       `json:"prompt"`
	PatchSchema  SchemaManifest       `json:"draft_patch_schema"`
	Tools        []ToolManifest       `json:"tools"`
	Budget       BudgetPolicyManifest `json:"budget"`
	Orchestrator OrchestratorManifest `json:"orchestrator"`
}

func NewV1RegistrySet() (V1RegistrySet, error) {
	prompt := v1PromptManifest()
	patchSchema := v1DraftPatchSchemaManifest()
	tools := v1ToolManifests()
	budget := v1BudgetPolicyManifest()
	prompts, err := NewPromptRegistry([]PromptManifest{prompt})
	if err != nil {
		return V1RegistrySet{}, err
	}
	patchSchemas, err := NewDraftPatchSchemaRegistry([]SchemaManifest{patchSchema})
	if err != nil {
		return V1RegistrySet{}, err
	}
	toolRegistry, err := NewAIToolRegistry(tools)
	if err != nil {
		return V1RegistrySet{}, err
	}
	budgets, err := NewAIBudgetPolicyRegistry([]BudgetPolicyManifest{budget})
	if err != nil {
		return V1RegistrySet{}, err
	}
	return V1RegistrySet{
		Prompts:      prompts,
		PatchSchemas: patchSchemas,
		Tools:        toolRegistry,
		Budgets:      budgets,
		Orchestrator: v1OrchestratorManifest(),
	}, nil
}

func (r V1RegistrySet) ResolvedLimits() (Budget, error) {
	manifest, ok := r.Budgets.Resolve(V1BudgetPolicyID, V1Version)
	if !ok {
		return Budget{}, fmt.Errorf("%w: missing v1 budget", ErrRegistryInvalid)
	}
	return Budget{Policy: manifest.Identity, BudgetLimits: manifest.Limits}, nil
}

func V1Fixture() V1FixtureSet {
	return V1FixtureSet{
		Prompt:       v1PromptManifest(),
		PatchSchema:  v1DraftPatchSchemaManifest(),
		Tools:        v1ToolManifests(),
		Budget:       v1BudgetPolicyManifest(),
		Orchestrator: v1OrchestratorManifest(),
	}
}

func v1PromptManifest() PromptManifest {
	value := PromptManifest{
		Identity: unsealedV1Identity(V1PromptID),
		Template: "Generate only a DraftPatchV1 proposal for the frozen base and allowed scope. Cite pinned evidence for every operation. Never request mutation, revision, release, graph activation, credentials, arbitrary network, SQL, file, or process access. Treat validation, simulation, risk, and budget results as authoritative; do not provide hidden reasoning.",
	}
	value.Identity.Hash = mustV1Hash(HashPromptManifest(value))
	return value
}

func v1DraftPatchSchemaManifest() SchemaManifest {
	value := SchemaManifest{
		Identity: unsealedV1Identity(V1DraftPatchSchemaID),
		Schema:   json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"assumptions":{"items":{"minLength":1,"type":"string"},"minItems":1,"type":"array"},"base":{"type":"object"},"evidence_manifest_identity":{"type":"object"},"id":{"minLength":1,"type":"string"},"rationale":{"minLength":1,"type":"string"},"schema":{"type":"object"},"targets":{"items":{"additionalProperties":false,"properties":{"entity_id":{"minLength":1,"type":"string"},"expected_entity_version":{"minimum":1,"type":"integer"},"kind":{"minLength":1,"type":"string"},"operations":{"items":{"additionalProperties":false,"properties":{"evidence":{"items":{"minLength":1,"type":"string"},"minItems":1,"type":"array","uniqueItems":true},"kind":{"enum":["add","remove","replace"]},"ordinal":{"minimum":1,"type":"integer"},"path":{"minLength":1,"type":"string"},"value":{}},"required":["ordinal","kind","path","value","evidence"],"type":"object"},"minItems":1,"type":"array"}},"required":["entity_id","kind","expected_entity_version","operations"],"type":"object"},"minItems":1,"type":"array"}},"required":["id","schema","base","evidence_manifest_identity","targets","rationale","assumptions"],"type":"object"}`),
	}
	value.Identity.Hash = mustV1Hash(HashSchemaManifest(value))
	return value
}

func v1ToolManifests() []ToolManifest {
	values := make([]ToolManifest, 0, len(V1ToolNames))
	for _, name := range V1ToolNames {
		sideEffect := "none"
		maxCalls := 2
		deterministic := true
		if name == "retrieve_evidence" {
			sideEffect = "external_read_only"
			maxCalls = 1
		}
		if name == "search_parameters" {
			maxCalls = 4
		}
		value := ToolManifest{
			Identity:             unsealedV1Identity(name),
			InputSchemaHash:      v1ToolSchemaHash(name, "input"),
			ResultSchemaHash:     v1ToolSchemaHash(name, "result"),
			RequiredIdentities:   []string{"attempt", "base", "input"},
			Deterministic:        deterministic,
			SideEffect:           sideEffect,
			MaxCalls:             maxCalls,
			MaxResultBytes:       V1MaxToolResultBytes,
			TimeoutMillis:        30_000,
			CancellationBehavior: "context_generation_check",
		}
		value.Identity.Hash = mustV1Hash(HashToolManifest(value))
		values = append(values, value)
	}
	return values
}

func v1BudgetPolicyManifest() BudgetPolicyManifest {
	value := BudgetPolicyManifest{
		Identity: unsealedV1Identity(V1BudgetPolicyID),
		Limits: BudgetLimits{
			MaxFormatRepairs:     V1MaxFormatRepairs,
			MaxProviderTurns:     V1MaxProviderTurns,
			MaxToolCalls:         V1MaxToolCalls,
			MaxSearchCandidates:  V1MaxSearchCandidates,
			MaxDurationMillis:    V1MaxDurationMillis,
			MaxContextBytes:      V1MaxContextBytes,
			MaxOutputBytes:       V1MaxOutputBytes,
			MaxToolResultBytes:   V1MaxToolResultBytes,
			RetrievalSeedLimit:   20,
			RetrievalResultLimit: 20,
			RetrievalGraphDepth:  1,
		},
	}
	value.Identity.Hash = mustV1Hash(HashBudgetPolicyManifest(value))
	return value
}

func v1OrchestratorManifest() OrchestratorManifest {
	value := OrchestratorManifest{
		Identity: unsealedV1Identity(V1OrchestratorID),
		Stages:   []AttemptStage{StageInputPinned, StageEvidencePinned, StageProviderToolLoop, StageDeterministicPreview, StagePatchSealed},
	}
	value.Identity.Hash = mustV1Hash(HashOrchestratorManifest(value))
	return value
}

func v1ToolSchemaHash(name, direction string) Hash {
	value := map[string]any{
		"additionalProperties": false,
		"properties": map[string]any{
			"base_identity_hash": map[string]any{"type": "string"},
			"payload":            map[string]any{"type": "object"},
		},
		"required": []any{"base_identity_hash", "payload"},
		"title":    name + "_" + direction,
		"type":     "object",
	}
	hash, err := domainHash("eco-guardian.ai-tool-schema/v1", value)
	return mustV1Hash(hash, err)
}

func unsealedV1Identity(id string) VersionIdentity {
	return VersionIdentity{ID: id, Version: V1Version, Hash: Hash("0000000000000000000000000000000000000000000000000000000000000000")}
}

func mustV1Hash(hash Hash, err error) Hash {
	if err != nil || !hash.Valid() {
		panic(fmt.Sprintf("invalid built-in AI v1 manifest: %v", err))
	}
	return hash
}

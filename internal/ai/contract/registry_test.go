package contract

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestImmutableAIRegistriesResolveSealedManifests(t *testing.T) {
	prompt := sealedPrompt(t, PromptManifest{Identity: version("prompt"), Template: "Return DraftPatchV1."})
	prompts, err := NewPromptRegistry([]PromptManifest{prompt})
	if err != nil {
		t.Fatal(err)
	}
	resolvedPrompt, ok := prompts.Resolve("prompt", "v1")
	if !ok || resolvedPrompt.Identity.Hash != prompt.Identity.Hash {
		t.Fatal("sealed prompt was not resolved")
	}

	schema := sealedSchema(t, SchemaManifest{Identity: version("draft-patch"), Schema: json.RawMessage(`{"type":"object"}`)})
	schemas, err := NewDraftPatchSchemaRegistry([]SchemaManifest{schema})
	if err != nil {
		t.Fatal(err)
	}
	resolvedSchema, ok := schemas.Resolve("draft-patch", "v1")
	if !ok {
		t.Fatal("sealed schema was not resolved")
	}
	resolvedSchema.Schema[0] = '['
	again, _ := schemas.Resolve("draft-patch", "v1")
	if string(again.Schema) != `{"type":"object"}` {
		t.Fatal("schema registry exposed mutable backing bytes")
	}

	tool := sealedTool(t, validToolManifest("read_revision_context"))
	tools, err := NewAIToolRegistry([]ToolManifest{tool})
	if err != nil {
		t.Fatal(err)
	}
	resolvedTool, ok := tools.Resolve("read_revision_context", "v1")
	if !ok {
		t.Fatal("sealed tool was not resolved")
	}
	resolvedTool.RequiredIdentities[0] = "changed"
	againTool, _ := tools.Resolve("read_revision_context", "v1")
	if againTool.RequiredIdentities[0] != "base" {
		t.Fatal("tool registry exposed mutable backing slices")
	}

	budget := sealedBudget(t, BudgetPolicyManifest{Identity: version("budget"), Limits: validBudget().BudgetLimits})
	budgets, err := NewAIBudgetPolicyRegistry([]BudgetPolicyManifest{budget})
	if err != nil {
		t.Fatal(err)
	}
	if resolved, ok := budgets.Resolve("budget", "v1"); !ok || resolved.Limits.MaxFormatRepairs != 3 {
		t.Fatal("sealed budget was not resolved")
	}
}

func TestAIRegistriesRejectDuplicateIdentityAndHashDrift(t *testing.T) {
	prompt := sealedPrompt(t, PromptManifest{Identity: version("prompt"), Template: "Return DraftPatchV1."})
	if _, err := NewPromptRegistry([]PromptManifest{prompt, prompt}); !errors.Is(err, ErrRegistryDuplicate) {
		t.Fatalf("duplicate error=%v", err)
	}
	drifted := prompt
	drifted.Template = "Changed after sealing."
	if _, err := NewPromptRegistry([]PromptManifest{drifted}); !errors.Is(err, ErrRegistryDrift) {
		t.Fatalf("drift error=%v", err)
	}
	schema := sealedSchema(t, SchemaManifest{Identity: version("draft-patch"), Schema: json.RawMessage(`{"type":"object"}`)})
	schema.Schema = json.RawMessage(`{"type":"array"}`)
	if _, err := NewDraftPatchSchemaRegistry([]SchemaManifest{schema}); !errors.Is(err, ErrRegistryDrift) {
		t.Fatalf("schema drift error=%v", err)
	}
}

func TestAIRegistriesRejectMissingHardLimitsAndForbiddenTools(t *testing.T) {
	missingToolLimit := validToolManifest("preview_simulation")
	missingToolLimit.MaxCalls = 0
	if _, err := NewAIToolRegistry([]ToolManifest{missingToolLimit}); !errors.Is(err, ErrRegistryInvalid) {
		t.Fatalf("missing tool limit error=%v", err)
	}
	missingBudgetLimit := BudgetPolicyManifest{Identity: version("budget"), Limits: validBudget().BudgetLimits}
	missingBudgetLimit.Limits.MaxDurationMillis = 0
	if _, err := NewAIBudgetPolicyRegistry([]BudgetPolicyManifest{missingBudgetLimit}); !errors.Is(err, ErrRegistryInvalid) {
		t.Fatalf("missing budget limit error=%v", err)
	}
	for _, name := range []string{"mutate_entity", "create_revision", "release_candidate", "activate_graph", "read_credentials"} {
		t.Run(name, func(t *testing.T) {
			tool := sealedTool(t, validToolManifest(name))
			if _, err := NewAIToolRegistry([]ToolManifest{tool}); !errors.Is(err, ErrForbiddenTool) {
				t.Fatalf("forbidden tool error=%v", err)
			}
		})
	}
}

func validToolManifest(id string) ToolManifest {
	return ToolManifest{
		Identity:             version(id),
		InputSchemaHash:      testHash,
		ResultSchemaHash:     testHash,
		RequiredIdentities:   []string{"base", "input"},
		Deterministic:        true,
		SideEffect:           "none",
		MaxCalls:             2,
		MaxResultBytes:       4096,
		TimeoutMillis:        1000,
		CancellationBehavior: "context",
	}
}

func sealedPrompt(t *testing.T, value PromptManifest) PromptManifest {
	t.Helper()
	hash, err := HashPromptManifest(value)
	if err != nil {
		t.Fatal(err)
	}
	value.Identity.Hash = hash
	return value
}

func sealedSchema(t *testing.T, value SchemaManifest) SchemaManifest {
	t.Helper()
	hash, err := HashSchemaManifest(value)
	if err != nil {
		t.Fatal(err)
	}
	value.Identity.Hash = hash
	return value
}

func sealedTool(t *testing.T, value ToolManifest) ToolManifest {
	t.Helper()
	hash, err := HashToolManifest(value)
	if err != nil {
		t.Fatal(err)
	}
	value.Identity.Hash = hash
	return value
}

func sealedBudget(t *testing.T, value BudgetPolicyManifest) BudgetPolicyManifest {
	t.Helper()
	hash, err := HashBudgetPolicyManifest(value)
	if err != nil {
		t.Fatal(err)
	}
	value.Identity.Hash = hash
	return value
}

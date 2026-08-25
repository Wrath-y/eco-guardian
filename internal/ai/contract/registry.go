package contract

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrRegistryDuplicate = errors.New("duplicate AI registry identity")
	ErrRegistryDrift     = errors.New("AI registry version/hash drift")
	ErrRegistryInvalid   = errors.New("invalid AI registry manifest")
	ErrForbiddenTool     = errors.New("forbidden AI tool capability")
)

type PromptRegistry struct{ entries map[string]PromptManifest }

func NewPromptRegistry(manifests []PromptManifest) (*PromptRegistry, error) {
	entries := make(map[string]PromptManifest, len(manifests))
	for _, manifest := range manifests {
		if !manifest.Valid() {
			return nil, ErrRegistryInvalid
		}
		hash, err := HashPromptManifest(manifest)
		if err != nil || hash != manifest.Identity.Hash {
			return nil, fmt.Errorf("%w: prompt %s@%s", ErrRegistryDrift, manifest.Identity.ID, manifest.Identity.Version)
		}
		key := registryKey(manifest.Identity)
		if _, duplicate := entries[key]; duplicate {
			return nil, fmt.Errorf("%w: prompt %s@%s", ErrRegistryDuplicate, manifest.Identity.ID, manifest.Identity.Version)
		}
		entries[key] = clonePromptManifest(manifest)
	}
	if len(entries) == 0 {
		return nil, ErrRegistryInvalid
	}
	return &PromptRegistry{entries: entries}, nil
}

func (r *PromptRegistry) Resolve(id, version string) (PromptManifest, bool) {
	if r == nil {
		return PromptManifest{}, false
	}
	manifest, ok := r.entries[registryKeyParts(id, version)]
	return clonePromptManifest(manifest), ok
}

type DraftPatchSchemaRegistry struct{ entries map[string]SchemaManifest }

func NewDraftPatchSchemaRegistry(manifests []SchemaManifest) (*DraftPatchSchemaRegistry, error) {
	entries := make(map[string]SchemaManifest, len(manifests))
	for _, manifest := range manifests {
		if !manifest.Valid() {
			return nil, ErrRegistryInvalid
		}
		hash, err := HashSchemaManifest(manifest)
		if err != nil || hash != manifest.Identity.Hash {
			return nil, fmt.Errorf("%w: schema %s@%s", ErrRegistryDrift, manifest.Identity.ID, manifest.Identity.Version)
		}
		key := registryKey(manifest.Identity)
		if _, duplicate := entries[key]; duplicate {
			return nil, fmt.Errorf("%w: schema %s@%s", ErrRegistryDuplicate, manifest.Identity.ID, manifest.Identity.Version)
		}
		entries[key] = cloneSchemaManifest(manifest)
	}
	if len(entries) == 0 {
		return nil, ErrRegistryInvalid
	}
	return &DraftPatchSchemaRegistry{entries: entries}, nil
}

func (r *DraftPatchSchemaRegistry) Resolve(id, version string) (SchemaManifest, bool) {
	if r == nil {
		return SchemaManifest{}, false
	}
	manifest, ok := r.entries[registryKeyParts(id, version)]
	return cloneSchemaManifest(manifest), ok
}

type AIToolRegistry struct{ entries map[string]ToolManifest }

func NewAIToolRegistry(manifests []ToolManifest) (*AIToolRegistry, error) {
	entries := make(map[string]ToolManifest, len(manifests))
	for _, manifest := range manifests {
		if !manifest.Valid() {
			return nil, ErrRegistryInvalid
		}
		if forbiddenToolIdentity(manifest.Identity.ID) {
			return nil, fmt.Errorf("%w: %s", ErrForbiddenTool, manifest.Identity.ID)
		}
		hash, err := HashToolManifest(manifest)
		if err != nil || hash != manifest.Identity.Hash {
			return nil, fmt.Errorf("%w: tool %s@%s", ErrRegistryDrift, manifest.Identity.ID, manifest.Identity.Version)
		}
		key := registryKey(manifest.Identity)
		if _, duplicate := entries[key]; duplicate {
			return nil, fmt.Errorf("%w: tool %s@%s", ErrRegistryDuplicate, manifest.Identity.ID, manifest.Identity.Version)
		}
		entries[key] = cloneToolManifest(manifest)
	}
	if len(entries) == 0 {
		return nil, ErrRegistryInvalid
	}
	return &AIToolRegistry{entries: entries}, nil
}

func (r *AIToolRegistry) Resolve(id, version string) (ToolManifest, bool) {
	if r == nil {
		return ToolManifest{}, false
	}
	manifest, ok := r.entries[registryKeyParts(id, version)]
	return cloneToolManifest(manifest), ok
}

type AIBudgetPolicyRegistry struct {
	entries map[string]BudgetPolicyManifest
}

func NewAIBudgetPolicyRegistry(manifests []BudgetPolicyManifest) (*AIBudgetPolicyRegistry, error) {
	entries := make(map[string]BudgetPolicyManifest, len(manifests))
	for _, manifest := range manifests {
		if !manifest.Valid() {
			return nil, ErrRegistryInvalid
		}
		hash, err := HashBudgetPolicyManifest(manifest)
		if err != nil || hash != manifest.Identity.Hash {
			return nil, fmt.Errorf("%w: budget %s@%s", ErrRegistryDrift, manifest.Identity.ID, manifest.Identity.Version)
		}
		key := registryKey(manifest.Identity)
		if _, duplicate := entries[key]; duplicate {
			return nil, fmt.Errorf("%w: budget %s@%s", ErrRegistryDuplicate, manifest.Identity.ID, manifest.Identity.Version)
		}
		entries[key] = manifest
	}
	if len(entries) == 0 {
		return nil, ErrRegistryInvalid
	}
	return &AIBudgetPolicyRegistry{entries: entries}, nil
}

func (r *AIBudgetPolicyRegistry) Resolve(id, version string) (BudgetPolicyManifest, bool) {
	if r == nil {
		return BudgetPolicyManifest{}, false
	}
	manifest, ok := r.entries[registryKeyParts(id, version)]
	return manifest, ok
}

func registryKey(identity VersionIdentity) string {
	return registryKeyParts(identity.ID, identity.Version)
}

func registryKeyParts(id, version string) string { return id + "\x00" + version }

func forbiddenToolIdentity(id string) bool {
	normalized := strings.ToLower(id)
	for _, forbidden := range []string{"mutate", "mutation", "write", "save", "create_revision", "delete_revision", "release", "publish", "activate", "credential", "secret"} {
		if strings.Contains(normalized, forbidden) {
			return true
		}
	}
	return false
}

func clonePromptManifest(value PromptManifest) PromptManifest { return value }

func cloneSchemaManifest(value SchemaManifest) SchemaManifest {
	value.Schema = append([]byte(nil), value.Schema...)
	return value
}

func cloneToolManifest(value ToolManifest) ToolManifest {
	value.RequiredIdentities = append([]string(nil), value.RequiredIdentities...)
	return value
}

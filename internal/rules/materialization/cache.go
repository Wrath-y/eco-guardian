package materialization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"

	"github.com/zouyi/eco-guardian/internal/domain"
)

// Cache is an optional, in-memory derived-artifact cache. Source facts are
// read on every request so a cached value can never substitute another
// revision, config hash, or validation certification.
type Cache struct {
	mu     sync.Mutex
	values map[string]RuleSetV1
}

func (c *Cache) Materialize(ctx context.Context, reader Reader, revisionID domain.ID) (RuleSetV1, error) {
	if reader == nil || !revisionID.Valid() {
		return RuleSetV1{}, ErrUnavailable
	}
	source, err := reader.ReadRuleSource(ctx, revisionID)
	if err != nil {
		return RuleSetV1{}, err
	}
	key, err := cacheKey(source)
	if err != nil {
		return RuleSetV1{}, err
	}
	c.mu.Lock()
	cached, found := c.values[key]
	c.mu.Unlock()
	if found && Verify(cached) == nil {
		return cloneRuleSet(cached), nil
	}
	set, err := Build(source)
	if err != nil {
		return RuleSetV1{}, err
	}
	c.mu.Lock()
	if c.values == nil {
		c.values = map[string]RuleSetV1{}
	}
	c.values[key] = cloneRuleSet(set)
	c.mu.Unlock()
	return set, nil
}

func Verify(set RuleSetV1) error {
	if !set.Valid() {
		return ErrInvalid
	}
	canonical, err := canonicalRuleSet(set)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(append([]byte(ContractVersionV1+"\x00"), canonical...))
	if string(canonical) != string(set.Canonical) || hex.EncodeToString(sum[:]) != set.MaterializationHash {
		return ErrInvalid
	}
	return nil
}

func cacheKey(source Source) (string, error) {
	versionHash, err := source.Certification.Versions.Hash()
	if err != nil {
		return "", err
	}
	return string(source.RevisionID) + "\x00" + source.ConfigHash + "\x00" + versionHash + "\x00" + source.Certification.ResultHash, nil
}

func cloneRuleSet(value RuleSetV1) RuleSetV1 {
	value.Canonical = append([]byte(nil), value.Canonical...)
	value.FormulaBindings = append([]FormulaBinding(nil), value.FormulaBindings...)
	for i := range value.FormulaBindings {
		value.FormulaBindings[i].AST = append([]byte(nil), value.FormulaBindings[i].AST...)
		value.FormulaBindings[i].Reads = append([]FormulaRead(nil), value.FormulaBindings[i].Reads...)
	}
	value.TriggerRules = append([]TriggerRule(nil), value.TriggerRules...)
	for i := range value.TriggerRules {
		value.TriggerRules[i].EffectIDs = append([]domain.ID(nil), value.TriggerRules[i].EffectIDs...)
	}
	value.Modifiers = append([]Modifier(nil), value.Modifiers...)
	value.StackRules = append([]StackRule(nil), value.StackRules...)
	return value
}

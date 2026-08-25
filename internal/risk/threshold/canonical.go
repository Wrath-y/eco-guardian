package threshold

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
)

var thresholdDecimalPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)

func (b Body) Normalize() (Body, error) {
	copy := b
	if copy.SchemaVersion != SchemaVersionV1 || strings.TrimSpace(copy.Source) == "" || len(copy.Entries) == 0 || len(copy.StructuralRuleVersions) == 0 {
		return Body{}, ErrThresholdInvalid
	}
	copy.Assumptions = append([]string(nil), b.Assumptions...)
	if copy.Assumptions == nil {
		copy.Assumptions = []string{}
	}
	for _, assumption := range copy.Assumptions {
		if strings.TrimSpace(assumption) == "" {
			return Body{}, ErrThresholdInvalid
		}
	}
	sort.Strings(copy.Assumptions)
	copy.Entries = append([]Entry(nil), b.Entries...)
	sort.Slice(copy.Entries, func(i, j int) bool { return copy.Entries[i].ScopeKey() < copy.Entries[j].ScopeKey() })
	for index, entry := range copy.Entries {
		if !entry.Valid() || (index > 0 && copy.Entries[index-1].ScopeKey() == entry.ScopeKey()) {
			return Body{}, ErrThresholdInvalid
		}
	}
	copy.StructuralRuleVersions = append([]riskcontract.Identity(nil), b.StructuralRuleVersions...)
	sort.Slice(copy.StructuralRuleVersions, func(i, j int) bool {
		return copy.StructuralRuleVersions[i].ID+"\x00"+copy.StructuralRuleVersions[i].Version < copy.StructuralRuleVersions[j].ID+"\x00"+copy.StructuralRuleVersions[j].Version
	})
	for index, rule := range copy.StructuralRuleVersions {
		if !rule.Valid() || (index > 0 && copy.StructuralRuleVersions[index-1].ID == rule.ID) {
			return Body{}, ErrThresholdInvalid
		}
	}
	return copy, nil
}

func (b Body) CanonicalJSON() ([]byte, error) {
	normalized, err := b.Normalize()
	if err != nil {
		return nil, err
	}
	return domain.CanonicalJSON(normalized)
}

func (b Body) Hash() (string, error) {
	normalized, err := b.Normalize()
	if err != nil {
		return "", err
	}
	hash, _, err := riskcontract.CanonicalHash("eco-guardian/risk-threshold/v1", normalized)
	return hash, err
}

func parseThresholdDecimal(value string) (formula.Decimal, bool) {
	decimal, err := formula.ParseDecimal(value)
	return decimal, err == nil && thresholdDecimalPattern.MatchString(value)
}

func validHash(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func requestHash(value any) (string, error) {
	hash, _, err := riskcontract.CanonicalHash("eco-guardian/risk-threshold-selection/v1", value)
	if err != nil {
		return "", fmt.Errorf("threshold request hash: %w", err)
	}
	return hash, nil
}

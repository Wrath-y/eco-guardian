package threshold

import (
	"errors"
	"sort"
	"strings"

	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
)

var (
	ErrThresholdNotConfigured = errors.New("threshold is not configured")
	ErrThresholdInvalid       = errors.New("threshold is invalid")
	ErrIdempotencyConflict    = errors.New("threshold idempotency conflict")
)

type ResolutionEvidence struct {
	RequestedScope riskcontract.ThresholdScope `json:"requested_scope"`
	TriedKeys      []string                    `json:"tried_keys"`
	SelectedKey    string                      `json:"selected_key,omitempty"`
	Priority       string                      `json:"priority,omitempty"`
}

type Resolution struct {
	Entry    Entry              `json:"entry"`
	Evidence ResolutionEvidence `json:"evidence"`
}

func Resolve(body Body, scope riskcontract.ThresholdScope, singleton bool) (Resolution, error) {
	if !scope.Valid() {
		return Resolution{}, ErrThresholdInvalid
	}
	normalized, err := body.Normalize()
	if err != nil {
		return Resolution{}, ErrThresholdInvalid
	}
	evidence := ResolutionEvidence{RequestedScope: scope}
	baseKey := scope.SceneID + "\x00" + scope.SceneVersion + "\x00" + scope.MetricID + "\x00" + scope.MetricVersion
	var priorities []*string
	if !singleton && scope.BalanceGroup != nil {
		priorities = append(priorities, scope.BalanceGroup)
	}
	priorities = append(priorities, nil)
	for _, group := range priorities {
		key := baseKey + "\x00\x00"
		priority := "project_default"
		if group != nil {
			key = baseKey + "\x00\x01" + *group
			priority = "exact_balance_group"
		}
		evidence.TriedKeys = append(evidence.TriedKeys, key)
		matches := make([]Entry, 0, 1)
		for _, entry := range normalized.Entries {
			if entry.ScopeKey() == key {
				matches = append(matches, entry)
			}
		}
		if len(matches) > 1 {
			return Resolution{Evidence: evidence}, ErrThresholdInvalid
		}
		if len(matches) == 1 {
			entry := matches[0]
			if entry.Unit != scope.Unit || entry.Direction != scope.Direction {
				return Resolution{Evidence: evidence}, ErrThresholdInvalid
			}
			evidence.SelectedKey = key
			evidence.Priority = priority
			return Resolution{Entry: entry, Evidence: evidence}, nil
		}
	}
	sort.Strings(evidence.TriedKeys)
	return Resolution{Evidence: evidence}, ErrThresholdNotConfigured
}

func NullableGroup(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	copy := value
	return &copy
}

package cohort

import (
	"fmt"
	"sort"

	"github.com/zouyi/eco-guardian/internal/domain"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
)

func CanonicalSubjects(subjects []riskcontract.Subject) ([]riskcontract.Subject, string, error) {
	copy := append([]riskcontract.Subject(nil), subjects...)
	seen := make(map[string]struct{}, len(copy))
	for index := range copy {
		copy[index].Members = append([]domain.ID(nil), copy[index].Members...)
		sort.Slice(copy[index].Members, func(i, j int) bool { return copy[index].Members[i] < copy[index].Members[j] })
		if !copy[index].Valid() {
			return nil, "", fmt.Errorf("invalid cohort subject")
		}
		if _, duplicate := seen[copy[index].Key()]; duplicate {
			return nil, "", fmt.Errorf("duplicate cohort subject")
		}
		seen[copy[index].Key()] = struct{}{}
	}
	sort.Slice(copy, func(i, j int) bool { return copy[i].Key() < copy[j].Key() })
	hash, _, err := riskcontract.CanonicalHash("eco-guardian/risk-cohort/v1", copy)
	return copy, hash, err
}

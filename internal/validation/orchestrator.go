package validation

import (
	"context"
	"fmt"
	"sort"
)

type Validator struct {
	Name  string
	Phase Scope
	Code  string
	Order int
	Run   func(context.Context) ([]Issue, error)
}
type Orchestrator struct{ validators []Validator }

func NewOrchestrator(validators []Validator) (*Orchestrator, error) {
	seenNames := map[string]struct{}{}
	seenCodes := map[string]struct{}{}
	out := append([]Validator(nil), validators...)
	for _, validator := range out {
		if validator.Name == "" || validator.Run == nil || !validator.Phase.Valid() {
			return nil, fmt.Errorf("invalid validator")
		}
		if _, ok := seenNames[validator.Name]; ok {
			return nil, fmt.Errorf("duplicate validator %q", validator.Name)
		}
		seenNames[validator.Name] = struct{}{}
		if _, ok := seenCodes[validator.Code]; ok {
			return nil, fmt.Errorf("duplicate validator code %q", validator.Code)
		}
		seenCodes[validator.Code] = struct{}{}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Order == out[j].Order {
			return out[i].Name < out[j].Name
		}
		return out[i].Order < out[j].Order
	})
	return &Orchestrator{out}, nil
}
func (o *Orchestrator) Run(ctx context.Context, scope Scope) ([]Issue, error) {
	if !scope.Valid() {
		return nil, fmt.Errorf("invalid validation scope")
	}
	issues := []Issue{}
	for _, validator := range o.validators {
		if validator.Phase != scope {
			continue
		}
		found, err := validator.Run(ctx)
		if err != nil {
			return nil, err
		}
		issues = append(issues, found...)
	}
	return SortIssues(issues), nil
}

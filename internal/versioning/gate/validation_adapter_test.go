package gate

import (
	"context"
	"errors"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/validation"
)

type fakeValidationSource struct {
	result validation.GateResult
	err    error
}

func (f fakeValidationSource) Check(context.Context, domain.ID, string, validation.VersionManifest) (validation.GateResult, error) {
	return f.result, f.err
}

func TestValidationGateAdapterOnlyExposesExactFullPass(t *testing.T) {
	revision, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	versions := validation.VersionManifest{Schema: "schema", DSL: "dsl", Registry: "registry", NumericPolicy: "numeric"}
	for name, source := range map[string]fakeValidationSource{
		"missing local working or stale": {result: validation.GateRequiresValidation},
		"error block cycle or unbounded": {result: validation.GateBlocked},
		"failed read":                    {err: errors.New("validation unavailable")},
	} {
		t.Run(name, func(t *testing.T) {
			state, err := (ValidationGateAdapter{Source: source}).Check(context.Background(), revision, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", versions)
			if state == Pass {
				t.Fatalf("non-exact FULL validation passed: state=%s err=%v", state, err)
			}
		})
	}
	state, err := (ValidationGateAdapter{Source: fakeValidationSource{result: validation.GatePass}}).Check(context.Background(), revision, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", versions)
	if err != nil || state != Pass {
		t.Fatalf("exact full state=%s err=%v", state, err)
	}
	descriptor := (ValidationGateAdapter{}).Descriptor()
	if !descriptor.Valid() || descriptor.OverridableNumericBlock {
		t.Fatalf("descriptor=%#v", descriptor)
	}
}

package sync

import (
	"context"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/validation"
)

type validationGateFake struct{ result validation.GateResult }

func (f validationGateFake) Check(context.Context, domain.ID, string, validation.VersionManifest) (validation.GateResult, error) {
	return f.result, nil
}

func TestRequireFullValidationAcceptsOnlyExactPass(t *testing.T) {
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	versions := validation.VersionManifest{Schema: "s", DSL: "d", Registry: "r", NumericPolicy: "n"}
	if err = RequireFullValidation(context.Background(), validationGateFake{validation.GatePass}, id, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", versions); err != nil {
		t.Fatal(err)
	}
	for _, result := range []validation.GateResult{validation.GateBlocked, validation.GateRequiresValidation} {
		if RequireFullValidation(context.Background(), validationGateFake{result}, id, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", versions) == nil {
			t.Fatal(result)
		}
	}
}

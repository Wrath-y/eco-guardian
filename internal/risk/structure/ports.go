package structure

import (
	"context"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/risk/contract"
)

// ImpactEvidenceReader is optional. A nil reader means #9 is not installed;
// deterministic calculation and Gate results must remain byte-identical.
type ImpactEvidenceReader interface {
	Evidence(context.Context, domain.ID, domain.ID) ([]contract.ImpactEvidenceRef, error)
}

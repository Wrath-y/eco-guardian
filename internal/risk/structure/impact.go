package structure

import (
	"context"
	"sort"

	"github.com/zouyi/eco-guardian/internal/domain"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
)

type ImpactAttachment struct {
	Status       string                           `json:"status"`
	Refs         []riskcontract.ImpactEvidenceRef `json:"refs"`
	EvidenceHash string                           `json:"evidence_hash,omitempty"`
}

// AttachImpactEvidence is deliberately separate from Analyze and validation
// findings. Provider absence, errors, suspected classifications and freshness
// can only alter this explanation attachment.
func AttachImpactEvidence(ctx context.Context, reader ImpactEvidenceReader, candidate, baseline domain.ID, revisionPairHash, snapshotHash string) ImpactAttachment {
	if reader == nil {
		return ImpactAttachment{Status: "NOT_INSTALLED", Refs: []riskcontract.ImpactEvidenceRef{}}
	}
	refs, err := reader.Evidence(ctx, candidate, baseline)
	if err != nil {
		return ImpactAttachment{Status: "UNAVAILABLE", Refs: []riskcontract.ImpactEvidenceRef{}}
	}
	matched := make([]riskcontract.ImpactEvidenceRef, 0, len(refs))
	for _, ref := range refs {
		if ref.Valid() && ref.RevisionPairHash == revisionPairHash && ref.SnapshotHash == snapshotHash {
			matched = append(matched, ref)
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].ReportID+"\x00"+matched[i].EvidenceID < matched[j].ReportID+"\x00"+matched[j].EvidenceID
	})
	if len(matched) == 0 {
		return ImpactAttachment{Status: "NO_EXACT_MATCH", Refs: []riskcontract.ImpactEvidenceRef{}}
	}
	hash, err := riskcontract.ExplanationEvidenceManifestHash(matched)
	if err != nil {
		return ImpactAttachment{Status: "UNAVAILABLE", Refs: []riskcontract.ImpactEvidenceRef{}}
	}
	return ImpactAttachment{Status: "ATTACHED", Refs: matched, EvidenceHash: hash}
}

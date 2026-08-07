package release

import (
	"sort"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioning "github.com/zouyi/eco-guardian/internal/versioning"
)

func (c Command) CanonicalJSON() ([]byte, error) {
	confirmations := append([]Confirmation(nil), c.Confirmations...)
	sort.Slice(confirmations, func(i, j int) bool { return confirmations[i].Kind < confirmations[j].Kind })
	var baseline *domain.ID
	if c.BaselineReleaseID != "" {
		value := c.BaselineReleaseID
		baseline = &value
	}
	return versioning.CanonicalJSON(struct {
		CandidateRevisionID domain.ID      `json:"candidate_revision_id"`
		ConfigHash          string         `json:"config_hash"`
		ManifestHash        string         `json:"version_manifest_hash"`
		PolicyID            domain.ID      `json:"policy_id"`
		BaselineReleaseID   *domain.ID     `json:"expected_baseline_release_id"`
		Notes               string         `json:"notes,omitempty"`
		Confirmations       []Confirmation `json:"confirmations"`
		Override            *OverrideAudit `json:"override,omitempty"`
	}{
		CandidateRevisionID: c.CandidateRevisionID,
		ConfigHash:          c.ConfigHash,
		ManifestHash:        c.ManifestHash,
		PolicyID:            c.PolicyID,
		BaselineReleaseID:   baseline,
		Notes:               c.Notes,
		Confirmations:       confirmations,
		Override:            c.Override,
	})
}
func (c Command) Hash() (string, error) {
	b, err := c.CanonicalJSON()
	return versioning.SHA256(b), err
}

package revision

import (
	"sort"

	versioning "github.com/zouyi/eco-guardian/internal/versioning"
)

func (m VersionManifest) CanonicalJSON() ([]byte, error) {
	entries := append([]VersionEntry(nil), m.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].CapabilityID < entries[j].CapabilityID })
	return versioning.CanonicalJSON(struct {
		Entries []VersionEntry `json:"entries"`
	}{entries})
}
func (m VersionManifest) Hash() (string, error) {
	b, err := m.CanonicalJSON()
	return versioning.SHA256(b), err
}

package policy

import (
	"sort"

	versioning "github.com/zouyi/eco-guardian/internal/versioning"
)

func (p ReleasePolicy) CanonicalJSON() ([]byte, error) {
	return p.Definition.CanonicalJSON()
}

func (d Definition) CanonicalJSON() ([]byte, error) {
	copy := d
	copy.Scenes = append([]Scene(nil), d.Scenes...)
	copy.Capabilities = append([]CapabilityRequirement(nil), d.Capabilities...)
	sort.Slice(copy.Capabilities, func(i, j int) bool {
		left, right := copy.Capabilities[i], copy.Capabilities[j]
		if left.CapabilityID != right.CapabilityID {
			return left.CapabilityID < right.CapabilityID
		}
		return left.GateID < right.GateID
	})
	for i := range copy.Scenes {
		copy.Scenes[i].Metrics = append([]Metric(nil), copy.Scenes[i].Metrics...)
		sort.Slice(copy.Scenes[i].Metrics, func(a, b int) bool { return copy.Scenes[i].Metrics[a].ID < copy.Scenes[i].Metrics[b].ID })
	}
	sort.Slice(copy.Scenes, func(i, j int) bool { return copy.Scenes[i].ID < copy.Scenes[j].ID })
	return versioning.CanonicalJSON(copy)
}
func (p ReleasePolicy) Hash() (string, error) {
	b, err := p.CanonicalJSON()
	return versioning.SHA256(b), err
}

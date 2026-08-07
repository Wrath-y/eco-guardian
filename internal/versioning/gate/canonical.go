package gate

import (
	"sort"

	versioning "github.com/zouyi/eco-guardian/internal/versioning"
)

func (r Result) CanonicalJSON() ([]byte, error) {
	copy := r
	copy.Evidence = append([]Evidence(nil), r.Evidence...)
	copy.Descriptor.RequiredInputs = append([]string(nil), r.Descriptor.RequiredInputs...)
	sort.Strings(copy.Descriptor.RequiredInputs)
	sort.Slice(copy.Evidence, func(i, j int) bool { return copy.Evidence[i].ID < copy.Evidence[j].ID })
	return versioning.CanonicalJSON(copy)
}
func (r Result) Hash() (string, error) { b, err := r.CanonicalJSON(); return versioning.SHA256(b), err }

package projector

import versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"

const CapabilityID = "graph-projector"

// VersionContributor records exactly the default descriptor installed at
// revision-save time. Historical revisions always use their saved entry.
type VersionContributor struct{ Descriptor Descriptor }

func (c VersionContributor) CapabilityID() string    { return CapabilityID }
func (c VersionContributor) ContractVersion() string { return "graph-projection-v1" }
func (c VersionContributor) ImplementationVersion() string {
	if !c.Descriptor.Valid() {
		return ""
	}
	return string(c.Descriptor.SchemaVersion) + "/" + string(c.Descriptor.Version)
}
func (c VersionContributor) RegistrationState() versioningrevision.RegistrationState {
	if !c.Descriptor.Valid() {
		return versioningrevision.Unregistered
	}
	return versioningrevision.Registered
}

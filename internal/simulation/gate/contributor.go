package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/zouyi/eco-guardian/internal/simulation/contract"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

const SimulationCapabilityID = "simulation-engine"

// VersionContributor pins the closed simulation implementation manifest into
// every newly written revision. It never fills an old missing entry with the
// current executable's identity.
type VersionContributor struct{ implementation string }

func NewVersionContributor(registry *contract.ManifestRegistry) (VersionContributor, error) {
	if registry == nil || len(registry.Descriptors()) != len(contract.RequiredV1Descriptors) {
		return VersionContributor{}, fmt.Errorf("incomplete simulation implementation registry")
	}
	body, err := json.Marshal(registry.Descriptors())
	if err != nil {
		return VersionContributor{}, err
	}
	digest := sha256.Sum256(append([]byte("eco-guardian/simulation-contributor/v1\x00"), body...))
	return VersionContributor{implementation: hex.EncodeToString(digest[:])}, nil
}

func (c VersionContributor) CapabilityID() string          { return SimulationCapabilityID }
func (c VersionContributor) ContractVersion() string       { return "simulation-v1" }
func (c VersionContributor) ImplementationVersion() string { return c.implementation }
func (c VersionContributor) RegistrationState() versioningrevision.RegistrationState {
	if c.implementation == "" {
		return versioningrevision.Unregistered
	}
	return versioningrevision.Registered
}

var _ versioningrevision.VersionContributor = VersionContributor{}

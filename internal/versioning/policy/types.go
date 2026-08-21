package policy

import (
	"errors"
	"strings"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
)

var (
	ErrInvalidDefinition = errors.New("invalid release policy definition")
	ErrUnknownContract   = errors.New("unknown release policy capability contract")
)

type Metric struct {
	ID       string `json:"id"`
	Required bool   `json:"required"`
}
type Scene struct {
	ID       string   `json:"id"`
	Version  string   `json:"scene_version,omitempty"`
	Seed     *uint64  `json:"seed,omitempty"`
	Required bool     `json:"required"`
	Metrics  []Metric `json:"metrics"`
}

type CapabilityRequirement struct {
	CapabilityID          string `json:"capability_id"`
	GateID                string `json:"gate_id"`
	ContractVersion       string `json:"contract_version"`
	ImplementationVersion string `json:"implementation_version,omitempty"`
}

type Definition struct {
	Scenes       []Scene                 `json:"scenes"`
	Samples      int                     `json:"samples"`
	ThresholdID  string                  `json:"threshold_id"`
	ThresholdOn  bool                    `json:"threshold_enabled"`
	Capabilities []CapabilityRequirement `json:"capabilities"`
}

type ReleasePolicy struct {
	Definition
	ID             domain.ID `json:"id"`
	DisplayVersion int64     `json:"display_version"`
	CanonicalHash  string    `json:"canonical_hash"`
	CreatedAt      time.Time `json:"created_at"`
}

func (p ReleasePolicy) Valid() bool {
	return p.ID.Valid() && p.DisplayVersion > 0 && hash(p.CanonicalHash) && !p.CreatedAt.IsZero() && p.Definition.Valid()
}

func (d Definition) Valid() bool {
	if len(d.Scenes) == 0 || d.Samples < 1 || strings.TrimSpace(d.ThresholdID) == "" || !d.ThresholdOn || len(d.Capabilities) == 0 {
		return false
	}
	seenScenes, seenCapabilities := map[string]struct{}{}, map[string]struct{}{}
	hasRequiredMetric := false
	for _, scene := range d.Scenes {
		if strings.TrimSpace(scene.ID) == "" || (scene.Version != "" && strings.TrimSpace(scene.Version) == "") {
			return false
		}
		if _, ok := seenScenes[scene.ID]; ok {
			return false
		}
		seenScenes[scene.ID] = struct{}{}
		seenMetrics := map[string]struct{}{}
		for _, metric := range scene.Metrics {
			if strings.TrimSpace(metric.ID) == "" {
				return false
			}
			if _, ok := seenMetrics[metric.ID]; ok {
				return false
			}
			seenMetrics[metric.ID] = struct{}{}
			if metric.Required {
				hasRequiredMetric = true
			}
		}
	}
	for _, capability := range d.Capabilities {
		if strings.TrimSpace(capability.CapabilityID) == "" || strings.TrimSpace(capability.GateID) == "" || strings.TrimSpace(capability.ContractVersion) == "" {
			return false
		}
		identity := capability.CapabilityID + "\x00" + capability.GateID
		if _, ok := seenCapabilities[identity]; ok {
			return false
		}
		seenCapabilities[identity] = struct{}{}
	}
	return hasRequiredMetric
}

func (d Definition) ValidateContracts(catalog ContractCatalog) error {
	if !d.Valid() {
		return ErrInvalidDefinition
	}
	if catalog == nil {
		return ErrUnknownContract
	}
	for _, capability := range d.Capabilities {
		if !catalog.SupportsCapabilityContract(capability) {
			return ErrUnknownContract
		}
	}
	return nil
}
func hash(v string) bool {
	if len(v) != 64 {
		return false
	}
	for _, r := range v {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

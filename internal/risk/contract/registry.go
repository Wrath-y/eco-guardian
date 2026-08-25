package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

type ManifestKind string

const (
	ThresholdManifest  ManifestKind = "threshold"
	ComparisonManifest ManifestKind = "comparison"
	CohortManifest     ManifestKind = "cohort"
	StructuralManifest ManifestKind = "structural"
	ReportManifest     ManifestKind = "report"
)

type Manifest struct {
	Kind              ManifestKind       `json:"kind"`
	ID                string             `json:"id"`
	Version           string             `json:"version"`
	SourceHash        string             `json:"source_hash"`
	GoldenHash        string             `json:"golden_hash"`
	Direction         *MetricDirection   `json:"direction,omitempty"`
	Severity          *Severity          `json:"severity,omitempty"`
	Override          OverrideClass      `json:"override_classification"`
	Unit              string             `json:"unit,omitempty"`
	BoundarySemantics string             `json:"boundary_semantics,omitempty"`
	StatusOrder       []ComparisonStatus `json:"status_order,omitempty"`
}

func (manifest Manifest) Valid() bool {
	if manifest.Kind != ThresholdManifest && manifest.Kind != ComparisonManifest && manifest.Kind != CohortManifest && manifest.Kind != StructuralManifest && manifest.Kind != ReportManifest {
		return false
	}
	if strings.TrimSpace(manifest.ID) == "" || strings.TrimSpace(manifest.Version) == "" || !validHash(manifest.SourceHash) || !validHash(manifest.GoldenHash) {
		return false
	}
	if manifest.Severity != nil && *manifest.Severity != Block && *manifest.Severity != Warning && *manifest.Severity != Info {
		return false
	}
	if manifest.Kind == ComparisonManifest {
		if manifest.Direction == nil || (*manifest.Direction != HigherIsRisk && *manifest.Direction != LowerIsRisk && *manifest.Direction != TargetRange) || manifest.Override != NumericOverrideEligible || manifest.Unit != "same_as_metric" || strings.TrimSpace(manifest.BoundarySemantics) == "" {
			return false
		}
		expected := []ComparisonStatus{Comparable, NotComparable, Unavailable, Stale}
		if len(manifest.StatusOrder) != len(expected) {
			return false
		}
		for index := range expected {
			if manifest.StatusOrder[index] != expected[index] {
				return false
			}
		}
		return true
	}
	return manifest.Direction == nil && manifest.Unit == "" && manifest.BoundarySemantics == "" && len(manifest.StatusOrder) == 0 && manifest.Override == NonOverridable
}

type Registry struct{ manifests map[string]Manifest }

type RegistrySet struct {
	Threshold  *Registry
	Comparison *Registry
	Cohort     *Registry
	Structural *Registry
	Report     *Registry
}

func NewRegistry(manifests []Manifest) (*Registry, error) {
	if len(manifests) == 0 {
		return nil, fmt.Errorf("risk manifests are required")
	}
	registry := &Registry{manifests: make(map[string]Manifest, len(manifests))}
	for _, manifest := range manifests {
		if !manifest.Valid() {
			return nil, fmt.Errorf("invalid risk manifest %q", manifest.ID)
		}
		key := manifestKey(manifest)
		if previous, duplicate := registry.manifests[key]; duplicate {
			if previous.SourceHash != manifest.SourceHash || previous.GoldenHash != manifest.GoldenHash {
				return nil, fmt.Errorf("same-version risk manifest drift %q", manifest.ID)
			}
			return nil, fmt.Errorf("duplicate risk manifest %q", manifest.ID)
		}
		registry.manifests[key] = manifest
	}
	return registry, nil
}

func (registry *Registry) Manifests() []Manifest {
	result := make([]Manifest, 0, len(registry.manifests))
	for _, manifest := range registry.manifests {
		result = append(result, manifest)
	}
	sort.Slice(result, func(i, j int) bool { return manifestKey(result[i]) < manifestKey(result[j]) })
	return result
}

func (registry *Registry) Resolve(kind ManifestKind, id, version string) (Manifest, bool) {
	manifest, found := registry.manifests[string(kind)+"\x00"+id+"\x00"+version]
	return manifest, found
}

func NewRegistrySet(manifests []Manifest) (RegistrySet, error) {
	byKind := make(map[ManifestKind][]Manifest, 5)
	for _, manifest := range manifests {
		byKind[manifest.Kind] = append(byKind[manifest.Kind], manifest)
	}
	build := func(kind ManifestKind) (*Registry, error) {
		entries := byKind[kind]
		if len(entries) == 0 {
			return nil, fmt.Errorf("missing %s risk registry", kind)
		}
		return NewRegistry(entries)
	}
	threshold, err := build(ThresholdManifest)
	if err != nil {
		return RegistrySet{}, err
	}
	comparison, err := build(ComparisonManifest)
	if err != nil {
		return RegistrySet{}, err
	}
	cohort, err := build(CohortManifest)
	if err != nil {
		return RegistrySet{}, err
	}
	structural, err := build(StructuralManifest)
	if err != nil {
		return RegistrySet{}, err
	}
	report, err := build(ReportManifest)
	if err != nil {
		return RegistrySet{}, err
	}
	return RegistrySet{Threshold: threshold, Comparison: comparison, Cohort: cohort, Structural: structural, Report: report}, nil
}

func V1Registries() (RegistrySet, error) { return NewRegistrySet(V1Manifests()) }

func manifestKey(manifest Manifest) string {
	return string(manifest.Kind) + "\x00" + manifest.ID + "\x00" + manifest.Version
}

func V1Manifests() []Manifest {
	hash := func(value string) string {
		sum := sha256.Sum256([]byte("eco-guardian/risk-manifest/v1\x00" + value))
		return hex.EncodeToString(sum[:])
	}
	directions := []MetricDirection{HigherIsRisk, LowerIsRisk, TargetRange}
	statusOrder := []ComparisonStatus{Comparable, NotComparable, Unavailable, Stale}
	result := []Manifest{
		{Kind: ThresholdManifest, ID: "risk-threshold-schema", Version: "v1", SourceHash: hash("threshold-source"), GoldenHash: hash("threshold-golden"), Override: NonOverridable},
		{Kind: CohortManifest, ID: "risk-cohort", Version: "v1", SourceHash: hash("cohort-source"), GoldenHash: hash("cohort-golden"), Override: NonOverridable},
		{Kind: StructuralManifest, ID: "risk-structural", Version: "v1", SourceHash: hash("structural-source"), GoldenHash: hash("structural-golden"), Override: NonOverridable},
		{Kind: ReportManifest, ID: "risk-report-schema", Version: "v1", SourceHash: hash("report-source"), GoldenHash: hash("report-golden"), Override: NonOverridable},
	}
	for index, direction := range directions {
		directionCopy := direction
		semantics := "signed-risk-delta;relative-nonzero-baseline;absolute-compatible-unit;block-first-greater-than-or-equal"
		if direction == TargetRange {
			semantics += ";inclusive-target-range-distance"
		}
		result = append(result, Manifest{Kind: ComparisonManifest, ID: "risk-comparison-" + string(direction), Version: "v1", SourceHash: hash(fmt.Sprintf("comparison-source-%d-%s-%s", index, direction, semantics)), GoldenHash: hash(fmt.Sprintf("comparison-golden-%d", index)), Direction: &directionCopy, Override: NumericOverrideEligible, Unit: "same_as_metric", BoundarySemantics: semantics, StatusOrder: append([]ComparisonStatus(nil), statusOrder...)})
	}
	return result
}

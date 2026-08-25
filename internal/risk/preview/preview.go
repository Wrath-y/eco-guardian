// Package preview evaluates advisory simulation results with the same pure
// threshold, comparison, and structural rules used by formal risk reviews.
// It deliberately has no Job, report repository, Gate, or persistence ports.
package preview

import (
	"context"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/zouyi/eco-guardian/internal/domain"
	riskcomparison "github.com/zouyi/eco-guardian/internal/risk/comparison"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
	riskstructure "github.com/zouyi/eco-guardian/internal/risk/structure"
	"github.com/zouyi/eco-guardian/internal/risk/threshold"
	"github.com/zouyi/eco-guardian/internal/validation"
	versioningdiff "github.com/zouyi/eco-guardian/internal/versioning/diff"
)

const SchemaVersionV1 = "v1"

var (
	ErrInvalid     = errors.New("risk preview is invalid")
	ErrUnavailable = errors.New("risk preview implementation is unavailable")
)

type EvidenceKind string

const (
	AdvisorySimulation EvidenceKind = "advisory_simulation"
	FormalSimulation   EvidenceKind = "formal_simulation"
)

// EvidenceRef is intentionally not a formal simulation RunRef. Candidate
// previews use an opaque advisory identity and therefore cannot be consumed by
// the release Gate or mistaken for an inserted simulation run.
type EvidenceRef struct {
	Kind            EvidenceKind `json:"kind"`
	ID              string       `json:"id"`
	InputHash       string       `json:"input_hash"`
	FingerprintHash string       `json:"fingerprint_hash"`
	ResultHash      string       `json:"result_hash"`
}

func (ref EvidenceRef) valid() bool {
	return (ref.Kind == AdvisorySimulation || ref.Kind == FormalSimulation) && stableID(ref.ID) && validHash(ref.InputHash) && validHash(ref.FingerprintHash) && validHash(ref.ResultHash)
}

type MetricComparison struct {
	ID                string                      `json:"id"`
	Role              riskcontract.PolicyRole     `json:"role"`
	SceneID           string                      `json:"scene_id"`
	SceneVersion      string                      `json:"scene_version"`
	Subject           riskcontract.Subject        `json:"subject"`
	Candidate         riskcontract.MetricEvidence `json:"candidate"`
	Baseline          riskcontract.MetricEvidence `json:"baseline"`
	CandidateEvidence EvidenceRef                 `json:"candidate_evidence"`
	BaselineEvidence  EvidenceRef                 `json:"baseline_evidence"`
	StaleReason       string                      `json:"stale_reason,omitempty"`
}

func (comparison MetricComparison) valid() bool {
	return stableID(comparison.ID) && (comparison.Role == riskcontract.Required || comparison.Role == riskcontract.Optional) && stableID(comparison.SceneID) && stableID(comparison.SceneVersion) && comparison.Subject.Valid() && comparison.Candidate.Valid() && comparison.Baseline.Valid() && comparison.CandidateEvidence.valid() && comparison.CandidateEvidence.Kind == AdvisorySimulation && comparison.BaselineEvidence.valid()
}

type StructuralInput struct {
	Changes          []versioningdiff.FieldChange    `json:"changes"`
	BaselineIndexes  []validation.FormulaIndexRecord `json:"baseline_indexes"`
	CandidateIndexes []validation.FormulaIndexRecord `json:"candidate_indexes"`
	Versions         riskstructure.IndexContract     `json:"versions"`
}

type Request struct {
	SchemaVersion               string                `json:"schema_version"`
	ProjectID                   domain.ID             `json:"project_id"`
	ProposalMaterializationHash string                `json:"proposal_materialization_hash"`
	ThresholdIdentity           riskcontract.Identity `json:"threshold_identity"`
	ThresholdBody               threshold.Body        `json:"threshold_body"`
	Comparisons                 []MetricComparison    `json:"comparisons"`
	Structural                  StructuralInput       `json:"structural"`
}

type MetricResult struct {
	ID                string                      `json:"id"`
	Role              riskcontract.PolicyRole     `json:"role"`
	SceneID           string                      `json:"scene_id"`
	SceneVersion      string                      `json:"scene_version"`
	Subject           riskcontract.Subject        `json:"subject"`
	Candidate         riskcontract.MetricEvidence `json:"candidate"`
	Baseline          riskcontract.MetricEvidence `json:"baseline"`
	CandidateEvidence EvidenceRef                 `json:"candidate_evidence"`
	BaselineEvidence  EvidenceRef                 `json:"baseline_evidence"`
	Threshold         threshold.Resolution        `json:"threshold"`
	Rule              riskcontract.Identity       `json:"rule"`
	Assessment        riskcomparison.Assessment   `json:"assessment"`
	EvidenceHash      string                      `json:"evidence_hash"`
}

type StructuralResult struct {
	Role    riskcontract.PolicyRole `json:"role"`
	Finding riskstructure.Finding   `json:"finding"`
}

type ResultV1 struct {
	SchemaVersion               string                  `json:"schema_version"`
	Advisory                    bool                    `json:"advisory"`
	ProposalMaterializationHash string                  `json:"proposal_materialization_hash"`
	InputHash                   string                  `json:"input_hash"`
	RegistryManifests           []riskcontract.Manifest `json:"registry_manifests"`
	Metrics                     []MetricResult          `json:"metrics"`
	Structural                  []StructuralResult      `json:"structural"`
	Acceptable                  bool                    `json:"acceptable"`
	ResultHash                  string                  `json:"result_hash"`
}

type Evaluator interface {
	Evaluate(context.Context, Request) (ResultV1, error)
}

type Service struct{ Registries riskcontract.RegistrySet }

func (service Service) Evaluate(ctx context.Context, request Request) (ResultV1, error) {
	if err := ctx.Err(); err != nil {
		return ResultV1{}, err
	}
	normalized, err := normalizeRequest(request)
	if err != nil {
		return ResultV1{}, err
	}
	manifests, err := resolveManifests(service.Registries, normalized)
	if err != nil {
		return ResultV1{}, err
	}
	inputHash, _, err := riskcontract.CanonicalHash("eco-guardian/risk-preview-input/v1", struct {
		Request   Request                 `json:"request"`
		Manifests []riskcontract.Manifest `json:"registry_manifests"`
	}{normalized, manifests})
	if err != nil {
		return ResultV1{}, errors.Join(ErrInvalid, err)
	}
	metricResults := make([]MetricResult, 0, len(normalized.Comparisons))
	acceptable := true
	for _, item := range normalized.Comparisons {
		if err = ctx.Err(); err != nil {
			return ResultV1{}, err
		}
		resolution, resolveErr := threshold.Resolve(normalized.ThresholdBody, thresholdScope(item), item.Subject.Kind == riskcontract.SingletonSubject)
		if resolveErr != nil {
			return ResultV1{}, errors.Join(ErrUnavailable, resolveErr)
		}
		manifest, found := service.Registries.Comparison.Resolve(riskcontract.ComparisonManifest, "risk-comparison-"+string(item.Candidate.Direction), riskcomparison.RuleVersionV1)
		if !found {
			return ResultV1{}, ErrUnavailable
		}
		rule := riskcontract.Identity{ID: manifest.ID, Version: manifest.Version, Hash: manifest.SourceHash}
		assessment := riskcomparison.Assess(riskcomparison.AssessmentRequest{Role: item.Role, Candidate: item.Candidate, Baseline: item.Baseline, Threshold: resolution.Entry, StaleReason: item.StaleReason, ContractValid: item.valid()})
		result := MetricResult{ID: item.ID, Role: item.Role, SceneID: item.SceneID, SceneVersion: item.SceneVersion, Subject: cloneSubject(item.Subject), Candidate: cloneMetric(item.Candidate), Baseline: cloneMetric(item.Baseline), CandidateEvidence: item.CandidateEvidence, BaselineEvidence: item.BaselineEvidence, Threshold: resolution, Rule: rule, Assessment: assessment}
		result.EvidenceHash, _, err = riskcontract.CanonicalHash("eco-guardian/risk-preview-comparison/v1", result)
		if err != nil {
			return ResultV1{}, err
		}
		if assessment.PolicyEffect == nil || *assessment.PolicyEffect == riskcontract.Block {
			acceptable = false
		}
		metricResults = append(metricResults, result)
	}
	findings := riskstructure.Analyze(normalized.Structural.Changes, normalized.Structural.BaselineIndexes, normalized.Structural.CandidateIndexes, normalized.Structural.Versions, riskstructure.V1Rules())
	structuralResults := make([]StructuralResult, 0, len(findings))
	for _, finding := range findings {
		if !finding.Valid() || finding.Status != riskcontract.Comparable || finding.Severity == riskcontract.Block {
			acceptable = false
		}
		structuralResults = append(structuralResults, StructuralResult{Role: riskcontract.Required, Finding: finding})
	}
	result := ResultV1{SchemaVersion: SchemaVersionV1, Advisory: true, ProposalMaterializationHash: normalized.ProposalMaterializationHash, InputHash: inputHash, RegistryManifests: manifests, Metrics: metricResults, Structural: structuralResults, Acceptable: acceptable}
	result.ResultHash, _, err = riskcontract.CanonicalHash("eco-guardian/risk-preview-result/v1", result)
	if err != nil {
		return ResultV1{}, err
	}
	return result, nil
}

func normalizeRequest(request Request) (Request, error) {
	if request.SchemaVersion != SchemaVersionV1 || !request.ProjectID.Valid() || !validHash(request.ProposalMaterializationHash) || !request.ThresholdIdentity.Valid() || (len(request.Comparisons) == 0 && len(request.Structural.Changes) == 0) {
		return Request{}, ErrInvalid
	}
	body, err := request.ThresholdBody.Normalize()
	if err != nil {
		return Request{}, errors.Join(ErrInvalid, err)
	}
	bodyHash, err := body.Hash()
	if err != nil || bodyHash != request.ThresholdIdentity.Hash {
		return Request{}, ErrInvalid
	}
	copy := request
	copy.ThresholdBody = body
	copy.Comparisons = append([]MetricComparison(nil), request.Comparisons...)
	for index := range copy.Comparisons {
		copy.Comparisons[index].Subject = cloneSubject(copy.Comparisons[index].Subject)
		copy.Comparisons[index].Candidate = normalizeMetric(copy.Comparisons[index].Candidate)
		copy.Comparisons[index].Baseline = normalizeMetric(copy.Comparisons[index].Baseline)
		if !copy.Comparisons[index].valid() {
			return Request{}, ErrInvalid
		}
	}
	sort.Slice(copy.Comparisons, func(i, j int) bool { return copy.Comparisons[i].ID < copy.Comparisons[j].ID })
	for index := 1; index < len(copy.Comparisons); index++ {
		if copy.Comparisons[index-1].ID == copy.Comparisons[index].ID {
			return Request{}, ErrInvalid
		}
	}
	copy.Structural = normalizeStructural(request.Structural)
	return copy, nil
}

func resolveManifests(registries riskcontract.RegistrySet, request Request) ([]riskcontract.Manifest, error) {
	if registries.Threshold == nil || registries.Comparison == nil || registries.Structural == nil {
		return nil, ErrUnavailable
	}
	thresholdManifest, thresholdFound := registries.Threshold.Resolve(riskcontract.ThresholdManifest, "risk-threshold-schema", SchemaVersionV1)
	structuralManifest, structuralFound := registries.Structural.Resolve(riskcontract.StructuralManifest, "risk-structural", SchemaVersionV1)
	if !thresholdFound || !structuralFound || !containsIdentity(request.ThresholdBody.StructuralRuleVersions, riskcontract.Identity{ID: structuralManifest.ID, Version: structuralManifest.Version, Hash: structuralManifest.SourceHash}) {
		return nil, ErrUnavailable
	}
	byKey := map[string]riskcontract.Manifest{
		manifestKey(thresholdManifest):  thresholdManifest,
		manifestKey(structuralManifest): structuralManifest,
	}
	for _, item := range request.Comparisons {
		manifest, found := registries.Comparison.Resolve(riskcontract.ComparisonManifest, "risk-comparison-"+string(item.Candidate.Direction), riskcomparison.RuleVersionV1)
		if !found {
			return nil, ErrUnavailable
		}
		byKey[manifestKey(manifest)] = manifest
	}
	result := make([]riskcontract.Manifest, 0, len(byKey))
	for _, manifest := range byKey {
		result = append(result, manifest)
	}
	sort.Slice(result, func(i, j int) bool { return manifestKey(result[i]) < manifestKey(result[j]) })
	return result, nil
}

func thresholdScope(item MetricComparison) riskcontract.ThresholdScope {
	var group *string
	if item.Subject.Kind == riskcontract.CohortSubject {
		value := item.Subject.BalanceGroup
		group = &value
	}
	return riskcontract.ThresholdScope{SceneID: item.SceneID, SceneVersion: item.SceneVersion, MetricID: item.Candidate.MetricID, MetricVersion: item.Candidate.MetricVersion, BalanceGroup: group, Unit: item.Candidate.Unit, Direction: item.Candidate.Direction}
}

func normalizeStructural(input StructuralInput) StructuralInput {
	copy := input
	copy.Changes = append([]versioningdiff.FieldChange(nil), input.Changes...)
	for index := range copy.Changes {
		copy.Changes[index].OldValue = append([]byte(nil), copy.Changes[index].OldValue...)
		copy.Changes[index].NewValue = append([]byte(nil), copy.Changes[index].NewValue...)
	}
	sort.Slice(copy.Changes, func(i, j int) bool { return changeKey(copy.Changes[i]) < changeKey(copy.Changes[j]) })
	copy.BaselineIndexes = cloneIndexes(input.BaselineIndexes)
	copy.CandidateIndexes = cloneIndexes(input.CandidateIndexes)
	return copy
}

func cloneIndexes(indexes []validation.FormulaIndexRecord) []validation.FormulaIndexRecord {
	copy := append([]validation.FormulaIndexRecord(nil), indexes...)
	for index := range copy {
		copy[index].AST = append([]byte(nil), copy[index].AST...)
		copy[index].Reads = append([]validation.FormulaRead(nil), copy[index].Reads...)
	}
	sort.Slice(copy, func(i, j int) bool { return indexKey(copy[i]) < indexKey(copy[j]) })
	return copy
}

func normalizeMetric(value riskcontract.MetricEvidence) riskcontract.MetricEvidence {
	copy := cloneMetric(value)
	sort.Strings(copy.Assumptions)
	if copy.Assumptions == nil {
		copy.Assumptions = []string{}
	}
	if copy.Unavailable != nil {
		sort.Strings(copy.Unavailable.Missing)
	}
	return copy
}

func cloneMetric(value riskcontract.MetricEvidence) riskcontract.MetricEvidence {
	copy := value
	copy.Assumptions = append([]string(nil), value.Assumptions...)
	if value.TargetRange != nil {
		target := *value.TargetRange
		copy.TargetRange = &target
	}
	if value.Unavailable != nil {
		reason := *value.Unavailable
		reason.Missing = append([]string(nil), value.Unavailable.Missing...)
		copy.Unavailable = &reason
	}
	return copy
}

func cloneSubject(value riskcontract.Subject) riskcontract.Subject {
	value.Members = append([]domain.ID(nil), value.Members...)
	sort.Slice(value.Members, func(i, j int) bool { return value.Members[i] < value.Members[j] })
	return value
}

func containsIdentity(values []riskcontract.Identity, expected riskcontract.Identity) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func manifestKey(value riskcontract.Manifest) string {
	return string(value.Kind) + "\x00" + value.ID + "\x00" + value.Version
}

func changeKey(value versioningdiff.FieldChange) string {
	return string(value.EntityID) + "\x00" + value.Path + "\x00" + string(value.Kind)
}

func indexKey(value validation.FormulaIndexRecord) string {
	return string(value.SourceID) + "\x00" + value.FieldPath + "\x00" + string(value.OutputAttributeID)
}

func stableID(value string) bool {
	return strings.TrimSpace(value) != "" && utf8.ValidString(value) && len(value) <= 256 && !strings.ContainsAny(value, "\x00\r\n")
}

func validHash(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

var _ Evaluator = Service{}

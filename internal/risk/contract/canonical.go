package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

const inputDomainV1 = "eco-guardian/risk-input/v1\x00"
const impactDomainV1 = "eco-guardian/risk-impact-evidence/v1\x00"

func CanonicalHash(domainSeparator string, value any) (string, []byte, error) {
	if strings.TrimSpace(domainSeparator) == "" {
		return "", nil, fmt.Errorf("canonical hash domain is required")
	}
	body, err := domain.CanonicalJSON(value)
	if err != nil {
		return "", nil, err
	}
	bytes := append([]byte(domainSeparator+"\x00"), body...)
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:]), bytes, nil
}

func CalculationItemHash(item RiskItemV1) (string, error) {
	if !item.Valid() {
		return "", fmt.Errorf("invalid risk item")
	}
	hash, _, err := CanonicalHash("eco-guardian/risk-item/v1", item)
	return hash, err
}

func ThresholdBodyHash(body any) (string, error) {
	hash, _, err := CanonicalHash("eco-guardian/risk-threshold/v1", body)
	return hash, err
}

func ReportHash(inputHash string, items []RiskItemV1) (string, error) {
	if !validHash(inputHash) {
		return "", fmt.Errorf("invalid risk report")
	}
	copy := append([]RiskItemV1(nil), items...)
	sort.Slice(copy, func(i, j int) bool { return copy[i].Ordinal < copy[j].Ordinal })
	seenIDs := make(map[string]struct{}, len(copy))
	for index, item := range copy {
		if !item.Valid() || (index > 0 && copy[index-1].Ordinal == item.Ordinal) {
			return "", fmt.Errorf("invalid or duplicate risk report item ordinal")
		}
		if _, duplicate := seenIDs[item.ID]; duplicate {
			return "", fmt.Errorf("duplicate risk report item identity")
		}
		seenIDs[item.ID] = struct{}{}
	}
	hash, _, err := CanonicalHash("eco-guardian/risk-report/v1", struct {
		InputHash string       `json:"input_hash"`
		Items     []RiskItemV1 `json:"items"`
	}{inputHash, copy})
	return hash, err
}

func (input RiskInputV1) Normalize() (RiskInputV1, error) {
	copy := input
	copy.Candidate = normalizeRevision(input.Candidate)
	if input.Baseline.Revision != nil {
		revision := normalizeRevision(*input.Baseline.Revision)
		copy.Baseline.Revision = &revision
	}
	copy.PolicyRequirements = append([]PolicyRequirement(nil), input.PolicyRequirements...)
	sort.Slice(copy.PolicyRequirements, func(i, j int) bool {
		return requirementKey(copy.PolicyRequirements[i]) < requirementKey(copy.PolicyRequirements[j])
	})
	copy.Implementations = append([]Identity(nil), input.Implementations...)
	sort.Slice(copy.Implementations, func(i, j int) bool {
		return identityKey(copy.Implementations[i]) < identityKey(copy.Implementations[j])
	})
	copy.Subjects = append([]Subject(nil), input.Subjects...)
	for index := range copy.Subjects {
		copy.Subjects[index].Members = append([]domain.ID(nil), copy.Subjects[index].Members...)
		sort.Slice(copy.Subjects[index].Members, func(i, j int) bool { return copy.Subjects[index].Members[i] < copy.Subjects[index].Members[j] })
	}
	sort.Slice(copy.Subjects, func(i, j int) bool { return copy.Subjects[i].Key() < copy.Subjects[j].Key() })
	copy.CandidateRuns = normalizeRuns(input.CandidateRuns)
	copy.BaselineRuns = normalizeRuns(input.BaselineRuns)
	if copy.BaselineRuns == nil {
		copy.BaselineRuns = []RunEvidence{}
	}
	if !copy.Valid() {
		return RiskInputV1{}, fmt.Errorf("invalid risk input")
	}
	return copy, nil
}

func (input RiskInputV1) Valid() bool {
	if input.SchemaVersion != "v1" || !input.ProjectID.Valid() || !input.Candidate.Valid() || !input.Baseline.Valid() || !input.Policy.Valid() || !input.Threshold.Valid() || !input.Validation.Valid() || len(input.PolicyRequirements) == 0 || len(input.Implementations) == 0 || len(input.Subjects) == 0 || len(input.CandidateRuns) == 0 || strings.TrimSpace(input.ComparisonVersion) == "" || strings.TrimSpace(input.CohortVersion) == "" || strings.TrimSpace(input.StructuralVersion) == "" || strings.TrimSpace(input.ReportSchemaVersion) == "" {
		return false
	}
	if input.Baseline.Kind == NoBaseline && len(input.BaselineRuns) != 0 {
		return false
	}
	if input.Baseline.Kind == BaselineCurrent && len(input.BaselineRuns) == 0 {
		return false
	}
	seenRequirements := make(map[string]struct{}, len(input.PolicyRequirements))
	for _, requirement := range input.PolicyRequirements {
		key := requirementKey(requirement)
		if !requirement.Valid() {
			return false
		}
		if _, duplicate := seenRequirements[key]; duplicate {
			return false
		}
		seenRequirements[key] = struct{}{}
	}
	seenImplementations := make(map[string]struct{}, len(input.Implementations))
	for _, implementation := range input.Implementations {
		if !implementation.Valid() {
			return false
		}
		if _, duplicate := seenImplementations[implementation.ID]; duplicate {
			return false
		}
		seenImplementations[implementation.ID] = struct{}{}
	}
	seenSubjects := make(map[string]struct{}, len(input.Subjects))
	for _, subject := range input.Subjects {
		if !subject.Valid() {
			return false
		}
		if _, duplicate := seenSubjects[subject.Key()]; duplicate {
			return false
		}
		seenSubjects[subject.Key()] = struct{}{}
	}
	for _, run := range input.CandidateRuns {
		if !run.Valid() || !sameRevision(run.Revision, input.Candidate) {
			return false
		}
	}
	for _, run := range input.BaselineRuns {
		if !run.Valid() || input.Baseline.Revision == nil || !sameRevision(run.Revision, *input.Baseline.Revision) {
			return false
		}
	}
	return true
}

func normalizeRuns(runs []RunEvidence) []RunEvidence {
	copy := append([]RunEvidence(nil), runs...)
	for index := range copy {
		copy[index].Revision = normalizeRevision(copy[index].Revision)
		copy[index].Participants = append([]domain.ID(nil), copy[index].Participants...)
		sort.Slice(copy[index].Participants, func(i, j int) bool { return copy[index].Participants[i] < copy[index].Participants[j] })
		copy[index].Implementations = append([]Identity(nil), copy[index].Implementations...)
		sort.Slice(copy[index].Implementations, func(i, j int) bool {
			return identityKey(copy[index].Implementations[i]) < identityKey(copy[index].Implementations[j])
		})
		copy[index].Metrics = append([]MetricEvidence(nil), copy[index].Metrics...)
		for metricIndex := range copy[index].Metrics {
			copy[index].Metrics[metricIndex].Assumptions = append([]string(nil), copy[index].Metrics[metricIndex].Assumptions...)
			if copy[index].Metrics[metricIndex].Assumptions == nil {
				copy[index].Metrics[metricIndex].Assumptions = []string{}
			}
			sort.Strings(copy[index].Metrics[metricIndex].Assumptions)
			if copy[index].Metrics[metricIndex].Unavailable != nil {
				reason := *copy[index].Metrics[metricIndex].Unavailable
				reason.Missing = append([]string(nil), reason.Missing...)
				if reason.Missing == nil {
					reason.Missing = []string{}
				}
				sort.Strings(reason.Missing)
				copy[index].Metrics[metricIndex].Unavailable = &reason
			}
		}
		sort.Slice(copy[index].Metrics, func(i, j int) bool {
			return copy[index].Metrics[i].MetricID+"\x00"+copy[index].Metrics[i].MetricVersion < copy[index].Metrics[j].MetricID+"\x00"+copy[index].Metrics[j].MetricVersion
		})
	}
	sort.Slice(copy, func(i, j int) bool {
		return copy[i].SceneID+"\x00"+copy[i].SceneVersion+"\x00"+string(copy[i].RunID) < copy[j].SceneID+"\x00"+copy[j].SceneVersion+"\x00"+string(copy[j].RunID)
	})
	return copy
}

func normalizeRevision(revision RevisionIdentity) RevisionIdentity {
	copy := revision
	copy.Manifest.Entries = append([]versioningrevision.VersionEntry(nil), revision.Manifest.Entries...)
	sort.Slice(copy.Manifest.Entries, func(i, j int) bool {
		return copy.Manifest.Entries[i].CapabilityID < copy.Manifest.Entries[j].CapabilityID
	})
	return copy
}

func requirementKey(requirement PolicyRequirement) string {
	return requirement.SceneID + "\x00" + requirement.SceneVersion + "\x00" + requirement.MetricID + "\x00" + requirement.MetricVersion
}

func identityKey(identity Identity) string {
	return identity.ID + "\x00" + identity.Version + "\x00" + identity.Hash
}

func sameRevision(left, right RevisionIdentity) bool {
	return left.RevisionID == right.RevisionID && left.ConfigHash == right.ConfigHash && left.ManifestHash == right.ManifestHash
}

func (input RiskInputV1) CanonicalBytes() ([]byte, error) {
	normalized, err := input.Normalize()
	if err != nil {
		return nil, err
	}
	body, err := domain.CanonicalJSON(normalized)
	if err != nil {
		return nil, err
	}
	return append([]byte(inputDomainV1), body...), nil
}

func (input RiskInputV1) Hash() (string, error) {
	body, err := input.CanonicalBytes()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func ImpactEvidenceHash(refs []ImpactEvidenceRef) (string, error) {
	copy := append([]ImpactEvidenceRef(nil), refs...)
	for _, ref := range copy {
		if !ref.Valid() {
			return "", fmt.Errorf("invalid impact evidence reference")
		}
	}
	sort.Slice(copy, func(i, j int) bool {
		return copy[i].ReportID+"\x00"+copy[i].EvidenceID < copy[j].ReportID+"\x00"+copy[j].EvidenceID
	})
	body, err := domain.CanonicalJSON(copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte(impactDomainV1), body...))
	return hex.EncodeToString(sum[:]), nil
}

func ExplanationEvidenceManifestHash(refs []ImpactEvidenceRef) (string, error) {
	return ImpactEvidenceHash(refs)
}

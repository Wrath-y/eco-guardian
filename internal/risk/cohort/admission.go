package cohort

import (
	"errors"
	"sort"

	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
)

var ErrAdmission = errors.New("risk simulation admission mismatch")

type RunContract struct {
	Revision        riskcontract.RevisionIdentity
	SceneID         string
	SceneVersion    string
	SampleCount     int
	Seed            uint64
	InputHash       string
	FingerprintHash string
	ResultHash      string
	Implementations []riskcontract.Identity
	Metrics         []riskcontract.PolicyRequirement
}

func AdmitRun(run riskcontract.RunEvidence, expected RunContract, subjects []riskcontract.Subject) error {
	if !run.Valid() || !sameRevision(run.Revision, expected.Revision) || run.SceneID != expected.SceneID || run.SceneVersion != expected.SceneVersion || run.SampleCount != expected.SampleCount || run.Seed != expected.Seed || run.InputHash != expected.InputHash || run.FingerprintHash != expected.FingerprintHash || run.ResultHash != expected.ResultHash || !sameIdentities(run.Implementations, expected.Implementations) {
		return ErrAdmission
	}
	expectedParticipants := make([]string, 0)
	seenParticipants := map[string]struct{}{}
	for _, subject := range subjects {
		if !subject.Valid() {
			return ErrAdmission
		}
		for _, member := range subject.Members {
			if _, found := seenParticipants[string(member)]; !found {
				expectedParticipants = append(expectedParticipants, string(member))
				seenParticipants[string(member)] = struct{}{}
			}
		}
	}
	actualParticipants := make([]string, len(run.Participants))
	for index, participant := range run.Participants {
		actualParticipants[index] = string(participant)
	}
	sort.Strings(expectedParticipants)
	sort.Strings(actualParticipants)
	if !sameStrings(expectedParticipants, actualParticipants) {
		return ErrAdmission
	}
	expectedMetrics := make([]string, 0, len(expected.Metrics))
	for _, requirement := range expected.Metrics {
		if !requirement.Valid() || requirement.SceneID != expected.SceneID || requirement.SceneVersion != expected.SceneVersion {
			return ErrAdmission
		}
		expectedMetrics = append(expectedMetrics, requirement.MetricID+"\x00"+requirement.MetricVersion)
	}
	actualMetrics := make([]string, 0, len(run.Metrics))
	for _, metric := range run.Metrics {
		actualMetrics = append(actualMetrics, metric.MetricID+"\x00"+metric.MetricVersion)
	}
	sort.Strings(expectedMetrics)
	sort.Strings(actualMetrics)
	if !sameStrings(expectedMetrics, actualMetrics) {
		return ErrAdmission
	}
	return nil
}

func AdmitSets(kind riskcontract.BaselineKind, candidateRuns []riskcontract.RunEvidence, candidateContracts []RunContract, baselineRuns []riskcontract.RunEvidence, baselineContracts []RunContract, subjects []riskcontract.Subject) error {
	if len(candidateRuns) == 0 || len(candidateRuns) != len(candidateContracts) {
		return ErrAdmission
	}
	if kind == riskcontract.NoBaseline {
		if len(baselineRuns) != 0 || len(baselineContracts) != 0 {
			return ErrAdmission
		}
	} else if kind != riskcontract.BaselineCurrent || len(baselineRuns) == 0 || len(baselineRuns) != len(baselineContracts) {
		return ErrAdmission
	}
	for index := range candidateRuns {
		if err := AdmitRun(candidateRuns[index], candidateContracts[index], subjects); err != nil {
			return err
		}
	}
	for index := range baselineRuns {
		if err := AdmitRun(baselineRuns[index], baselineContracts[index], subjects); err != nil {
			return err
		}
	}
	return nil
}

func sameRevision(left, right riskcontract.RevisionIdentity) bool {
	return left.RevisionID == right.RevisionID && left.ConfigHash == right.ConfigHash && left.ManifestHash == right.ManifestHash
}

func sameIdentities(left, right []riskcontract.Identity) bool {
	if len(left) != len(right) {
		return false
	}
	key := func(identity riskcontract.Identity) string {
		return identity.ID + "\x00" + identity.Version + "\x00" + identity.Hash
	}
	leftKeys, rightKeys := make([]string, len(left)), make([]string, len(right))
	for index := range left {
		leftKeys[index], rightKeys[index] = key(left[index]), key(right[index])
	}
	sort.Strings(leftKeys)
	sort.Strings(rightKeys)
	return sameStrings(leftKeys, rightKeys)
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

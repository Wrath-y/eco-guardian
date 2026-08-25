package cohort

import (
	"sort"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
)

type Status string

const (
	Resolved       Status = "RESOLVED"
	Missing        Status = "MISSING"
	MemberMismatch Status = "MEMBER_MISMATCH"
	KindMismatch   Status = "KIND_MISMATCH"
	GroupMismatch  Status = "GROUP_MISMATCH"
)

type Issue struct {
	Status       Status    `json:"status"`
	StableID     domain.ID `json:"stable_id,omitempty"`
	EntityKind   string    `json:"entity_kind,omitempty"`
	BalanceGroup string    `json:"balance_group,omitempty"`
	Reason       string    `json:"reason"`
}

type Materialization struct {
	Subjects []riskcontract.Subject `json:"subjects"`
	Issues   []Issue                `json:"issues"`
}

// Materialize resolves only immutable IDs, kind and balance_group fields. Name,
// tag, payload, Graph and similarity data are deliberately ignored.
func Materialize(entities []domain.Entity, participantIDs []domain.ID) Materialization {
	active := make(map[domain.ID]domain.Entity, len(entities))
	groups := make(map[string][]domain.ID)
	for _, entity := range entities {
		if !entity.ID.Valid() || !entity.Kind.Valid() || entity.Status != domain.StatusActive {
			continue
		}
		active[entity.ID] = entity
		if strings.TrimSpace(entity.BalanceGroup) != "" {
			key := string(entity.Kind) + "\x00" + entity.BalanceGroup
			groups[key] = append(groups[key], entity.ID)
		}
	}
	for key := range groups {
		sort.Slice(groups[key], func(i, j int) bool { return groups[key][i] < groups[key][j] })
	}
	seenParticipants := make(map[domain.ID]struct{}, len(participantIDs))
	seenSubjects := make(map[string]struct{})
	result := Materialization{Subjects: []riskcontract.Subject{}, Issues: []Issue{}}
	for _, participantID := range participantIDs {
		if !participantID.Valid() {
			result.Issues = append(result.Issues, Issue{Status: Missing, StableID: participantID, Reason: "PARTICIPANT_ID_INVALID"})
			continue
		}
		if _, duplicate := seenParticipants[participantID]; duplicate {
			result.Issues = append(result.Issues, Issue{Status: MemberMismatch, StableID: participantID, Reason: "PARTICIPANT_DUPLICATE"})
			continue
		}
		seenParticipants[participantID] = struct{}{}
		entity, found := active[participantID]
		if !found {
			result.Issues = append(result.Issues, Issue{Status: Missing, StableID: participantID, Reason: "PARTICIPANT_NOT_IN_REVISION"})
			continue
		}
		var subject riskcontract.Subject
		if strings.TrimSpace(entity.BalanceGroup) == "" {
			subject = riskcontract.Subject{Kind: riskcontract.SingletonSubject, EntityKind: string(entity.Kind), StableID: entity.ID, Members: []domain.ID{entity.ID}}
		} else {
			subject = riskcontract.Subject{Kind: riskcontract.CohortSubject, EntityKind: string(entity.Kind), BalanceGroup: entity.BalanceGroup, Members: append([]domain.ID(nil), groups[string(entity.Kind)+"\x00"+entity.BalanceGroup]...)}
		}
		if _, duplicate := seenSubjects[subject.Key()]; duplicate {
			continue
		}
		seenSubjects[subject.Key()] = struct{}{}
		result.Subjects = append(result.Subjects, subject)
	}
	sort.Slice(result.Subjects, func(i, j int) bool { return result.Subjects[i].Key() < result.Subjects[j].Key() })
	sort.Slice(result.Issues, func(i, j int) bool { return issueKey(result.Issues[i]) < issueKey(result.Issues[j]) })
	return result
}

type Pairing struct {
	Candidate riskcontract.Subject  `json:"candidate"`
	Baseline  *riskcontract.Subject `json:"baseline"`
	Status    Status                `json:"status"`
	Reason    string                `json:"reason,omitempty"`
}

func Pair(candidate, baseline []riskcontract.Subject) []Pairing {
	baselineByKey := make(map[string]riskcontract.Subject, len(baseline))
	for _, subject := range baseline {
		baselineByKey[subject.Key()] = subject
	}
	result := make([]Pairing, 0, len(candidate))
	for _, subject := range candidate {
		paired, found := baselineByKey[subject.Key()]
		if found {
			status, reason := Resolved, ""
			if !sameMembers(subject.Members, paired.Members) {
				status, reason = MemberMismatch, "COHORT_MEMBERS_CHANGED"
			}
			pairedCopy := paired
			result = append(result, Pairing{Candidate: subject, Baseline: &pairedCopy, Status: status, Reason: reason})
			continue
		}
		status, reason := Missing, "BASELINE_SUBJECT_MISSING"
		for _, other := range baseline {
			if subject.Kind == riskcontract.SingletonSubject && other.StableID == subject.StableID && other.EntityKind != subject.EntityKind {
				status, reason = KindMismatch, "BASELINE_ENTITY_KIND_CHANGED"
				break
			}
			if subject.Kind == riskcontract.CohortSubject && other.BalanceGroup == subject.BalanceGroup && other.EntityKind != subject.EntityKind {
				status, reason = KindMismatch, "BASELINE_COHORT_KIND_CHANGED"
				break
			}
			if subject.Kind == riskcontract.CohortSubject && other.EntityKind == subject.EntityKind && overlaps(subject.Members, other.Members) {
				status, reason = GroupMismatch, "BASELINE_BALANCE_GROUP_CHANGED"
			}
		}
		result = append(result, Pairing{Candidate: subject, Status: status, Reason: reason})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Candidate.Key() < result[j].Candidate.Key() })
	return result
}

func sameMembers(left, right []domain.ID) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy, rightCopy := append([]domain.ID(nil), left...), append([]domain.ID(nil), right...)
	sort.Slice(leftCopy, func(i, j int) bool { return leftCopy[i] < leftCopy[j] })
	sort.Slice(rightCopy, func(i, j int) bool { return rightCopy[i] < rightCopy[j] })
	for index := range leftCopy {
		if leftCopy[index] != rightCopy[index] {
			return false
		}
	}
	return true
}

func overlaps(left, right []domain.ID) bool {
	seen := make(map[domain.ID]struct{}, len(left))
	for _, id := range left {
		seen[id] = struct{}{}
	}
	for _, id := range right {
		if _, found := seen[id]; found {
			return true
		}
	}
	return false
}

func issueKey(issue Issue) string {
	return string(issue.Status) + "\x00" + string(issue.StableID) + "\x00" + issue.EntityKind + "\x00" + issue.BalanceGroup
}

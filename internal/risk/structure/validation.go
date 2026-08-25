package structure

import (
	"fmt"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
	"github.com/zouyi/eco-guardian/internal/validation"
)

var supportedValidationCodes = map[string]struct{}{
	"STATIC_FORMULA_CYCLE": {},
	"EVENT_LOOP_UNBOUNDED": {},
	"STACK_UNBOUNDED":      {},
}

type ValidationExpectation struct {
	RevisionID domain.ID
	ConfigHash string
	RunID      domain.ID
	ResultHash string
	Versions   validation.VersionManifest
}

func ValidationEvidence(run validation.ValidationRun, issues []validation.Issue, expected ValidationExpectation) ([]Finding, error) {
	if !expected.RevisionID.Valid() || !expected.RunID.Valid() || !validHash(expected.ConfigHash) || !validHash(expected.ResultHash) || !expected.Versions.Valid() || run.ID != expected.RunID || run.Source.Kind != validation.SourceRevision || run.Source.RevisionID != expected.RevisionID || run.Source.InputHash != expected.ConfigHash || run.Scope != validation.ScopeFull || run.Status != validation.RunCompleted || run.ResultHash != expected.ResultHash || run.Versions != expected.Versions {
		return nil, fmt.Errorf("validation evidence identity mismatch")
	}
	calculated, err := validation.ResultHash(run.Source, run.Scope, run.Versions, issues)
	if err != nil || calculated != run.ResultHash {
		return nil, fmt.Errorf("validation result hash mismatch")
	}
	findings := make([]Finding, 0)
	for _, issue := range issues {
		if _, supported := supportedValidationCodes[issue.Code]; !supported {
			continue
		}
		if !issue.Valid() || issue.Severity != validation.SeverityBlock || !validHash(issue.Fingerprint) {
			return nil, fmt.Errorf("invalid structural validation issue")
		}
		ordinal := 0
		if issue.Ordinal != nil {
			ordinal = *issue.Ordinal
		}
		rule := structuralRuleIdentity(strings.ToLower(issue.Code))
		evidence := riskcontract.StructureEvidence{Kind: riskcontract.ValidationStructureIssue, Rule: rule, EntityID: issue.EntityID, FieldPath: issue.FieldPath, Ordinal: ordinal, Fingerprint: issue.Fingerprint, Override: riskcontract.NonOverridable}
		finding := Finding{ID: issue.Code + "\x00" + string(issue.EntityID) + "\x00" + issue.FieldPath + fmt.Sprintf("\x00%d", ordinal), Status: riskcontract.Comparable, Severity: riskcontract.Block, Rule: rule, Evidence: &evidence, Override: riskcontract.NonOverridable}
		finding.EvidenceHash = findingHash(finding)
		findings = append(findings, finding)
	}
	sortFindings(findings)
	return findings, nil
}

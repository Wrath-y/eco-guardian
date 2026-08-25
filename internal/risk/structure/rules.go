package structure

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
	"github.com/zouyi/eco-guardian/internal/validation"
	versioningdiff "github.com/zouyi/eco-guardian/internal/versioning/diff"
)

type RuleKind string

const (
	NewMultiplierRule      RuleKind = "new_multiplier"
	RepeatedMultiplierRule RuleKind = "repeated_multiplier"
)

type Rule struct {
	Identity riskcontract.Identity `json:"identity"`
	Kind     RuleKind              `json:"kind"`
	Limit    int                   `json:"limit"`
	Severity riskcontract.Severity `json:"severity"`
}

func (rule Rule) Valid() bool {
	if !rule.Identity.Valid() || (rule.Severity != riskcontract.Block && rule.Severity != riskcontract.Warning) {
		return false
	}
	if rule.Kind == NewMultiplierRule {
		return rule.Limit == 0
	}
	return rule.Kind == RepeatedMultiplierRule && rule.Limit > 0
}

func V1Rules() []Rule {
	return []Rule{
		{Identity: structuralRuleIdentity(string(NewMultiplierRule)), Kind: NewMultiplierRule, Severity: riskcontract.Block},
		{Identity: structuralRuleIdentity(string(RepeatedMultiplierRule)), Kind: RepeatedMultiplierRule, Limit: 2, Severity: riskcontract.Block},
	}
}

type IndexContract struct {
	ASTVersion      string
	DSLVersion      string
	RegistryVersion string
}

func Analyze(changes []versioningdiff.FieldChange, baseline, candidate []validation.FormulaIndexRecord, versions IndexContract, rules []Rule) []Finding {
	ruleByKind := make(map[RuleKind]Rule, len(rules))
	for _, rule := range rules {
		if !rule.Valid() {
			return []Finding{unavailableFinding("STRUCTURAL_RULE_CONTRACT_INVALID", structuralRuleIdentity("registry"))}
		}
		if _, duplicate := ruleByKind[rule.Kind]; duplicate {
			return []Finding{unavailableFinding("STRUCTURAL_RULE_DUPLICATE", rule.Identity)}
		}
		ruleByKind[rule.Kind] = rule
	}
	if _, found := ruleByKind[NewMultiplierRule]; !found {
		return []Finding{unavailableFinding("STRUCTURAL_RULE_MISSING", structuralRuleIdentity(string(NewMultiplierRule)))}
	}
	if _, found := ruleByKind[RepeatedMultiplierRule]; !found {
		return []Finding{unavailableFinding("STRUCTURAL_RULE_MISSING", structuralRuleIdentity(string(RepeatedMultiplierRule)))}
	}
	changed := make(map[string]struct{})
	for _, change := range changes {
		if !change.Valid() || (change.Kind != versioningdiff.Add && change.Kind != versioningdiff.Modify) {
			continue
		}
		changed[indexKey(change.EntityID, change.Path)] = struct{}{}
	}
	baselineByKey := make(map[string]validation.FormulaIndexRecord, len(baseline))
	for _, record := range baseline {
		baselineByKey[indexKey(record.SourceID, record.FieldPath)] = record
	}
	findings := make([]Finding, 0)
	for _, record := range candidate {
		key := indexKey(record.SourceID, record.FieldPath)
		if _, relevant := changed[key]; !relevant {
			continue
		}
		candidateAST, reason := exactAST(record, versions)
		if reason != "" {
			findings = append(findings, unavailableFindingForRecord(reason, record, structuralRuleIdentity("index-contract")))
			continue
		}
		baselineRecord, hasBaseline := baselineByKey[key]
		baselineAST := formula.NewAST(formula.Node{})
		if hasBaseline {
			var baselineReason string
			baselineAST, baselineReason = exactAST(baselineRecord, versions)
			if baselineReason != "" {
				findings = append(findings, unavailableFindingForRecord(baselineReason, record, structuralRuleIdentity("index-contract")))
				continue
			}
		}
		candidateMultipliers := multiplyCount(candidateAST.Root)
		baselineMultipliers := 0
		if hasBaseline {
			baselineMultipliers = multiplyCount(baselineAST.Root)
		}
		if candidateMultipliers > baselineMultipliers {
			rule := ruleByKind[NewMultiplierRule]
			findings = append(findings, structuralFinding(record, riskcontract.NewMultiplierIssue, rule, candidateMultipliers-baselineMultipliers-1, struct {
				BeforeHash  string `json:"before_hash"`
				AfterHash   string `json:"after_hash"`
				BeforeCount int    `json:"before_count"`
				AfterCount  int    `json:"after_count"`
			}{baselineRecord.ASTHash, record.ASTHash, baselineMultipliers, candidateMultipliers}))
		}
		candidateSources := selectorCounts(candidateAST.Root)
		baselineSources := map[string]int{}
		if hasBaseline {
			baselineSources = selectorCounts(baselineAST.Root)
		}
		sources := make([]string, 0, len(candidateSources))
		for source := range candidateSources {
			sources = append(sources, source)
		}
		sort.Strings(sources)
		for ordinal, source := range sources {
			rule := ruleByKind[RepeatedMultiplierRule]
			if candidateMultipliers == 0 || candidateSources[source] <= rule.Limit || candidateSources[source] <= baselineSources[source] {
				continue
			}
			findings = append(findings, structuralFinding(record, riskcontract.RepeatedMultiplierIssue, rule, ordinal, struct {
				OutputID         domain.ID `json:"output_id"`
				MultiplierSource string    `json:"multiplier_source"`
				BeforeCount      int       `json:"before_count"`
				AfterCount       int       `json:"after_count"`
				Limit            int       `json:"limit"`
			}{record.OutputAttributeID, source, baselineSources[source], candidateSources[source], rule.Limit}))
		}
	}
	sortFindings(findings)
	return findings
}

func exactAST(record validation.FormulaIndexRecord, versions IndexContract) (formula.AST, string) {
	if !record.SourceID.Valid() || !record.OutputAttributeID.Valid() || !strings.HasPrefix(record.FieldPath, "/") || !validHash(record.ASTHash) || record.ASTVersion != versions.ASTVersion || record.DSLVersion != versions.DSLVersion || record.RegistryVersion != versions.RegistryVersion || versions.ASTVersion != formula.ASTSchemaVersion || versions.DSLVersion != formula.DSLVersion {
		return formula.AST{}, "STRUCTURAL_INDEX_VERSION_UNAVAILABLE"
	}
	var ast formula.AST
	if err := json.Unmarshal(record.AST, &ast); err != nil || ast.Version != versions.ASTVersion {
		return formula.AST{}, "STRUCTURAL_INDEX_INVALID"
	}
	canonical, err := ast.CanonicalBytes()
	if err != nil || !bytes.Equal(canonical, record.AST) {
		return formula.AST{}, "STRUCTURAL_INDEX_INVALID"
	}
	hash, err := ast.Hash()
	if err != nil || hash != record.ASTHash {
		return formula.AST{}, "STRUCTURAL_INDEX_INVALID"
	}
	return ast, ""
}

func multiplyCount(node formula.Node) int {
	count := 0
	if node.Kind == formula.NodeBinary && node.Operator == "*" {
		count++
	}
	for _, child := range node.Args {
		count += multiplyCount(child)
	}
	return count
}

func selectorCounts(node formula.Node) map[string]int {
	result := make(map[string]int)
	var visit func(formula.Node)
	visit = func(current formula.Node) {
		if current.Kind == formula.NodeSelector {
			result[current.Scope+"\x00"+current.Symbol]++
		}
		for _, child := range current.Args {
			visit(child)
		}
	}
	visit(node)
	return result
}

func structuralFinding(record validation.FormulaIndexRecord, kind riskcontract.StructureEvidenceKind, rule Rule, ordinal int, facts any) Finding {
	fingerprint, _, _ := riskcontract.CanonicalHash("eco-guardian/risk-structure-fingerprint/v1", facts)
	evidence := riskcontract.StructureEvidence{Kind: kind, Rule: rule.Identity, EntityID: record.SourceID, FieldPath: record.FieldPath, Ordinal: ordinal, Fingerprint: fingerprint, Override: riskcontract.NonOverridable}
	finding := Finding{ID: string(kind) + "\x00" + string(record.SourceID) + "\x00" + record.FieldPath + fmt.Sprintf("\x00%d", ordinal), Status: riskcontract.Comparable, Severity: rule.Severity, Rule: rule.Identity, Evidence: &evidence, Override: riskcontract.NonOverridable}
	finding.EvidenceHash = findingHash(finding)
	return finding
}

func unavailableFindingForRecord(reason string, record validation.FormulaIndexRecord, rule riskcontract.Identity) Finding {
	finding := unavailableFinding(reason, rule)
	finding.ID += "\x00" + string(record.SourceID) + "\x00" + record.FieldPath
	finding.EvidenceHash = findingHash(finding)
	return finding
}

func unavailableFinding(reason string, rule riskcontract.Identity) Finding {
	finding := Finding{ID: reason, Status: riskcontract.Unavailable, Reason: reason, Rule: rule, Override: riskcontract.NonOverridable}
	finding.EvidenceHash = findingHash(finding)
	return finding
}

func structuralRuleIdentity(id string) riskcontract.Identity {
	hash, _, _ := riskcontract.CanonicalHash("eco-guardian/risk-structural-rule/v1", struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	}{id, "v1"})
	return riskcontract.Identity{ID: id, Version: "v1", Hash: hash}
}

func findingHash(finding Finding) string {
	copy := finding
	copy.EvidenceHash = ""
	hash, _, _ := riskcontract.CanonicalHash("eco-guardian/risk-structural-finding/v1", copy)
	return hash
}

func sortFindings(findings []Finding) {
	sort.Slice(findings, func(i, j int) bool { return findings[i].ID < findings[j].ID })
}

func indexKey(id domain.ID, path string) string { return string(id) + "\x00" + path }

func validHash(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func stableHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

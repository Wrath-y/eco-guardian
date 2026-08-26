package graphprocess

import (
	"regexp"
	"sort"
	"strconv"
)

const KnownFixTableVersion = 1

type KnownFixRule struct {
	Major        int
	Minor        int
	MinimumPatch int
	Reason       string
}

type KnownFixTable struct {
	Version         int
	DiagnosticMajor int
	Rules           []KnownFixRule
}

type KnownFixEvaluation struct {
	Allowed         bool
	ObservedVersion string
	RequiredVersion string
	Reason          string
	Diagnostics     []string
}

// CompiledKnownFixTable is deliberately local and versioned. No remote or
// settings-controlled rule can change compatibility. There are currently no
// identified affected release lines; new rules require a source change and
// fixture update.
var CompiledKnownFixTable = KnownFixTable{Version: KnownFixTableVersion, DiagnosticMajor: 0, Rules: []KnownFixRule{}}

var serviceSemVerPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

func (table KnownFixTable) Evaluate(version string) KnownFixEvaluation {
	result := KnownFixEvaluation{Allowed: true, ObservedVersion: version, Diagnostics: []string{}}
	major, minor, patch, prerelease, ok := parseServiceSemVer(version)
	if table.Version != KnownFixTableVersion || !ok {
		result.Diagnostics = append(result.Diagnostics, "SERVICE_VERSION_INVALID")
		return result
	}
	if major != table.DiagnosticMajor {
		result.Diagnostics = append(result.Diagnostics, "SERVICE_MAJOR_DIFFERENCE")
	}
	rules := append([]KnownFixRule(nil), table.Rules...)
	sort.Slice(rules, func(left, right int) bool {
		if rules[left].Major != rules[right].Major {
			return rules[left].Major < rules[right].Major
		}
		if rules[left].Minor != rules[right].Minor {
			return rules[left].Minor < rules[right].Minor
		}
		return rules[left].MinimumPatch < rules[right].MinimumPatch
	})
	for _, rule := range rules {
		if major != rule.Major || minor != rule.Minor {
			continue
		}
		result.RequiredVersion = strconv.Itoa(rule.Major) + "." + strconv.Itoa(rule.Minor) + "." + strconv.Itoa(rule.MinimumPatch)
		if patch < rule.MinimumPatch || patch == rule.MinimumPatch && prerelease != "" {
			result.Allowed = false
			result.Reason = rule.Reason
			return result
		}
	}
	return result
}

func parseServiceSemVer(value string) (major, minor, patch int, prerelease string, ok bool) {
	match := serviceSemVerPattern.FindStringSubmatch(value)
	if match == nil {
		return 0, 0, 0, "", false
	}
	major, _ = strconv.Atoi(match[1])
	minor, _ = strconv.Atoi(match[2])
	patch, _ = strconv.Atoi(match[3])
	return major, minor, patch, match[4], true
}

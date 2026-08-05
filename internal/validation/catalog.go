package validation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

type Code struct {
	Name                  string   `json:"name"`
	Severity              Severity `json:"severity"`
	Phase                 Scope    `json:"phase"`
	ImplementationVersion string   `json:"implementation_version"`
}

var CodesV1 = []Code{
	{"SCHEMA_INVALID", SeverityError, ScopeBase, "1"}, {"REFERENCE_NOT_FOUND", SeverityBlock, ScopeLocal, "1"}, {"REFERENCE_TARGET_INACTIVE", SeverityBlock, ScopeLocal, "1"}, {"REFERENCE_KIND_MISMATCH", SeverityBlock, ScopeLocal, "1"},
	{"FORMULA_SYNTAX_INVALID", SeverityBlock, ScopeLocal, "1"}, {"FORMULA_UNKNOWN_VARIABLE", SeverityBlock, ScopeLocal, "1"}, {"FORMULA_UNKNOWN_FUNCTION", SeverityBlock, ScopeLocal, "1"}, {"FORMULA_TYPE_MISMATCH", SeverityBlock, ScopeLocal, "1"}, {"FORMULA_UNIT_MISMATCH", SeverityBlock, ScopeLocal, "1"}, {"FORMULA_DIVISION_BY_ZERO", SeverityBlock, ScopeLocal, "1"}, {"NUMERIC_NON_FINITE", SeverityBlock, ScopeLocal, "1"}, {"NUMERIC_OUT_OF_RANGE", SeverityBlock, ScopeLocal, "1"},
	{"STATIC_FORMULA_CYCLE", SeverityBlock, ScopeFull, "1"}, {"EVENT_LOOP_UNBOUNDED", SeverityBlock, ScopeFull, "1"}, {"STACK_PRIORITY_MISSING", SeverityBlock, ScopeFull, "1"}, {"STACK_UNBOUNDED", SeverityBlock, ScopeFull, "1"},
}
var v1CodeDefinition = func() map[string]Code {
	values := make(map[string]Code, len(CodesV1))
	for _, code := range CodesV1 {
		values[code.Name] = code
	}
	return values
}()

func ValidateCodesV1(codes []Code) error {
	if len(codes) != len(v1CodeDefinition) {
		return fmt.Errorf("validation module count changed; create a new Registry version")
	}
	seen := map[string]struct{}{}
	for _, code := range codes {
		expected, ok := v1CodeDefinition[code.Name]
		if !ok || expected.Severity != code.Severity || expected.Phase != code.Phase || expected.ImplementationVersion != code.ImplementationVersion {
			return fmt.Errorf("code-to-severity or implementation drift for %q", code.Name)
		}
		if _, ok := seen[code.Name]; ok {
			return fmt.Errorf("duplicate validation code %q", code.Name)
		}
		seen[code.Name] = struct{}{}
	}
	return nil
}

func CodeManifestHash(codes []Code) (string, error) {
	parts := make([]string, 0, len(codes))
	seen := map[string]struct{}{}
	for _, c := range codes {
		if c.Name == "" || !c.Severity.Valid() || !c.Phase.Valid() || c.ImplementationVersion == "" {
			return "", fmt.Errorf("invalid validation code")
		}
		if _, ok := seen[c.Name]; ok {
			return "", fmt.Errorf("duplicate validation code %q", c.Name)
		}
		seen[c.Name] = struct{}{}
		parts = append(parts, strings.Join([]string{c.Name, string(c.Severity), string(c.Phase), c.ImplementationVersion}, ":"))
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:]), nil
}

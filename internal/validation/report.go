package validation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
)

type CodeText struct {
	MessageKey string
	FixHintKey string
	Message    string
	FixHint    string
}

var CodeTextsV1 = func() map[string]CodeText {
	values := map[string]CodeText{}
	for _, code := range CodesV1 {
		key := strings.ToLower(code.Name)
		readable := strings.ToLower(strings.ReplaceAll(code.Name, "_", " "))
		values[code.Name] = CodeText{"validation." + key, "validation.fix." + key, readable, "Review the field and apply the validation guidance."}
	}
	return values
}()

func NewIssue(severity Severity, code string, entityID domain.ID, fieldPath string, span *FormulaSpan, ordinal *int, params, evidence map[string]string, registryVersion string) (Issue, error) {
	text, ok := CodeTextsV1[code]
	if !ok {
		return Issue{}, fmt.Errorf("unknown validation code %q", code)
	}
	issue := Issue{Severity: severity, Code: code, EntityID: entityID, FieldPath: fieldPath, Span: span, Ordinal: ordinal, MessageKey: text.MessageKey, MessageParams: cloneMap(params), FixHintKey: text.FixHintKey, Evidence: cloneMap(evidence)}
	expected, ok := v1CodeDefinition[code]
	if !ok || expected.Severity != severity {
		return Issue{}, fmt.Errorf("invalid severity for %s", code)
	}
	fingerprint, err := Fingerprint(registryVersion, issue)
	if err != nil {
		return Issue{}, err
	}
	issue.Fingerprint = fingerprint
	return issue, nil
}

// Presentation is intentionally derived from the frozen catalog rather than
// persisted or hashed. Copy changes cannot alter issue identity or a gate.
func Presentation(issue Issue) (message, fixHint string) {
	text, ok := CodeTextsV1[issue.Code]
	if !ok {
		return issue.Code, ""
	}
	return text.Message, text.FixHint
}
func Fingerprint(registryVersion string, issue Issue) (string, error) {
	if registryVersion == "" || !issue.Severity.Valid() || issue.Code == "" || !issue.EntityID.Valid() || issue.FieldPath == "" {
		return "", fmt.Errorf("incomplete issue identity")
	}
	if issue.Span != nil && (issue.Span.StartByte < 0 || issue.Span.EndByte <= issue.Span.StartByte) {
		return "", fmt.Errorf("invalid formula span")
	}
	parts := []string{registryVersion, issue.Code, string(issue.EntityID), issue.FieldPath, canonicalMap(issue.Evidence)}
	if issue.Span != nil {
		parts = append(parts, fmt.Sprintf("%d:%d", issue.Span.StartByte, issue.Span.EndByte))
	}
	if issue.Ordinal != nil {
		parts = append(parts, fmt.Sprintf("ordinal:%d", *issue.Ordinal))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:]), nil
}
func SortIssues(issues []Issue) []Issue {
	out := append([]Issue(nil), issues...)
	sort.SliceStable(out, func(i, j int) bool { return issueSortKey(out[i]) < issueSortKey(out[j]) })
	return out
}
func issueSortKey(issue Issue) string {
	span := "-"
	if issue.Span != nil {
		span = fmt.Sprintf("%020d:%020d", issue.Span.StartByte, issue.Span.EndByte)
	}
	return string(issue.EntityID) + "\x00" + issue.FieldPath + "\x00" + span + "\x00" + issue.Code + "\x00" + canonicalMap(issue.Evidence)
}
func canonicalMap(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+values[key])
	}
	return strings.Join(parts, "\x00")
}
func cloneMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

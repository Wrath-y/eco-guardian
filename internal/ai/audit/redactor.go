package audit

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

const Redacted = "[REDACTED]"

var (
	bearerPattern     = regexp.MustCompile(`(?i)\bbearer\s+[^\s,;"']+`)
	assignmentPattern = regexp.MustCompile(`(?i)\b(authorization|proxy[_ -]?authorization|api[_ -]?key|credential|password|secret|access[_ -]?token|refresh[_ -]?token)\s*[:=]\s*[^\s,;]+`)
)

// Redactor is an immutable, process-memory-only sensitive-value filter. It
// retains copies of configured values and their common transport encodings;
// callers should build a new instance when credentials rotate.
type Redactor struct {
	values []string
}

func NewRedactor(values ...[]byte) Redactor {
	seen := map[string]struct{}{}
	for _, value := range values {
		if len(value) == 0 {
			continue
		}
		variants := []string{
			string(value),
			url.QueryEscape(string(value)),
			base64.StdEncoding.EncodeToString(value),
			base64.RawStdEncoding.EncodeToString(value),
		}
		for _, variant := range variants {
			if variant != "" {
				seen[variant] = struct{}{}
			}
		}
	}
	result := Redactor{values: make([]string, 0, len(seen))}
	for value := range seen {
		result.values = append(result.values, value)
	}
	// Longest first prevents a short encoded variant from leaving a suffix.
	slices.SortFunc(result.values, func(left, right string) int {
		if len(left) > len(right) {
			return -1
		}
		if len(left) < len(right) {
			return 1
		}
		return strings.Compare(left, right)
	})
	return result
}

func (r Redactor) RedactString(value string) string {
	for _, sensitive := range r.values {
		value = strings.ReplaceAll(value, sensitive, Redacted)
	}
	value = bearerPattern.ReplaceAllString(value, "Bearer "+Redacted)
	value = assignmentPattern.ReplaceAllStringFunc(value, func(match string) string {
		separator := strings.IndexAny(match, ":=")
		if separator < 0 {
			return Redacted
		}
		return strings.TrimSpace(match[:separator]) + match[separator:separator+1] + Redacted
	})
	return value
}

func (Redactor) String() string   { return "audit.Redactor{[REDACTED]}" }
func (Redactor) GoString() string { return "audit.Redactor{[REDACTED]}" }

func (r Redactor) Error(err error) error {
	if err == nil {
		return nil
	}
	// Deliberately do not wrap the source error: custom formatting or unwrapping
	// must not recover an unredacted Provider payload.
	return errors.New(r.RedactString(err.Error()))
}

func (r Redactor) Headers(headers http.Header) http.Header {
	result := make(http.Header, len(headers))
	for name, values := range headers {
		if sensitiveKey(name) {
			result[name] = []string{Redacted}
			continue
		}
		result[name] = make([]string, len(values))
		for index, value := range values {
			result[name][index] = r.RedactString(value)
		}
	}
	return result
}

// JSON redacts sensitive object members and known values while retaining a
// valid JSON envelope suitable for bounded logs, Problems, SSE, audit or
// backup diagnostics. Invalid input is returned as a redacted JSON string.
func (r Redactor) JSON(input []byte) []byte {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		encoded, _ := json.Marshal(r.RedactString(string(input)))
		return encoded
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		encoded, _ := json.Marshal(r.RedactString(string(input)))
		return encoded
	}
	encoded, err := json.Marshal(r.Value(value))
	if err != nil {
		return []byte(`"[REDACTED]"`)
	}
	return encoded
}

func (r Redactor) Value(value any) any {
	switch current := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(current))
		for key, child := range current {
			if sensitiveKey(key) {
				result[key] = Redacted
			} else {
				result[key] = r.Value(child)
			}
		}
		return result
	case []any:
		result := make([]any, len(current))
		for index, child := range current {
			result[index] = r.Value(child)
		}
		return result
	case string:
		return r.RedactString(current)
	case error:
		return r.RedactString(current.Error())
	default:
		return current
	}
}

func sensitiveKey(value string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "_", " ", "_", ".", "_").Replace(value))
	switch normalized {
	case "authorization", "proxy_authorization", "api_key", "apikey", "x_api_key", "credential", "credential_value", "password", "secret", "access_token", "refresh_token", "token", "cookie", "set_cookie":
		return true
	}
	for _, suffix := range []string{"_api_key", "_credential", "_password", "_secret", "_access_token", "_refresh_token"} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

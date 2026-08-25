package audit

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestRedactorRemovesCredentialAndEnvironmentCanariesFromEverySinkEnvelope(t *testing.T) {
	credential := []byte("credential-canary-7f9a")
	environment := []byte("environment-canary-3b2c")
	redactor := NewRedactor(credential, environment)
	encodedCredential := base64.StdEncoding.EncodeToString(credential)
	queryEnvironment := url.QueryEscape(string(environment))

	headers := redactor.Headers(http.Header{
		"Authorization": []string{"Bearer " + string(credential)},
		"X-Trace":       []string{"environment=" + string(environment)},
	})
	channels := map[string]string{
		"provider_headers": fmt.Sprint(headers),
		"provider_body":    string(redactor.JSON([]byte(`{"api_key":"credential-canary-7f9a","input":"environment-canary-3b2c"}`))),
		"error":            redactor.Error(errors.New("provider echoed credential-canary-7f9a")).Error(),
		"problem_details":  string(redactor.JSON([]byte(`{"code":"AI_PROVIDER_FAILED","details":{"diagnostic":"environment-canary-3b2c"}}`))),
		"log":              redactor.RedactString("authorization=credential-canary-7f9a environment-canary-3b2c"),
		"sse":              string(redactor.JSON([]byte(`{"phase":"failed","warning":"credential-canary-7f9a"}`))),
		"audit":            string(redactor.JSON([]byte(`{"provider_response":"environment-canary-3b2c","credential":"credential-canary-7f9a"}`))),
		"backup":           string(redactor.JSON([]byte(`{"settings":{"api_key":"credential-canary-7f9a"},"event":"environment-canary-3b2c"}`))),
		"encoded":          redactor.RedactString(encodedCredential + " " + queryEnvironment),
	}
	for channel, output := range channels {
		for _, canary := range []string{string(credential), string(environment), encodedCredential, queryEnvironment} {
			if strings.Contains(output, canary) {
				t.Errorf("%s leaked canary in %q", channel, output)
			}
		}
		if !strings.Contains(output, Redacted) {
			t.Errorf("%s did not retain a redaction marker: %q", channel, output)
		}
	}
}

func TestRedactorFormattingCannotRevealRegisteredValues(t *testing.T) {
	redactor := NewRedactor([]byte("format-canary"))
	formatted := fmt.Sprintf("%v %#v", redactor, redactor)
	if strings.Contains(formatted, "format-canary") || formatted != "audit.Redactor{[REDACTED]} audit.Redactor{[REDACTED]}" {
		t.Fatalf("redactor formatting leaked: %s", formatted)
	}
}

func TestRedactorPreservesSafeMetadataAndDoesNotRedactTokenBudgets(t *testing.T) {
	redactor := NewRedactor([]byte("canary-secret"))
	output := string(redactor.JSON([]byte(`{"max_output_tokens":32768,"credential_present":true,"endpoint":"http://127.0.0.1:11434/v1","error":"Bearer unknown"}`)))
	for _, safe := range []string{`"max_output_tokens":32768`, `"credential_present":true`, `"endpoint":"http://127.0.0.1:11434/v1"`} {
		if !strings.Contains(output, safe) {
			t.Errorf("safe metadata lost from %s", output)
		}
	}
	if strings.Contains(output, "Bearer unknown") || !strings.Contains(output, "Bearer [REDACTED]") {
		t.Fatalf("bearer value not redacted: %s", output)
	}
}

func TestRedactedErrorCannotBeUnwrapped(t *testing.T) {
	source := fmt.Errorf("wrapped: %w", errors.New("secret-canary"))
	redacted := NewRedactor([]byte("secret-canary")).Error(source)
	if strings.Contains(redacted.Error(), "secret-canary") || errors.Unwrap(redacted) != nil {
		t.Fatalf("unsafe redacted error: %v", redacted)
	}
}

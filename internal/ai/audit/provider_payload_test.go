package audit

import (
	"encoding/json"
	"strings"
	"testing"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
)

func payloadIdentity(id string) aicontract.VersionIdentity {
	return aicontract.VersionIdentity{ID: id, Version: "v1", Hash: aicontract.Hash(strings.Repeat("a", 64))}
}

func TestSealProviderResponseRedactsBeforeRetentionAndDropsHiddenReasoning(t *testing.T) {
	const canary = "provider-payload-canary"
	body := json.RawMessage(`{"targets":[],"rationale":"safe summary","analysis":"hidden provider-payload-canary","nested":{"chain_of_thought":"provider-payload-canary","api_key":"provider-payload-canary"}}`)
	response := aiprovider.StructuredResponse{Schema: payloadIdentity("draft-patch"), Body: body, BodyHash: aicontract.Hash(strings.Repeat("b", 64))}
	sealed, err := NewRedactor([]byte(canary)).SealProviderResponse(response, 4096)
	if err != nil {
		t.Fatal(err)
	}
	stored := string(sealed.StoredBody)
	if strings.Contains(stored, canary) || strings.Contains(stored, "analysis") || strings.Contains(stored, "chain_of_thought") || !strings.Contains(stored, `"rationale":"safe summary"`) || !strings.Contains(stored, Redacted) {
		t.Fatalf("unsafe stored Provider response: %s", stored)
	}
	if sealed.OriginalBodyHash != response.BodyHash || sealed.StoredBodyHash == response.BodyHash {
		t.Fatalf("response hashes=%#v", sealed)
	}
}

func TestProviderPayloadAndEventLimitsAreHard(t *testing.T) {
	redactor := NewRedactor([]byte("canary"))
	if _, err := redactor.ProviderJSON([]byte(`{"value":"oversized"}`), 8); err != ErrProviderPayloadLimit {
		t.Fatalf("payload limit error=%v", err)
	}
	event := aiprovider.Event{Sequence: 1, Type: aiprovider.EventWarning, Warning: &aiprovider.Warning{Code: "SAFE_WARNING", Message: "Bearer canary"}}
	safe, err := redactor.SanitizeProviderEvent(event, 1024)
	if err != nil || strings.Contains(safe.Warning.Message, "canary") {
		t.Fatalf("safe event=%#v err=%v", safe, err)
	}
	if _, err := redactor.SanitizeProviderEvent(event, 8); err != ErrProviderPayloadLimit {
		t.Fatalf("event limit error=%v", err)
	}
}

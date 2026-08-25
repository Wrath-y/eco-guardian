package audit

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
)

func TestCanonicalAuditEventsPinPayloadOrderVersionsAndChain(t *testing.T) {
	hash := aicontract.Hash(strings.Repeat("a", 64))
	versions := []aicontract.VersionIdentity{
		{ID: "tool", Version: "v1", Hash: hash},
		{ID: "provider", Version: "v1", Hash: hash},
	}
	firstDraft, err := NewEventDraft(NewRedactor(), 1, "attempt-1", EventToolCall, map[string]any{"z": 1, "a": "call"}, versions)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstDraft.Payload()) != `{"a":"call","z":1}` || firstDraft.Versions()[0].ID != "provider" {
		t.Fatalf("draft=%#v", firstDraft)
	}
	first, err := BuildEvent("", firstDraft)
	if err != nil || !first.Valid() {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	secondDraft, _ := NewEventDraft(NewRedactor(), 2, "attempt-1", EventToolResult, map[string]any{"result": "ok"}, versions)
	second, err := BuildEvent(first.ChainHash, secondDraft)
	if err != nil || !second.Valid() || second.PreviousHash != first.ChainHash || second.ChainHash == first.ChainHash {
		t.Fatalf("second=%#v err=%v", second, err)
	}
	tampered := second
	tampered.Payload = []byte(`{"result":"changed"}`)
	if tampered.Valid() {
		t.Fatal("tampered payload retained a valid audit chain")
	}
	if _, err := NewEventDraft(NewRedactor(), 1, "attempt-1", EventToolCall, map[string]any{"ok": true}, []aicontract.VersionIdentity{versions[0], versions[0]}); !errors.Is(err, ErrAuditEventInvalid) {
		t.Fatalf("duplicate version err=%v", err)
	}
}

func TestProviderAuditEventRetainsSanitizedUsageAndDuration(t *testing.T) {
	usage := aiprovider.Usage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10}
	draft, err := NewProviderEvent(NewRedactor(), 1, "attempt-1", aiprovider.Event{Sequence: 1, Type: aiprovider.EventUsage, Usage: &usage}, 12, nil)
	if err != nil || draft.Kind() != EventProviderUsage || !bytes.Contains(draft.Payload(), []byte(`"duration_millis":12`)) {
		t.Fatalf("draft=%#v err=%v", draft, err)
	}
}

func TestAuditEventConstructorRedactsSecretsAndDropsHiddenReasoning(t *testing.T) {
	const secret = "audit-event-secret-canary"
	draft, err := NewEventDraft(NewRedactor([]byte(secret)), 1, "attempt-1", EventStructuredResponse, map[string]any{
		"rationale": "safe " + secret,
		"api_key":   secret,
		"analysis":  "hidden " + secret,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := draft.Payload()
	if bytes.Contains(payload, []byte(secret)) || bytes.Contains(payload, []byte("analysis")) || !bytes.Contains(payload, []byte(Redacted)) {
		t.Fatalf("unsafe payload=%s", payload)
	}
}

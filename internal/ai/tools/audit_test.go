package tools

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
)

func TestAuditRecorderCreatesStableOrderedCallAndResultEnvelopes(t *testing.T) {
	started := time.Unix(1_700_000_000, 123_000_000).UTC()
	scope := policyScope(started)
	recorder := NewAuditRecorder()
	firstCall := policyInvocation(t, scope, "preview_simulation")
	first, err := recorder.Begin(firstCall, json.RawMessage(`{"scene":"fixed","parameters":{"cost":10}}`), started)
	if err != nil {
		t.Fatal(err)
	}
	secondCall := policyInvocation(t, scope, "preview_risk")
	secondCall.CallID = "call-2"
	second, err := recorder.Begin(secondCall, json.RawMessage(`{"parameters":{"cost":10},"scene":"fixed"}`), started.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if first.Ordinal != 1 || first.Call.Ordinal != 1 || second.Ordinal != 2 || second.Call.Ordinal != 2 {
		t.Fatalf("ordinals first=%#v second=%#v", first, second)
	}
	if first.PayloadHash != second.PayloadHash || first.InputEnvelopeHash == second.InputEnvelopeHash || !first.InputEnvelopeHash.Valid() || !second.InputEnvelopeHash.Valid() {
		t.Fatalf("canonical hashes first=%#v second=%#v", first, second)
	}
	first.Payload[0] = '['
	if second.Payload[0] != '{' {
		t.Fatal("audit envelopes share mutable payload storage")
	}

	manifestHash := aicontract.Hash(strings.Repeat("b", 64))
	evidence := []aicontract.EvidenceRef{
		{ID: "evidence-2", Kind: aicontract.EvidenceSimulation, ManifestHash: manifestHash},
		{ID: "evidence-1", Kind: aicontract.EvidenceRetrieval, ManifestHash: manifestHash},
	}
	resultPayload := json.RawMessage(`{"advisory":true}`)
	result, err := recorder.Complete(firstCall.CallID, "simulation-preview-v1", evidence, resultPayload, started.Add(1250*time.Millisecond), 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Ordinal != 1 || result.Tool != firstCall.Tool || result.Result == nil || result.PolicyError != nil || !result.Result.ResultHash.Valid() || !result.ResultEnvelopeHash.Valid() {
		t.Fatalf("result=%#v", result)
	}
	if result.StartedAtUnixMillis != started.UnixMilli() || result.CompletedAtUnixMillis != started.Add(1250*time.Millisecond).UnixMilli() || result.DurationMillis != 1250 {
		t.Fatalf("timing=%#v", result)
	}
	if result.Usage.InputBytes != len(first.Payload) || result.Usage.ResultBytes != len(resultPayload) || result.Usage.SearchCandidates != 0 {
		t.Fatalf("usage=%#v", result.Usage)
	}
	if result.Result.Evidence[0].ID != "evidence-1" || result.Result.Evidence[1].ID != "evidence-2" {
		t.Fatalf("evidence order=%#v", result.Result.Evidence)
	}
	if _, err = recorder.Complete(firstCall.CallID, "simulation-preview-v1", evidence, resultPayload, started.Add(2*time.Second), 0); !errors.Is(err, ErrAuditEnvelopeConflict) {
		t.Fatalf("duplicate completion err=%v", err)
	}
}

func TestAuditRecorderRecordsStablePolicyErrorWithoutUntrustedMessage(t *testing.T) {
	started := time.Unix(1_700_000_000, 0).UTC()
	scope := policyScope(started)
	recorder := NewAuditRecorder()
	call := policyInvocation(t, scope, "preview_risk")
	if _, err := recorder.Begin(call, json.RawMessage(`{"target":"skill"}`), started); err != nil {
		t.Fatal(err)
	}
	result, err := recorder.Reject(call.CallID, deny(PolicyPathDenied), started.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if result.Result != nil || result.ResultEnvelopeHash != "" || result.PolicyError == nil || result.PolicyError.Code != PolicyPathDenied || len(result.Payload) != 0 {
		t.Fatalf("policy result=%#v", result)
	}
}

func TestAuditRecorderRejectsControlPlaneAndAmbiguousToolData(t *testing.T) {
	started := time.Unix(1_700_000_000, 0).UTC()
	scope := policyScope(started)
	tests := []string{
		`{"permissions":["publish"]}`,
		`{"nested":{"tools":[{"name":"create_revision"}]}}`,
		`{"capabilities":{"http":true}}`,
		`{"allowed-tools":["read_file"]}`,
		`{"value":1,"value":2}`,
		`{"value":1.0}`,
		`[]`,
		`{"value":1} trailing`,
	}
	for index, payload := range tests {
		t.Run(strings.ReplaceAll(payload, "\"", ""), func(t *testing.T) {
			recorder := NewAuditRecorder()
			call := policyInvocation(t, scope, "preview_risk")
			call.CallID = aicontract.ToolCallID("call-invalid-" + string(rune('a'+index)))
			if _, err := recorder.Begin(call, json.RawMessage(payload), started); !errors.Is(err, ErrAuditEnvelopeInvalid) {
				t.Fatalf("payload=%s err=%v", payload, err)
			}
		})
	}

	recorder := NewAuditRecorder()
	call := policyInvocation(t, scope, "preview_risk")
	if _, err := recorder.Begin(call, json.RawMessage(`{"target":"skill"}`), started); err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Complete(call.CallID, "risk-preview-v1", nil, json.RawMessage(`{"tool_definitions":[]}`), started.Add(time.Millisecond), 0); !errors.Is(err, ErrAuditEnvelopeInvalid) {
		t.Fatalf("control-plane result err=%v", err)
	}
}

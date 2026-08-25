package tools

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
)

func TestPolicyGuardAuthorizesOnlyExactScopedReadOnlyCall(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	scope := policyScope(now)
	guard, err := NewPolicyGuard(scope)
	if err != nil {
		t.Fatal(err)
	}
	call := policyInvocation(t, scope, "preview_simulation")
	permit, err := guard.Authorize(now, call)
	if err != nil {
		t.Fatal(err)
	}
	if permit.Tool != call.Tool || !permit.Deadline.Equal(now.Add(30*time.Second)) {
		t.Fatalf("permit=%#v", permit)
	}
	if err = permit.ValidateResult(now.Add(time.Second), call.CallID, json.RawMessage(`{"metrics":[]}`)); err != nil {
		t.Fatal(err)
	}
	if err = permit.ValidateResult(now.Add(time.Second), call.CallID, json.RawMessage(`{}`)); policyCode(err) != PolicyResultMismatch {
		t.Fatalf("second result err=%v", err)
	}
}

func TestPolicyGuardRejectsUnregisteredCapabilitiesAndScopeEscapes(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	baseScope := policyScope(now)
	for _, name := range []string{"http_get", "sql_query", "read_file", "spawn_process", "save_working_draft", "create_revision", "publish_release", "activate_graph", "read_credentials"} {
		t.Run(name, func(t *testing.T) {
			guard, _ := NewPolicyGuard(baseScope)
			call := policyInvocation(t, baseScope, "preview_simulation")
			call.Tool = aicontract.VersionIdentity{ID: name, Version: "v1", Hash: aicontract.Hash(strings.Repeat("f", 64))}
			if _, err := guard.Authorize(now, call); policyCode(err) != PolicyUnknownTool {
				t.Fatalf("err=%v", err)
			}
		})
	}

	tests := []struct {
		name string
		code PolicyCode
		edit func(*Invocation)
	}{
		{"attempt", PolicyAttemptMismatch, func(v *Invocation) { v.AttemptID = "attempt-other" }},
		{"stage", PolicyStageMismatch, func(v *Invocation) { v.Stage = aicontract.StageDeterministicPreview }},
		{"base", PolicyBaseMismatch, func(v *Invocation) { v.Base.GraphSnapshot = "other" }},
		{"input", PolicyInputMismatch, func(v *Invocation) { v.InputHash = aicontract.Hash(strings.Repeat("e", 64)) }},
		{"target", PolicyTargetDenied, func(v *Invocation) { v.Targets[0].EntityID = "018f9e40-0000-7000-8000-000000000299" }},
		{"path", PolicyPathDenied, func(v *Invocation) { v.Targets[0].Path = "/payload/secret" }},
		{"operation", PolicyOperationDenied, func(v *Invocation) { v.Targets[0].Operation = aicontract.OperationRemove }},
		{"search_on_other_tool", PolicySearchBudget, func(v *Invocation) { v.SearchCandidates = 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			guard, _ := NewPolicyGuard(baseScope)
			call := policyInvocation(t, baseScope, "preview_simulation")
			test.edit(&call)
			if _, err := guard.Authorize(now, call); policyCode(err) != test.code {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestPolicyGuardEnforcesCallSearchTimeAndOutputBudgets(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	scope := policyScope(now)
	guard, _ := NewPolicyGuard(scope)
	for ordinal := 1; ordinal <= 2; ordinal++ {
		call := policyInvocation(t, scope, "preview_simulation")
		call.CallID = aicontract.ToolCallID("call-" + string(rune('0'+ordinal)))
		if _, err := guard.Authorize(now, call); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := guard.Authorize(now, policyInvocation(t, scope, "preview_simulation")); policyCode(err) != PolicyCallBudgetExceeded {
		t.Fatalf("per-tool budget err=%v", err)
	}

	searchScope := policyScope(now)
	searchScope.Limits.MaxSearchCandidates = 2
	searchGuard, _ := NewPolicyGuard(searchScope)
	search := policyInvocation(t, searchScope, "search_parameters")
	search.SearchCandidates = 2
	if _, err := searchGuard.Authorize(now, search); err != nil {
		t.Fatal(err)
	}
	search.CallID = "call-search-2"
	search.SearchCandidates = 1
	if _, err := searchGuard.Authorize(now, search); policyCode(err) != PolicySearchBudget {
		t.Fatalf("search budget err=%v", err)
	}

	timeGuard, _ := NewPolicyGuard(scope)
	if _, err := timeGuard.Authorize(now.Add(time.Duration(scope.Limits.MaxDurationMillis)*time.Millisecond), policyInvocation(t, scope, "preview_simulation")); policyCode(err) != PolicyTimeBudget {
		t.Fatalf("time budget err=%v", err)
	}

	outputScope := policyScope(now)
	outputScope.Limits.MaxToolResultBytes = 16
	outputGuard, _ := NewPolicyGuard(outputScope)
	call := policyInvocation(t, outputScope, "preview_risk")
	permit, err := outputGuard.Authorize(now, call)
	if err != nil {
		t.Fatal(err)
	}
	if err = permit.ValidateResult(now, call.CallID, json.RawMessage(`{"payload":"too large"}`)); policyCode(err) != PolicyOutputTooLarge {
		t.Fatalf("output size err=%v", err)
	}

	invalidGuard, _ := NewPolicyGuard(scope)
	invalidCall := policyInvocation(t, scope, "preview_risk")
	invalidPermit, _ := invalidGuard.Authorize(now, invalidCall)
	if err = invalidPermit.ValidateResult(now, invalidCall.CallID, json.RawMessage(`[]`)); policyCode(err) != PolicyOutputInvalid {
		t.Fatalf("invalid output err=%v", err)
	}
}

func TestRetrieveEvidenceCanOnlyRunOnceBeforeProvider(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	scope := policyScope(now)
	scope.Stage = aicontract.StageInputPinned
	guard, _ := NewPolicyGuard(scope)
	call := policyInvocation(t, scope, "retrieve_evidence")
	call.Targets = nil
	if _, err := guard.Authorize(now, call); err != nil {
		t.Fatal(err)
	}
	call.CallID = "call-retrieve-2"
	if _, err := guard.Authorize(now, call); policyCode(err) != PolicyCallBudgetExceeded {
		t.Fatalf("second retrieval err=%v", err)
	}

	providerScope := policyScope(now)
	providerGuard, _ := NewPolicyGuard(providerScope)
	providerCall := policyInvocation(t, providerScope, "retrieve_evidence")
	providerCall.Targets = nil
	if _, err := providerGuard.Authorize(now, providerCall); policyCode(err) != PolicyStageMismatch {
		t.Fatalf("provider-controlled retrieval err=%v", err)
	}
}

func policyScope(now time.Time) Scope {
	hash := aicontract.Hash(strings.Repeat("a", 64))
	projectID := aicontract.ProjectID("018f9e40-0000-7000-8000-000000000201")
	revisionID := aicontract.RevisionID("018f9e40-0000-7000-8000-000000000202")
	fixture := aicontract.V1Fixture()
	return Scope{
		AttemptID: "attempt-1", InputHash: hash,
		Base:  aicontract.FrozenBaseIdentity{ProjectID: projectID, ConfigRevisionID: revisionID, ConfigHash: hash, VersionManifestHash: hash, MaterializationHash: hash, GraphNamespace: string(projectID), GraphSnapshot: string(revisionID), GraphContentHash: hash},
		Stage: aicontract.StageProviderToolLoop,
		AllowedTargets: []aicontract.AllowedTarget{{
			EntityID: "018f9e40-0000-7000-8000-000000000203", Kind: "skill", ExpectedEntityVersion: 2,
			Paths: []aicontract.AllowedPath{{Path: "/payload/cost", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}}},
		}},
		Limits: fixture.Budget.Limits, StartedAt: now,
	}
}

func policyInvocation(t *testing.T, scope Scope, toolName string) Invocation {
	t.Helper()
	registry, err := aicontract.NewV1RegistrySet()
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := registry.Tools.Resolve(toolName, aicontract.V1Version)
	if !ok {
		t.Fatalf("missing tool %s", toolName)
	}
	return Invocation{
		CallID: "call-1", AttemptID: scope.AttemptID, InputHash: scope.InputHash, Tool: tool.Identity, Stage: scope.Stage, Base: scope.Base,
		Targets: []TargetAccess{{EntityID: scope.AllowedTargets[0].EntityID, Path: scope.AllowedTargets[0].Paths[0].Path, Operation: scope.AllowedTargets[0].Paths[0].Operations[0]}},
	}
}

func policyCode(err error) PolicyCode {
	var policy *PolicyError
	if errors.As(err, &policy) {
		return policy.Code
	}
	return ""
}

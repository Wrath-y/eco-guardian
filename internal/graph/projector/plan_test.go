package projector

import (
	"reflect"
	"testing"
)

func TestPlanDeltaUsesFullRecordsAndDeletesIncidentEdges(t *testing.T) {
	base := Result{Nodes: []Node{{ID: "a"}, {ID: "b"}}, Edges: []Edge{{ID: "ab", From: "a", To: "b"}}}
	target := Result{Nodes: []Node{{ID: "a"}}, Edges: []Edge{}}
	plan, err := PlanDelta("project", "target", "base", base, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.NodeDeletes) != 1 || plan.NodeDeletes[0] != "b" || len(plan.EdgeDeletes) != 1 || plan.EdgeDeletes[0] != "ab" || len(plan.NodeUpserts) != 0 || len(plan.EdgeUpserts) != 0 {
		t.Fatalf("%#v", plan)
	}
}

func TestFullPlanChecksLimitsBeforeProviderWork(t *testing.T) {
	target := Result{Nodes: []Node{{ID: "a"}}}
	if _, err := PlanFullWithLimits("project", "revision", target, Limits{MaxNodes: 0, MaxPayloadBytes: 1}); err == nil {
		t.Fatal("expected payload limit")
	}
	if _, err := PlanFullWithLimits("project", "revision", target, Limits{MaxNodes: 0, MaxPayloadBytes: 1 << 20, MaxEdges: 0}); err != nil {
		t.Fatal(err)
	}
}

func TestEligibleBaseRequiresExactIdentityAndCoreReadiness(t *testing.T) {
	base := BaseObservation{Namespace: "project", Version: "base", ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Status: "ready", QueryReady: true, Components: map[string]string{"graph": "ready", "fts": "ready", "vector": "unavailable"}}
	if !EligibleBase("project", "base", base.ContentHash, base) {
		t.Fatal("vector degradation must not reject core-ready base")
	}
	base.Components["fts"] = "building"
	if EligibleBase("project", "base", base.ContentHash, base) {
		t.Fatal("building FTS must reject base")
	}
}

func TestOnlyBaseErrorsMayFallbackToFull(t *testing.T) {
	for _, code := range []string{"BASE_SNAPSHOT_NOT_FOUND", "BASE_SNAPSHOT_NOT_READY"} {
		if !MayFallbackToFull(code) {
			t.Fatal(code)
		}
	}
	for _, code := range []string{"CONTENT_HASH_CONFLICT", "CONTENT_HASH_MISMATCH", "INVALID_SNAPSHOT_REQUEST", "REIMPORT_REQUIRED"} {
		if MayFallbackToFull(code) {
			t.Fatal(code)
		}
	}
}

func TestDeltaRoundTripsToTargetWithoutMutatingBase(t *testing.T) {
	base := Result{Nodes: []Node{{ID: "a", Label: "before"}, {ID: "b"}}, Edges: []Edge{{ID: "ab", From: "a", To: "b"}}}
	target := Result{Nodes: []Node{{ID: "a", Label: "after"}, {ID: "c"}}, Edges: []Edge{{ID: "ac", From: "a", To: "c"}}}
	before := base
	before.Nodes = append([]Node(nil), base.Nodes...)
	before.Edges = append([]Edge(nil), base.Edges...)
	plan, err := PlanDelta("project", "target", "base", base, target)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := ApplyDelta(base, plan)
	if err != nil {
		t.Fatal(err)
	}
	_, rebuiltHash, err := ManifestBytes(rebuilt)
	if err != nil {
		t.Fatal(err)
	}
	_, targetHash, err := ManifestBytes(target)
	if err != nil || rebuiltHash != targetHash || plan.ContentHash != targetHash {
		t.Fatalf("rebuilt=%#v target=%#v hashes=%s/%s err=%v", rebuilt, target, rebuiltHash, targetHash, err)
	}
	if !reflect.DeepEqual(base, before) {
		t.Fatalf("base was mutated: %#v", base)
	}
}

func TestDeltaRejectsDanglingAndConflictingOperations(t *testing.T) {
	base := Result{Nodes: []Node{{ID: "a"}, {ID: "b"}}, Edges: []Edge{{ID: "ab", From: "a", To: "b"}}}
	for _, plan := range []DeltaPlan{
		{NodeDeletes: []string{"a"}, NodeUpserts: []Node{{ID: "a"}}},
		{EdgeDeletes: []string{"ab"}, EdgeUpserts: []Edge{{ID: "ab", From: "a", To: "b"}}},
		{NodeDeletes: []string{"b"}},
		{EdgeUpserts: []Edge{{ID: "missing", From: "a", To: "missing"}}},
	} {
		if _, err := ApplyDelta(base, plan); err == nil {
			t.Fatalf("invalid delta accepted: %#v", plan)
		}
	}
	if _, err := PlanDelta("project", "target", "base", base, Result{Nodes: []Node{{ID: "a"}}, Edges: []Edge{{ID: "dangling", From: "a", To: "missing"}}}); err == nil {
		t.Fatal("dangling target accepted")
	}
}

func TestBaseEligibilityRejectsMissingUnreadyOrMismatchedBase(t *testing.T) {
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	valid := BaseObservation{Namespace: "project", Version: "base", ContentHash: hash, Status: "ready", QueryReady: true, Components: map[string]string{"graph": "ready", "fts": "ready"}}
	for _, observation := range []BaseObservation{
		{},
		{Namespace: "project", Version: "base", ContentHash: hash, Status: "building", QueryReady: true, Components: valid.Components},
		{Namespace: "project", Version: "base", ContentHash: hash, Status: "ready", QueryReady: false, Components: valid.Components},
		{Namespace: "project", Version: "base", ContentHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Status: "ready", QueryReady: true, Components: valid.Components},
	} {
		if EligibleBase("project", "base", hash, observation) {
			t.Fatalf("ineligible base accepted: %#v", observation)
		}
	}
}

func TestSummaryBindsProjectionEvidence(t *testing.T) {
	d := Descriptor{SchemaVersion: ProjectionSchemaV1, Version: ProjectorV1, Relations: V1Relations(), Formatter: V1Formatter{}}
	summary, err := NewSummary("project", "revision", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", d, Result{}, "cache-1")
	if err != nil || !summary.Valid() || summary.NodeCount != 0 {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
}

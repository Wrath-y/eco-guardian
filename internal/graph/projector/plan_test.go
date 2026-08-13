package projector

import "testing"

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

func TestSummaryBindsProjectionEvidence(t *testing.T) {
	d := Descriptor{SchemaVersion: ProjectionSchemaV1, Version: ProjectorV1, Relations: V1Relations(), Formatter: V1Formatter{}}
	summary, err := NewSummary("project", "revision", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", d, Result{}, "cache-1")
	if err != nil || !summary.Valid() || summary.NodeCount != 0 {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
}

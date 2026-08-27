package sync

import "testing"

func TestVerifySnapshotRequiresExactCoreReadinessAndPreservesVectorDegradation(t *testing.T) {
	expected := SnapshotExpectation{Namespace: "project", Version: "revision", ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", NodeCount: 2, EdgeCount: 1}
	snapshot := Snapshot{Namespace: "project", Version: "revision", ContentHash: expected.ContentHash, NodeCount: 2, EdgeCount: 1, Status: "ready", QueryReady: true, Components: []Component{{Name: "graph", State: "ready"}, {Name: "fts", State: "ready"}, {Name: "vector", State: "unavailable"}}}
	verified := VerifySnapshot(expected, snapshot)
	if !verified.Ready || len(verified.Reasons) != 0 || len(verified.Warnings) != 1 || verified.Warnings[0] != "DEGRADED_VECTOR" {
		t.Fatalf("verification=%#v", verified)
	}
	snapshot.QueryReady, snapshot.Components[1].State = false, "building"
	blocked := VerifySnapshot(expected, snapshot)
	if blocked.Ready || len(blocked.Reasons) != 2 || blocked.Reasons[0] != "QUERY_NOT_READY" || blocked.Reasons[1] != "COMPONENT_FTS_NOT_READY" {
		t.Fatalf("verification=%#v", blocked)
	}
}

func TestRestoreFullRebuildNeverAcceptsNewerMissingOrWrongExternalSnapshot(t *testing.T) {
	expected := SnapshotExpectation{Namespace: "project", Version: "restored-revision", ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", NodeCount: 2, EdgeCount: 1}
	exact := Snapshot{Namespace: expected.Namespace, Version: expected.Version, ContentHash: expected.ContentHash, NodeCount: 2, EdgeCount: 1, Status: "ready", QueryReady: true, Components: []Component{{Name: "graph", State: "ready"}, {Name: "fts", State: "ready"}}}
	if result := VerifySnapshot(expected, exact); !result.Ready {
		t.Fatalf("exact full rebuild was not ready: %#v", result)
	}
	for _, test := range []struct {
		name   string
		mutate func(*Snapshot)
	}{
		{name: "newer external active revision", mutate: func(value *Snapshot) { value.Version = "newer-revision" }},
		{name: "missing namespace", mutate: func(value *Snapshot) { value.Namespace = "" }},
		{name: "same revision wrong manifest hash", mutate: func(value *Snapshot) {
			value.ContentHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		}},
		{name: "provider unavailable observation", mutate: func(value *Snapshot) { value.Status, value.QueryReady, value.Components = "", false, nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := exact
			test.mutate(&candidate)
			if result := VerifySnapshot(expected, candidate); result.Ready {
				t.Fatalf("unsafe snapshot was accepted: %#v", candidate)
			}
		})
	}
}

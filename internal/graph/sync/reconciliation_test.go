package sync

import "testing"

func TestReconcileMissingTaskAcceptsOnlyVerifiedTargetOrAbsence(t *testing.T) {
	expected := SnapshotExpectation{Namespace: "p", Version: "v", ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if decision, err := ReconcileMissingTask(expected, nil); err != nil || !decision.Resubmit {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
	snapshot := &Snapshot{Namespace: "p", Version: "v", ContentHash: expected.ContentHash, Status: "ready", QueryReady: true, Components: []Component{{Name: "graph", State: "ready"}, {Name: "fts", State: "ready"}}}
	if decision, err := ReconcileMissingTask(expected, snapshot); err != nil || !decision.AcceptExisting || decision.Resubmit {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
	snapshot.ContentHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := ReconcileMissingTask(expected, snapshot); err == nil {
		t.Fatal("unverified target accepted")
	}
}

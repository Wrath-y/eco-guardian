package runtime

import (
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/app/runtime/capability"
	"github.com/zouyi/eco-guardian/internal/app/runtime/graphprocess"
	"github.com/zouyi/eco-guardian/internal/buildinfo"
	platformprocess "github.com/zouyi/eco-guardian/internal/platform/process"
)

func TestStatusAssemblerPublishesOneDetachedDeterministicSnapshot(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	input := StatusResourceInput{
		Lifecycle:    StatusSnapshot{SchemaVersion: 1, Generation: 7, Phase: PhaseDegraded, ListenerURL: "http://127.0.0.1:8123", UpdatedAt: now},
		Build:        buildinfo.Info{Version: "1.2.3", Build: "release", Commit: "abc123", PackageMode: buildinfo.PackageComplete},
		Project:      ProjectStatus{State: "active", ProjectID: "01991a39-e000-7000-8000-000000000001", RecentCount: 2},
		Process:      graphprocess.ProcessObservation{Ownership: platformprocess.OwnershipBundled, State: graphprocess.StateReady, Generation: 3, Endpoint: "http://127.0.0.1:9000", Attempt: 1},
		Dependencies: []DependencyStatus{{ID: "z", State: "healthy", Generation: 2}, {ID: "a", State: "degraded", Generation: 3, ExpiresAt: now.Add(-time.Second), Reasons: []capability.Reason{{Code: "Z_REASON", Component: "z"}, {Code: "A_REASON", Component: "a"}}}},
		Capabilities: []capability.Result{{ID: "z", Version: "1", State: capability.Available, Reasons: []capability.Reason{}, Actions: []capability.Action{}}, {ID: "a", Version: "1", State: capability.Degraded, Reasons: []capability.Reason{}, Actions: []capability.Action{}}},
		Recovery:     []RecoveryStatus{{JobKind: "release", State: "complete", Count: 1}, {JobKind: "graph", State: "pending", Count: 2}},
		LogLocation:  "<project-directory>/logs",
	}
	assembler, err := NewStatusAssembler(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Dependencies[0].ID = "mutated"
	first := assembler.Snapshot()
	if first.Dependencies[0].ID != "a" || first.Capabilities[0].ID != "a" || first.Recovery[0].JobKind != "graph" {
		t.Fatalf("snapshot is not normalized: %#v", first)
	}
	first.Dependencies[0].Reasons[0].Code = "MUTATED"
	first.Capabilities[0].ID = "mutated"
	second := assembler.Snapshot()
	if second.Dependencies[0].Reasons[0].Code != "A_REASON" || second.Capabilities[0].ID != "a" {
		t.Fatalf("snapshot aliases caller memory: %#v", second)
	}
	if second.Process.Generation != 3 || second.Process.Endpoint == "" || second.Build.PackageMode != buildinfo.PackageComplete {
		t.Fatalf("safe summaries missing: %#v", second)
	}
	if second.Dependencies[0].State != "unknown" || second.Dependencies[0].Reasons[1].Code != "OBSERVATION_STALE" {
		t.Fatalf("stale dependency was presented as current: %#v", second.Dependencies[0])
	}
}

func TestStatusAssemblerRejectsUnsafeOrIncompletePublication(t *testing.T) {
	now := time.Now().UTC()
	base := StatusResourceInput{Lifecycle: StatusSnapshot{Phase: PhaseReady, UpdatedAt: now}, Build: buildinfo.Info{Version: "1.0.0", Build: "release", Commit: "abc", PackageMode: buildinfo.PackageLightweight}, LogLocation: "<project-directory>/logs"}
	base.Process.Reason = "unsafe\nchild output"
	if _, err := NewStatusAssembler(base); err != ErrRuntimeStatusInvalid {
		t.Fatalf("err=%v", err)
	}
	base.Process.Reason = ""
	base.LogLocation = ""
	if _, err := NewStatusAssembler(base); err != ErrRuntimeStatusInvalid {
		t.Fatalf("err=%v", err)
	}
}

package contract

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/zouyi/eco-guardian/internal/simulation/scenario"
)

type countingReleaseSource struct {
	revision Revision
	calls    int
}

func (f *countingReleaseSource) ResolveReleaseRevision(context.Context, ID) (Revision, error) {
	f.calls++
	return f.revision, nil
}

var _ ReleaseSource = (*countingReleaseSource)(nil)

func TestResolveSourcePinsRevisionOnceAndChecksOwnership(t *testing.T) {
	revision := Revision{ID: "revision", ProjectID: "project", ConfigHash: "config", ManifestHash: "manifest"}
	releases := &countingReleaseSource{revision: revision}
	resolved, err := ResolveSource(context.Background(), nil, releases, SourceSelection{ProjectID: "project", ReleaseID: "release"})
	if err != nil || resolved != revision || releases.calls != 1 {
		t.Fatalf("resolved=%#v calls=%d err=%v", resolved, releases.calls, err)
	}
	releases.revision.ProjectID = "other"
	if _, err = ResolveSource(context.Background(), nil, releases, SourceSelection{ProjectID: "project", ReleaseID: "release"}); !errors.Is(err, ErrSourceOwnership) {
		t.Fatalf("ownership err=%v", err)
	}
}

func TestResolveSourceRejectsMutableOrAmbiguousSelection(t *testing.T) {
	for _, selection := range []SourceSelection{{}, {ProjectID: "project"}, {ProjectID: "project", RevisionID: "revision", ReleaseID: "release"}} {
		if _, err := ResolveSource(context.Background(), fakeRevisionSource{}, nil, selection); !errors.Is(err, ErrSourceInvalid) {
			t.Fatalf("selection=%#v err=%v", selection, err)
		}
	}
}

func TestCapturedInputDoesNotChangeWhenSourceOrSceneChangesLater(t *testing.T) {
	source := &countingReleaseSource{revision: Revision{ID: "revision", ProjectID: "project", ConfigHash: "config", ManifestHash: "manifest"}}
	captured, err := ResolveSource(context.Background(), nil, source, SourceSelection{ProjectID: "project", ReleaseID: "release"})
	if err != nil {
		t.Fatal(err)
	}
	template := scenario.BuiltinTemplates()[0]
	input, err := NormalizeInput(InputRequest{Revision: captured, Scene: template, Metrics: []MetricIdentity{{ID: "metric-dps", Version: "v1"}}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := input.Hash()
	if err != nil {
		t.Fatal(err)
	}
	source.revision.ConfigHash = "new-working-state"
	template.Body[0] = '!'
	after, err := input.Hash()
	if err != nil || after != before || input.ConfigHash != "config" {
		t.Fatalf("before=%s after=%s input=%#v err=%v", before, after, input, err)
	}
}

func TestAcceptedInputAndFingerprintSurviveConcurrentPostCaptureChanges(t *testing.T) {
	source := &countingReleaseSource{revision: Revision{ID: "revision", ProjectID: "project", ConfigHash: "config", ManifestHash: "manifest"}}
	scene := scenario.BuiltinTemplates()[0]
	inputRevision, err := ResolveSource(context.Background(), nil, source, SourceSelection{ProjectID: "project", ReleaseID: "release"})
	if err != nil {
		t.Fatal(err)
	}
	input, err := NormalizeInput(InputRequest{Revision: inputRevision, Scene: scene, Metrics: []MetricIdentity{{ID: "metric-dps", Version: "v1"}}})
	if err != nil {
		t.Fatal(err)
	}
	inputHash, err := input.Hash()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewManifestRegistry(RequiredV1Descriptors, V1Descriptors())
	if err != nil {
		t.Fatal(err)
	}
	revision := []RevisionImplementation{{CapabilityID: "numeric-policy", ContractVersion: "v1", ImplementationVersion: "decimal-v1", State: "registered"}, {CapabilityID: "schema", ContractVersion: "v1", ImplementationVersion: "schema-v1", State: "registered"}, {CapabilityID: "dsl", ContractVersion: "v1", ImplementationVersion: "dsl-v1", State: "registered"}, {CapabilityID: "validator-registry", ContractVersion: "v1", ImplementationVersion: "registry-v1", State: "registered"}}
	_, fingerprintHash, err := ResolveFingerprint(registry, input, revision)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		source.revision = Revision{ID: "later-release", ProjectID: "project", ConfigHash: "working-state", ManifestHash: "later-manifest"}
		scene.Body[0] = '!'
		registry = nil // emulate an implementation registry becoming unavailable.
	}()
	group.Wait()
	afterInputHash, err := input.Hash()
	if err != nil || afterInputHash != inputHash || input.RevisionID != "revision" || fingerprintHash == "" {
		t.Fatalf("input hash %q -> %q input=%#v fingerprint=%q err=%v", inputHash, afterInputHash, input, fingerprintHash, err)
	}
}

package contract

import (
	"context"
	"errors"
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

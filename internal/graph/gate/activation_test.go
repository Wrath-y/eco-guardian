package gate

import (
	"context"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	versioningrelease "github.com/zouyi/eco-guardian/internal/versioning/release"
)

type activationSummariesFake struct{ summary projector.Summary }

func (f activationSummariesFake) GraphProjectionSummary(context.Context, domain.ID) (projector.Summary, bool, error) {
	return f.summary, true, nil
}

type activationProviderFake struct {
	providerFake
	activation graphsync.Activation
	calls      int
}

func (f *activationProviderFake) ActivateSnapshot(context.Context, string, string, string) (graphsync.Activation, error) {
	f.calls++
	return f.activation, nil
}

func TestActivationAdapterRechecksReadyCandidateAndAcceptsReplay(t *testing.T) {
	projectID, _ := domain.NewID()
	revisionID, _ := domain.NewID()
	intentID, _ := domain.NewID()
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	summary := projector.Summary{ProjectID: string(projectID), RevisionID: string(revisionID), ConfigHash: hash, SchemaVersion: projector.ProjectionSchemaV1, ProjectorVersion: projector.ProjectorV1, ManifestHash: hash, NodeCount: 1}
	provider := &activationProviderFake{providerFake: providerFake{snapshot: graphsync.Snapshot{Namespace: string(projectID), Version: string(revisionID), ContentHash: hash, NodeCount: 1, TaskID: "task", Status: "ready", QueryReady: true, Components: []graphsync.Component{{Name: "graph", State: "ready"}, {Name: "fts", State: "ready"}}}}, activation: graphsync.Activation{Namespace: string(projectID), ActiveVersion: string(revisionID), Changed: false}}
	request := versioningrelease.GraphActivationRequest{ProjectID: projectID, RevisionID: revisionID, ConfigHash: hash, IntentID: intentID}
	evidence, err := (ActivationAdapter{Provider: provider, Summaries: activationSummariesFake{summary}}).Activate(context.Background(), request)
	if err != nil || evidence.Changed || !evidence.Matches(request) || evidence.TaskID != "task" || provider.calls != 1 {
		t.Fatalf("evidence=%#v calls=%d err=%v", evidence, provider.calls, err)
	}
}

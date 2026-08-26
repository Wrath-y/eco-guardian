package retrieval

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/impact"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	versioningdiff "github.com/zouyi/eco-guardian/internal/versioning/diff"
)

type providerFake struct {
	request  impact.RetrieveRequest
	response impact.RetrieveResponse
	err      error
	calls    int
}

func (f *providerFake) Traverse(context.Context, impact.TraverseRequest, string) (impact.TraverseResponse, error) {
	return impact.TraverseResponse{}, nil
}
func (f *providerFake) Paths(context.Context, impact.PathsRequest, string) (impact.PathsResponse, error) {
	return impact.PathsResponse{}, nil
}
func (f *providerFake) Retrieve(_ context.Context, request impact.RetrieveRequest, _ string) (impact.RetrieveResponse, error) {
	f.calls++
	f.request = request
	return f.response, f.err
}

func retrievalInput(t *testing.T) impact.Input {
	t.Helper()
	project, _ := domain.NewID()
	base, _ := domain.NewID()
	target, _ := domain.NewID()
	hash := strings.Repeat("a", 64)
	return impact.Input{ProjectID: project, Base: impact.RevisionIdentity{RevisionID: base, ConfigHash: hash, VersionManifestHash: hash, GraphManifestHash: hash}, Target: impact.RevisionIdentity{RevisionID: target, ConfigHash: hash, VersionManifestHash: hash, GraphManifestHash: hash}, AnalysisContractVersion: impact.AnalysisContractVersion, Filters: impact.Filters{RelationshipKinds: []string{"explicit"}, Direction: impact.DirectionIncoming}, Limits: impact.Limits{MaxDepth: 3, MaxNodes: 500, DefaultPathsPerTarget: 1, ExpandedMaxPaths: 20}, Suspected: impact.SuspectedOptions{Enabled: true, MaxSeeds: 20, MaxResults: 20, GraphMaxDepth: 2}}
}

func TestRetrievalQueryIsVersionedDeterministicAndNotAIGenerated(t *testing.T) {
	firstID, _ := domain.NewID()
	secondID, _ := domain.NewID()
	changed := []impact.ChangedEntity{{EntityID: secondID, Kind: domain.KindSkill, ChangeKind: versioningdiff.Modify, FieldPaths: []string{"/z", "/a"}, TargetNodeID: "b", QueryEligible: true}, {EntityID: firstID, Kind: domain.KindItem, ChangeKind: versioningdiff.Add, FieldPaths: []string{"/"}, TargetNodeID: "a", QueryEligible: true}}
	nodes := map[string]graphsync.Node{"a": {ID: "a", Label: "Alpha", Text: "canonical alpha"}, "b": {ID: "b", Label: "Beta", Text: "canonical beta"}}
	a := BuildQuery(changed, nodes)
	changed[0], changed[1] = changed[1], changed[0]
	b := BuildQuery(changed, nodes)
	if a != b || !strings.HasPrefix(a, "dependency-impact-query-v1\n") || strings.Contains(a, "AI") {
		t.Fatalf("queries differ a=%q b=%q", a, b)
	}
}

func TestRetrievePreservesHybridAndDegradedEvidenceSeparately(t *testing.T) {
	input := retrievalInput(t)
	rank, score := 1, "0.25"
	evidence := impact.SuspectedEvidence{Rank: 1, Node: graphsync.Node{ID: "suspected", Type: "skill"}, CitationText: "citation", SeedNodeID: "seed", RelationshipKinds: []string{"inferred"}, Scores: impact.RetrievalScores{BM25Rank: &rank, RRFScore: &score}, FTSGeneration: "fts-1", VectorGeneration: "vector-1", AlgorithmVersion: "rrf-v1", ModelProvider: "local", Model: "reranker-v1"}
	provider := &providerFake{response: impact.RetrieveResponse{ResolvedSnapshotVersion: string(input.Target.RevisionID), ContentHash: input.Target.GraphManifestHash, Mode: "bm25_only", Degraded: true, Warnings: []string{"VECTOR_UNAVAILABLE"}, Results: []impact.SuspectedEvidence{evidence}}}
	result, err := Retrieve(context.Background(), provider, input, []impact.ChangedEntity{{TargetNodeID: "seed", QueryEligible: true}}, map[string]graphsync.Node{"seed": {ID: "seed", Label: "Seed", Text: "Seed"}}, "request")
	if err != nil || result.State != impact.SuspectedDegraded || len(result.Evidence) != 1 || result.Evidence[0].RelationshipKinds[0] != "inferred" || result.Evidence[0].Scores.RRFScore == nil || provider.request.SnapshotVersion != string(input.Target.RevisionID) {
		t.Fatalf("result=%#v request=%#v err=%v", result, provider.request, err)
	}
}

func TestRetrieveDegradationMatrixAndIndexEvictionRemainOptional(t *testing.T) {
	input := retrievalInput(t)
	for code, state := range map[string]impact.SuspectedState{"SNAPSHOT_INDEX_NOT_READY": impact.SuspectedRebuildRequired, "RETRIEVAL_UNAVAILABLE": impact.SuspectedUnavailable, "GRAPH_STORE_UNAVAILABLE": impact.SuspectedUnavailable} {
		provider := &providerFake{err: &graphsync.ProviderError{Code: code, Message: "safe", RequestID: "provider", Details: map[string]any{}, Retryable: code == "GRAPH_STORE_UNAVAILABLE"}}
		result, err := Retrieve(context.Background(), provider, input, nil, nil, "request")
		if err != nil || result.State != state || len(result.Evidence) != 0 || len(result.Warnings) != 1 || result.Warnings[0] != code {
			t.Fatalf("code=%s result=%#v err=%v", code, result, err)
		}
	}
	input.Suspected.Enabled = false
	provider := &providerFake{err: errors.New("must not run")}
	result, err := Retrieve(context.Background(), provider, input, nil, nil, "request")
	if err != nil || result.State != impact.SuspectedDisabled || provider.calls != 0 {
		t.Fatalf("disabled result=%#v calls=%d err=%v", result, provider.calls, err)
	}
}

func TestRetrievePreservesEveryProviderModeWithoutInventingScores(t *testing.T) {
	input := retrievalInput(t)
	vectorRank, rerank := 2, "0.875"
	evidence := impact.SuspectedEvidence{Rank: 1, Node: graphsync.Node{ID: "candidate", Type: "item"}, CitationText: "fixed citation", SeedNodeID: "seed", RelationshipKinds: []string{"explicit", "inferred"}, Scores: impact.RetrievalScores{VectorRank: &vectorRank, RerankScore: &rerank}, VectorGeneration: "vector-7", AlgorithmVersion: "retrieve-v1", ModelProvider: "local", Model: "rerank-v2"}
	for _, test := range []struct {
		name, mode, warning string
		degraded            bool
	}{
		{name: "hybrid", mode: "hybrid"},
		{name: "bm25-only", mode: "bm25_only", warning: "VECTOR_UNAVAILABLE", degraded: true},
		{name: "vector-only", mode: "vector_only", warning: "FTS_UNAVAILABLE", degraded: true},
		{name: "rerank-fallback", mode: "hybrid", warning: "RERANK_UNAVAILABLE", degraded: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			warnings := []string{}
			if test.warning != "" {
				warnings = []string{test.warning, test.warning}
			}
			response := impact.RetrieveResponse{ResolvedSnapshotVersion: string(input.Target.RevisionID), ContentHash: input.Target.GraphManifestHash, Mode: test.mode, Degraded: test.degraded, Warnings: warnings, Results: []impact.SuspectedEvidence{evidence}}
			provider := &providerFake{response: response}
			first, err := Retrieve(context.Background(), provider, input, []impact.ChangedEntity{{TargetNodeID: "seed", QueryEligible: true}}, map[string]graphsync.Node{"seed": {ID: "seed", Label: "Seed", Text: "canonical"}}, "fixed")
			second, secondErr := Retrieve(context.Background(), provider, input, []impact.ChangedEntity{{TargetNodeID: "seed", QueryEligible: true}}, map[string]graphsync.Node{"seed": {ID: "seed", Label: "Seed", Text: "canonical"}}, "fixed")
			wantState := impact.SuspectedReady
			if test.degraded {
				wantState = impact.SuspectedDegraded
			}
			if err != nil || secondErr != nil || first.State != wantState || len(first.Evidence) != 1 || first.Evidence[0].Scores.BM25Rank != nil || first.Evidence[0].Scores.VectorRank == nil || len(first.Evidence[0].RelationshipKinds) != 2 || len(first.Warnings) != len(warnings)/2 || first.Evidence[0].CitationText != second.Evidence[0].CitationText {
				t.Fatalf("first=%#v second=%#v err=%v/%v", first, second, err, secondErr)
			}
		})
	}
}

type explainerFake struct {
	output impact.ExplanationOutput
	err    error
}

func (f explainerFake) Explain(context.Context, impact.ExplanationInput) (impact.ExplanationOutput, error) {
	return f.output, f.err
}

type idFake struct{ id domain.ID }

func (f idFake) New() (domain.ID, error) { return f.id, nil }

type clockFake struct{ now time.Time }

func (f clockFake) Now() time.Time { return f.now }

func TestExplanationAcceptsOnlySavedRefsAndFailureLeavesEvidenceUsable(t *testing.T) {
	reportID, _ := domain.NewID()
	attemptID, _ := domain.NewID()
	refs := []impact.EvidenceRef{{ID: "path-1", Hash: strings.Repeat("a", 64), Kind: "path"}}
	clock := clockFake{now: time.Now().UTC()}
	attempt, err := Explain(context.Background(), explainerFake{output: impact.ExplanationOutput{Text: "Explanation", Provider: "fake", Model: "fake-v1", Refs: []string{"path-1"}}}, idFake{attemptID}, clock, reportID, refs)
	if err != nil || attempt.Status != "succeeded" || attempt.Text == "" || !impact.ValidHash(attempt.InputHash) {
		t.Fatalf("attempt=%#v err=%v", attempt, err)
	}
	invalid, err := Explain(context.Background(), explainerFake{output: impact.ExplanationOutput{Text: "Unsupported", Provider: "fake", Model: "fake-v1", Refs: []string{"invented"}}}, idFake{attemptID}, clock, reportID, refs)
	if err != nil || invalid.Status != "failed" || invalid.Diagnostics != "UNSUPPORTED_EVIDENCE_REF" {
		t.Fatalf("invalid=%#v err=%v", invalid, err)
	}
	timedOut, err := Explain(context.Background(), explainerFake{err: context.DeadlineExceeded}, idFake{attemptID}, clock, reportID, refs)
	if err != nil || timedOut.Status != "failed" || timedOut.Diagnostics != "AI_TIMEOUT" {
		t.Fatalf("timeout=%#v err=%v", timedOut, err)
	}
}

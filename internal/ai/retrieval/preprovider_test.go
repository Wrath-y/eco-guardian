package retrieval

import (
	"context"
	"errors"
	"testing"
)

type retrievalPortFunc func(context.Context, Request) (Response, error)

func (f retrievalPortFunc) Retrieve(ctx context.Context, request Request) (Response, error) {
	return f(ctx, request)
}

func TestPreProviderRetrievalIsFrozenOneShotAndSealsBeforeProvider(t *testing.T) {
	input := retrievalInput(t)
	store := &evidenceStoreFake{}
	calls := 0
	providerCalls := 0
	runner := &PreProviderRetrieval{}
	port := retrievalPortFunc(func(_ context.Context, request Request) (Response, error) {
		calls++
		expected, err := BuildV1Request(input)
		if err != nil {
			t.Fatal(err)
		}
		if request.Query != expected.Query || request.Base != expected.Base || request.Budget != expected.Budget {
			t.Fatalf("request was not derived from frozen input: %#v", request)
		}
		response := hybridEvidenceResponse(t)
		response.Request = Request{Query: "transport-controlled query"}
		response.ResolvedSnapshotVersion = input.Base.GraphSnapshot
		response.ContentHash = string(input.Base.GraphContentHash)
		response.Results[0].Node.Type = "skill"
		response.Results[0].Evidence.Path.Nodes[0].Type = "skill"
		return response, nil
	})

	err := runner.Run(context.Background(), input, port, store, func(pinned PinnedEvidence) error {
		providerCalls++
		if store.sealed == nil || pinned.ManifestHash != store.sealed.ManifestHash {
			t.Fatal("provider ran before evidence was sealed")
		}
		if pinned.Manifest.RequestHash != store.sealed.Manifest.RequestHash || pinned.Manifest.Base != input.Base {
			t.Fatal("transport was able to replace the frozen retrieval request")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || providerCalls != 1 {
		t.Fatalf("retrieval calls=%d provider calls=%d", calls, providerCalls)
	}
	if err = runner.Run(context.Background(), input, port, store, func(PinnedEvidence) error { return nil }); !errors.Is(err, ErrPreProviderAlreadyUsed) {
		t.Fatalf("second run err=%v", err)
	}
	if calls != 1 || providerCalls != 1 {
		t.Fatalf("second run crossed boundary: retrieval=%d provider=%d", calls, providerCalls)
	}
}

func TestPreProviderRetrievalNeverInvokesProviderOnRetrievalFailure(t *testing.T) {
	runner := &PreProviderRetrieval{}
	want := errors.New("retrieval unavailable")
	invoked := false
	err := runner.Run(context.Background(), retrievalInput(t), retrievalPortFunc(func(context.Context, Request) (Response, error) {
		return Response{}, want
	}), &evidenceStoreFake{}, func(PinnedEvidence) error {
		invoked = true
		return nil
	})
	if !errors.Is(err, want) || invoked {
		t.Fatalf("err=%v invoked=%v", err, invoked)
	}
}

package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/graph/impact"
)

func TestImpactQueryClientPinsIdentityAndPreservesStoredOrientation(t *testing.T) {
	hash := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/graphs/project/snapshots/revision/paths" || r.Header.Get("X-Request-ID") != "root" {
			t.Errorf("request path=%q id=%q", r.URL.Path, r.Header.Get("X-Request-ID"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resolved_snapshot_version":"revision","content_hash":"` + hash + `","paths":[{"source_node_id":"a","target_node_id":"b","node_ids":["a","b"],"edge_ids":["e"],"nodes":[{"id":"a","type":"skill","label":"A","text":"A","properties":{},"provenance":{}},{"id":"b","type":"effect","label":"B","text":"B","properties":{},"provenance":{}}],"edges":[{"id":"e","from":"b","to":"a","type":"skill_applies_effect","relation_kind":"explicit","confidence":1,"properties":{},"provenance":{"field_path":"/payload/effect_ids/0"}}],"hop_count":1,"truncated":false,"truncation_reasons":[]}],"truncated":false,"truncation_reasons":[],"warnings":[]}`))
	}))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL, HTTPClient: &http.Client{Timeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Paths(context.Background(), impact.PathsRequest{Namespace: "project", SnapshotVersion: "revision", SourceNodeIDs: []string{"a"}, TargetNodeIDs: []string{"b"}, RelationshipKinds: []string{"explicit"}, Direction: impact.DirectionIncoming, MaxDepth: 3, MaxNodes: 500, MaxPaths: 1}, "root")
	if err != nil || len(response.Paths) != 1 || response.Paths[0].Edges[0].From != "b" || response.Paths[0].Edges[0].To != "a" {
		t.Fatalf("response=%#v err=%v", response, err)
	}
}

func TestImpactQueryRetriesOnlyRetryableReadBodies(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := attempts.Add(1)
		if count < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"code":"GRAPH_STORE_UNAVAILABLE","message":"safe","retryable":true,"details":{},"request_id":"provider"}`))
			return
		}
		_, _ = w.Write([]byte(`{"resolved_snapshot_version":"revision","content_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","nodes":[],"edges":[],"truncated":false,"truncation_reasons":[],"warnings":[]}`))
	}))
	defer server.Close()
	client, _ := New(Config{Endpoint: server.URL, HTTPClient: &http.Client{Timeout: time.Second}})
	provider := ImpactQueryProvider{Client: client, Retry: RetryPolicy{MaxRetries: 3, BaseDelay: time.Nanosecond, Sleep: func(context.Context, time.Duration) error { return nil }}}
	_, err := provider.Traverse(context.Background(), impact.TraverseRequest{Namespace: "project", SnapshotVersion: "revision", StartNodeIDs: []string{"a"}, RelationshipKinds: []string{"explicit"}, Direction: impact.DirectionIncoming, MaxDepth: 3, MaxNodes: 500}, "root")
	if err != nil || attempts.Load() != 3 {
		t.Fatalf("attempts=%d err=%v", attempts.Load(), err)
	}
}

func TestImpactQueryRejectsInvalidLimitsBeforeProvider(t *testing.T) {
	client, _ := New(Config{Endpoint: "http://127.0.0.1:1", HTTPClient: &http.Client{Timeout: time.Second}})
	_, err := client.Traverse(context.Background(), impact.TraverseRequest{Namespace: "project", SnapshotVersion: "revision", StartNodeIDs: []string{"a"}, RelationshipKinds: []string{"explicit"}, Direction: impact.DirectionIncoming, MaxDepth: 7, MaxNodes: 500}, "root")
	if err == nil || !strings.Contains(err.Error(), "contract") {
		t.Fatalf("error=%v", err)
	}
}

package projector

import (
	"encoding/json"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
)

// TestGraphCapacityFixtures is a deterministic, fixed-shape capacity
// generator. It deliberately uses only registered tag-parent relations so the
// measurement exercises projection, canonicalization, and delta planning
// without introducing inferred business edges.
func TestGraphCapacityFixtures(t *testing.T) {
	for _, profile := range []struct {
		name         string
		nodes, edges int
	}{
		{"typical_2000_nodes_20000_edges", 2_000, 20_000},
		{"limit_10000_nodes_100000_edges", 10_000, 100_000},
	} {
		t.Run(profile.name, func(t *testing.T) {
			started := time.Now()
			revision := generatedGraphRevision(t, profile.nodes, profile.edges)
			descriptor := Descriptor{SchemaVersion: ProjectionSchemaV1, Version: ProjectorV1, Relations: V1Relations(), Formatter: V1Formatter{}}
			base, err := Project(descriptor, revision)
			if err != nil || len(base.Nodes) != profile.nodes || len(base.Edges) != profile.edges {
				t.Fatalf("projection nodes=%d edges=%d err=%v", len(base.Nodes), len(base.Edges), err)
			}
			_, hash, err := ManifestBytes(base)
			if err != nil || len(hash) != 64 {
				t.Fatalf("canonical hash=%q err=%v", hash, err)
			}
			if _, err = PlanFull(string(revision.ProjectID), string(revision.RevisionID), base); err != nil {
				t.Fatalf("full plan: %v", err)
			}
			target := base
			target.Nodes = append([]Node(nil), base.Nodes...)
			target.Nodes[0].Label = "changed"
			delta, err := PlanDelta(string(revision.ProjectID), string(revision.RevisionID)+"-next", string(revision.RevisionID), base, target)
			if err != nil || len(delta.NodeUpserts) != 1 {
				t.Fatalf("delta upserts=%d err=%v", len(delta.NodeUpserts), err)
			}
			var memory runtime.MemStats
			runtime.ReadMemStats(&memory)
			elapsed := time.Since(started)
			t.Logf("nodes=%d edges=%d projection/canonical/delta=%s heap=%d", profile.nodes, profile.edges, elapsed, memory.HeapAlloc)
			if elapsed >= 60*time.Second {
				t.Fatalf("reference graph operation=%s, want <60s", elapsed)
			}
			if memory.HeapAlloc > 768<<20 {
				t.Fatalf("heap=%d, want <=768MiB", memory.HeapAlloc)
			}
		})
	}
}

func generatedGraphRevision(t *testing.T, nodes, edges int) Revision {
	t.Helper()
	project, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	revision, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	entities := make([]domain.Entity, nodes)
	for i := range entities {
		id, idErr := domain.NewID()
		if idErr != nil {
			t.Fatal(idErr)
		}
		entities[i] = domain.Entity{ID: id, Kind: domain.KindTag, Key: fmt.Sprintf("tag-%05d", i), Name: fmt.Sprintf("Tag %05d", i), Status: domain.StatusActive, SchemaVersion: 1, Payload: map[string]json.RawMessage{"category": json.RawMessage(`"capacity"`), "parent_tag_ids": json.RawMessage(`[]`)}}
	}
	references := make([]Reference, edges)
	for i := range references {
		references[i] = Reference{SourceID: entities[i%nodes].ID, TargetID: entities[(i+1)%nodes].ID, TargetKind: domain.KindTag, FieldPath: fmt.Sprintf("/tag_ids/%d", i/nodes), Ordinal: i / nodes}
	}
	return Revision{ProjectID: project, RevisionID: revision, ConfigHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Entities: entities, References: references}
}

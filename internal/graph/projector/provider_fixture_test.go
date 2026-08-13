package projector

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gowebpki/jcs"
)

func TestProviderV1CanonicalFixtureRoundTripsByteForByte(t *testing.T) {
	root := filepath.Join("..", "..", "..", "tests", "contract", "fixtures", "local-rag-graph-snapshot-v1")
	want, err := os.ReadFile(filepath.Join(root, "canonical-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	type node struct {
		ID         string         `json:"id"`
		Type       string         `json:"type"`
		Label      string         `json:"label"`
		Text       string         `json:"text"`
		Properties map[string]any `json:"properties"`
		Provenance map[string]any `json:"provenance"`
	}
	type edge struct {
		ID           string         `json:"id"`
		From         string         `json:"from"`
		To           string         `json:"to"`
		Type         string         `json:"type"`
		RelationKind string         `json:"relation_kind"`
		Confidence   int            `json:"confidence"`
		Properties   map[string]any `json:"properties"`
		Provenance   map[string]any `json:"provenance"`
	}
	var manifest struct {
		SchemaVersion string `json:"schema_version"`
		Nodes         []node `json:"nodes"`
		Edges         []edge `json:"edges"`
	}
	if err = json.Unmarshal(want, &manifest); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	got, err := jcs.Transform(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), `{"edges":`) {
		t.Fatalf("provider JCS root order drifted: %s", got)
	}
	hash, err := os.ReadFile(filepath.Join(root, "expected-hash.txt"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(got)
	if hex.EncodeToString(sum[:]) != strings.TrimSpace(string(hash)) {
		t.Fatalf("hash=%x want=%s", sum, hash)
	}
}

func TestManifestBytesUsesProviderJCSForUnicodeDecimalsAndObjectOrder(t *testing.T) {
	base := Result{Nodes: []Node{{
		ID:         "node",
		Type:       "attribute",
		Label:      "水",
		Text:       "水位 1.23",
		Properties: map[string]any{"zeta": "最后", "nested": map[string]any{"beta": "b", "alpha": "a"}, "decimal": "1.23", "unicode": "水"},
		Provenance: NodeProvenance{ProjectID: "project", RevisionID: "revision", ConfigHash: strings.Repeat("a", 64), EntityID: "entity", EntityKind: "attribute", EntitySchemaVersion: 1, ProjectionSchema: string(ProjectionSchemaV1), Projector: string(ProjectorV1)},
	}}}
	bytes, hash, err := ManifestBytes(base)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bytes), `"properties":{"decimal":"1.23","nested":{"alpha":"a","beta":"b"},"unicode":"水","zeta":"最后"}`) {
		t.Fatalf("properties are not provider-canonical: %s", bytes)
	}
	if !strings.Contains(string(bytes), `"label":"水"`) || hash == "" {
		t.Fatalf("unicode or hash missing: bytes=%s hash=%s", bytes, hash)
	}

	reordered := base
	reordered.Nodes = append([]Node(nil), base.Nodes...)
	reordered.Nodes[0].Properties = map[string]any{"unicode": "水", "decimal": "1.23", "nested": map[string]any{"alpha": "a", "beta": "b"}, "zeta": "最后"}
	again, againHash, err := ManifestBytes(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(bytes) || againHash != hash {
		t.Fatalf("object insertion order changed canonical output: %s != %s", again, bytes)
	}
}

func TestManifestHashExcludesTransportMetadataAndDiffersFromConfigHash(t *testing.T) {
	target := Result{Nodes: []Node{{
		ID: "node", Type: "attribute", Label: "label", Text: "text", Properties: map[string]any{},
		Provenance: NodeProvenance{ProjectID: "project", RevisionID: "revision", ConfigHash: strings.Repeat("a", 64), EntityID: "entity", EntityKind: "attribute", EntitySchemaVersion: 1, ProjectionSchema: string(ProjectionSchemaV1), Projector: string(ProjectorV1)},
	}}}
	first, err := PlanFull("project-a", "revision-a", target)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PlanFull("project-b", "revision-b", target)
	if err != nil {
		t.Fatal(err)
	}
	if first.ContentHash != second.ContentHash {
		t.Fatalf("transport identity entered manifest hash: %s != %s", first.ContentHash, second.ContentHash)
	}
	descriptor := Descriptor{SchemaVersion: ProjectionSchemaV1, Version: ProjectorV1, Relations: V1Relations(), Formatter: V1Formatter{}}
	summary, err := NewSummary("project", "revision", strings.Repeat("a", 64), descriptor, target, "")
	if err != nil {
		t.Fatal(err)
	}
	if summary.ManifestHash == summary.ConfigHash {
		t.Fatal("graph manifest hash must remain distinct from config hash")
	}
}

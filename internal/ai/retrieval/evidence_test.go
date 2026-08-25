package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	aiaudit "github.com/zouyi/eco-guardian/internal/ai/audit"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
)

type evidenceStoreFake struct{ sealed *PinnedEvidence }

func (s *evidenceStoreFake) InsertRetrievalEvidence(_ context.Context, value PinnedEvidence) (bool, error) {
	if s.sealed == nil {
		copy := clonePinnedEvidence(value)
		s.sealed = &copy
		return false, nil
	}
	if s.sealed.ManifestHash != value.ManifestHash || !bytes.Equal(s.sealed.Canonical, value.Canonical) {
		return false, ErrEvidenceConflict
	}
	return true, nil
}

func TestCanonicalizeEvidencePinsCompleteRecordsAndMinimalProviderView(t *testing.T) {
	response := hybridEvidenceResponse(t)
	pinned, err := CanonicalizeEvidence(response)
	if err != nil {
		t.Fatal(err)
	}
	if !pinned.ManifestHash.Valid() || len(pinned.Canonical) == 0 || len(pinned.Manifest.Evidence) != 1 || len(pinned.ProviderView) != 1 {
		t.Fatalf("pinned=%#v", pinned)
	}
	record := pinned.Manifest.Evidence[0]
	if !record.ID.Valid() || !record.RecordHash.Valid() || record.Node.ID != "alpha" || record.Citation != "alpha" || record.Seed.NodeID != "alpha" || len(record.Path.Nodes) != 1 || record.Scores.BM25Score == nil || *record.Scores.BM25Score != "-1" || record.Scores.RRFScore == "" || len(record.Generations) != 2 || record.Mode != "hybrid" || record.Degraded {
		t.Fatalf("record=%#v", record)
	}
	providerJSON, _ := json.Marshal(pinned.ProviderView)
	for _, forbidden := range [][]byte{[]byte(`"scores"`), []byte(`"node"`), []byte(`"generations"`), []byte(`"provenance"`), []byte(`"mode"`)} {
		if bytes.Contains(providerJSON, forbidden) {
			t.Fatalf("provider view leaked %s: %s", forbidden, providerJSON)
		}
	}
	if !bytes.Contains(providerJSON, []byte(`"evidence_id"`)) || !bytes.Contains(providerJSON, []byte(`"citation":"alpha"`)) {
		t.Fatalf("provider view=%s", providerJSON)
	}
}

func TestEvidenceCanonicalIdentityNormalizesDecimalsAndInsertOnlyReplay(t *testing.T) {
	response := hybridEvidenceResponse(t)
	first, err := CanonicalizeEvidence(response)
	if err != nil {
		t.Fatal(err)
	}
	alternate := hybridEvidenceResponse(t)
	bm25, confidence := json.Number("-1.0"), json.Number("1.000")
	alternate.Results[0].Scores.BM25Score = &bm25
	alternate.Results[0].PathConfidence = confidence
	second, err := CanonicalizeEvidence(alternate)
	if err != nil {
		t.Fatal(err)
	}
	if first.ManifestHash != second.ManifestHash || first.Manifest.Evidence[0].ID != second.Manifest.Evidence[0].ID || !bytes.Equal(first.Canonical, second.Canonical) {
		t.Fatal("equivalent decimal spelling changed evidence identity")
	}
	store := &evidenceStoreFake{}
	pinned, replayed, err := PinEvidence(context.Background(), store, response)
	if err != nil || replayed {
		t.Fatalf("first replay=%v err=%v", replayed, err)
	}
	secondPinned, replayed, err := PinEvidence(context.Background(), store, alternate)
	if err != nil || !replayed || secondPinned.ManifestHash != pinned.ManifestHash {
		t.Fatalf("second replay=%v err=%v", replayed, err)
	}
	changed := hybridEvidenceResponse(t)
	changed.Results[0].CitationText = "different citation"
	if _, _, err = PinEvidence(context.Background(), store, changed); !errors.Is(err, ErrEvidenceConflict) {
		t.Fatalf("changed insert err=%v", err)
	}
}

func TestEvidenceRejectsCitationAboveProviderBound(t *testing.T) {
	response := hybridEvidenceResponse(t)
	response.Results[0].CitationText = strings.Repeat("x", MaxProviderCitationBytes+1)
	if _, err := CanonicalizeEvidence(response); !errors.Is(err, ErrEvidenceInvalid) {
		t.Fatalf("err=%v", err)
	}
}

func TestPinnedEvidenceRedactionRecomputesEveryContentIdentity(t *testing.T) {
	const secret = "evidence-secret-canary"
	response := hybridEvidenceResponse(t)
	response.Results[0].CitationText = "citation " + secret
	response.Results[0].Node.Text = "node " + secret
	response.Results[0].Node.Properties = json.RawMessage(`{"api_key":"` + secret + `","analysis":"hidden","safe":true}`)
	response.Results[0].Evidence.Path.Nodes[0] = response.Results[0].Node
	pinned, err := CanonicalizeEvidence(response)
	if err != nil {
		t.Fatal(err)
	}
	redacted, err := RedactPinnedEvidence(pinned, aiaudit.NewRedactor([]byte(secret)))
	if err != nil || !redacted.Valid() {
		t.Fatalf("redacted=%#v err=%v", redacted, err)
	}
	if redacted.ManifestHash == pinned.ManifestHash || redacted.Manifest.Evidence[0].ID == pinned.Manifest.Evidence[0].ID {
		t.Fatal("redaction did not change content-derived identities")
	}
	if bytes.Contains(redacted.Canonical, []byte(secret)) || bytes.Contains(redacted.Canonical, []byte(`"analysis"`)) || !bytes.Contains(redacted.Canonical, []byte(aiaudit.Redacted)) {
		t.Fatalf("unsafe evidence=%s", redacted.Canonical)
	}
}

func hybridEvidenceResponse(t *testing.T) Response {
	t.Helper()
	body, err := os.ReadFile("../../../tests/contract/fixtures/local-rag-hybrid-graph-retrieval-v1/hybrid-response.json")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var response Response
	if err = decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	hash := aicontract.Hash(strings.Repeat("a", 64))
	projectID := aicontract.ProjectID("018f9e40-0000-7000-8000-000000000201")
	fixture := aicontract.V1Fixture()
	response.Request = Request{
		Base:  aicontract.FrozenBaseIdentity{ProjectID: projectID, ConfigRevisionID: "candidate", ConfigHash: hash, VersionManifestHash: hash, MaterializationHash: hash, GraphNamespace: string(projectID), GraphSnapshot: "candidate", GraphContentHash: hash},
		Query: "find alpha", Filters: Filters{NodeTypes: []string{"kind"}}, Budget: aicontract.Budget{Policy: fixture.Budget.Identity, BudgetLimits: fixture.Budget.Limits},
	}
	return response
}

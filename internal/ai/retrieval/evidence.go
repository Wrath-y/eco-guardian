package retrieval

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"unicode/utf8"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	"github.com/zouyi/eco-guardian/internal/formula"
)

const (
	EvidenceManifestVersionV1 = "retrieval-evidence-v1"
	MaxProviderCitationBytes  = 4_000
)

var (
	ErrEvidenceInvalid  = errors.New("retrieval evidence is invalid")
	ErrEvidenceConflict = errors.New("retrieval evidence conflicts with sealed content")
)

type PinnedGeneration struct {
	Component     string `json:"component"`
	Generation    string `json:"generation"`
	Algorithm     string `json:"algorithm"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	Dimensions    int    `json:"dimensions,omitempty"`
	Tokenizer     string `json:"tokenizer,omitempty"`
	ContentDigest string `json:"content_digest"`
}

type PinnedWarning struct {
	Stage     string `json:"stage"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type PinnedNode struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Label      string          `json:"label"`
	Text       string          `json:"text"`
	Properties json.RawMessage `json:"properties"`
	Provenance json.RawMessage `json:"provenance"`
}

type PinnedEdge struct {
	ID           string          `json:"id"`
	From         string          `json:"from"`
	To           string          `json:"to"`
	Type         string          `json:"type"`
	RelationKind string          `json:"relation_kind"`
	Confidence   string          `json:"confidence"`
	Properties   json.RawMessage `json:"properties"`
	Provenance   json.RawMessage `json:"provenance"`
}

type PinnedScores struct {
	BM25Rank    *int    `json:"bm25_rank,omitempty"`
	BM25Score   *string `json:"bm25_score,omitempty"`
	VectorRank  *int    `json:"vector_rank,omitempty"`
	VectorScore *string `json:"vector_score,omitempty"`
	RRFScore    string  `json:"rrf_score"`
	GraphScore  string  `json:"graph_score"`
	RerankScore *string `json:"rerank_score,omitempty"`
}

type PinnedPath struct {
	NodeIDs       []string     `json:"node_ids"`
	EdgeIDs       []string     `json:"edge_ids"`
	Nodes         []PinnedNode `json:"nodes"`
	Edges         []PinnedEdge `json:"edges"`
	ExplicitEdges []PinnedEdge `json:"explicit_edges"`
	InferredEdges []PinnedEdge `json:"inferred_edges"`
}

type EvidenceRefV1 struct {
	ID             aicontract.EvidenceID `json:"id"`
	RecordHash     aicontract.Hash       `json:"record_hash"`
	Rank           int                   `json:"rank"`
	Node           PinnedNode            `json:"node"`
	Citation       string                `json:"citation"`
	HopCount       int                   `json:"hop_count"`
	PathConfidence string                `json:"path_confidence"`
	Scores         PinnedScores          `json:"scores"`
	Seed           SeedEvidence          `json:"seed"`
	Path           PinnedPath            `json:"path"`
	Mode           string                `json:"mode"`
	Degraded       bool                  `json:"degraded"`
	Warnings       []PinnedWarning       `json:"warnings"`
	Generations    []PinnedGeneration    `json:"generations"`
	Rerank         string                `json:"rerank"`
}

type EvidenceManifestV1 struct {
	Version      string                        `json:"version"`
	Base         aicontract.FrozenBaseIdentity `json:"base"`
	RequestHash  aicontract.Hash               `json:"request_hash"`
	ResponseHash aicontract.Hash               `json:"response_hash"`
	Filters      Filters                       `json:"filters"`
	Limits       aicontract.BudgetLimits       `json:"limits"`
	Mode         string                        `json:"mode"`
	Degraded     bool                          `json:"degraded"`
	Warnings     []PinnedWarning               `json:"warnings"`
	Generations  []PinnedGeneration            `json:"generations"`
	Rerank       string                        `json:"rerank"`
	Evidence     []EvidenceRefV1               `json:"evidence"`
}

type ProviderCitation struct {
	EvidenceID aicontract.EvidenceID `json:"evidence_id"`
	Citation   string                `json:"citation"`
}

type PinnedEvidence struct {
	Manifest     EvidenceManifestV1
	Canonical    []byte
	ManifestHash aicontract.Hash
	ProviderView []ProviderCitation
}

type EvidenceRedactor interface {
	RedactString(string) string
	ProviderJSON([]byte, int) ([]byte, error)
}

// RedactPinnedEvidence removes configured secrets and hidden-reasoning fields
// before the evidence identity is sealed. All content-derived record and
// manifest hashes are recomputed over the retained representation.
func RedactPinnedEvidence(pinned PinnedEvidence, redactor EvidenceRedactor) (PinnedEvidence, error) {
	if !pinned.Valid() || redactor == nil {
		return PinnedEvidence{}, ErrEvidenceInvalid
	}
	body, err := json.Marshal(pinned.Manifest)
	if err != nil {
		return PinnedEvidence{}, ErrEvidenceInvalid
	}
	var manifest EvidenceManifestV1
	if err = json.Unmarshal(body, &manifest); err != nil {
		return PinnedEvidence{}, ErrEvidenceInvalid
	}
	redactWarnings := func(values []PinnedWarning) {
		for index := range values {
			values[index].Stage = redactor.RedactString(values[index].Stage)
			values[index].Code = redactor.RedactString(values[index].Code)
			values[index].Message = redactor.RedactString(values[index].Message)
		}
	}
	redactGenerations := func(values []PinnedGeneration) {
		for index := range values {
			values[index].Provider = redactor.RedactString(values[index].Provider)
			values[index].Model = redactor.RedactString(values[index].Model)
			values[index].Tokenizer = redactor.RedactString(values[index].Tokenizer)
		}
	}
	redactJSON := func(raw json.RawMessage) (json.RawMessage, error) {
		if len(raw) == 0 {
			return raw, nil
		}
		value, redactErr := redactor.ProviderJSON(raw, 32_768)
		return json.RawMessage(value), redactErr
	}
	redactNode := func(node *PinnedNode) error {
		node.Label = redactor.RedactString(node.Label)
		node.Text = redactor.RedactString(node.Text)
		var nodeErr error
		if node.Properties, nodeErr = redactJSON(node.Properties); nodeErr != nil {
			return nodeErr
		}
		node.Provenance, nodeErr = redactJSON(node.Provenance)
		return nodeErr
	}
	redactEdge := func(edge *PinnedEdge) error {
		var edgeErr error
		if edge.Properties, edgeErr = redactJSON(edge.Properties); edgeErr != nil {
			return edgeErr
		}
		edge.Provenance, edgeErr = redactJSON(edge.Provenance)
		return edgeErr
	}
	redactWarnings(manifest.Warnings)
	redactGenerations(manifest.Generations)
	providerView := make([]ProviderCitation, len(manifest.Evidence))
	for index := range manifest.Evidence {
		record := &manifest.Evidence[index]
		record.Citation = redactor.RedactString(record.Citation)
		redactWarnings(record.Warnings)
		redactGenerations(record.Generations)
		if err = redactNode(&record.Node); err != nil {
			return PinnedEvidence{}, ErrEvidenceInvalid
		}
		for nodeIndex := range record.Path.Nodes {
			if err = redactNode(&record.Path.Nodes[nodeIndex]); err != nil {
				return PinnedEvidence{}, ErrEvidenceInvalid
			}
		}
		for _, edges := range [][]PinnedEdge{record.Path.Edges, record.Path.ExplicitEdges, record.Path.InferredEdges} {
			for edgeIndex := range edges {
				if err = redactEdge(&edges[edgeIndex]); err != nil {
					return PinnedEvidence{}, ErrEvidenceInvalid
				}
			}
		}
		recordBody, marshalErr := json.Marshal(recordPayload(*record))
		if marshalErr != nil {
			return PinnedEvidence{}, ErrEvidenceInvalid
		}
		recordHash := domainHash("eco-guardian.ai-retrieval-evidence-ref/v1", recordBody)
		record.ID, record.RecordHash = aicontract.EvidenceID(recordHash), aicontract.Hash(recordHash)
		providerView[index] = ProviderCitation{EvidenceID: record.ID, Citation: record.Citation}
	}
	canonical, err := json.Marshal(manifest)
	if err != nil || len(canonical) > 262_144 {
		return PinnedEvidence{}, ErrEvidenceInvalid
	}
	result := PinnedEvidence{Manifest: manifest, Canonical: canonical, ManifestHash: aicontract.Hash(domainHash("eco-guardian.ai-retrieval-evidence-manifest/v1", canonical)), ProviderView: providerView}
	if !result.Valid() {
		return PinnedEvidence{}, ErrEvidenceInvalid
	}
	return result, nil
}

func (pinned PinnedEvidence) Valid() bool {
	if pinned.Manifest.Version != EvidenceManifestVersionV1 || !pinned.Manifest.Base.Valid() || !pinned.Manifest.RequestHash.Valid() || !pinned.Manifest.ResponseHash.Valid() || !pinned.ManifestHash.Valid() || len(pinned.Canonical) == 0 || len(pinned.Canonical) > 262_144 || len(pinned.Manifest.Evidence) > 100 || len(pinned.ProviderView) != len(pinned.Manifest.Evidence) {
		return false
	}
	canonical, err := json.Marshal(pinned.Manifest)
	if err != nil || !bytes.Equal(canonical, pinned.Canonical) || domainHash("eco-guardian.ai-retrieval-evidence-manifest/v1", canonical) != string(pinned.ManifestHash) {
		return false
	}
	for index, record := range pinned.Manifest.Evidence {
		body, err := json.Marshal(recordPayload(record))
		hash := domainHash("eco-guardian.ai-retrieval-evidence-ref/v1", body)
		if err != nil || record.ID != aicontract.EvidenceID(hash) || record.RecordHash != aicontract.Hash(hash) || record.Rank < 1 || pinned.ProviderView[index].EvidenceID != record.ID || pinned.ProviderView[index].Citation != record.Citation {
			return false
		}
	}
	return true
}

type EvidenceStore interface {
	InsertRetrievalEvidence(context.Context, PinnedEvidence) (bool, error)
}

func PinEvidence(ctx context.Context, store EvidenceStore, response Response) (PinnedEvidence, bool, error) {
	if store == nil {
		return PinnedEvidence{}, false, ErrEvidenceInvalid
	}
	pinned, err := CanonicalizeEvidence(response)
	if err != nil {
		return PinnedEvidence{}, false, err
	}
	replayed, err := store.InsertRetrievalEvidence(ctx, clonePinnedEvidence(pinned))
	return pinned, replayed, err
}

func CanonicalizeEvidence(response Response) (PinnedEvidence, error) {
	if err := ValidateResponse(response.Request, response); err != nil {
		return PinnedEvidence{}, errors.Join(ErrEvidenceInvalid, err)
	}
	requestHash, err := canonicalHash("eco-guardian.ai-retrieval-request/v1", canonicalRequest(response.Request))
	if err != nil {
		return PinnedEvidence{}, err
	}
	warnings := pinWarnings(response.Warnings)
	generations := pinGenerations(response)
	records := make([]EvidenceRefV1, 0, len(response.Results))
	providerView := make([]ProviderCitation, 0, len(response.Results))
	providerBytes := 0
	for _, result := range response.Results {
		if len(result.CitationText) > MaxProviderCitationBytes || !utf8.ValidString(result.CitationText) {
			return PinnedEvidence{}, ErrEvidenceInvalid
		}
		record, err := pinResult(result, response.ModeUsed, response.Degraded, warnings, generations, response.Rerank)
		if err != nil {
			return PinnedEvidence{}, err
		}
		recordBody, err := json.Marshal(recordPayload(record))
		if err != nil {
			return PinnedEvidence{}, ErrEvidenceInvalid
		}
		recordHash := domainHash("eco-guardian.ai-retrieval-evidence-ref/v1", recordBody)
		record.ID, record.RecordHash = aicontract.EvidenceID(recordHash), aicontract.Hash(recordHash)
		records = append(records, record)
		providerBytes += len(record.ID) + len(record.Citation)
		if providerBytes > response.Request.Budget.MaxContextBytes {
			return PinnedEvidence{}, ErrEvidenceInvalid
		}
		providerView = append(providerView, ProviderCitation{EvidenceID: record.ID, Citation: record.Citation})
	}
	responseHash, err := canonicalHash("eco-guardian.ai-retrieval-response/v1", canonicalResponse(response, records))
	if err != nil {
		return PinnedEvidence{}, err
	}
	manifest := EvidenceManifestV1{
		Version: EvidenceManifestVersionV1, Base: response.Request.Base, RequestHash: requestHash, ResponseHash: responseHash,
		Filters: cloneFilters(response.Request.Filters), Limits: response.Request.Budget.BudgetLimits,
		Mode: response.ModeUsed, Degraded: response.Degraded, Warnings: warnings, Generations: generations, Rerank: response.Rerank, Evidence: records,
	}
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return PinnedEvidence{}, ErrEvidenceInvalid
	}
	manifestHash := aicontract.Hash(domainHash("eco-guardian.ai-retrieval-evidence-manifest/v1", canonical))
	return PinnedEvidence{Manifest: manifest, Canonical: canonical, ManifestHash: manifestHash, ProviderView: providerView}, nil
}

func pinResult(result Result, mode string, degraded bool, warnings []PinnedWarning, generations []PinnedGeneration, rerank string) (EvidenceRefV1, error) {
	node, err := pinNode(result.Node)
	if err != nil {
		return EvidenceRefV1{}, err
	}
	path, err := pinPath(result.Evidence.Path)
	if err != nil {
		return EvidenceRefV1{}, err
	}
	confidence, err := decimal(result.PathConfidence)
	if err != nil {
		return EvidenceRefV1{}, err
	}
	scores, err := pinScores(result.Scores)
	if err != nil {
		return EvidenceRefV1{}, err
	}
	return EvidenceRefV1{
		Rank: result.Rank, Node: node, Citation: result.CitationText, HopCount: result.HopCount, PathConfidence: confidence,
		Scores: scores, Seed: *result.Evidence.Seed, Path: path, Mode: mode, Degraded: degraded,
		Warnings: append([]PinnedWarning(nil), warnings...), Generations: append([]PinnedGeneration(nil), generations...), Rerank: rerank,
	}, nil
}

func pinNode(value Node) (PinnedNode, error) {
	properties, err := canonicalObject(value.Properties)
	if err != nil {
		return PinnedNode{}, err
	}
	provenance, err := canonicalObject(value.Provenance)
	if err != nil {
		return PinnedNode{}, err
	}
	return PinnedNode{ID: value.ID, Type: value.Type, Label: value.Label, Text: value.Text, Properties: properties, Provenance: provenance}, nil
}

func pinEdge(value Edge) (PinnedEdge, error) {
	confidence, err := decimal(value.Confidence)
	if err != nil {
		return PinnedEdge{}, err
	}
	properties, err := canonicalObject(value.Properties)
	if err != nil {
		return PinnedEdge{}, err
	}
	provenance, err := canonicalObject(value.Provenance)
	if err != nil {
		return PinnedEdge{}, err
	}
	return PinnedEdge{ID: value.ID, From: value.From, To: value.To, Type: value.Type, RelationKind: value.RelationKind, Confidence: confidence, Properties: properties, Provenance: provenance}, nil
}

func pinPath(value *PathEvidence) (PinnedPath, error) {
	if value == nil {
		return PinnedPath{}, ErrEvidenceInvalid
	}
	result := PinnedPath{NodeIDs: append([]string(nil), value.NodeIDs...), EdgeIDs: append([]string(nil), value.EdgeIDs...)}
	var err error
	if result.Nodes, err = pinNodes(value.Nodes); err != nil {
		return PinnedPath{}, err
	}
	if result.Edges, err = pinEdges(value.Edges); err != nil {
		return PinnedPath{}, err
	}
	if result.ExplicitEdges, err = pinEdges(value.ExplicitEdges); err != nil {
		return PinnedPath{}, err
	}
	if result.InferredEdges, err = pinEdges(value.InferredEdges); err != nil {
		return PinnedPath{}, err
	}
	return result, nil
}

func pinNodes(values []Node) ([]PinnedNode, error) {
	result := make([]PinnedNode, len(values))
	for index, value := range values {
		var err error
		if result[index], err = pinNode(value); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func pinEdges(values []Edge) ([]PinnedEdge, error) {
	result := make([]PinnedEdge, len(values))
	for index, value := range values {
		var err error
		if result[index], err = pinEdge(value); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func pinScores(value Scores) (PinnedScores, error) {
	rrf, err := decimal(value.RRFScore)
	if err != nil {
		return PinnedScores{}, err
	}
	graph, err := decimal(value.GraphScore)
	if err != nil {
		return PinnedScores{}, err
	}
	result := PinnedScores{BM25Rank: cloneInt(value.BM25Rank), VectorRank: cloneInt(value.VectorRank), RRFScore: rrf, GraphScore: graph}
	if result.BM25Score, err = decimalPointer(value.BM25Score); err != nil {
		return PinnedScores{}, err
	}
	if result.VectorScore, err = decimalPointer(value.VectorScore); err != nil {
		return PinnedScores{}, err
	}
	if result.RerankScore, err = decimalPointer(value.RerankScore); err != nil {
		return PinnedScores{}, err
	}
	return result, nil
}

func pinWarnings(values []Warning) []PinnedWarning {
	result := make([]PinnedWarning, len(values))
	for index, value := range values {
		result[index] = PinnedWarning(value)
	}
	return result
}

func pinGenerations(response Response) []PinnedGeneration {
	result := []PinnedGeneration{}
	if response.FTSGeneration != nil {
		result = append(result, PinnedGeneration(*response.FTSGeneration))
	}
	if response.VectorGeneration != nil {
		result = append(result, PinnedGeneration(*response.VectorGeneration))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Component < result[j].Component })
	return result
}

func decimal(value json.Number) (string, error) {
	parsed, err := formula.ParseDecimal(string(value))
	if err != nil {
		return "", errors.Join(ErrEvidenceInvalid, err)
	}
	return parsed.String(), nil
}

func decimalPointer(value *json.Number) (*string, error) {
	if value == nil {
		return nil, nil
	}
	canonical, err := decimal(*value)
	return &canonical, err
}

func canonicalObject(raw json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, ErrEvidenceInvalid
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, ErrEvidenceInvalid
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, ErrEvidenceInvalid
	}
	canonical, err := canonicalizeJSONNumbers(object)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(canonical)
	return body, err
}

func canonicalizeJSONNumbers(value any) (any, error) {
	switch typed := value.(type) {
	case json.Number:
		canonical, err := decimal(typed)
		return json.Number(canonical), err
	case map[string]any:
		for key, child := range typed {
			canonical, err := canonicalizeJSONNumbers(child)
			if err != nil {
				return nil, err
			}
			typed[key] = canonical
		}
	case []any:
		for index, child := range typed {
			canonical, err := canonicalizeJSONNumbers(child)
			if err != nil {
				return nil, err
			}
			typed[index] = canonical
		}
	}
	return value, nil
}

func canonicalHash(domain string, value any) (aicontract.Hash, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return "", errors.Join(ErrEvidenceInvalid, err)
	}
	return aicontract.Hash(domainHash(domain, body)), nil
}

func domainHash(domain string, body []byte) string {
	hash := sha256.New()
	hash.Write([]byte(domain))
	hash.Write([]byte{0})
	hash.Write(body)
	return hex.EncodeToString(hash.Sum(nil))
}

func canonicalRequest(value Request) any {
	return struct {
		Base              aicontract.FrozenBaseIdentity `json:"base"`
		Query             string                        `json:"query"`
		Filters           Filters                       `json:"filters"`
		RelationshipKinds []string                      `json:"relationship_kinds"`
		Limits            aicontract.BudgetLimits       `json:"limits"`
	}{value.Base, value.Query, cloneFilters(value.Filters), []string{"explicit"}, value.Budget.BudgetLimits}
}

func canonicalResponse(response Response, records []EvidenceRefV1) any {
	payloads := make([]any, len(records))
	for index, record := range records {
		payloads[index] = recordPayload(record)
	}
	return struct {
		ResolvedSnapshotVersion string `json:"resolved_snapshot_version"`
		ContentHash             string `json:"content_hash"`
		Mode                    string `json:"mode"`
		Degraded                bool   `json:"degraded"`
		Rerank                  string `json:"rerank"`
		Evidence                []any  `json:"evidence"`
	}{response.ResolvedSnapshotVersion, response.ContentHash, response.ModeUsed, response.Degraded, response.Rerank, payloads}
}

func recordPayload(value EvidenceRefV1) any {
	value.ID, value.RecordHash = "", ""
	return value
}

func cloneFilters(value Filters) Filters {
	return Filters{NodeTypes: append([]string(nil), value.NodeTypes...), EdgeTypes: append([]string(nil), value.EdgeTypes...)}
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

func clonePinnedEvidence(value PinnedEvidence) PinnedEvidence {
	value.Canonical = append([]byte(nil), value.Canonical...)
	value.ProviderView = append([]ProviderCitation(nil), value.ProviderView...)
	value.Manifest.Evidence = append([]EvidenceRefV1(nil), value.Manifest.Evidence...)
	return value
}

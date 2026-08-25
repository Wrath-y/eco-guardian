package localrag

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/zouyi/eco-guardian/internal/ai/retrieval"
)

const maxResponseBytes int64 = 1 << 20

var (
	ErrConfiguration = errors.New("local-rag retrieval configuration is invalid")
	ErrRequest       = errors.New("local-rag retrieval request is invalid")
	ErrResponse      = errors.New("local-rag retrieval response is invalid")
)

type DependencyError struct {
	StatusCode      int
	Code            string
	Retryable       bool
	RebuildRequired bool
	RequestID       string
}

func (e *DependencyError) Error() string {
	return fmt.Sprintf("local-rag retrieval failed: %s", e.Code)
}

func (e *DependencyError) RetrievalFailureCode() string { return e.Code }
func (e *DependencyError) RetrievalRetryable() bool     { return e.Retryable }
func (e *DependencyError) RetrievalRebuildRequired() bool {
	return e.RebuildRequired
}
func (e *DependencyError) RetrievalRequestID() string { return e.RequestID }

type Client struct {
	base *url.URL
	http *http.Client
}

func New(endpoint string, client *http.Client) (*Client, error) {
	base, err := url.Parse(endpoint)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, ErrConfiguration
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &Client{base: base, http: client}, nil
}

type wireRequest struct {
	Query             string   `json:"query"`
	SnapshotVersion   string   `json:"snapshot_version"`
	NodeTypes         []string `json:"node_types,omitempty"`
	EdgeTypes         []string `json:"edge_types,omitempty"`
	RelationshipKinds []string `json:"relationship_kinds"`
	SeedLimit         int      `json:"seed_limit"`
	ResultLimit       int      `json:"result_limit"`
	GraphDepth        int      `json:"graph_depth"`
}

func (c *Client) Retrieve(ctx context.Context, request retrieval.Request) (retrieval.Response, error) {
	if c == nil || c.base == nil || c.http == nil || !request.Valid() {
		return retrieval.Response{}, ErrRequest
	}
	wire := wireRequest{
		Query: request.Query, SnapshotVersion: request.Base.GraphSnapshot,
		NodeTypes: append([]string(nil), request.Filters.NodeTypes...), EdgeTypes: append([]string(nil), request.Filters.EdgeTypes...),
		RelationshipKinds: []string{"explicit"}, SeedLimit: request.Budget.RetrievalSeedLimit,
		ResultLimit: request.Budget.RetrievalResultLimit, GraphDepth: request.Budget.RetrievalGraphDepth,
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return retrieval.Response{}, ErrRequest
	}
	target := *c.base
	target.Path = strings.TrimRight(target.Path, "/") + "/v1/graphs/" + url.PathEscape(request.Base.GraphNamespace) + "/retrieve"
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return retrieval.Response{}, ErrRequest
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	httpResponse, err := c.http.Do(httpRequest)
	if err != nil {
		return retrieval.Response{}, err
	}
	defer httpResponse.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(httpResponse.Body, maxResponseBytes+1))
	if err != nil {
		return retrieval.Response{}, err
	}
	if int64(len(responseBody)) > maxResponseBytes {
		return retrieval.Response{}, ErrResponse
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return retrieval.Response{}, decodeDependencyError(httpResponse.StatusCode, responseBody)
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var response retrieval.Response
	if err = decoder.Decode(&response); err != nil {
		return retrieval.Response{}, ErrResponse
	}
	if err = requireEOF(decoder); err != nil {
		return retrieval.Response{}, ErrResponse
	}
	response.Request = cloneRequest(request)
	if err = retrieval.ValidateResponse(request, response); err != nil {
		return retrieval.Response{}, err
	}
	return response, nil
}

func decodeDependencyError(status int, body []byte) error {
	var value struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
		Details   struct {
			RebuildRequired bool `json:"rebuild_required"`
		} `json:"details"`
		RequestID string `json:"request_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || requireEOF(decoder) != nil || value.Code == "" {
		return ErrResponse
	}
	return &DependencyError{StatusCode: status, Code: value.Code, Retryable: value.Retryable, RebuildRequired: value.Details.RebuildRequired, RequestID: safeRequestID(value.RequestID)}
}

func safeRequestID(value string) string {
	if value == "" || len(value) > 128 {
		return ""
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("._:/-", character) {
			continue
		}
		return ""
	}
	return value
}

func requireEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrResponse
	}
	return nil
}

func cloneRequest(value retrieval.Request) retrieval.Request {
	value.Filters.NodeTypes = append([]string(nil), value.Filters.NodeTypes...)
	value.Filters.EdgeTypes = append([]string(nil), value.Filters.EdgeTypes...)
	return value
}

var _ retrieval.Port = (*Client)(nil)

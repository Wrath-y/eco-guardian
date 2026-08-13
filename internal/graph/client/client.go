package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
)

const (
	maxResponseBytes int64 = 2 << 20
	defaultTimeout         = 10 * time.Second
)

var (
	ErrEndpointNotLoopback = errors.New("graph endpoint must be loopback-only")
	ErrContract            = errors.New("local-rag contract violation")
)

type Config struct {
	Endpoint         string
	HTTPClient       *http.Client
	MaxResponseBytes int64
}
type Client struct {
	base        *url.URL
	http        *http.Client
	maxResponse int64
}

func New(config Config) (*Client, error) {
	endpoint, err := loopbackURL(config.Endpoint)
	if err != nil {
		return nil, err
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	if httpClient.Timeout <= 0 {
		return nil, fmt.Errorf("%w: HTTP timeout is required", ErrContract)
	}
	limit := config.MaxResponseBytes
	if limit <= 0 {
		limit = maxResponseBytes
	}
	return &Client{base: endpoint, http: httpClient, maxResponse: limit}, nil
}

func loopbackURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Hostname() == "" || u.User != nil {
		return nil, ErrEndpointNotLoopback
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return nil, ErrEndpointNotLoopback
		}
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u, nil
}

func (c *Client) Health(ctx context.Context, requestID string) (graphsync.Health, error) {
	var response healthWire
	if err := c.call(ctx, http.MethodGet, "/health", requestID, nil, &response, http.StatusOK, http.StatusServiceUnavailable); err != nil {
		return graphsync.Health{}, err
	}
	return response.toDomain()
}
func (c *Client) InspectSnapshot(ctx context.Context, namespace, version, requestID string) (graphsync.Snapshot, error) {
	var response snapshotWire
	if err := c.call(ctx, http.MethodGet, snapshotPath(namespace, version), requestID, nil, &response, http.StatusOK); err != nil {
		return graphsync.Snapshot{}, err
	}
	return response.toDomain()
}
func (c *Client) PutSnapshot(ctx context.Context, namespace, version string, request graphsync.PutSnapshotRequest, requestID string) (graphsync.Snapshot, error) {
	if err := validatePut(request); err != nil {
		return graphsync.Snapshot{}, err
	}
	var response snapshotWire
	if err := c.call(ctx, http.MethodPut, snapshotPath(namespace, version), requestID, request, &response, http.StatusOK, http.StatusAccepted); err != nil {
		return graphsync.Snapshot{}, err
	}
	return response.toDomain()
}
func (c *Client) GetTask(ctx context.Context, taskID, requestID string) (graphsync.Task, error) {
	if strings.TrimSpace(taskID) == "" {
		return graphsync.Task{}, fmt.Errorf("%w: missing task ID", ErrContract)
	}
	var response taskWire
	if err := c.call(ctx, http.MethodGet, "/v1/tasks/"+url.PathEscape(taskID), requestID, nil, &response, http.StatusOK); err != nil {
		return graphsync.Task{}, err
	}
	return response.toDomain()
}
func (c *Client) ActivateSnapshot(ctx context.Context, namespace, version, requestID string) (graphsync.Activation, error) {
	var response activationWire
	if err := c.call(ctx, http.MethodPost, snapshotPath(namespace, version)+"/activate", requestID, nil, &response, http.StatusOK); err != nil {
		return graphsync.Activation{}, err
	}
	return response.toDomain(namespace, version)
}
func (c *Client) DeleteSnapshotForRetry(ctx context.Context, namespace, version, requestID string) error {
	return c.call(ctx, http.MethodDelete, snapshotPath(namespace, version), requestID, nil, nil, http.StatusNoContent)
}

func snapshotPath(namespace, version string) string {
	return "/v1/graphs/" + url.PathEscape(namespace) + "/snapshots/" + url.PathEscape(version)
}

func (c *Client) call(ctx context.Context, method, path, requestID string, request, response any, allowed ...int) error {
	if strings.TrimSpace(requestID) == "" {
		return fmt.Errorf("%w: request ID is required", ErrContract)
	}
	var body io.Reader
	if request != nil {
		encoded, err := json.Marshal(request)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	u := *c.base
	decodedPath, err := url.PathUnescape(path)
	if err != nil {
		return fmt.Errorf("%w: invalid route", ErrContract)
	}
	prefix := strings.TrimSuffix(c.base.Path, "/")
	u.Path = prefix + decodedPath
	u.RawPath = prefix + path
	httpRequest, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return err
	}
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("X-Request-ID", requestID)
	if request != nil {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	// Snapshot PUT uses content-addressed idempotency and intentionally never
	// sends Idempotency-Key.
	httpResponse, err := c.http.Do(httpRequest)
	if err != nil {
		return err
	}
	defer httpResponse.Body.Close()
	data, err := io.ReadAll(io.LimitReader(httpResponse.Body, c.maxResponse+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > c.maxResponse {
		return fmt.Errorf("%w: response too large", ErrContract)
	}
	for _, status := range allowed {
		if httpResponse.StatusCode == status {
			if response == nil || len(data) == 0 {
				return nil
			}
			if err := json.Unmarshal(data, response); err != nil {
				return fmt.Errorf("%w: invalid JSON response", ErrContract)
			}
			return nil
		}
	}
	providerError := graphsync.ProviderError{}
	if err := json.Unmarshal(data, &providerError); err != nil || !providerError.Valid() {
		return fmt.Errorf("%w: HTTP %d", ErrContract, httpResponse.StatusCode)
	}
	return &providerError
}

// Retry executes only caller-designated idempotent work. It does not know how
// to retry destructive cleanup or non-idempotent commands.
type RetryPolicy struct {
	MaxRetries int
	BaseDelay  time.Duration
	Sleep      func(context.Context, time.Duration) error
}

type RetryAttempt struct {
	RootRequestID    string
	AttemptRequestID string
	Number           int
}

// DoWithCorrelation retains one eco root request ID while exposing a distinct
// bounded attempt identity to persistence/diagnostics. Callers may pass the
// root ID on the wire and retain AttemptRequestID locally.
func (p RetryPolicy) DoWithCorrelation(ctx context.Context, rootRequestID string, attempt func(context.Context, RetryAttempt) error) error {
	if strings.TrimSpace(rootRequestID) == "" {
		return fmt.Errorf("root request ID is required")
	}
	return p.Do(ctx, func(ctx context.Context, number int) error {
		return attempt(ctx, RetryAttempt{RootRequestID: rootRequestID, AttemptRequestID: fmt.Sprintf("%s.%d", rootRequestID, number), Number: number})
	})
}

func (p RetryPolicy) Do(ctx context.Context, attempt func(context.Context, int) error) error {
	max := p.MaxRetries
	if max == 0 {
		max = 3
	}
	if max < 1 {
		return fmt.Errorf("invalid retry count")
	}
	base := p.BaseDelay
	if base <= 0 {
		base = 50 * time.Millisecond
	}
	sleep := p.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	var err error
	for index := 0; index < max; index++ {
		if err = attempt(ctx, index+1); err == nil {
			return nil
		}
		provider, isProvider := err.(*graphsync.ProviderError)
		if isProvider && !provider.Retryable {
			return err
		}
		if !isProvider && !isTransient(err) {
			return err
		}
		if index+1 == max {
			break
		}
		delay := base << index
		jitter := time.Duration(rand.Int64N(int64(delay)/2 + 1))
		if err = sleep(ctx, delay/2+jitter); err != nil {
			return err
		}
	}
	return err
}
func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func isTransient(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError) && (networkError.Timeout() || networkError.Temporary())
}

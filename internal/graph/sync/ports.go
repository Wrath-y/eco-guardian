package sync

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

const SnapshotSchemaVersion = "1.0"

type Node struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Label      string         `json:"label"`
	Text       string         `json:"text"`
	Properties map[string]any `json:"properties"`
	Provenance map[string]any `json:"provenance"`
}

type Edge struct {
	ID           string         `json:"id"`
	From         string         `json:"from"`
	To           string         `json:"to"`
	Type         string         `json:"type"`
	RelationKind string         `json:"relation_kind"`
	Confidence   float64        `json:"confidence"`
	Properties   map[string]any `json:"properties"`
	Provenance   map[string]any `json:"provenance"`
}

type PutSnapshotRequest struct {
	SchemaVersion string   `json:"schema_version"`
	Mode          string   `json:"mode"`
	BaseVersion   string   `json:"base_version,omitempty"`
	ContentHash   string   `json:"content_hash"`
	Nodes         []Node   `json:"nodes,omitempty"`
	Edges         []Edge   `json:"edges,omitempty"`
	NodeUpserts   []Node   `json:"node_upserts,omitempty"`
	NodeDeletes   []string `json:"node_deletes,omitempty"`
	EdgeUpserts   []Edge   `json:"edge_upserts,omitempty"`
	EdgeDeletes   []string `json:"edge_deletes,omitempty"`
}

type Component struct {
	Name, State, Generation string
	Error                   *ProviderError
}
type Warning struct {
	Code    string
	Message string
}
type Snapshot struct {
	Namespace, Version, BaseVersion, SchemaVersion, ContentHash, TaskID, Status string
	NodeCount, EdgeCount                                                        int
	QueryReady                                                                  bool
	Components                                                                  []Component
	Warnings                                                                    []string
}
type Task struct {
	ID, Operation, Namespace, SnapshotVersion, State, Phase, RequestID string
	Progress                                                           float64
	Warnings                                                           []string
	CreatedAt, StartedAt, FinishedAt                                   *time.Time
	Error                                                              *ProviderError
	Result                                                             json.RawMessage
}
type Activation struct {
	Namespace, ActiveVersion string
	Changed                  bool
}
type HealthState struct {
	Name, State, Reason, Provider, Model string
	CheckedAt                            *time.Time
}
type Limit struct {
	Name  string
	Value int
}
type Health struct {
	SchemaVersion, Status, Service, ServiceVersion string
	APIVersions, SupportedSchemaVersions           []string
	Capabilities, Dependencies                     []HealthState
	Limits                                         []Limit
}

// ProviderError contains only the stable, transport-safe provider envelope.
// Raw response bodies must never cross this boundary.
type ProviderError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"request_id"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details"`
}

func (e *ProviderError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code
}
func (e *ProviderError) Valid() bool {
	return e != nil && strings.TrimSpace(e.Code) != "" && strings.TrimSpace(e.Message) != "" && strings.TrimSpace(e.RequestID) != "" && e.Details != nil
}

type GraphProvider interface {
	Health(context.Context, string) (Health, error)
	InspectSnapshot(context.Context, string, string, string) (Snapshot, error)
	PutSnapshot(context.Context, string, string, PutSnapshotRequest, string) (Snapshot, error)
	GetTask(context.Context, string, string) (Task, error)
	ActivateSnapshot(context.Context, string, string, string) (Activation, error)
	DeleteSnapshotForRetry(context.Context, string, string, string) error
}

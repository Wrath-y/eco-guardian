package client

import (
	"fmt"
	"strings"
	"time"

	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
)

type stateWire struct {
	Name      string     `json:"name"`
	State     string     `json:"state"`
	Reason    string     `json:"reason"`
	Provider  string     `json:"provider"`
	Model     string     `json:"model"`
	CheckedAt *time.Time `json:"checked_at"`
}
type limitWire struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}
type healthWire struct {
	SchemaVersion           string      `json:"schema_version"`
	Status                  string      `json:"status"`
	Service                 string      `json:"service"`
	ServiceVersion          string      `json:"service_version"`
	APIVersions             []string    `json:"api_versions"`
	SupportedSchemaVersions []string    `json:"supported_schema_versions"`
	Capabilities            []stateWire `json:"capabilities"`
	Limits                  []limitWire `json:"limits"`
	Dependencies            []stateWire `json:"dependencies"`
}

func (w healthWire) toDomain() (graphsync.Health, error) {
	if w.SchemaVersion != graphsync.SnapshotSchemaVersion || !oneOf(w.Status, "ok", "degraded", "unavailable") || w.Service != "local-rag" || len(w.APIVersions) == 0 || len(w.SupportedSchemaVersions) == 0 {
		return graphsync.Health{}, fmt.Errorf("%w: invalid health response", ErrContract)
	}
	return graphsync.Health{SchemaVersion: w.SchemaVersion, Status: w.Status, Service: w.Service, ServiceVersion: w.ServiceVersion, APIVersions: append([]string(nil), w.APIVersions...), SupportedSchemaVersions: append([]string(nil), w.SupportedSchemaVersions...), Capabilities: states(w.Capabilities), Dependencies: states(w.Dependencies), Limits: limits(w.Limits)}, nil
}
func states(values []stateWire) []graphsync.HealthState {
	out := make([]graphsync.HealthState, 0, len(values))
	for _, value := range values {
		out = append(out, graphsync.HealthState{Name: value.Name, State: value.State, Reason: value.Reason, Provider: value.Provider, Model: value.Model, CheckedAt: value.CheckedAt})
	}
	return out
}
func limits(values []limitWire) []graphsync.Limit {
	out := make([]graphsync.Limit, 0, len(values))
	for _, value := range values {
		out = append(out, graphsync.Limit{Name: value.Name, Value: value.Value})
	}
	return out
}

type componentWire struct {
	Name       string                   `json:"name"`
	State      string                   `json:"state"`
	Generation string                   `json:"generation"`
	Error      *graphsync.ProviderError `json:"error"`
}
type snapshotWire struct {
	Namespace     string          `json:"namespace"`
	Version       string          `json:"version"`
	BaseVersion   *string         `json:"base_version"`
	SchemaVersion string          `json:"schema_version"`
	ContentHash   string          `json:"content_hash"`
	NodeCount     int             `json:"node_count"`
	EdgeCount     int             `json:"edge_count"`
	TaskID        string          `json:"task_id"`
	Status        string          `json:"status"`
	QueryReady    bool            `json:"query_ready"`
	Components    []componentWire `json:"components"`
	Warnings      []string        `json:"warnings"`
}

func (w snapshotWire) toDomain() (graphsync.Snapshot, error) {
	if strings.TrimSpace(w.Namespace) == "" || strings.TrimSpace(w.Version) == "" || w.SchemaVersion != graphsync.SnapshotSchemaVersion || !hash(w.ContentHash) || w.NodeCount < 0 || w.EdgeCount < 0 || strings.TrimSpace(w.TaskID) == "" || !oneOf(w.Status, "building", "ready", "failed") || w.Components == nil || w.Warnings == nil {
		return graphsync.Snapshot{}, fmt.Errorf("%w: invalid snapshot response", ErrContract)
	}
	components := make([]graphsync.Component, 0, len(w.Components))
	for _, component := range w.Components {
		if !oneOf(component.Name, "graph", "fts", "vector") || !oneOf(component.State, "pending", "building", "ready", "failed", "unavailable") {
			return graphsync.Snapshot{}, fmt.Errorf("%w: invalid component", ErrContract)
		}
		components = append(components, graphsync.Component{Name: component.Name, State: component.State, Generation: component.Generation, Error: component.Error})
	}
	base := ""
	if w.BaseVersion != nil {
		base = *w.BaseVersion
	}
	return graphsync.Snapshot{Namespace: w.Namespace, Version: w.Version, BaseVersion: base, SchemaVersion: w.SchemaVersion, ContentHash: w.ContentHash, NodeCount: w.NodeCount, EdgeCount: w.EdgeCount, TaskID: w.TaskID, Status: w.Status, QueryReady: w.QueryReady, Components: components, Warnings: append([]string(nil), w.Warnings...)}, nil
}

type taskWire struct {
	ID              string                   `json:"task_id"`
	Operation       string                   `json:"operation"`
	Namespace       string                   `json:"namespace"`
	SnapshotVersion string                   `json:"snapshot_version"`
	State           string                   `json:"state"`
	Phase           string                   `json:"phase"`
	Progress        float64                  `json:"progress"`
	Warnings        []string                 `json:"warnings"`
	CreatedAt       *time.Time               `json:"created_at"`
	StartedAt       *time.Time               `json:"started_at"`
	FinishedAt      *time.Time               `json:"finished_at"`
	Error           *graphsync.ProviderError `json:"error"`
	Result          []byte                   `json:"result"`
}

func (w taskWire) toDomain() (graphsync.Task, error) {
	if strings.TrimSpace(w.ID) == "" || strings.TrimSpace(w.Operation) == "" || strings.TrimSpace(w.Namespace) == "" || strings.TrimSpace(w.SnapshotVersion) == "" || !oneOf(w.State, "queued", "running", "succeeded", "failed") || strings.TrimSpace(w.Phase) == "" || w.Progress < 0 || w.Progress > 1 || w.Warnings == nil || w.CreatedAt == nil || (w.Error != nil && !w.Error.Valid()) {
		return graphsync.Task{}, fmt.Errorf("%w: invalid task response", ErrContract)
	}
	return graphsync.Task{ID: w.ID, Operation: w.Operation, Namespace: w.Namespace, SnapshotVersion: w.SnapshotVersion, State: w.State, Phase: w.Phase, Progress: w.Progress, Warnings: append([]string(nil), w.Warnings...), CreatedAt: w.CreatedAt, StartedAt: w.StartedAt, FinishedAt: w.FinishedAt, Error: w.Error, Result: append([]byte(nil), w.Result...)}, nil
}

type activationWire struct {
	Namespace     string `json:"namespace"`
	ActiveVersion string `json:"active_version"`
	Changed       *bool  `json:"changed"`
}

func (w activationWire) toDomain(namespace, version string) (graphsync.Activation, error) {
	if w.Changed == nil || w.Namespace != namespace || w.ActiveVersion != version {
		return graphsync.Activation{}, fmt.Errorf("%w: invalid activation response", ErrContract)
	}
	return graphsync.Activation{Namespace: w.Namespace, ActiveVersion: w.ActiveVersion, Changed: *w.Changed}, nil
}

func validatePut(request graphsync.PutSnapshotRequest) error {
	if request.SchemaVersion != graphsync.SnapshotSchemaVersion || !hash(request.ContentHash) {
		return fmt.Errorf("%w: invalid snapshot identity", ErrContract)
	}
	switch request.Mode {
	case "full":
		if request.BaseVersion != "" || request.Nodes == nil || request.Edges == nil {
			return fmt.Errorf("%w: invalid full snapshot", ErrContract)
		}
	case "delta":
		if strings.TrimSpace(request.BaseVersion) == "" {
			return fmt.Errorf("%w: invalid delta snapshot", ErrContract)
		}
	default:
		return fmt.Errorf("%w: invalid snapshot mode", ErrContract)
	}
	return nil
}
func hash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}
func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

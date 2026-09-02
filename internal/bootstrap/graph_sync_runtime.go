package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	graphclient "github.com/zouyi/eco-guardian/internal/graph/client"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	"github.com/zouyi/eco-guardian/internal/project"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
	"github.com/zouyi/eco-guardian/internal/validation"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

var errGraphSyncRuntimeUnavailable = errors.New("graph sync runtime unavailable")

// graphSyncRuntime owns only process lifecycle and dispatch. Projection,
// provider submission, polling, verification, and durable commits remain in
// their existing module services. No path or process behavior is OS-specific.
type graphSyncRuntime struct {
	projects   *project.Manager
	provider   graphsync.GraphProvider
	descriptor projector.Descriptor

	mu       sync.Mutex
	cancel   context.CancelFunc
	queue    chan domain.ID
	inflight map[domain.ID]struct{}
	wait     sync.WaitGroup
	started  bool
}

func (runtime *graphSyncRuntime) Start(context.Context) error {
	if runtime == nil || runtime.projects == nil || runtime.provider == nil || !runtime.descriptor.Valid() {
		return errGraphSyncRuntimeUnavailable
	}
	runtime.mu.Lock()
	if runtime.started {
		runtime.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	runtime.cancel = cancel
	runtime.queue = make(chan domain.ID, 128)
	runtime.inflight = map[domain.ID]struct{}{}
	runtime.started = true
	runtime.wait.Add(1)
	runtime.mu.Unlock()
	go runtime.loop(ctx)
	return nil
}

func (runtime *graphSyncRuntime) Close(ctx context.Context) error {
	if runtime == nil {
		return nil
	}
	runtime.mu.Lock()
	if !runtime.started {
		runtime.mu.Unlock()
		return nil
	}
	cancel := runtime.cancel
	runtime.started = false
	runtime.mu.Unlock()
	cancel()
	done := make(chan struct{})
	go func() { runtime.wait.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (runtime *graphSyncRuntime) Submit(ctx context.Context, job graphsync.GraphJob) error {
	if runtime == nil || !job.Valid() {
		return errGraphSyncRuntimeUnavailable
	}
	return runtime.enqueue(ctx, job.ID)
}

func (runtime *graphSyncRuntime) enqueue(ctx context.Context, jobID domain.ID) error {
	if !jobID.Valid() {
		return errGraphSyncRuntimeUnavailable
	}
	runtime.mu.Lock()
	queue, started := runtime.queue, runtime.started
	runtime.mu.Unlock()
	if !started {
		return errGraphSyncRuntimeUnavailable
	}
	select {
	case queue <- jobID:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (runtime *graphSyncRuntime) loop(ctx context.Context) {
	defer runtime.wait.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	runtime.scan(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runtime.scan(ctx)
		case jobID := <-runtime.queue:
			runtime.dispatch(ctx, jobID)
		}
	}
}

func (runtime *graphSyncRuntime) scan(ctx context.Context) {
	s := runtime.activeStore()
	if s == nil {
		return
	}
	states, err := s.ListRecoverableGraphSyncStates(ctx, 100)
	if err != nil {
		return
	}
	for _, state := range states {
		jobID := domain.ID(state.LatestJobID)
		if jobID.Valid() {
			runtime.dispatch(ctx, jobID)
		}
	}
}

func (runtime *graphSyncRuntime) dispatch(ctx context.Context, jobID domain.ID) {
	runtime.mu.Lock()
	if _, exists := runtime.inflight[jobID]; exists || !runtime.started {
		runtime.mu.Unlock()
		return
	}
	runtime.inflight[jobID] = struct{}{}
	runtime.wait.Add(1)
	runtime.mu.Unlock()
	go func() {
		defer runtime.wait.Done()
		defer func() {
			runtime.mu.Lock()
			delete(runtime.inflight, jobID)
			runtime.mu.Unlock()
		}()
		runtime.run(ctx, jobID)
	}()
}

func (runtime *graphSyncRuntime) activeStore() *store.Store {
	if runtime == nil || runtime.projects == nil {
		return nil
	}
	handle, ok := runtime.projects.ActiveHandle()
	if !ok {
		return nil
	}
	provider, ok := handle.(interface{ Store() *store.Store })
	if !ok {
		return nil
	}
	return provider.Store()
}

func (runtime *graphSyncRuntime) run(ctx context.Context, jobID domain.ID) {
	s := runtime.activeStore()
	if s == nil {
		return
	}
	job, err := s.GetGraphJob(ctx, jobID)
	if err != nil || job.ProjectID != s.ProjectID() {
		return
	}
	// Optional Graph outages must not consume a queued durable Job. Leave it
	// eligible for the periodic scanner until a compatible provider is back.
	health, err := runtime.provider.Health(ctx, "graph-health-"+string(jobID))
	if err != nil || !graphclient.EvaluateCompatibility(health).Compatible {
		return
	}
	if _, found, stateErr := s.GetGraphSyncState(ctx, job.RevisionID); stateErr != nil {
		return
	} else if !found {
		if stateErr = s.CreateGraphSyncState(ctx, graphsync.SyncState{
			RevisionID: string(job.RevisionID), Pipeline: graphsync.StateQueued, LatestJobID: string(job.ID), Warnings: []string{},
		}); stateErr != nil {
			return
		}
	}
	worker := graphsync.PhaseWorker{Jobs: s, Events: s}
	if _, _, err = worker.Start(ctx, jobID); err != nil {
		return
	}
	revision, err := graphSyncRevision(ctx, s, job)
	if err != nil {
		runtime.fail(ctx, s, job, "GRAPH_VALIDATION_UNAVAILABLE")
		return
	}
	if _, _, err = worker.Checkpoint(ctx, jobID, graphsync.PhaseValidationConfirmed, 10, "", "", nil); err != nil {
		runtime.fail(ctx, s, job, "GRAPH_CHECKPOINT_FAILED")
		return
	}
	if _, _, err = (graphsync.PipelineMapper{States: s}).MarkBuilding(ctx, job.RevisionID); err != nil {
		runtime.fail(ctx, s, job, "GRAPH_STATE_TRANSITION_FAILED")
		return
	}

	projected, err := projector.Project(runtime.descriptor, revision)
	if err != nil {
		runtime.fail(ctx, s, job, "GRAPH_PROJECTION_FAILED")
		return
	}
	plan, err := projector.PlanFull(string(job.ProjectID), string(job.RevisionID), projected)
	if err != nil {
		runtime.fail(ctx, s, job, "GRAPH_PROJECTION_FAILED")
		return
	}
	summary, err := projector.NewSummary(string(job.ProjectID), string(job.RevisionID), job.InputHash, runtime.descriptor, projected, "")
	if err != nil || summary.ManifestHash != plan.ContentHash {
		runtime.fail(ctx, s, job, "GRAPH_PROJECTION_FAILED")
		return
	}
	if _, _, err = worker.Checkpoint(ctx, jobID, graphsync.PhaseProjected, 20, "", "", nil); err != nil {
		runtime.fail(ctx, s, job, "GRAPH_CHECKPOINT_FAILED")
		return
	}

	health, err = runtime.provider.Health(ctx, "graph-health-"+string(jobID))
	if err != nil || !graphclient.EvaluateCompatibility(health).Compatible {
		runtime.fail(ctx, s, job, "GRAPH_PROVIDER_INCOMPATIBLE")
		return
	}
	if _, _, err = worker.Checkpoint(ctx, jobID, graphsync.PhaseProviderCompatible, 30, "", "", nil); err != nil {
		runtime.fail(ctx, s, job, "GRAPH_CHECKPOINT_FAILED")
		return
	}

	request := graphsync.PutSnapshotRequest{
		SchemaVersion: graphsync.SnapshotSchemaVersion, Mode: "full", ContentHash: plan.ContentHash,
		Nodes: graphSnapshotNodes(plan.Nodes), Edges: graphSnapshotEdges(plan.Edges),
	}
	requestID := "graph-sync-" + string(jobID)
	if _, _, err = (graphsync.SubmissionService{States: s, Worker: worker, Provider: runtime.provider}).Submit(ctx, graphsync.SubmissionRequest{
		JobID: jobID, RevisionID: job.RevisionID, Namespace: string(job.ProjectID), Version: string(job.RevisionID), Snapshot: request, RequestID: requestID,
	}); err != nil {
		runtime.fail(ctx, s, job, graphFailureCode(err, "GRAPH_SUBMISSION_FAILED"))
		return
	}

	expectation := graphsync.SnapshotExpectation{Namespace: string(job.ProjectID), Version: string(job.RevisionID), ContentHash: plan.ContentHash, NodeCount: len(plan.Nodes), EdgeCount: len(plan.Edges)}
	poller := graphsync.PollingService{States: s, Worker: worker, Provider: runtime.provider}
	for {
		result, pollErr := poller.Poll(ctx, graphsync.PollingRequest{JobID: jobID, RevisionID: job.RevisionID, Namespace: string(job.ProjectID), Version: string(job.RevisionID), Expectation: expectation, RequestID: requestID})
		if pollErr != nil {
			runtime.fail(ctx, s, job, graphFailureCode(pollErr, "GRAPH_POLL_FAILED"))
			return
		}
		switch result.Task.State {
		case "queued", "running":
			timer := time.NewTimer(250 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		case "failed":
			_, _, _ = (graphsync.FailureService{States: s, Committer: s}).FailProviderTask(ctx, jobID, job.RevisionID, result.Task)
			return
		case "succeeded":
			if result.Verification == nil || !result.Verification.Ready {
				runtime.fail(ctx, s, job, "GRAPH_VERIFICATION_FAILED")
				return
			}
			evidence, _ := json.Marshal(map[string]any{
				"project_id": job.ProjectID, "revision_id": job.RevisionID, "config_hash": job.InputHash,
				"graph_manifest_hash": summary.ManifestHash, "provider_task_id": result.Task.ID,
			})
			if _, _, err = (graphsync.ReadyService{States: s, Worker: worker, Committer: s}).Commit(ctx, jobID, job.RevisionID, summary, string(evidence), *result.Verification); err != nil {
				runtime.fail(ctx, s, job, "GRAPH_READY_COMMIT_FAILED")
			}
			return
		default:
			runtime.fail(ctx, s, job, "GRAPH_TASK_STATE_INVALID")
			return
		}
	}
}

func graphSyncRevision(ctx context.Context, s *store.Store, job graphsync.GraphJob) (projector.Revision, error) {
	record, err := s.GetRevisionRecord(ctx, job.RevisionID)
	if err != nil || record.Metadata.ConfigHash != job.InputHash {
		return projector.Revision{}, errGraphSyncRuntimeUnavailable
	}
	versions, ok := graphValidationVersions(record.Metadata.Manifest)
	if !ok {
		return projector.Revision{}, errGraphSyncRuntimeUnavailable
	}
	gate := validation.NewValidationGate(s)
	state, err := gate.Check(ctx, job.RevisionID, job.InputHash, versions)
	if err != nil {
		return projector.Revision{}, err
	}
	if state == validation.GateRequiresValidation {
		if err = s.RunFullValidation(ctx, job.RevisionID); err != nil {
			return projector.Revision{}, err
		}
		state, err = gate.Check(ctx, job.RevisionID, job.InputHash, versions)
	}
	if err != nil || state != validation.GatePass {
		return projector.Revision{}, errGraphSyncRuntimeUnavailable
	}
	revision, err := s.ReadProjectionRevision(ctx, job.RevisionID)
	return revision, err
}

func graphValidationVersions(manifest versioningrevision.VersionManifest) (validation.VersionManifest, bool) {
	values := map[string]string{}
	for _, entry := range manifest.Entries {
		if entry.State == versioningrevision.Registered {
			values[entry.CapabilityID] = entry.ImplementationVersion
		}
	}
	versions := validation.VersionManifest{Schema: values["schema"], DSL: values["dsl"], Registry: values["validator-registry"], NumericPolicy: values["numeric-policy"]}
	return versions, versions.Valid()
}

func (runtime *graphSyncRuntime) fail(ctx context.Context, s *store.Store, job graphsync.GraphJob, code string) {
	_, _, _ = (graphsync.FailureService{States: s, Committer: s}).Fail(ctx, job.ID, job.RevisionID, code)
}

func graphFailureCode(err error, fallback string) string {
	var provider *graphsync.ProviderError
	if errors.As(err, &provider) && provider.Valid() {
		return provider.Code
	}
	return fallback
}

func graphSnapshotNodes(values []projector.Node) []graphsync.Node {
	result := make([]graphsync.Node, len(values))
	for index, value := range values {
		provenance := value.Provenance
		result[index] = graphsync.Node{
			ID: value.ID, Type: value.Type, Label: value.Label, Text: value.Text, Properties: value.Properties,
			Provenance: map[string]any{
				"project_id": provenance.ProjectID, "revision_id": provenance.RevisionID, "config_hash": provenance.ConfigHash,
				"entity_id": provenance.EntityID, "entity_kind": provenance.EntityKind, "entity_schema_version": provenance.EntitySchemaVersion,
				"projection_schema_version": provenance.ProjectionSchema, "projector_version": provenance.Projector,
			},
		}
	}
	return result
}

func graphSnapshotEdges(values []projector.Edge) []graphsync.Edge {
	result := make([]graphsync.Edge, len(values))
	for index, value := range values {
		provenance := value.Provenance
		result[index] = graphsync.Edge{
			ID: value.ID, From: value.From, To: value.To, Type: value.Type, RelationKind: value.RelationKind,
			Confidence: float64(value.Confidence), Properties: value.Properties,
			Provenance: map[string]any{
				"project_id": provenance.ProjectID, "revision_id": provenance.RevisionID, "config_hash": provenance.ConfigHash,
				"source_entity_id": provenance.SourceEntityID, "field_path": provenance.FieldPath, "ordinal": provenance.Ordinal,
				"projection_schema_version": provenance.ProjectionSchema, "projector_version": provenance.Projector,
			},
		}
	}
	return result
}

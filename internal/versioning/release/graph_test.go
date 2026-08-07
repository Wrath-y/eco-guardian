package release

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	versioning "github.com/zouyi/eco-guardian/internal/versioning"
)

func TestActivateGraphBindsNamespaceSnapshotAndIntentAndAcceptsVerifiedReplay(t *testing.T) {
	job := workerJob(t)
	intent := Intent{ID: workerID(t), JobID: job.ID, CandidateRevisionID: job.RevisionID, PolicyID: workerID(t), GateManifest: []byte(`{"gates":[]}`), GateManifestHash: strings.Repeat("a", 64), Backup: BackupEvidence{Online: true, IntegrityChecked: true, Checksum: strings.Repeat("b", 64)}, RequestHash: job.RequestHash, IdempotencyKey: job.IdempotencyKey, Phase: IntentRecorded, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	intent.GateManifestHash = versioning.SHA256(intent.GateManifest)
	request := GraphActivationRequest{ProjectID: job.ProjectID, RevisionID: job.RevisionID, ConfigHash: job.InputHash, IntentID: intent.ID}
	gate := &graphFake{evidence: GraphActivationEvidence{Changed: false, ProjectID: request.ProjectID, RevisionID: request.RevisionID, ConfigHash: request.ConfigHash, IntentID: request.IntentID}}
	if evidence, err := ActivateGraph(context.Background(), gate, job, intent); err != nil || evidence.Changed || gate.request != request {
		t.Fatalf("evidence=%#v request=%#v err=%v", evidence, gate.request, err)
	}
	gate.evidence.ConfigHash = strings.Repeat("c", 64)
	if _, err := ActivateGraph(context.Background(), gate, job, intent); !errors.Is(err, ErrGraphActivationInvalid) {
		t.Fatalf("mismatch=%v", err)
	}
}

type graphFake struct {
	request  GraphActivationRequest
	evidence GraphActivationEvidence
	err      error
}

func (f *graphFake) Activate(_ context.Context, request GraphActivationRequest) (GraphActivationEvidence, error) {
	f.request = request
	return f.evidence, f.err
}

package release

import (
	"context"
	"errors"
	"fmt"
)

var ErrGraphActivationInvalid = errors.New("graph activation could not be verified")

// ActivateGraph uses project UUID as namespace, the immutable config revision
// as snapshot version, and the intent ID as the external idempotency key.
// A verified changed=false response is a successful replay, never a shortcut.
func ActivateGraph(ctx context.Context, gate GraphActivationGate, job Job, intent Intent) (GraphActivationEvidence, error) {
	if gate == nil || !job.Valid() || !intent.Valid() || job.ID != intent.JobID || job.RevisionID != intent.CandidateRevisionID || job.RequestHash != intent.RequestHash {
		return GraphActivationEvidence{}, ErrGraphActivationInvalid
	}
	request := GraphActivationRequest{ProjectID: job.ProjectID, RevisionID: job.RevisionID, ConfigHash: job.InputHash, IntentID: intent.ID}
	evidence, err := gate.Activate(ctx, request)
	if err != nil || !evidence.Matches(request) {
		return GraphActivationEvidence{}, fmt.Errorf("%w: %v", ErrGraphActivationInvalid, err)
	}
	return evidence, nil
}

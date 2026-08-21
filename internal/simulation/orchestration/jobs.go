package orchestration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

var ErrSimulationJobInvalid = errors.New("simulation job is invalid")

func AutomaticIdempotencyKey(inputHash string) (string, error) {
	if len(inputHash) != 64 {
		return "", ErrSimulationJobInvalid
	}
	digest := sha256.Sum256([]byte("eco-guardian/simulation-automatic-key/v1\x00" + inputHash))
	return "simulation:auto:" + hex.EncodeToString(digest[:]), nil
}

// CreateSimulationJob delegates identity and same-key conflict handling to the
// shared durable Job Store. No simulation-specific parallel Job table exists.
func CreateSimulationJob(ctx context.Context, store sharedjob.Store, request sharedjob.Request) (sharedjob.Record, bool, error) {
	if store == nil || request.Kind != sharedjob.Kind("simulation") || !request.Valid() {
		return sharedjob.Record{}, false, ErrSimulationJobInvalid
	}
	return store.CreateOrGet(ctx, request)
}

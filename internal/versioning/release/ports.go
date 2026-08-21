package release

import (
	"context"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

type Clock interface{ Now() time.Time }
type IDGenerator interface{ New() (domain.ID, error) }

// CommandSources are the immutable reads required to validate a release
// request before it is allowed to reach a Job repository. The active pointer
// is deliberately read separately from the requested baseline so that an
// unknown ID and a concurrent-pointer mismatch remain distinct outcomes.
type CommandSources interface {
	GetRevisionRecord(context.Context, domain.ID) (versioningrevision.Record, error)
	GetPolicy(context.Context, domain.ID) (versioningpolicy.ReleasePolicy, error)
	GetRelease(context.Context, domain.ID) (Release, error)
	GetActivePointer(context.Context) (ActivePointer, error)
}

// TransactionRunner keeps final release/pointer commits atomic without tying
// the state machine to SQLite.
type TransactionRunner interface {
	Within(context.Context, func(context.Context) error) error
}

// JobStore and EventSink are persistence ports. Concrete record types are
// introduced with the release state machine; adapters own storage details.
type JobStore interface {
	Create(context.Context, domain.ID, []byte) error
	Get(context.Context, domain.ID) ([]byte, error)
	CompareAndSwap(context.Context, domain.ID, string, string, []byte) (bool, error)
}

// IdempotentJobRepository creates a queued Job once per project/key/canonical
// request. Replays return the original immutable Job; changed input is a
// stable conflict rather than a second external side effect.
type IdempotentJobRepository interface {
	CreateOrGetReleaseJob(context.Context, JobRequest) (Job, bool, error)
}

// DurableJobRepository is the worker boundary. A transition is conditional on
// the caller's observed status, providing durable compare-and-swap ownership.
type DurableJobRepository interface {
	GetReleaseJob(context.Context, domain.ID) (Job, error)
	TransitionReleaseJob(context.Context, domain.ID, JobStatus, JobStatus, *JobResult) (Job, bool, error)
}

// CancellationIntentRepository is optional for legacy fakes but implemented
// by the shared durable Job adapter before terminal state resolution.
type CancellationIntentRepository interface {
	RequestReleaseCancellation(context.Context, domain.ID) (Job, bool, error)
}

// ActiveJobReader lets the active-project manager install a release close
// guard without importing storage or knowing Job table details.
type ActiveJobReader interface {
	HasActiveReleaseJob(context.Context) (bool, error)
}

type JobEventRepository interface {
	AppendReleaseJobEvent(context.Context, Event) (Event, bool, error)
	ListReleaseJobEvents(context.Context, domain.ID, int64) ([]Event, error)
}

type IntentRepository interface {
	CreateIntent(context.Context, Intent) (Intent, bool, error)
	GetIntent(context.Context, domain.ID) (Intent, error)
	TransitionIntent(context.Context, domain.ID, IntentPhase, IntentPhase, string, string) (Intent, bool, error)
}

// IntentRecoveryRepository enumerates only durable saga records that still
// need reconciliation after a process or project-open restart.
type IntentRecoveryRepository interface {
	IntentRepository
	ListNonterminalReleaseIntents(context.Context) ([]Intent, error)
}

// ActivatedIntentCommitter owns the short atomic SQLite commit. Recovery uses
// the same operation as the live worker so a replay cannot create another
// release or separately advance the active pointer.
type ActivatedIntentCommitter interface {
	CommitActivatedIntent(context.Context, domain.ID, int64) (Release, ActivePointer, error)
}
type EventSink interface {
	Append(context.Context, domain.ID, int64, []byte) error
}

// BackupGate performs the mandatory Online Backup/integrity/checksum action.
// Its payload is canonical evidence and its idempotency key is supplied by the
// release worker.
type BackupGate interface {
	Backup(context.Context, domain.ID, string) (BackupEvidence, error)
}

// GraphActivationGate is the only external activation boundary required by
// this change. Snapshot identity and idempotency are explicit inputs.
type GraphActivationGate interface {
	Activate(context.Context, GraphActivationRequest) (GraphActivationEvidence, error)
}

// GraphRecoveryGate makes the external active snapshot observable and gives
// recovery one idempotent restore operation. The previous Graph identity is
// opaque to versioning: only the registered Graph adapter knows how to restore
// it safely.
type GraphRecoveryGate interface {
	GraphActivationGate
	ActiveSnapshot(context.Context, GraphReadRequest) (GraphSnapshot, error)
	Restore(context.Context, GraphRestoreRequest) error
}

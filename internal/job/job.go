// Package job defines the durable work protocol shared by capability modules.
// It deliberately contains no feature, storage, or transport imports.
package job

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zouyi/eco-guardian/internal/domain"
)

type Kind string

func (k Kind) Valid() bool {
	return strings.TrimSpace(string(k)) == string(k) && utf8.ValidString(string(k)) && len(k) > 0 && len(k) <= 64
}

type Status string

const (
	Queued      Status = "queued"
	Running     Status = "running"
	Succeeded   Status = "succeeded"
	Failed      Status = "failed"
	Canceled    Status = "canceled"
	Interrupted Status = "interrupted"
)

func (s Status) Valid() bool {
	switch s {
	case Queued, Running, Succeeded, Failed, Canceled, Interrupted:
		return true
	default:
		return false
	}
}

func (s Status) Terminal() bool {
	return s == Succeeded || s == Failed || s == Canceled
}

func (s Status) CanTransitionTo(next Status) bool {
	switch s {
	case Queued:
		return next == Running || next == Failed || next == Canceled || next == Interrupted
	case Running:
		return next == Succeeded || next == Failed || next == Canceled || next == Interrupted
	case Interrupted:
		return next == Succeeded || next == Failed || next == Canceled
	default:
		return false
	}
}

type Result struct {
	Type string    `json:"type"`
	ID   domain.ID `json:"id"`
	URL  string    `json:"url"`
}

func (r Result) Valid() bool {
	return strings.TrimSpace(r.Type) != "" && r.ID.Valid() && strings.TrimSpace(r.URL) != ""
}

// Request is the immutable, project-scoped idempotency identity of a Job.
type Request struct {
	ProjectID      domain.ID
	Kind           Kind
	RevisionID     domain.ID
	InputHash      string
	IdempotencyKey string
	RequestHash    string
}

func (r Request) Valid() bool {
	return r.ProjectID.Valid() && r.Kind.Valid() && (r.RevisionID == "" || r.RevisionID.Valid()) && validHash(r.InputHash) && validKey(r.IdempotencyKey) && validHash(r.RequestHash)
}

// Equivalent reports whether two requests express precisely the same durable
// work identity, including the project-scoped idempotency key and canonical
// request hash. Stores use a false result with a matching key as a conflict.
func (r Request) Equivalent(other Request) bool {
	return r == other
}

type Record struct {
	ID                domain.ID  `json:"id"`
	ProjectID         domain.ID  `json:"project_id"`
	Kind              Kind       `json:"kind"`
	RevisionID        domain.ID  `json:"revision_id,omitempty"`
	InputHash         string     `json:"input_hash"`
	IdempotencyKey    string     `json:"idempotency_key"`
	RequestHash       string     `json:"request_hash"`
	Status            Status     `json:"status"`
	Result            *Result    `json:"result,omitempty"`
	CancelGeneration  int64      `json:"cancel_generation"`
	CancelRequestedAt *time.Time `json:"cancel_requested_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

func (r Record) Request() Request {
	return Request{ProjectID: r.ProjectID, Kind: r.Kind, RevisionID: r.RevisionID, InputHash: r.InputHash, IdempotencyKey: r.IdempotencyKey, RequestHash: r.RequestHash}
}

func (r Record) Valid() bool {
	if !r.ID.Valid() || !r.Request().Valid() || !r.Status.Valid() || r.CancelGeneration < 0 || r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() || r.UpdatedAt.Before(r.CreatedAt) {
		return false
	}
	if r.Result != nil && !r.Result.Valid() {
		return false
	}
	if r.Status == Succeeded && r.Result == nil {
		return false
	}
	return (r.CancelGeneration == 0 && r.CancelRequestedAt == nil) || (r.CancelGeneration > 0 && r.CancelRequestedAt != nil && !r.CancelRequestedAt.IsZero())
}

// Event is immutable audit evidence. Its ordinal is scoped to one Job.
type Event struct {
	JobID     domain.ID `json:"job_id"`
	Ordinal   int64     `json:"ordinal"`
	Phase     string    `json:"phase"`
	Progress  int       `json:"progress"`
	Warning   string    `json:"warning,omitempty"`
	SafeError string    `json:"error,omitempty"`
	Result    *Result   `json:"result,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func (e Event) Valid() bool {
	return e.JobID.Valid() && e.Ordinal > 0 && strings.TrimSpace(e.Phase) != "" && utf8.ValidString(e.Phase) && e.Progress >= 0 && e.Progress <= 100 && len(e.Warning) <= 256 && len(e.SafeError) <= 1024 && utf8.ValidString(e.Warning) && utf8.ValidString(e.SafeError) && (e.Result == nil || e.Result.Valid()) && !e.CreatedAt.IsZero()
}

type Clock interface{ Now() time.Time }
type IDGenerator interface{ New() (domain.ID, error) }

// Store atomically owns idempotency, transitions, result links, and
// cancellation intent. observedCancelGeneration prevents a worker from
// sealing success after a newer cancellation request.
type Store interface {
	CreateOrGet(context.Context, Request) (Record, bool, error)
	GetJob(context.Context, domain.ID) (Record, error)
	Transition(context.Context, domain.ID, Status, Status, *Result, int64) (Record, bool, error)
	RequestCancellation(context.Context, domain.ID) (Record, bool, error)
}

// RecoverableStore extends the shared repository only with a bounded scan of
// the three nonterminal states eligible for startup/project-open recovery.
// Feature-specific immutable facts remain in their owning module tables.
type RecoverableStore interface {
	Store
	ListRecoverableJobs(context.Context, int) ([]Record, error)
}

type EventStore interface {
	Append(context.Context, Event) (Event, bool, error)
	ListEvents(context.Context, domain.ID, int64) ([]Event, error)
}

func RequireUncanceled(record Record, observed int64) error {
	if !record.Valid() || observed < 0 || observed != record.CancelGeneration || record.CancelGeneration != 0 {
		return fmt.Errorf("job cancellation generation changed")
	}
	return nil
}

func validHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	return strings.Trim(value, "0123456789abcdef") == ""
}

func validKey(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && utf8.ValidString(value) && utf8.RuneCountInString(value) <= 256
}

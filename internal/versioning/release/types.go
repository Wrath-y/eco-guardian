package release

import (
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zouyi/eco-guardian/internal/domain"
)

type JobStatus string

const (
	JobQueued      JobStatus = "queued"
	JobRunning     JobStatus = "running"
	JobSucceeded   JobStatus = "succeeded"
	JobFailed      JobStatus = "failed"
	JobCanceled    JobStatus = "canceled"
	JobInterrupted JobStatus = "interrupted"
)

func (s JobStatus) Valid() bool {
	return s == JobQueued || s == JobRunning || s == JobSucceeded || s == JobFailed || s == JobCanceled || s == JobInterrupted
}

type Confirmation struct {
	Kind      string `json:"kind"`
	Confirmed bool   `json:"confirmed"`
}
type OverrideAudit struct {
	GateResultID domain.ID `json:"gate_result_id"`
	Reason       string    `json:"reason"`
	Confirmed    bool      `json:"confirmed"`
}

// Command is the canonical release request. Candidate content is referenced by
// identity only and first-baseline is represented by an empty BaselineReleaseID.
type Command struct {
	CandidateRevisionID domain.ID `json:"candidate_revision_id"`
	ConfigHash          string    `json:"config_hash"`
	ManifestHash        string    `json:"version_manifest_hash"`
	PolicyID            domain.ID `json:"policy_id"`
	BaselineReleaseID   domain.ID `json:"expected_baseline_release_id,omitempty"`
	// BaselineSpecified preserves the difference between an omitted baseline
	// and the explicit JSON null required for a first release. It is populated
	// by the HTTP adapter and is intentionally not an API payload field.
	BaselineSpecified bool           `json:"-"`
	Notes             string         `json:"notes,omitempty"`
	Confirmations     []Confirmation `json:"confirmations"`
	Override          *OverrideAudit `json:"override,omitempty"`
	IdempotencyKey    string         `json:"-"`
}

func (c Command) Valid() bool {
	if !c.CandidateRevisionID.Valid() || !c.PolicyID.Valid() || !validHash(c.ConfigHash) || !validHash(c.ManifestHash) || !c.BaselineSpecified || (c.BaselineReleaseID != "" && !c.BaselineReleaseID.Valid()) {
		return false
	}
	if !validIdempotencyKey(c.IdempotencyKey) || !utf8.ValidString(c.Notes) || utf8.RuneCountInString(c.Notes) > 10000 {
		return false
	}
	seen := make(map[string]struct{}, len(c.Confirmations))
	for _, confirmation := range c.Confirmations {
		if !confirmation.Valid() {
			return false
		}
		if _, duplicate := seen[confirmation.Kind]; duplicate {
			return false
		}
		seen[confirmation.Kind] = struct{}{}
	}
	if c.Override != nil && (!c.Override.GateResultID.Valid() || !c.Override.Confirmed || !utf8.ValidString(c.Override.Reason) || strings.TrimSpace(c.Override.Reason) == "" || utf8.RuneCountInString(c.Override.Reason) > 10000) {
		return false
	}
	return true
}

func validIdempotencyKey(key string) bool {
	return key != "" && key == strings.TrimSpace(key) && utf8.ValidString(key) && utf8.RuneCountInString(key) <= 256
}

func (c Confirmation) Valid() bool {
	return c.Confirmed && (c.Kind == ConfirmationEstablishBaseline || c.Kind == ConfirmationAcknowledgeWarning || c.Kind == ConfirmationNumericOverride)
}

type Job struct {
	ID                domain.ID  `json:"id"`
	ProjectID         domain.ID  `json:"project_id"`
	RevisionID        domain.ID  `json:"revision_id"`
	InputHash         string     `json:"input_hash"`
	IdempotencyKey    string     `json:"idempotency_key"`
	Status            JobStatus  `json:"status"`
	RequestHash       string     `json:"request_hash"`
	Result            *JobResult `json:"result,omitempty"`
	CancelGeneration  int64      `json:"cancel_generation"`
	CancelRequestedAt *time.Time `json:"cancel_requested_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

func (j Job) Valid() bool {
	if !j.ID.Valid() || !j.ProjectID.Valid() || !j.RevisionID.Valid() || !validHash(j.InputHash) || !validIdempotencyKey(j.IdempotencyKey) || !j.Status.Valid() || !validHash(j.RequestHash) || j.CancelGeneration < 0 || j.CreatedAt.IsZero() || j.UpdatedAt.IsZero() || j.UpdatedAt.Before(j.CreatedAt) || (j.CancelGeneration == 0 && j.CancelRequestedAt != nil) || (j.CancelGeneration > 0 && (j.CancelRequestedAt == nil || j.CancelRequestedAt.IsZero())) {
		return false
	}
	if j.Result != nil && !j.Result.Valid() {
		return false
	}
	return j.Status != JobSucceeded || j.Result != nil
}

type JobResult struct {
	Type string    `json:"type"`
	ID   domain.ID `json:"id"`
	URL  string    `json:"url"`
}

// BackupEvidence is the mandatory, auditable proof that an Online Backup,
// integrity check, and checksum completed for this release request.
type BackupEvidence struct {
	Online           bool   `json:"online"`
	IntegrityChecked bool   `json:"integrity_checked"`
	Checksum         string `json:"checksum"`
}

type GraphActivationRequest struct {
	ProjectID  domain.ID
	RevisionID domain.ID
	ConfigHash string
	IntentID   domain.ID
}

func (r GraphActivationRequest) Valid() bool {
	return r.ProjectID.Valid() && r.RevisionID.Valid() && validHash(r.ConfigHash) && r.IntentID.Valid()
}

type GraphActivationEvidence struct {
	Changed    bool
	ProjectID  domain.ID
	RevisionID domain.ID
	ConfigHash string
	IntentID   domain.ID
	TaskID     string
}

func (e GraphActivationEvidence) Matches(request GraphActivationRequest) bool {
	return e.ProjectID == request.ProjectID && e.RevisionID == request.RevisionID && e.ConfigHash == request.ConfigHash && e.IntentID == request.IntentID
}

// GraphSnapshot is the externally active revision projection. It is compared
// with the immutable intent candidate before recovery may advance SQLite.
type GraphSnapshot struct {
	ProjectID  domain.ID
	RevisionID domain.ID
	ConfigHash string
}

func (s GraphSnapshot) Valid() bool {
	return s.ProjectID.Valid() && s.RevisionID.Valid() && validHash(s.ConfigHash)
}

func (s GraphSnapshot) Matches(request GraphActivationRequest) bool {
	return s.ProjectID == request.ProjectID && s.RevisionID == request.RevisionID && s.ConfigHash == request.ConfigHash
}

// GraphReadRequest makes every Graph read used by release/version checks
// explicitly scoped to one project namespace and immutable revision snapshot.
// It is intentionally separate from activation's idempotency key.
type GraphReadRequest struct {
	ProjectID  domain.ID
	RevisionID domain.ID
	ConfigHash string
}

func (r GraphReadRequest) Valid() bool {
	return r.ProjectID.Valid() && r.RevisionID.Valid() && validHash(r.ConfigHash)
}

func (s GraphSnapshot) MatchesRead(request GraphReadRequest) bool {
	return s.ProjectID == request.ProjectID && s.RevisionID == request.RevisionID && s.ConfigHash == request.ConfigHash
}

// GraphRestoreRequest binds an opaque previous Graph identity and previous
// release reference to the failed intent. It deliberately contains no copied
// configuration manifest or entity data.
type GraphRestoreRequest struct {
	ProjectID             domain.ID
	PreviousGraphIdentity string
	PreviousReleaseID     domain.ID
	IntentID              domain.ID
}

func (r GraphRestoreRequest) Valid() bool {
	return r.ProjectID.Valid() && strings.TrimSpace(r.PreviousGraphIdentity) != "" && r.IntentID.Valid()
}

func (e BackupEvidence) Valid() bool {
	return e.Online && e.IntegrityChecked && validHash(e.Checksum)
}

func (r JobResult) Valid() bool {
	return strings.TrimSpace(r.Type) != "" && r.ID.Valid() && strings.TrimSpace(r.URL) != ""
}

func (s JobStatus) CanTransitionTo(next JobStatus) bool {
	switch s {
	case JobQueued:
		return next == JobRunning || next == JobFailed || next == JobCanceled || next == JobInterrupted
	case JobRunning:
		return next == JobSucceeded || next == JobFailed || next == JobCanceled || next == JobInterrupted
	case JobInterrupted:
		return next == JobSucceeded || next == JobFailed
	default:
		return false
	}
}

// JobRequest is the immutable idempotency identity for a release Job. The
// request hash is the canonical command hash; the idempotency key is scoped
// separately by project so a changed canonical input is detectable.
type JobRequest struct {
	ProjectID      domain.ID
	RevisionID     domain.ID
	InputHash      string
	IdempotencyKey string
	RequestHash    string
}

func (r JobRequest) Valid() bool {
	return r.ProjectID.Valid() && r.RevisionID.Valid() && validHash(r.InputHash) && validIdempotencyKey(r.IdempotencyKey) && validHash(r.RequestHash)
}

type Event struct {
	JobID     domain.ID  `json:"job_id"`
	Ordinal   int64      `json:"ordinal"`
	Phase     string     `json:"phase"`
	Progress  int        `json:"progress"`
	Warning   string     `json:"warning,omitempty"`
	Error     string     `json:"error,omitempty"`
	Result    *JobResult `json:"result,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

func (e Event) Valid() bool {
	return e.JobID.Valid() && e.Ordinal > 0 && strings.TrimSpace(e.Phase) != "" && e.Progress >= 0 && e.Progress <= 100 && (e.Result == nil || e.Result.Valid()) && !e.CreatedAt.IsZero()
}

type IntentPhase string

const (
	IntentQueued            IntentPhase = "QUEUED"
	IntentRechecked         IntentPhase = "RECHECKED"
	IntentBackupSucceeded   IntentPhase = "BACKUP_SUCCEEDED"
	IntentRecorded          IntentPhase = "INTENT_RECORDED"
	IntentGraphActivating   IntentPhase = "GRAPH_ACTIVATING"
	IntentGraphActivated    IntentPhase = "GRAPH_ACTIVATED"
	IntentPointerCommitting IntentPhase = "POINTER_COMMITTING"
	IntentSucceeded         IntentPhase = "SUCCEEDED"
	IntentFailed            IntentPhase = "FAILED"
)

func (p IntentPhase) Valid() bool {
	return p == IntentQueued || p == IntentRechecked || p == IntentBackupSucceeded || p == IntentRecorded || p == IntentGraphActivating || p == IntentGraphActivated || p == IntentPointerCommitting || p == IntentSucceeded || p == IntentFailed
}

type Intent struct {
	ID                    domain.ID
	JobID                 domain.ID
	CandidateRevisionID   domain.ID
	BaselineReleaseID     domain.ID
	PolicyID              domain.ID
	GateManifest          []byte
	GateManifestHash      string
	Confirmations         []Confirmation
	Backup                BackupEvidence
	PreviousGraphIdentity string
	PreviousReleaseID     domain.ID
	RequestHash           string
	IdempotencyKey        string
	Notes                 string
	Override              *OverrideAudit
	ExternalTaskID        string
	Phase                 IntentPhase
	ErrorDetails          string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

func (i Intent) Valid() bool {
	return i.ID.Valid() && i.JobID.Valid() && i.CandidateRevisionID.Valid() && i.PolicyID.Valid() && len(i.GateManifest) > 0 && validHash(i.GateManifestHash) && i.Backup.Valid() && validHash(i.RequestHash) && validIdempotencyKey(i.IdempotencyKey) && utf8.ValidString(i.Notes) && utf8.RuneCountInString(i.Notes) <= 10000 && (i.Override == nil || (i.Override.GateResultID.Valid() && i.Override.Confirmed && strings.TrimSpace(i.Override.Reason) != "")) && i.Phase.Valid() && !i.CreatedAt.IsZero() && !i.UpdatedAt.IsZero() && !i.UpdatedAt.Before(i.CreatedAt)
}

type Release struct {
	ID         domain.ID `json:"id"`
	RevisionID domain.ID `json:"revision_id"`
	PolicyID   domain.ID `json:"policy_id"`
	IntentID   domain.ID `json:"intent_id"`
	CreatedAt  time.Time `json:"created_at"`
}

func (r Release) Valid() bool {
	return r.ID.Valid() && r.RevisionID.Valid() && r.PolicyID.Valid() && r.IntentID.Valid() && !r.CreatedAt.IsZero()
}

// ReadRecord is the immutable, audit-complete release view used by history
// and detail readers.  It deliberately references the candidate revision and
// gate evidence rather than copying configuration entities or manifests.
type ReadRecord struct {
	Release
	BaselineReleaseID domain.ID       `json:"baseline_release_id,omitempty"`
	Notes             string          `json:"notes,omitempty"`
	GateEvidence      json.RawMessage `json:"gate_evidence"`
	Confirmations     []Confirmation  `json:"confirmations"`
	Override          *OverrideAudit  `json:"override,omitempty"`
}

// ReadPage keeps the repository cursor opaque while providing stable,
// newest-first immutable release history.
type ReadPage struct {
	Items      []ReadRecord `json:"items"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

type ActivePointer struct {
	ReleaseID  domain.ID `json:"release_id,omitempty"`
	Generation int64     `json:"generation"`
}

func (p ActivePointer) Valid() bool {
	return p.Generation >= 0 && (p.ReleaseID == "" || p.ReleaseID.Valid())
}

func validHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

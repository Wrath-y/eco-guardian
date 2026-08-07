package diff

import (
	"encoding/json"

	"github.com/zouyi/eco-guardian/internal/domain"
)

type ChangeKind string

const (
	Add    ChangeKind = "ADD"
	Delete ChangeKind = "DELETE"
	Move   ChangeKind = "MOVE"
	Modify ChangeKind = "MODIFY"
)

func (k ChangeKind) Valid() bool { return k == Add || k == Delete || k == Move || k == Modify }

// BaselineState distinguishes a usable active-release baseline from a first
// release. NO_BASELINE is deliberately not represented as an empty diff.
type BaselineState string

const (
	BaselineAvailable BaselineState = "AVAILABLE"
	NoBaseline        BaselineState = "NO_BASELINE"
)

func (s BaselineState) Valid() bool { return s == BaselineAvailable || s == NoBaseline }

// Comparison is a candidate comparison against the active release. When
// BaselineState is NO_BASELINE, BaseRevisionID and Changes are absent: callers
// must guide the first-release flow instead of interpreting the state as no
// changes or a low-risk comparison.
type Comparison struct {
	BaselineState    BaselineState
	BaseRevisionID   domain.ID
	TargetRevisionID domain.ID
	Changes          []FieldChange
}

const (
	DefaultEntitySegmentSize = 200
	MaxEntitySegmentSize     = 200
)

// Segment is one bounded, deterministic portion of a revision diff. The
// cursor is the last processed stable entity ID; an empty cursor is terminal.
type Segment struct {
	Changes          []FieldChange
	NextEntityCursor domain.ID
}

// FieldChange is a stable, read-only diff record. Values remain raw canonical
// JSON so unknown extension namespaces are preserved without interpretation.
type FieldChange struct {
	EntityID   domain.ID         `json:"entity_id"`
	EntityKind domain.EntityKind `json:"entity_kind"`
	Path       string            `json:"path"`
	Kind       ChangeKind        `json:"kind"`
	OldValue   json.RawMessage   `json:"old_value,omitempty"`
	NewValue   json.RawMessage   `json:"new_value,omitempty"`
	OldOrdinal *int              `json:"old_ordinal,omitempty"`
	NewOrdinal *int              `json:"new_ordinal,omitempty"`
}

func (c FieldChange) Valid() bool {
	return c.EntityID.Valid() && c.EntityKind.Valid() && c.Kind.Valid() && c.Path != "" &&
		(c.OldOrdinal == nil || *c.OldOrdinal >= 0) && (c.NewOrdinal == nil || *c.NewOrdinal >= 0)
}

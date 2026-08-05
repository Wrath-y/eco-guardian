package validation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
)

type Scope string

const (
	ScopeBase  Scope = "BASE"
	ScopeLocal Scope = "LOCAL"
	ScopeFull  Scope = "FULL"
)

func (s Scope) Valid() bool { return s == ScopeBase || s == ScopeLocal || s == ScopeFull }

type SourceKind string

const (
	SourceWorking  SourceKind = "working"
	SourceRevision SourceKind = "revision"
)

type Source struct {
	Kind       SourceKind `json:"type"`
	RevisionID domain.ID  `json:"revision_id,omitempty"`
	InputHash  string     `json:"input_hash"`
}

func NewSource(kind SourceKind, revisionID domain.ID, inputHash string) (Source, error) {
	if kind != SourceWorking && kind != SourceRevision {
		return Source{}, fmt.Errorf("invalid source type %q", kind)
	}
	if kind == SourceRevision && !revisionID.Valid() {
		return Source{}, fmt.Errorf("revision source requires UUIDv7 revision id")
	}
	if kind == SourceWorking && revisionID != "" {
		return Source{}, fmt.Errorf("working source cannot carry revision id")
	}
	if len(inputHash) != 64 {
		return Source{}, fmt.Errorf("source requires SHA-256 input hash")
	}
	return Source{kind, revisionID, inputHash}, nil
}

type VersionManifest struct {
	Schema        string `json:"schema"`
	DSL           string `json:"dsl"`
	Registry      string `json:"registry"`
	NumericPolicy string `json:"numeric_policy"`
}

func (m VersionManifest) Valid() bool {
	return m.Schema != "" && m.DSL != "" && m.Registry != "" && m.NumericPolicy != ""
}
func (m VersionManifest) Hash() (string, error) {
	if !m.Valid() {
		return "", fmt.Errorf("incomplete version manifest")
	}
	sum := sha256.Sum256([]byte(m.Schema + "\n" + m.DSL + "\n" + m.Registry + "\n" + m.NumericPolicy))
	return hex.EncodeToString(sum[:]), nil
}

type Severity string

const (
	SeverityError   Severity = "ERROR"
	SeverityBlock   Severity = "BLOCK"
	SeverityWarning Severity = "WARNING"
	SeverityInfo    Severity = "INFO"
)

func (s Severity) Valid() bool {
	return s == SeverityError || s == SeverityBlock || s == SeverityWarning || s == SeverityInfo
}

type FormulaSpan struct {
	StartByte int `json:"start_byte"`
	EndByte   int `json:"end_byte"`
}

func NewFormulaSpan(start, end int) (FormulaSpan, error) {
	if start < 0 || end <= start {
		return FormulaSpan{}, fmt.Errorf("invalid formula span [%d,%d)", start, end)
	}
	return FormulaSpan{start, end}, nil
}

type Issue struct {
	Severity      Severity          `json:"severity"`
	Code          string            `json:"code"`
	EntityID      domain.ID         `json:"entity_id"`
	FieldPath     string            `json:"field_path"`
	Span          *FormulaSpan      `json:"formula_span,omitempty"`
	Ordinal       *int              `json:"ordinal,omitempty"`
	MessageKey    string            `json:"message_key"`
	MessageParams map[string]string `json:"message_params,omitempty"`
	FixHintKey    string            `json:"fix_hint_key,omitempty"`
	Evidence      map[string]string `json:"evidence,omitempty"`
	Fingerprint   string            `json:"fingerprint"`
}

func (i Issue) Valid() bool {
	return i.Severity.Valid() && i.Code != "" && i.EntityID.Valid() && i.FieldPath != "" && i.MessageKey != "" && (i.Ordinal == nil || *i.Ordinal >= 0) && (i.Span == nil || (i.Span.StartByte >= 0 && i.Span.EndByte > i.Span.StartByte))
}

type SeveritySummary struct {
	Error   int `json:"error"`
	Block   int `json:"block"`
	Warning int `json:"warning"`
	Info    int `json:"info"`
}

func Summarize(issues []Issue) SeveritySummary {
	var out SeveritySummary
	for _, i := range issues {
		switch i.Severity {
		case SeverityError:
			out.Error++
		case SeverityBlock:
			out.Block++
		case SeverityWarning:
			out.Warning++
		case SeverityInfo:
			out.Info++
		}
	}
	return out
}

type RunStatus string

const RunCompleted RunStatus = "completed"

type ValidationRun struct {
	ID         domain.ID       `json:"id"`
	Source     Source          `json:"source"`
	Scope      Scope           `json:"scope"`
	Versions   VersionManifest `json:"versions"`
	Status     RunStatus       `json:"status"`
	Summary    SeveritySummary `json:"summary"`
	ResultHash string          `json:"result_hash"`
	CreatedAt  time.Time       `json:"created_at"`
}

func NewCompletedRun(id domain.ID, source Source, scope Scope, versions VersionManifest, issues []Issue, createdAt time.Time) (ValidationRun, error) {
	if !id.Valid() || !scope.Valid() || !versions.Valid() {
		return ValidationRun{}, fmt.Errorf("invalid completed run identity")
	}
	for _, issue := range issues {
		if !issue.Valid() {
			return ValidationRun{}, fmt.Errorf("invalid validation issue")
		}
	}
	h, err := ResultHash(source, scope, versions, issues)
	if err != nil {
		return ValidationRun{}, err
	}
	return ValidationRun{id, source, scope, versions, RunCompleted, Summarize(issues), h, createdAt.UTC()}, nil
}
func ResultHash(source Source, scope Scope, versions VersionManifest, issues []Issue) (string, error) {
	if !scope.Valid() || !versions.Valid() {
		return "", fmt.Errorf("invalid result identity")
	}
	cloned := SortIssues(issues)
	manifestHash, err := versions.Hash()
	if err != nil {
		return "", err
	}
	summary := Summarize(cloned)
	payload := fmt.Sprintf("%s|%s|%s|%s|%s|%d|%d|%d|%d", source.Kind, source.RevisionID, source.InputHash, scope, manifestHash, summary.Error, summary.Block, summary.Warning, summary.Info)
	for _, issue := range cloned {
		payload += "|" + issue.Severity.String() + ":" + issue.Code + ":" + string(issue.EntityID) + ":" + issue.FieldPath + ":" + issue.Fingerprint
	}
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:]), nil
}
func (s Severity) String() string { return string(s) }

type GateResult string

const (
	GatePass               GateResult = "PASS"
	GateRequiresValidation GateResult = "REQUIRES_VALIDATION"
	GateBlocked            GateResult = "BLOCKED"
)

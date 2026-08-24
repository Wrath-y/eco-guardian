// Package materialization exposes immutable, transport-neutral rule graphs
// derived from one fully validated configuration revision.
package materialization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/validation"
)

const ContractVersionV1 = "typed-rule-materialization-v1"

var (
	ErrUnavailable = errors.New("typed rule materialization is unavailable")
	ErrInvalid     = errors.New("typed rule materialization is invalid")
)

type DiagnosticCode string

const (
	DiagnosticValidationRequired DiagnosticCode = "RULE_MATERIALIZATION_VALIDATION_REQUIRED"
	DiagnosticVersionMismatch    DiagnosticCode = "RULE_MATERIALIZATION_VERSION_MISMATCH"
	DiagnosticArtifactMissing    DiagnosticCode = "RULE_MATERIALIZATION_ARTIFACT_MISSING"
	DiagnosticArtifactInvalid    DiagnosticCode = "RULE_MATERIALIZATION_ARTIFACT_INVALID"
	DiagnosticPayloadInvalid     DiagnosticCode = "RULE_MATERIALIZATION_PAYLOAD_INVALID"
	DiagnosticReferenceInvalid   DiagnosticCode = "RULE_MATERIALIZATION_REFERENCE_INVALID"
)

type Diagnostic struct {
	Code         DiagnosticCode
	SourceEntity domain.ID
	FieldPath    string
}

func (d Diagnostic) Error() string {
	if d.SourceEntity.Valid() && d.FieldPath != "" {
		return fmt.Sprintf("%s at %s%s", d.Code, d.SourceEntity, d.FieldPath)
	}
	return string(d.Code)
}

type Certification struct {
	RunID      domain.ID
	ResultHash string
	Versions   validation.VersionManifest
}

func (c Certification) Valid() bool {
	return c.RunID.Valid() && validHash(c.ResultHash) && c.Versions.Valid()
}

// Source is an immutable source snapshot supplied by an application/storage
// adapter. It intentionally has no working-state or transport fields.
type Source struct {
	ProjectID     domain.ID
	RevisionID    domain.ID
	ConfigHash    string
	Certification Certification
	Entities      []domain.Entity
	Formulas      []validation.FormulaIndexRecord
}

func (s Source) Valid() bool {
	return s.ProjectID.Valid() && s.RevisionID.Valid() && validHash(s.ConfigHash) && s.Certification.Valid()
}

type Provenance struct {
	RuleID       string            `json:"rule_id"`
	SourceEntity domain.ID         `json:"source_entity_id"`
	SourceKind   domain.EntityKind `json:"source_kind"`
	FieldPath    string            `json:"field_path"`
	Ordinal      int               `json:"ordinal"`
	Kind         string            `json:"kind"`
	BodyHash     string            `json:"body_hash"`
}

func (p Provenance) Valid() bool {
	return p.RuleID != "" && p.SourceEntity.Valid() && p.SourceKind.Valid() && validPointer(p.FieldPath) && p.Ordinal >= 0 && p.Kind != "" && validHash(p.BodyHash)
}

type FormulaRead struct {
	Scope  string `json:"scope"`
	Symbol string `json:"symbol"`
	Start  int    `json:"start"`
	End    int    `json:"end"`
}

type FormulaBinding struct {
	Provenance
	OutputAttributeID domain.ID     `json:"output_attribute_id"`
	AST               []byte        `json:"ast"`
	ASTHash           string        `json:"ast_hash"`
	ASTVersion        string        `json:"ast_version"`
	DSLVersion        string        `json:"dsl_version"`
	RegistryVersion   string        `json:"registry_version"`
	Reads             []FormulaRead `json:"reads"`
}

type TargetSelector struct {
	Type  string    `json:"type"`
	TagID domain.ID `json:"tag_id,omitempty"`
}

type TriggerRule struct {
	Provenance
	Event             string         `json:"event"`
	Condition         string         `json:"condition,omitempty"`
	Target            TargetSelector `json:"target"`
	EffectIDs         []domain.ID    `json:"effect_ids"`
	TerminationBudget string         `json:"termination_budget,omitempty"`
}

type Modifier struct {
	Provenance
	AttributeID domain.ID `json:"attribute_id"`
	Operation   string    `json:"operation"`
	Value       string    `json:"value"`
}

type StackRule struct {
	Provenance
	Operation     string `json:"operation"`
	Priority      string `json:"priority,omitempty"`
	MaxStacks     string `json:"max_stacks"`
	RefreshPolicy string `json:"refresh_policy"`
	Cap           string `json:"cap,omitempty"`
}

type RuleSetV1 struct {
	ContractVersion     string           `json:"contract_version"`
	ProjectID           domain.ID        `json:"project_id"`
	RevisionID          domain.ID        `json:"revision_id"`
	ConfigHash          string           `json:"config_hash"`
	Certification       Certification    `json:"certification"`
	FormulaBindings     []FormulaBinding `json:"formula_bindings"`
	TriggerRules        []TriggerRule    `json:"trigger_rules"`
	Modifiers           []Modifier       `json:"modifiers"`
	StackRules          []StackRule      `json:"stack_rules"`
	Canonical           []byte           `json:"-"`
	MaterializationHash string           `json:"materialization_hash"`
}

func (s RuleSetV1) Valid() bool {
	return s.ContractVersion == ContractVersionV1 && s.ProjectID.Valid() && s.RevisionID.Valid() && validHash(s.ConfigHash) && s.Certification.Valid() && len(s.Canonical) > 0 && validHash(s.MaterializationHash)
}

// Reader is the only application-facing port. Implementations must resolve
// an explicit immutable revision and enforce exact FULL validation before
// returning its source data.
type Reader interface {
	ReadRuleSource(context.Context, domain.ID) (Source, error)
}

func StableRuleID(revisionID, sourceID domain.ID, fieldPath string, ordinal int, kind string) (string, error) {
	if !revisionID.Valid() || !sourceID.Valid() || !validPointer(fieldPath) || ordinal < 0 || strings.TrimSpace(kind) == "" {
		return "", ErrInvalid
	}
	body := strings.Join([]string{ContractVersionV1, string(revisionID), string(sourceID), fieldPath, fmt.Sprintf("%d", ordinal), kind}, "\x00")
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:]), nil
}

func validHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validPointer(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.Contains(value, "//")
}

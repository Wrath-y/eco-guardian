package threshold

import (
	"strings"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
)

const SchemaVersionV1 = "v1"

type Boundaries struct {
	Warning string `json:"warning"`
	Block   string `json:"block"`
}

func (b Boundaries) Valid() bool {
	warning, valid := parseThresholdDecimal(b.Warning)
	if !valid {
		return false
	}
	block, valid := parseThresholdDecimal(b.Block)
	zero, _ := formula.ParseDecimal("0")
	return valid && warning.Compare(zero) >= 0 && warning.Compare(block) <= 0
}

type Entry struct {
	SceneID       string                       `json:"scene_id"`
	SceneVersion  string                       `json:"scene_version"`
	MetricID      string                       `json:"metric_id"`
	MetricVersion string                       `json:"metric_version"`
	BalanceGroup  *string                      `json:"balance_group"`
	Unit          string                       `json:"unit"`
	Direction     riskcontract.MetricDirection `json:"direction"`
	Relative      Boundaries                   `json:"relative"`
	Absolute      *Boundaries                  `json:"absolute"`
}

func (e Entry) ScopeKey() string {
	group := "\x00"
	if e.BalanceGroup != nil {
		group = "\x01" + *e.BalanceGroup
	}
	return e.SceneID + "\x00" + e.SceneVersion + "\x00" + e.MetricID + "\x00" + e.MetricVersion + "\x00" + group
}

func (e Entry) Valid() bool {
	if strings.TrimSpace(e.SceneID) == "" || strings.TrimSpace(e.SceneVersion) == "" || strings.TrimSpace(e.MetricID) == "" || strings.TrimSpace(e.MetricVersion) == "" || strings.TrimSpace(e.Unit) == "" || !e.Relative.Valid() {
		return false
	}
	if e.BalanceGroup != nil && strings.TrimSpace(*e.BalanceGroup) == "" {
		return false
	}
	if e.Direction != riskcontract.HigherIsRisk && e.Direction != riskcontract.LowerIsRisk && e.Direction != riskcontract.TargetRange {
		return false
	}
	return e.Absolute == nil || e.Absolute.Valid()
}

type Body struct {
	SchemaVersion          string                  `json:"schema_version"`
	Source                 string                  `json:"source"`
	Assumptions            []string                `json:"assumptions"`
	Entries                []Entry                 `json:"entries"`
	StructuralRuleVersions []riskcontract.Identity `json:"structural_rule_versions"`
}

func (b Body) Valid() bool {
	_, err := b.Normalize()
	return err == nil
}

type Origin string

const (
	OriginStarter         Origin = "starter"
	OriginModifiedStarter Origin = "modified_starter"
)

type Version struct {
	ProjectID      domain.ID `json:"project_id"`
	ID             domain.ID `json:"id"`
	DisplayVersion int64     `json:"display_version"`
	Origin         Origin    `json:"origin"`
	Enabled        bool      `json:"enabled"`
	Body           Body      `json:"body"`
	BodyHash       string    `json:"body_hash"`
	CreatedBy      string    `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
}

func (v Version) Valid() bool {
	if !v.ProjectID.Valid() || !v.ID.Valid() || v.DisplayVersion < 1 || (v.Origin != OriginStarter && v.Origin != OriginModifiedStarter) || !v.Body.Valid() || !validHash(v.BodyHash) || strings.TrimSpace(v.CreatedBy) == "" || v.CreatedAt.IsZero() {
		return false
	}
	hash, err := v.Body.Hash()
	return err == nil && hash == v.BodyHash
}

func NewVersion(projectID, id domain.ID, displayVersion int64, origin Origin, enabled bool, body Body, createdBy string, createdAt time.Time) (Version, error) {
	normalized, err := body.Normalize()
	if err != nil {
		return Version{}, err
	}
	hash, err := normalized.Hash()
	if err != nil {
		return Version{}, err
	}
	version := Version{ProjectID: projectID, ID: id, DisplayVersion: displayVersion, Origin: origin, Enabled: enabled, Body: normalized, BodyHash: hash, CreatedBy: createdBy, CreatedAt: createdAt.UTC()}
	if !version.Valid() {
		return Version{}, ErrThresholdInvalid
	}
	return version, nil
}

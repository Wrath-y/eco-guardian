package report

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/zouyi/eco-guardian/internal/graph/impact"
)

var ErrInvalidReport = errors.New("invalid impact report")

func ResultHash(value impact.Report) (string, error) {
	payload := struct {
		Input          impact.Input               `json:"input"`
		InputHash      string                     `json:"input_hash"`
		Mode           impact.ResultMode          `json:"mode"`
		Changed        []impact.ChangedEntity     `json:"changed_entities"`
		Affected       []impact.AffectedEntity    `json:"deterministic_affected"`
		Suspected      []impact.SuspectedEvidence `json:"suspected_associations"`
		SuspectedState impact.SuspectedState      `json:"suspected_state"`
		Truncated      bool                       `json:"truncated"`
		Reasons        []impact.TruncationReason  `json:"truncation_reasons"`
		Warnings       []string                   `json:"warnings"`
	}{value.Input, value.InputHash, value.Mode, value.Changed, value.Affected, value.Suspected, value.SuspectedState, value.Truncated, value.Reasons, value.Warnings}
	if !impact.ValidHash(value.InputHash) || value.Input.AnalysisContractVersion != impact.AnalysisContractVersion {
		return "", ErrInvalidReport
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func ExpansionHash(reportID, targetNodeID string, maxPaths int, input impact.Input) (string, error) {
	if reportID == "" || targetNodeID == "" || maxPaths < 1 || maxPaths > 100 {
		return "", ErrInvalidReport
	}
	encoded, err := json.Marshal(struct {
		Version      string         `json:"version"`
		ReportID     string         `json:"report_id"`
		TargetNodeID string         `json:"target_node_id"`
		MaxPaths     int            `json:"max_paths"`
		Filters      impact.Filters `json:"filters"`
		Limits       impact.Limits  `json:"limits"`
	}{impact.AnalysisContractVersion, reportID, targetNodeID, maxPaths, input.Filters, input.Limits})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

type Freshness struct {
	Fresh   bool     `json:"fresh"`
	Reasons []string `json:"reasons"`
}

func EvaluateFreshness(saved impact.Input, current *impact.Input) Freshness {
	if current == nil {
		return Freshness{Fresh: false, Reasons: []string{"no_current_selection"}}
	}
	savedBytes, _ := json.Marshal(saved)
	currentBytes, _ := json.Marshal(current)
	if string(savedBytes) == string(currentBytes) {
		return Freshness{Fresh: true}
	}
	reasons := []string{}
	if saved.Base.RevisionID != current.Base.RevisionID {
		reasons = append(reasons, "base_revision_changed")
	}
	if saved.Target.RevisionID != current.Target.RevisionID {
		reasons = append(reasons, "target_revision_changed")
	}
	if saved.Base.GraphManifestHash != current.Base.GraphManifestHash || saved.Target.GraphManifestHash != current.Target.GraphManifestHash {
		reasons = append(reasons, "graph_identity_changed")
	}
	if saved.AnalysisContractVersion != current.AnalysisContractVersion {
		reasons = append(reasons, "analysis_version_changed")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "analysis_options_changed")
	}
	return Freshness{Fresh: false, Reasons: reasons}
}

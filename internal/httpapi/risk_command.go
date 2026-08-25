package httpapi

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/zouyi/eco-guardian/internal/formula"
	"github.com/zouyi/eco-guardian/internal/httpapi/riskdto"
)

const maxRiskCommandBytes = 1 << 20

var thresholdDecimalPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)

// DecodeRiskReviewCommand is the single strict JSON boundary for risk POSTs.
// It enforces the OpenAPI unknown-field policy before application admission and
// never accepts client-derived severity, Gate, PASS or override outcomes.
func DecodeRiskReviewCommand(reader io.Reader) (riskdto.RiskReviewCommand, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maxRiskCommandBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maxRiskCommandBytes {
		return riskdto.RiskReviewCommand{}, fmt.Errorf("invalid risk command payload")
	}
	var envelope struct {
		Command string `json:"command"`
	}
	if err = strictJSON(raw, &envelope); err != nil {
		// The envelope intentionally has only the discriminator, so decode it
		// without unknown-field rejection before the concrete strict pass.
		if unmarshalErr := json.Unmarshal(raw, &envelope); unmarshalErr != nil {
			return riskdto.RiskReviewCommand{}, fmt.Errorf("invalid risk command payload")
		}
	}
	switch envelope.Command {
	case "evaluate":
		var value riskdto.EvaluateRiskReviewCommand
		if err = strictJSON(raw, &value); err != nil || !validEvaluateCommand(value) {
			return riskdto.RiskReviewCommand{}, fmt.Errorf("invalid evaluate risk command")
		}
		if err = validateBaselineUnion(raw, value.Baseline); err != nil {
			return riskdto.RiskReviewCommand{}, err
		}
		if err = validateThresholdUnion(raw, value.Threshold); err != nil {
			return riskdto.RiskReviewCommand{}, err
		}
	case "record_numeric_decision":
		var value riskdto.RecordNumericDecisionCommand
		if err = strictJSON(raw, &value); err != nil || !validDecisionCommand(value) {
			return riskdto.RiskReviewCommand{}, fmt.Errorf("invalid numeric decision command")
		}
	default:
		return riskdto.RiskReviewCommand{}, fmt.Errorf("unknown risk command")
	}
	var command riskdto.RiskReviewCommand
	if err = json.Unmarshal(raw, &command); err != nil {
		return riskdto.RiskReviewCommand{}, fmt.Errorf("invalid risk command union")
	}
	if _, err = command.ValueByDiscriminator(); err != nil {
		return riskdto.RiskReviewCommand{}, fmt.Errorf("invalid risk command discriminator")
	}
	return command, nil
}

func strictJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("multiple JSON values")
	}
	return nil
}

func validEvaluateCommand(command riskdto.EvaluateRiskReviewCommand) bool {
	if string(command.Command) != "evaluate" || !validRevisionIdentity(command.Candidate) || !validRiskIdentity(command.Policy) || len(command.PolicyRequirements) == 0 || len(command.PolicyRequirements) > 500 || len(command.SimulationRuns) == 0 || len(command.SimulationRuns) > 500 {
		return false
	}
	seenRequirements := make(map[string]struct{}, len(command.PolicyRequirements))
	for _, requirement := range command.PolicyRequirements {
		key := requirement.SceneId + "\x00" + requirement.SceneVersion + "\x00" + requirement.MetricId + "\x00" + requirement.MetricVersion
		if strings.TrimSpace(requirement.SceneId) == "" || strings.TrimSpace(requirement.SceneVersion) == "" || strings.TrimSpace(requirement.MetricId) == "" || strings.TrimSpace(requirement.MetricVersion) == "" || (string(requirement.Role) != "required" && string(requirement.Role) != "optional") {
			return false
		}
		if _, duplicate := seenRequirements[key]; duplicate {
			return false
		}
		seenRequirements[key] = struct{}{}
	}
	seenRuns := make(map[uuid.UUID]struct{}, len(command.SimulationRuns))
	for _, run := range command.SimulationRuns {
		if run.RunId == uuid.Nil || !validHashText(run.ResultHash) {
			return false
		}
		if _, duplicate := seenRuns[run.RunId]; duplicate {
			return false
		}
		seenRuns[run.RunId] = struct{}{}
	}
	return true
}

func validDecisionCommand(command riskdto.RecordNumericDecisionCommand) bool {
	if string(command.Command) != "record_numeric_decision" || command.SourceReportId == uuid.Nil || !validHashText(command.SourceCalculationHash) || strings.TrimSpace(command.Reason) == "" || len(command.Reason) > 2000 || len(command.EligibleItems) == 0 || len(command.EligibleItems) > 500 {
		return false
	}
	seen := make(map[string]struct{}, len(command.EligibleItems))
	for _, item := range command.EligibleItems {
		if strings.TrimSpace(item.ItemId) == "" || !validHashText(item.ItemHash) {
			return false
		}
		if _, duplicate := seen[item.ItemId]; duplicate {
			return false
		}
		seen[item.ItemId] = struct{}{}
	}
	return true
}

func validateBaselineUnion(commandRaw []byte, union riskdto.RiskBaseline) error {
	member, err := objectMember(commandRaw, "baseline")
	if err != nil {
		return fmt.Errorf("invalid baseline")
	}
	discriminator, err := union.Discriminator()
	if err != nil {
		return fmt.Errorf("invalid baseline discriminator")
	}
	switch discriminator {
	case "NO_BASELINE":
		var value riskdto.RiskNoBaseline
		if err = strictJSON(member, &value); err != nil || string(value.Type) != "NO_BASELINE" {
			return fmt.Errorf("invalid NO_BASELINE")
		}
	case "BASELINE":
		var value riskdto.RiskCurrentBaseline
		if err = strictJSON(member, &value); err != nil || string(value.Type) != "BASELINE" || value.ReleaseId == uuid.Nil || !validRevisionIdentity(value.Revision) {
			return fmt.Errorf("invalid current baseline")
		}
	default:
		return fmt.Errorf("unknown baseline discriminator")
	}
	return nil
}

func validateThresholdUnion(commandRaw []byte, union riskdto.RiskThresholdSelection) error {
	member, err := objectMember(commandRaw, "threshold")
	if err != nil {
		return fmt.Errorf("invalid threshold selection")
	}
	discriminator, err := union.Discriminator()
	if err != nil {
		return fmt.Errorf("invalid threshold discriminator")
	}
	switch discriminator {
	case "EXISTING":
		var value riskdto.RiskExistingThresholdSelection
		if err = strictJSON(member, &value); err != nil || string(value.Type) != "EXISTING" || !validRiskIdentity(value.Threshold) {
			return fmt.Errorf("invalid existing threshold selection")
		}
	case "STARTER":
		var value riskdto.RiskStarterThresholdSelection
		if err = strictJSON(member, &value); err != nil || string(value.Type) != "STARTER" || !bool(value.Confirmed) {
			return fmt.Errorf("invalid starter threshold selection")
		}
	case "MODIFIED_STARTER":
		var value riskdto.RiskModifiedStarterThresholdSelection
		if err = strictJSON(member, &value); err != nil || string(value.Type) != "MODIFIED_STARTER" || !bool(value.Confirmed) || !validThresholdBody(value.Body) {
			return fmt.Errorf("invalid modified starter threshold selection")
		}
	default:
		return fmt.Errorf("unknown threshold discriminator")
	}
	return nil
}

func validThresholdBody(body riskdto.RiskThresholdBody) bool {
	if string(body.SchemaVersion) != "v1" || strings.TrimSpace(body.Source) == "" || len(body.Entries) == 0 || len(body.Entries) > 500 || len(body.StructuralRuleVersions) == 0 {
		return false
	}
	for _, rule := range body.StructuralRuleVersions {
		if !validRiskIdentity(rule) {
			return false
		}
	}
	for _, entry := range body.Entries {
		if strings.TrimSpace(entry.SceneId) == "" || strings.TrimSpace(entry.SceneVersion) == "" || strings.TrimSpace(entry.MetricId) == "" || strings.TrimSpace(entry.MetricVersion) == "" || strings.TrimSpace(entry.Unit) == "" || (entry.BalanceGroup != nil && strings.TrimSpace(*entry.BalanceGroup) == "") || (string(entry.Direction) != "higher_is_risk" && string(entry.Direction) != "lower_is_risk" && string(entry.Direction) != "target_range") {
			return false
		}
		warning, ok := canonicalDecimalText(string(entry.RelativeWarning))
		if !ok {
			return false
		}
		block, ok := canonicalDecimalText(string(entry.RelativeBlock))
		if !ok || warning.Compare(block) > 0 {
			return false
		}
		if (entry.AbsoluteWarning == nil) != (entry.AbsoluteBlock == nil) {
			return false
		}
		if entry.AbsoluteWarning != nil {
			absoluteWarning, valid := canonicalDecimalText(string(*entry.AbsoluteWarning))
			if !valid {
				return false
			}
			absoluteBlock, valid := canonicalDecimalText(string(*entry.AbsoluteBlock))
			if !valid || absoluteWarning.Compare(absoluteBlock) > 0 {
				return false
			}
		}
	}
	return true
}

func objectMember(raw []byte, name string) ([]byte, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	member, found := object[name]
	if !found {
		return nil, fmt.Errorf("missing %s", name)
	}
	return member, nil
}

func validRevisionIdentity(identity riskdto.RiskRevisionIdentity) bool {
	return identity.RevisionId != uuid.Nil && validHashText(identity.ConfigHash) && validHashText(identity.VersionManifestHash)
}

func validRiskIdentity(identity riskdto.RiskIdentity) bool {
	return strings.TrimSpace(identity.Id) != "" && strings.TrimSpace(identity.Version) != "" && validHashText(identity.Hash)
}

func validHashText(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func canonicalDecimalText(value string) (formula.Decimal, bool) {
	decimal, err := formula.ParseDecimal(value)
	return decimal, err == nil && thresholdDecimalPattern.MatchString(value)
}

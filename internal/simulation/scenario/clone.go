package scenario

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// ParameterOverlay is one explicit value for a declared parameter path. The
// order of this input never changes scenario action order or canonical output.
type ParameterOverlay struct {
	Path  string
	Value json.RawMessage
}

// Clone creates a new immutable scene/version. The optional participantExists
// boundary lets application code reject references that are missing or
// tombstoned in the captured revision without coupling this package to storage.
func Clone(template Template, sceneID, version string, overlays []ParameterOverlay, participantExists func(string) bool) (Template, error) {
	definition, err := ParseDefinition(template.Body)
	if err != nil {
		return Template{}, err
	}
	if !stableID(sceneID) || !stableID(version) {
		return Template{}, fmt.Errorf("invalid clone scene identity")
	}
	parameters := make(map[string]Parameter, len(definition.Parameters))
	for _, parameter := range definition.Parameters {
		parameters[parameter.Path] = parameter
	}
	seen := make(map[string]struct{}, len(overlays))
	for _, overlay := range overlays {
		parameter, declared := parameters[overlay.Path]
		if !declared {
			return Template{}, fmt.Errorf("undeclared scenario parameter %q", overlay.Path)
		}
		if _, duplicate := seen[overlay.Path]; duplicate {
			return Template{}, fmt.Errorf("duplicate scenario parameter %q", overlay.Path)
		}
		seen[overlay.Path] = struct{}{}
		value, err := validateOverlay(parameter, overlay.Value)
		if err != nil {
			return Template{}, err
		}
		if err = setParameter(&definition, parameter, value); err != nil {
			return Template{}, err
		}
	}
	if participantExists != nil {
		for _, participant := range definition.Participants {
			if !participantExists(participant.ID) {
				return Template{}, fmt.Errorf("scenario participant %q is unavailable", participant.ID)
			}
		}
	}
	definition.ID, definition.Version = sceneID, version
	if err = definition.Validate(); err != nil {
		return Template{}, err
	}
	body, err := json.Marshal(definition)
	if err != nil {
		return Template{}, err
	}
	return templateFromCanonical(definition, "clone", body), nil
}

func validateOverlay(parameter Parameter, raw json.RawMessage) (string, error) {
	if !json.Valid(raw) {
		return "", fmt.Errorf("invalid scenario parameter %q", parameter.Path)
	}
	switch parameter.Type {
	case ParameterString:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("invalid string parameter %q", parameter.Path)
		}
		return value, nil
	case ParameterBoolean:
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", fmt.Errorf("invalid boolean parameter %q", parameter.Path)
		}
		return strconv.FormatBool(value), nil
	case ParameterInteger, ParameterDurationMS:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", fmt.Errorf("invalid integer parameter %q", parameter.Path)
		}
		integer, err := strconv.ParseInt(value, 10, 64)
		if err != nil || !withinBounds(big.NewRat(integer, 1), parameter.Minimum, parameter.Maximum) {
			return "", fmt.Errorf("out-of-range integer parameter %q", parameter.Path)
		}
		return value, nil
	case ParameterDecimal:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", fmt.Errorf("invalid decimal parameter %q", parameter.Path)
		}
		decimal, ok := new(big.Rat).SetString(value)
		if !ok || !withinBounds(decimal, parameter.Minimum, parameter.Maximum) {
			return "", fmt.Errorf("out-of-range decimal parameter %q", parameter.Path)
		}
		return value, nil
	default:
		return "", fmt.Errorf("unsupported scenario parameter %q", parameter.Path)
	}
}

func withinBounds(value *big.Rat, minimum, maximum string) bool {
	if minimum != "" {
		bound, ok := new(big.Rat).SetString(minimum)
		if !ok || value.Cmp(bound) < 0 {
			return false
		}
	}
	if maximum != "" {
		bound, ok := new(big.Rat).SetString(maximum)
		if !ok || value.Cmp(bound) > 0 {
			return false
		}
	}
	return true
}

func setParameter(definition *Definition, parameter Parameter, value string) error {
	parts := strings.Split(parameter.Path, "/")
	if len(parts) != 5 || parts[1] != "actions" || parts[3] != "inputs" || !stableID(parts[2]) || !stableID(parts[4]) {
		return fmt.Errorf("unsupported scenario parameter path %q", parameter.Path)
	}
	for actionIndex := range definition.Actions {
		action := &definition.Actions[actionIndex]
		if action.ID != parts[2] {
			continue
		}
		for inputIndex := range action.Inputs {
			if action.Inputs[inputIndex].ID == parts[4] {
				if parameter.Unit != "" && action.Inputs[inputIndex].Unit != parameter.Unit {
					return fmt.Errorf("scenario parameter %q has incompatible unit", parameter.Path)
				}
				action.Inputs[inputIndex].Value = value
				return nil
			}
		}
	}
	return fmt.Errorf("scenario parameter path %q has no target", parameter.Path)
}

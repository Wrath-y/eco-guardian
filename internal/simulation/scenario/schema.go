package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	MaxParticipants = 64
	MaxActions      = 4096
	MaxParameters   = 128
)

// Definition is a bounded declarative scenario. It deliberately has no
// scripts, expressions, callbacks, or executable extension fields.
type Definition struct {
	ID           string        `json:"id"`
	Version      string        `json:"version"`
	Participants []Participant `json:"participants"`
	Actions      []Action      `json:"actions"`
	DurationMS   int64         `json:"duration_ms"`
	DefaultSeed  uint64        `json:"default_seed"`
	Parameters   []Parameter   `json:"parameters"`
	Budgets      Budgets       `json:"budgets"`
}

type Participant struct {
	ID         string      `json:"id"`
	Kind       string      `json:"kind"`
	Attributes []Attribute `json:"attributes"`
}

type Attribute struct {
	ID    string `json:"id"`
	Value string `json:"value"`
	Unit  string `json:"unit,omitempty"`
}

type Action struct {
	ID          string      `json:"id"`
	EventID     string      `json:"event_id"`
	EvaluatorID string      `json:"evaluator_id"`
	AtMS        int64       `json:"at_ms"`
	SourceID    string      `json:"source_id"`
	TargetID    string      `json:"target_id"`
	Inputs      []Attribute `json:"inputs,omitempty"`
}

type ParameterType string

const (
	ParameterString     ParameterType = "string"
	ParameterInteger    ParameterType = "integer"
	ParameterDecimal    ParameterType = "decimal"
	ParameterDurationMS ParameterType = "duration_ms"
	ParameterBoolean    ParameterType = "boolean"
)

type Parameter struct {
	Path         string          `json:"path"`
	Type         ParameterType   `json:"type"`
	DefaultValue json.RawMessage `json:"default_value"`
	Minimum      string          `json:"minimum,omitempty"`
	Maximum      string          `json:"maximum,omitempty"`
	Unit         string          `json:"unit,omitempty"`
}

type Budgets struct {
	MaxEvents    int `json:"max_events"`
	MaxSteps     int `json:"max_steps"`
	MaxSamples   int `json:"max_samples"`
	MaxRuntimeMS int `json:"max_runtime_ms"`
}

func ParseDefinition(body []byte) (Definition, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var definition Definition
	if err := decoder.Decode(&definition); err != nil {
		return Definition{}, fmt.Errorf("invalid scenario definition: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Definition{}, fmt.Errorf("invalid scenario definition: multiple values")
	}
	if err := definition.Validate(); err != nil {
		return Definition{}, err
	}
	return definition, nil
}

func (d Definition) Validate() error {
	if !stableID(d.ID) || !stableID(d.Version) || d.DurationMS <= 0 || len(d.Participants) == 0 || len(d.Participants) > MaxParticipants || len(d.Actions) > MaxActions || len(d.Parameters) > MaxParameters || !d.Budgets.valid() {
		return fmt.Errorf("invalid bounded scenario definition")
	}
	participants := make(map[string]struct{}, len(d.Participants))
	for _, participant := range d.Participants {
		if !stableID(participant.ID) || !stableID(participant.Kind) {
			return fmt.Errorf("invalid scenario participant %q", participant.ID)
		}
		if _, duplicate := participants[participant.ID]; duplicate {
			return fmt.Errorf("duplicate scenario participant %q", participant.ID)
		}
		participants[participant.ID] = struct{}{}
		if err := validateAttributes(participant.Attributes); err != nil {
			return err
		}
	}
	actions := make(map[string]struct{}, len(d.Actions))
	for _, action := range d.Actions {
		if !stableID(action.ID) || !stableID(action.EventID) || !stableID(action.EvaluatorID) || action.AtMS < 0 || action.AtMS > d.DurationMS {
			return fmt.Errorf("invalid scenario action %q", action.ID)
		}
		if _, duplicate := actions[action.ID]; duplicate {
			return fmt.Errorf("duplicate scenario action %q", action.ID)
		}
		actions[action.ID] = struct{}{}
		if _, found := participants[action.SourceID]; !found {
			return fmt.Errorf("unknown action source %q", action.SourceID)
		}
		if _, found := participants[action.TargetID]; !found {
			return fmt.Errorf("unknown action target %q", action.TargetID)
		}
		if err := validateAttributes(action.Inputs); err != nil {
			return err
		}
	}
	paths := make(map[string]struct{}, len(d.Parameters))
	for _, parameter := range d.Parameters {
		if !validParameter(parameter) {
			return fmt.Errorf("invalid scenario parameter %q", parameter.Path)
		}
		if _, duplicate := paths[parameter.Path]; duplicate {
			return fmt.Errorf("duplicate scenario parameter %q", parameter.Path)
		}
		paths[parameter.Path] = struct{}{}
	}
	return nil
}

func (b Budgets) valid() bool {
	return b.MaxEvents > 0 && b.MaxSteps > 0 && b.MaxSamples > 0 && b.MaxRuntimeMS > 0
}

func validateAttributes(attributes []Attribute) error {
	seen := make(map[string]struct{}, len(attributes))
	for _, attribute := range attributes {
		if !stableID(attribute.ID) || strings.TrimSpace(attribute.Value) == "" || (attribute.Unit != "" && !stableID(attribute.Unit)) {
			return fmt.Errorf("invalid scenario attribute %q", attribute.ID)
		}
		if _, duplicate := seen[attribute.ID]; duplicate {
			return fmt.Errorf("duplicate scenario attribute %q", attribute.ID)
		}
		seen[attribute.ID] = struct{}{}
	}
	return nil
}

func validParameter(parameter Parameter) bool {
	if !strings.HasPrefix(parameter.Path, "/") || !utf8.ValidString(parameter.Path) || !json.Valid(parameter.DefaultValue) {
		return false
	}
	switch parameter.Type {
	case ParameterString, ParameterInteger, ParameterDecimal, ParameterDurationMS, ParameterBoolean:
		return true
	default:
		return false
	}
}

func stableID(value string) bool {
	return strings.TrimSpace(value) != "" && utf8.ValidString(value) && len(value) <= 128 && !strings.ContainsAny(value, "\x00\r\n")
}

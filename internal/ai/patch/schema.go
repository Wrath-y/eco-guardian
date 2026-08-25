package patch

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strconv"
	"strings"

	"github.com/google/uuid"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
)

// BuildDecodeContext derives every value rule and original value from the
// registered entity schemas and the frozen base materialization. Callers
// cannot broaden types or collection semantics with Provider-supplied data.
func BuildDecodeContext(
	patchID aicontract.PatchID,
	input aicontract.AIDesignInputV1,
	evidenceManifest aicontract.VersionIdentity,
	evidenceIDs []aicontract.EvidenceID,
	registry *domain.Registry,
	entities []domain.Entity,
) (DecodeContext, error) {
	if registry == nil {
		return DecodeContext{}, decodeError(ErrorSchemaViolation)
	}
	byID := make(map[aicontract.EntityID]domain.Entity, len(entities))
	for _, entity := range entities {
		id := aicontract.EntityID(entity.ID)
		if _, duplicate := byID[id]; duplicate {
			return DecodeContext{}, decodeError(ErrorScopeViolation)
		}
		byID[id] = entity
	}
	values := []ValueScope{}
	for _, target := range input.AllowedTargets {
		entity, found := byID[target.EntityID]
		if !found || string(entity.Kind) != target.Kind || entity.EntityVersion != target.ExpectedEntityVersion {
			return DecodeContext{}, decodeError(ErrorScopeViolation)
		}
		document, err := entityDocument(entity)
		if err != nil {
			return DecodeContext{}, decodeError(ErrorSchemaViolation)
		}
		for _, allowed := range target.Paths {
			if forbiddenMutationPath(allowed.Path) {
				return DecodeContext{}, decodeError(ErrorScopeViolation)
			}
			tokens, ok := pointerTokens(string(allowed.Path))
			if !ok {
				return DecodeContext{}, decodeError(ErrorScopeViolation)
			}
			original, ok := valueAtPointer(document, tokens)
			if !ok {
				return DecodeContext{}, decodeError(ErrorScopeViolation)
			}
			schema, err := schemaAtPath(registry, entity.Kind, tokens)
			if err != nil {
				return DecodeContext{}, err
			}
			valueType, err := schemaJSONType(schema, original)
			if err != nil {
				return DecodeContext{}, err
			}
			originalJSON, err := json.Marshal(original)
			if err != nil {
				return DecodeContext{}, decodeError(ErrorSchemaViolation)
			}
			schemaJSON, _ := json.Marshal(schema)
			value := ValueScope{EntityID: target.EntityID, Path: allowed.Path, ValueType: valueType, Schema: schemaJSON, Original: originalJSON}
			if valueType == JSONArray {
				items, ok := schema["items"].(map[string]any)
				if !ok {
					return DecodeContext{}, decodeError(ErrorSchemaViolation)
				}
				items, err = resolveSchema(registry, items)
				if err != nil {
					return DecodeContext{}, err
				}
				value.ElementType, err = schemaJSONType(items, nil)
				if err != nil {
					return DecodeContext{}, err
				}
				value.ElementSchema, _ = json.Marshal(items)
			}
			if allowed.Path == "/payload/base_unit" {
				_ = json.Unmarshal(entity.Payload["dimension"], &value.UnitDimension)
				_ = json.Unmarshal(entity.Payload["value_type"], &value.UnitValueType)
			}
			values = append(values, value)
		}
	}
	context := DecodeContext{
		PatchID: patchID, Input: input, EvidenceManifestIdentity: evidenceManifest,
		EvidenceIDs: append([]aicontract.EvidenceID(nil), evidenceIDs...), Values: values, Registry: registry,
	}
	if !context.Valid() {
		return DecodeContext{}, decodeError(ErrorSchemaViolation)
	}
	return context, nil
}

func entityDocument(entity domain.Entity) (any, error) {
	body, err := json.Marshal(entity)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err = decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func schemaAtPath(registry *domain.Registry, kind domain.EntityKind, tokens []string) (map[string]any, error) {
	if len(tokens) == 0 {
		return nil, decodeError(ErrorScopeViolation)
	}
	var schema map[string]any
	switch tokens[0] {
	case "description", "balance_group":
		schema = map[string]any{"type": "string"}
	case "tag_ids":
		schema = map[string]any{"type": "array", "items": map[string]any{"type": "string", "format": "uuid"}}
	case "payload":
		registered, found := registry.Schema(kind)
		if !found {
			return nil, decodeError(ErrorSchemaViolation)
		}
		if json.Unmarshal(registered.Raw, &schema) != nil {
			return nil, decodeError(ErrorSchemaViolation)
		}
	default:
		return nil, decodeError(ErrorScopeViolation)
	}
	for _, token := range tokens[1:] {
		resolved, err := resolveSchema(registry, schema)
		if err != nil {
			return nil, err
		}
		schema = resolved
		if properties, ok := schema["properties"].(map[string]any); ok {
			next, found := properties[token].(map[string]any)
			if !found {
				return nil, decodeError(ErrorScopeViolation)
			}
			schema = next
			continue
		}
		if items, ok := schema["items"].(map[string]any); ok && arrayIndex(token) {
			schema = items
			continue
		}
		return nil, decodeError(ErrorScopeViolation)
	}
	return resolveSchema(registry, schema)
}

func resolveSchema(registry *domain.Registry, schema map[string]any) (map[string]any, error) {
	for depth := 0; depth < 16; depth++ {
		reference, found := schema["$ref"].(string)
		if !found {
			return schema, nil
		}
		resolved, found := registry.SchemaByID(reference)
		var next map[string]any
		if !found || json.Unmarshal(resolved.Raw, &next) != nil {
			return nil, decodeError(ErrorSchemaViolation)
		}
		schema = next
	}
	return nil, decodeError(ErrorSchemaViolation)
}

func schemaJSONType(schema map[string]any, original any) (JSONType, error) {
	if enum, ok := schema["enum"].([]any); ok && len(enum) > 0 {
		return JSONString, nil
	}
	rawType := schema["type"]
	if values, ok := rawType.([]any); ok {
		for _, candidate := range values {
			name, _ := candidate.(string)
			if matchesSchemaType(original, name) {
				rawType = name
				break
			}
		}
	}
	switch rawType {
	case "string":
		return JSONString, nil
	case "boolean":
		return JSONBoolean, nil
	case "integer":
		return JSONInteger, nil
	case "number":
		return JSONDecimal, nil
	case "object":
		return JSONObject, nil
	case "array":
		return JSONArray, nil
	default:
		return "", decodeError(ErrorSchemaViolation)
	}
}

func matchesSchemaType(value any, name string) bool {
	switch name {
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer", "number":
		_, ok := value.(json.Number)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	default:
		return false
	}
}

func validateSchemaValue(raw json.RawMessage, rawSchema json.RawMessage, registry *domain.Registry, scope ValueScope) error {
	if registry == nil {
		return decodeError(ErrorSchemaViolation)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return decodeError(ErrorSchemaViolation)
	}
	var schema map[string]any
	if json.Unmarshal(rawSchema, &schema) != nil {
		return decodeError(ErrorSchemaViolation)
	}
	return validateAgainstSchema(value, schema, registry, scope, lastPointerToken(string(scope.Path)))
}

func validateAgainstSchema(value any, schema map[string]any, registry *domain.Registry, scope ValueScope, field string) error {
	resolved, err := resolveSchema(registry, schema)
	if err != nil {
		return err
	}
	schema = resolved
	if constant, found := schema["const"]; found && !reflect.DeepEqual(value, constant) {
		return decodeError(ErrorSchemaViolation)
	}
	if enum, found := schema["enum"].([]any); found {
		match := false
		for _, candidate := range enum {
			if reflect.DeepEqual(value, candidate) {
				match = true
				break
			}
		}
		if !match {
			return decodeError(ErrorSchemaViolation)
		}
	}
	if !schemaAllowsValue(schema["type"], value) && schema["enum"] == nil {
		return decodeError(ErrorTypeViolation)
	}
	switch typed := value.(type) {
	case map[string]any:
		properties, _ := schema["properties"].(map[string]any)
		for name := range typed {
			if _, found := properties[name]; !found {
				return decodeError(ErrorSchemaViolation)
			}
		}
		if required, ok := schema["required"].([]any); ok {
			for _, item := range required {
				name, _ := item.(string)
				if _, found := typed[name]; !found {
					return decodeError(ErrorSchemaViolation)
				}
			}
		}
		for name, child := range typed {
			childSchema, _ := properties[name].(map[string]any)
			if childSchema == nil {
				return decodeError(ErrorSchemaViolation)
			}
			if err := validateAgainstSchema(child, childSchema, registry, scope, name); err != nil {
				return err
			}
		}
	case []any:
		items, _ := schema["items"].(map[string]any)
		if items == nil {
			return decodeError(ErrorSchemaViolation)
		}
		for _, child := range typed {
			if err := validateAgainstSchema(child, items, registry, scope, field); err != nil {
				return err
			}
		}
	case string:
		if schema["format"] == "uuid" {
			parsed, err := uuid.Parse(typed)
			if err != nil || parsed.Version() != 7 {
				return decodeError(ErrorTypeViolation)
			}
		}
		switch field {
		case "default", "min", "max", "display_scale", "value", "cap":
			decimal, err := formula.ParseDecimal(typed)
			if err != nil || decimal.String() != typed {
				return decodeError(ErrorNumericViolation)
			}
		case "duration", "cooldown", "termination_budget", "priority", "max_stacks":
			duration, err := formula.ParseDuration(typed)
			if err != nil || duration.String() != typed || (field != "priority" && duration < 0) {
				return decodeError(ErrorNumericViolation)
			}
		case "base_unit":
			units, err := formula.V1Registry()
			if err != nil {
				return decodeError(ErrorUnitViolation)
			}
			unit, found := units.Unit(typed)
			if !found || scope.UnitDimension != "" && unit.Dimension != scope.UnitDimension || scope.UnitValueType != "" && string(unit.ValueType) != scope.UnitValueType {
				return decodeError(ErrorUnitViolation)
			}
		case "dimension":
			if !registeredDimension(typed) {
				return decodeError(ErrorUnitViolation)
			}
		}
	}
	return nil
}

func schemaAllowsValue(rawType any, value any) bool {
	if rawType == nil {
		return true
	}
	if values, ok := rawType.([]any); ok {
		for _, valueType := range values {
			if name, ok := valueType.(string); ok && matchesSchemaType(value, name) {
				return true
			}
		}
		return false
	}
	name, _ := rawType.(string)
	return matchesSchemaType(value, name)
}

func registeredDimension(value string) bool {
	registry, err := formula.V1Registry()
	if err != nil {
		return false
	}
	for _, unit := range registry.Units() {
		if unit.Dimension == value {
			return true
		}
	}
	return false
}

func pointerTokens(pointer string) ([]string, bool) {
	if pointer == "" || pointer[0] != '/' {
		return nil, false
	}
	values := strings.Split(pointer[1:], "/")
	for index, value := range values {
		var builder strings.Builder
		for cursor := 0; cursor < len(value); cursor++ {
			if value[cursor] != '~' {
				builder.WriteByte(value[cursor])
				continue
			}
			if cursor+1 >= len(value) || value[cursor+1] != '0' && value[cursor+1] != '1' {
				return nil, false
			}
			cursor++
			if value[cursor] == '0' {
				builder.WriteByte('~')
			} else {
				builder.WriteByte('/')
			}
		}
		values[index] = builder.String()
	}
	return values, true
}

func valueAtPointer(value any, tokens []string) (any, bool) {
	current := value
	for _, token := range tokens {
		switch typed := current.(type) {
		case map[string]any:
			var found bool
			current, found = typed[token]
			if !found {
				return nil, false
			}
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false
			}
			current = typed[index]
		default:
			return nil, false
		}
	}
	return current, true
}

func arrayIndex(value string) bool {
	if value == "-" {
		return true
	}
	index, err := strconv.Atoi(value)
	return err == nil && index >= 0 && strconv.Itoa(index) == value
}

func lastPointerToken(pointer string) string {
	tokens, ok := pointerTokens(pointer)
	if !ok || len(tokens) == 0 {
		return ""
	}
	return tokens[len(tokens)-1]
}

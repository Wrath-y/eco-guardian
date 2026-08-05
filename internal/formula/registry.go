package formula

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

type ValueType string

const (
	DecimalType ValueType = "decimal"
	IntegerType ValueType = "integer"
	BooleanType ValueType = "boolean"
)

type Unit struct {
	Name      string    `json:"name"`
	ValueType ValueType `json:"value_type"`
	Dimension string    `json:"dimension"`
	Base      string    `json:"base"`
}

type BinaryOperation string

const (
	OperationAdd      BinaryOperation = "add"
	OperationSubtract BinaryOperation = "subtract"
	OperationCompare  BinaryOperation = "compare"
	OperationMultiply BinaryOperation = "multiply"
	OperationDivide   BinaryOperation = "divide"
)

func (r *Registry) AllowsBinary(operation BinaryOperation, left, right Unit) bool {
	switch operation {
	case OperationAdd, OperationSubtract, OperationCompare:
		return left.Dimension == right.Dimension && left.ValueType == right.ValueType
	case OperationMultiply, OperationDivide:
		return left.Dimension == "scalar" || right.Dimension == "scalar"
	default:
		return false
	}
}

// UnitsV1 is the complete public v1 registry. Compatibility is explicit: no
// caller may infer it from a unit's spelling.
var UnitsV1 = []Unit{
	{"scalar", DecimalType, "scalar", "scalar"},
	{"count", IntegerType, "count", "count"},
	{"millisecond", IntegerType, "duration", "millisecond"},
	{"ratio", DecimalType, "ratio", "ratio"},
	{"resource_point", DecimalType, "resource", "resource_point"},
	{"health_point", DecimalType, "health", "health_point"},
	{"damage_point", DecimalType, "damage", "damage_point"},
	{"healing_point", DecimalType, "healing", "healing_point"},
	{"control_millisecond", IntegerType, "control_duration", "control_millisecond"},
}

type Function struct {
	Name                  string `json:"name"`
	Signature             string `json:"signature"`
	Pure                  bool   `json:"pure"`
	ImplementationVersion string `json:"implementation_version"`
}

var FunctionsV1 = []Function{
	{"if", "(Boolean,T,T)->T", true, "1"},
	{"min", "(T,T,...)->T", true, "1"},
	{"max", "(T,T,...)->T", true, "1"},
	{"clamp", "(T,T,T)->T", true, "1"},
	{"abs", "(T)->T", true, "1"},
	{"floor", "(T)->T", true, "1"},
	{"ceil", "(T)->T", true, "1"},
	{"round", "(T)->T", true, "1"},
}

var v1FunctionSignatures = map[string]string{
	"if": "(Boolean,T,T)->T", "min": "(T,T,...)->T", "max": "(T,T,...)->T", "clamp": "(T,T,T)->T",
	"abs": "(T)->T", "floor": "(T)->T", "ceil": "(T)->T", "round": "(T)->T",
}

// Registry is deliberately built from compile-time entries. It has no plugin
// or dynamic loading API.
type Registry struct {
	units        map[string]Unit
	functions    map[string]Function
	manifestHash string
}

func NewRegistry(units []Unit, functions []Function) (*Registry, error) {
	if len(units) != len(UnitsV1) || len(functions) != len(v1FunctionSignatures) {
		return nil, fmt.Errorf("v1 registry module count changed; create a new DSL/Registry version")
	}
	r := &Registry{units: make(map[string]Unit, len(units)), functions: make(map[string]Function, len(functions))}
	parts := make([]string, 0, len(units)+len(functions)+1)
	parts = append(parts, DSLVersion)
	for _, unit := range units {
		if unit.Name == "" || unit.Dimension == "" || unit.Base == "" || (unit.ValueType != DecimalType && unit.ValueType != IntegerType) {
			return nil, fmt.Errorf("invalid unit %q", unit.Name)
		}
		if _, ok := r.units[unit.Name]; ok {
			return nil, fmt.Errorf("duplicate unit %q", unit.Name)
		}
		r.units[unit.Name] = unit
		parts = append(parts, "unit:"+unit.Name+":"+string(unit.ValueType)+":"+unit.Dimension+":"+unit.Base)
	}
	for _, fn := range functions {
		if fn.Name == "" || fn.Signature == "" || !fn.Pure || fn.ImplementationVersion == "" {
			return nil, fmt.Errorf("invalid function %q", fn.Name)
		}
		if _, ok := r.functions[fn.Name]; ok {
			return nil, fmt.Errorf("duplicate function %q", fn.Name)
		}
		if signature, ok := v1FunctionSignatures[fn.Name]; !ok || signature != fn.Signature {
			return nil, fmt.Errorf("unknown or changed v1 function signature for %q", fn.Name)
		}
		r.functions[fn.Name] = fn
		parts = append(parts, "function:"+fn.Name+":"+fn.Signature+":"+fn.ImplementationVersion)
	}
	for name := range v1FunctionSignatures {
		if _, ok := r.functions[name]; !ok {
			return nil, fmt.Errorf("missing v1 function %q", name)
		}
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	r.manifestHash = hex.EncodeToString(sum[:])
	return r, nil
}

func V1Registry() (*Registry, error)                      { return NewRegistry(UnitsV1, FunctionsV1) }
func (r *Registry) Unit(name string) (Unit, bool)         { v, ok := r.units[name]; return v, ok }
func (r *Registry) Function(name string) (Function, bool) { v, ok := r.functions[name]; return v, ok }
func (r *Registry) ManifestHash() string                  { return r.manifestHash }
func (r *Registry) Units() []Unit {
	values := make([]Unit, 0, len(r.units))
	for _, unit := range r.units {
		values = append(values, unit)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
	return values
}
func (r *Registry) Functions() []Function {
	values := make([]Function, 0, len(r.functions))
	for _, function := range r.functions {
		values = append(values, function)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
	return values
}
func (r *Registry) VerifyManifestHash(expected string) error {
	if expected != r.manifestHash {
		return fmt.Errorf("registry manifest hash mismatch")
	}
	return nil
}

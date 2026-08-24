package formula

type Type struct {
	ValueType ValueType
	Unit      Unit
}

type SymbolResolver interface {
	Resolve(scope, symbol string) (Type, bool)
}
type SymbolTable map[string]Type

func (t SymbolTable) Resolve(scope, symbol string) (Type, bool) {
	value, ok := t[scope+":"+symbol]
	return value, ok
}

// RuntimeResolver is the typed evaluator port for a validated AST.  It keeps
// type checking and value lookup coupled to the same immutable scope names so
// callers cannot substitute untyped strings or reparse formula source at run
// time.
type RuntimeResolver interface {
	SymbolResolver
	ResolveValue(scope, symbol string) (Result, bool)
}

// RuntimeTable is a small read-only evaluator context keyed by
// "scope:symbol". Values must carry their schema-validated type and unit.
type RuntimeTable map[string]Result

func (t RuntimeTable) Resolve(scope, symbol string) (Type, bool) {
	value, ok := t[scope+":"+symbol]
	return value.Type, ok
}

func (t RuntimeTable) ResolveValue(scope, symbol string) (Result, bool) {
	value, ok := t[scope+":"+symbol]
	return value, ok
}

type Diagnostic struct {
	Code    string
	Span    Span
	Message string
}

func Infer(node Node, registry *Registry, symbols SymbolResolver) (Type, []Diagnostic) {
	scalar, _ := registry.Unit("scalar")
	switch node.Kind {
	case NodeDecimal:
		return Type{DecimalType, scalar}, nil
	case NodeBoolean:
		return Type{BooleanType, Unit{}}, nil
	case NodeQuantity:
		unit, ok := registry.Unit(node.Unit)
		if !ok {
			return Type{}, []Diagnostic{{"FORMULA_UNIT_MISMATCH", node.Span, "unknown unit"}}
		}
		return Type{unit.ValueType, unit}, nil
	case NodeSelector:
		value, ok := symbols.Resolve(node.Scope, node.Symbol)
		if !ok {
			return Type{}, []Diagnostic{{"FORMULA_UNKNOWN_VARIABLE", node.Span, "unknown variable"}}
		}
		return value, nil
	case NodeUnary:
		value, issues := Infer(node.Args[0], registry, symbols)
		if len(issues) > 0 {
			return Type{}, issues
		}
		if node.Operator == "!" {
			if value.ValueType != BooleanType {
				return Type{}, []Diagnostic{{"FORMULA_TYPE_MISMATCH", node.Span, "! requires Boolean"}}
			}
			return Type{BooleanType, Unit{}}, nil
		}
		if value.ValueType == BooleanType {
			return Type{}, []Diagnostic{{"FORMULA_TYPE_MISMATCH", node.Span, "- requires numeric"}}
		}
		return value, nil
	case NodeBinary:
		left, issues := Infer(node.Args[0], registry, symbols)
		right, rightIssues := Infer(node.Args[1], registry, symbols)
		issues = append(issues, rightIssues...)
		if len(issues) > 0 {
			return Type{}, issues
		}
		switch node.Operator {
		case "&&", "||":
			if left.ValueType != BooleanType || right.ValueType != BooleanType {
				return Type{}, []Diagnostic{{"FORMULA_TYPE_MISMATCH", node.Span, "Boolean operator requires Booleans"}}
			}
			return Type{BooleanType, Unit{}}, nil
		case "<", "<=", ">", ">=", "==", "!=":
			if !registry.AllowsBinary(OperationCompare, left.Unit, right.Unit) {
				return Type{}, []Diagnostic{{"FORMULA_UNIT_MISMATCH", node.Span, "comparison requires same dimension"}}
			}
			return Type{BooleanType, Unit{}}, nil
		case "+", "-":
			if !registry.AllowsBinary(OperationAdd, left.Unit, right.Unit) {
				return Type{}, []Diagnostic{{"FORMULA_UNIT_MISMATCH", node.Span, "arithmetic requires same dimension"}}
			}
			return left, nil
		case "*", "/":
			if !registry.AllowsBinary(OperationMultiply, left.Unit, right.Unit) {
				return Type{}, []Diagnostic{{"FORMULA_UNIT_MISMATCH", node.Span, "operation is not registered"}}
			}
			if left.Unit.Dimension == "scalar" {
				return right, nil
			}
			return left, nil
		}
	case NodeCall:
		if _, ok := registry.Function(node.Text); !ok {
			return Type{}, []Diagnostic{{"FORMULA_UNKNOWN_FUNCTION", node.Span, "unknown function"}}
		}
		if len(node.Args) == 0 {
			return Type{}, []Diagnostic{{"FORMULA_TYPE_MISMATCH", node.Span, "function requires arguments"}}
		}
		if node.Text == "if" {
			if len(node.Args) != 3 {
				return Type{}, []Diagnostic{{"FORMULA_TYPE_MISMATCH", node.Span, "if requires three arguments"}}
			}
			condition, issues := Infer(node.Args[0], registry, symbols)
			if len(issues) > 0 || condition.ValueType != BooleanType {
				return Type{}, []Diagnostic{{"FORMULA_TYPE_MISMATCH", node.Args[0].Span, "if condition must be Boolean"}}
			}
			left, leftIssues := Infer(node.Args[1], registry, symbols)
			right, rightIssues := Infer(node.Args[2], registry, symbols)
			if len(leftIssues)+len(rightIssues) > 0 || left.ValueType != right.ValueType || left.Unit.Dimension != right.Unit.Dimension {
				return Type{}, []Diagnostic{{"FORMULA_TYPE_MISMATCH", node.Span, "if branches must match"}}
			}
			return left, nil
		}
		if (node.Text == "min" || node.Text == "max") && len(node.Args) < 2 {
			return Type{}, []Diagnostic{{"FORMULA_TYPE_MISMATCH", node.Span, "min/max require two arguments"}}
		}
		if node.Text == "clamp" && len(node.Args) != 3 {
			return Type{}, []Diagnostic{{"FORMULA_TYPE_MISMATCH", node.Span, "clamp requires three arguments"}}
		}
		if (node.Text == "abs" || node.Text == "floor" || node.Text == "ceil" || node.Text == "round") && len(node.Args) != 1 {
			return Type{}, []Diagnostic{{"FORMULA_TYPE_MISMATCH", node.Span, "function requires one argument"}}
		}
		first, issues := Infer(node.Args[0], registry, symbols)
		for _, arg := range node.Args[1:] {
			value, argIssues := Infer(arg, registry, symbols)
			issues = append(issues, argIssues...)
			if value.ValueType != first.ValueType || value.Unit.Dimension != first.Unit.Dimension {
				issues = append(issues, Diagnostic{"FORMULA_TYPE_MISMATCH", arg.Span, "function argument mismatch"})
			}
		}
		return first, issues
	}
	return Type{}, []Diagnostic{{"FORMULA_SYNTAX_INVALID", node.Span, "invalid expression"}}
}

// InferOutput ensures a FormulaBinding cannot silently produce a value that is
// incompatible with its schema-declared output attribute.
func InferOutput(node Node, expected Type, registry *Registry, symbols SymbolResolver) []Diagnostic {
	actual, issues := Infer(node, registry, symbols)
	if len(issues) > 0 {
		return issues
	}
	if actual.ValueType != expected.ValueType || actual.Unit.Dimension != expected.Unit.Dimension {
		return []Diagnostic{{"FORMULA_TYPE_MISMATCH", node.Span, "formula output is incompatible with target attribute"}}
	}
	return nil
}

type Result struct {
	Type    Type
	Decimal *Decimal
	Boolean *bool
}

func Evaluate(node Node, registry *Registry) (Result, []Diagnostic) {
	return EvaluateWithSymbols(node, registry, RuntimeTable{})
}

// EvaluateWithSymbols evaluates a typed, already-parsed AST against a
// read-only runtime context. It deliberately accepts no formula source text;
// parser admission remains the responsibility of validation.
func EvaluateWithSymbols(node Node, registry *Registry, symbols RuntimeResolver) (Result, []Diagnostic) {
	if symbols == nil {
		symbols = RuntimeTable{}
	}
	typeInfo, issues := Infer(node, registry, symbols)
	if len(issues) > 0 {
		return Result{}, issues
	}
	value, issues := evaluate(node, registry, symbols)
	value.Type = typeInfo
	return value, issues
}
func evaluate(node Node, registry *Registry, symbols RuntimeResolver) (Result, []Diagnostic) {
	switch node.Kind {
	case NodeDecimal, NodeQuantity:
		d, err := ParseDecimal(node.Text)
		if err != nil {
			return Result{}, []Diagnostic{{"NUMERIC_NON_FINITE", node.Span, err.Error()}}
		}
		return Result{Decimal: &d}, nil
	case NodeBoolean:
		b := node.Text == "true"
		return Result{Boolean: &b}, nil
	case NodeSelector:
		value, ok := symbols.ResolveValue(node.Scope, node.Symbol)
		if !ok || (value.Decimal == nil && value.Boolean == nil) || (value.Decimal != nil && value.Boolean != nil) {
			return Result{}, []Diagnostic{{"FORMULA_UNKNOWN_VARIABLE", node.Span, "unknown variable"}}
		}
		return value, nil
	case NodeUnary:
		value, issues := evaluate(node.Args[0], registry, symbols)
		if len(issues) > 0 {
			return Result{}, issues
		}
		if node.Operator == "!" {
			result := !*value.Boolean
			return Result{Boolean: &result}, nil
		}
		zero, _ := ParseDecimal("0")
		result, err := Subtract(zero, *value.Decimal)
		if err != nil {
			return Result{}, []Diagnostic{{"NUMERIC_OUT_OF_RANGE", node.Span, err.Error()}}
		}
		return Result{Decimal: &result}, nil
	case NodeBinary:
		if node.Operator == "&&" || node.Operator == "||" {
			left, issues := evaluate(node.Args[0], registry, symbols)
			if len(issues) > 0 {
				return Result{}, issues
			}
			if node.Operator == "&&" && !(*left.Boolean) {
				b := false
				return Result{Boolean: &b}, nil
			}
			if node.Operator == "||" && *left.Boolean {
				b := true
				return Result{Boolean: &b}, nil
			}
			return evaluate(node.Args[1], registry, symbols)
		}
		left, issues := evaluate(node.Args[0], registry, symbols)
		if len(issues) > 0 {
			return Result{}, issues
		}
		right, issues := evaluate(node.Args[1], registry, symbols)
		if len(issues) > 0 {
			return Result{}, issues
		}
		if left.Decimal == nil || right.Decimal == nil {
			return Result{}, []Diagnostic{{"FORMULA_TYPE_MISMATCH", node.Span, "numeric operands required"}}
		}
		if node.Operator == "<" || node.Operator == "<=" || node.Operator == ">" || node.Operator == ">=" || node.Operator == "==" || node.Operator == "!=" {
			comparison := left.Decimal.Compare(*right.Decimal)
			outcome := false
			switch node.Operator {
			case "<":
				outcome = comparison < 0
			case "<=":
				outcome = comparison <= 0
			case ">":
				outcome = comparison > 0
			case ">=":
				outcome = comparison >= 0
			case "==":
				outcome = comparison == 0
			case "!=":
				outcome = comparison != 0
			}
			return Result{Boolean: &outcome}, nil
		}
		var value Decimal
		var err error
		switch node.Operator {
		case "+":
			value, err = Add(*left.Decimal, *right.Decimal)
		case "-":
			value, err = Subtract(*left.Decimal, *right.Decimal)
		case "*":
			value, err = Multiply(*left.Decimal, *right.Decimal)
		case "/":
			if right.Decimal.String() == "0" {
				return Result{}, []Diagnostic{{"FORMULA_DIVISION_BY_ZERO", node.Args[1].Span, "division by zero"}}
			}
			value, err = Divide(*left.Decimal, *right.Decimal)
		}
		if err != nil {
			return Result{}, []Diagnostic{{"NUMERIC_OUT_OF_RANGE", node.Span, err.Error()}}
		}
		return Result{Decimal: &value}, nil
	case NodeCall:
		if node.Text == "if" {
			condition, issues := evaluate(node.Args[0], registry, symbols)
			if len(issues) > 0 {
				return Result{}, issues
			}
			if *condition.Boolean {
				return evaluate(node.Args[1], registry, symbols)
			}
			return evaluate(node.Args[2], registry, symbols)
		}
		values := []Decimal{}
		for _, arg := range node.Args {
			value, issues := evaluate(arg, registry, symbols)
			if len(issues) > 0 {
				return Result{}, issues
			}
			if value.Decimal == nil {
				return Result{}, []Diagnostic{{"FORMULA_TYPE_MISMATCH", arg.Span, "numeric argument required"}}
			}
			values = append(values, *value.Decimal)
		}
		current := values[0]
		if node.Text == "min" || node.Text == "max" {
			for _, value := range values[1:] {
				if (node.Text == "min" && value.Compare(current) < 0) || (node.Text == "max" && value.Compare(current) > 0) {
					current = value
				}
			}
			return Result{Decimal: &current}, nil
		}
		if node.Text == "clamp" {
			if values[0].Compare(values[1]) < 0 {
				current = values[1]
			}
			if current.Compare(values[2]) > 0 {
				current = values[2]
			}
			return Result{Decimal: &current}, nil
		}
		var err error
		switch node.Text {
		case "abs":
			current, err = Abs(current)
		case "floor":
			current, err = Floor(current)
		case "ceil":
			current, err = Ceil(current)
		case "round":
			current, err = Round(current)
		}
		if err == nil && (node.Text == "abs" || node.Text == "floor" || node.Text == "ceil" || node.Text == "round") {
			return Result{Decimal: &current}, nil
		}
	}
	return Result{}, []Diagnostic{{"FORMULA_TYPE_MISMATCH", node.Span, "expression needs a runtime value"}}
}

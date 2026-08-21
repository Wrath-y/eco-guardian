package engine

import (
	"errors"
	"strings"
)

// DiagnosticCode is the closed, transport-neutral error vocabulary emitted by
// simulation execution. Its detail is deliberately fixed so evaluator errors
// cannot leak source text, host paths, or a current implementation fallback.
type DiagnosticCode string

const (
	DiagnosticNumericNonFinite  DiagnosticCode = "SIMULATION_NUMERIC_NON_FINITE"
	DiagnosticDivisionByZero    DiagnosticCode = "SIMULATION_DIVISION_BY_ZERO"
	DiagnosticNumericOutOfRange DiagnosticCode = "SIMULATION_NUMERIC_OUT_OF_RANGE"
	DiagnosticUnitOrScope       DiagnosticCode = "SIMULATION_UNIT_OR_SCOPE_INVALID"
	DiagnosticMissingEvaluator  DiagnosticCode = "SIMULATION_EVALUATOR_MISSING"
	DiagnosticContractDrift     DiagnosticCode = "SIMULATION_CONTRACT_DRIFT"
)

// Diagnostic is safe to persist or expose through a simulation Job failure.
type Diagnostic struct{ Code DiagnosticCode }

func (d Diagnostic) Error() string { return string(d.Code) }

// EvaluatorDiagnostic maps upstream formula diagnostics into simulation's
// stable vocabulary. Unknown upstream failures are contract drift, never a
// zero value or an attempt to evaluate with a newer implementation.
func EvaluatorDiagnostic(code string) error {
	switch strings.TrimSpace(code) {
	case "NUMERIC_NON_FINITE":
		return Diagnostic{Code: DiagnosticNumericNonFinite}
	case "FORMULA_DIVISION_BY_ZERO":
		return Diagnostic{Code: DiagnosticDivisionByZero}
	case "NUMERIC_OUT_OF_RANGE":
		return Diagnostic{Code: DiagnosticNumericOutOfRange}
	case "FORMULA_UNIT_MISMATCH", "FORMULA_UNKNOWN_VARIABLE", "FORMULA_TYPE_MISMATCH", "FORMULA_UNKNOWN_FUNCTION":
		return Diagnostic{Code: DiagnosticUnitOrScope}
	case "SIMULATION_EVALUATOR_MISSING":
		return Diagnostic{Code: DiagnosticMissingEvaluator}
	default:
		return Diagnostic{Code: DiagnosticContractDrift}
	}
}

func normalizeEvaluatorError(err error) error {
	if err == nil {
		return nil
	}
	for _, stable := range []error{ErrPastEvent, ErrEventBudget, ErrStepBudget, ErrEventInvalid, ErrDuplicateEvent, ErrOrdinalOverflow} {
		if errors.Is(err, stable) {
			return err
		}
	}
	var diagnostic Diagnostic
	if errors.As(err, &diagnostic) {
		return diagnostic
	}
	return EvaluatorDiagnostic("")
}

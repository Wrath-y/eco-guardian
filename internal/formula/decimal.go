package formula

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/cockroachdb/apd/v3"
)

type NumericPolicy struct {
	Version     string `json:"version"`
	Precision   int    `json:"precision"`
	MinExponent int    `json:"min_exponent"`
	MaxExponent int    `json:"max_exponent"`
	Rounding    string `json:"rounding"`
	FiniteOnly  bool   `json:"finite_only"`
}

var NumericPolicyV1 = NumericPolicy{"numeric-v1", 34, -6143, 6144, "HALF_EVEN", true}

var decimalContextV1 = apd.Context{Precision: 34, MaxExponent: 6144, MinExponent: -6143, Rounding: apd.RoundHalfEven, Traps: apd.DivisionByZero | apd.InvalidOperation | apd.Overflow | apd.Underflow | apd.Subnormal}

// Decimal is a finite decimal128 value. Its implementation is not exposed, so
// formula callers cannot accidentally round through float64.
type Decimal struct{ value apd.Decimal }

func ParseDecimal(text string) (Decimal, error) {
	if text == "" || strings.ContainsAny(strings.ToLower(text), "naninf") {
		return Decimal{}, fmt.Errorf("decimal must be finite")
	}
	d, _, err := decimalContextV1.NewFromString(text)
	if err != nil || d.Form != apd.Finite {
		if err == nil {
			err = fmt.Errorf("decimal must be finite")
		}
		return Decimal{}, fmt.Errorf("invalid decimal %q: %w", text, err)
	}
	return Decimal{value: *d}, nil
}
func (d Decimal) String() string {
	if d.value.IsZero() {
		return "0"
	}
	text := d.value.Text('f')
	if strings.Contains(text, ".") {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	return text
}
func (d Decimal) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }
func (d *Decimal) UnmarshalJSON(raw []byte) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("decimal must be a canonical string: %w", err)
	}
	parsed, err := ParseDecimal(value)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}
func binary(left, right Decimal, operation func(*apd.Decimal, *apd.Decimal, *apd.Decimal) (apd.Condition, error)) (Decimal, error) {
	var result apd.Decimal
	if _, err := operation(&result, &left.value, &right.value); err != nil {
		return Decimal{}, err
	}
	return Decimal{value: result}, nil
}
func Add(left, right Decimal) (Decimal, error)      { return binary(left, right, decimalContextV1.Add) }
func Subtract(left, right Decimal) (Decimal, error) { return binary(left, right, decimalContextV1.Sub) }
func Multiply(left, right Decimal) (Decimal, error) { return binary(left, right, decimalContextV1.Mul) }
func Divide(left, right Decimal) (Decimal, error)   { return binary(left, right, decimalContextV1.Quo) }
func (d Decimal) Compare(other Decimal) int         { return d.value.Cmp(&other.value) }
func unary(value Decimal, operation func(*apd.Decimal, *apd.Decimal) (apd.Condition, error)) (Decimal, error) {
	var result apd.Decimal
	if _, err := operation(&result, &value.value); err != nil {
		return Decimal{}, err
	}
	return Decimal{value: result}, nil
}
func Abs(value Decimal) (Decimal, error)   { return unary(value, decimalContextV1.Abs) }
func Floor(value Decimal) (Decimal, error) { return unary(value, decimalContextV1.Floor) }
func Ceil(value Decimal) (Decimal, error)  { return unary(value, decimalContextV1.Ceil) }
func Round(value Decimal) (Decimal, error) {
	return unary(value, decimalContextV1.RoundToIntegralValue)
}

// Percentage is a canonical ratio: 1 represents exactly 100 percent.
type Percentage struct{ ratio Decimal }

func ParsePercentage(text string) (Percentage, error) {
	d, err := ParseDecimal(text)
	return Percentage{ratio: d}, err
}
func (p Percentage) String() string               { return p.ratio.String() }
func (p Percentage) MarshalJSON() ([]byte, error) { return json.Marshal(p.String()) }
func ParsePercentageDisplay(text string) (Percentage, error) {
	percent, err := ParseDecimal(text)
	if err != nil {
		return Percentage{}, err
	}
	hundred, _ := ParseDecimal("100")
	ratio, err := Divide(percent, hundred)
	return Percentage{ratio: ratio}, err
}
func (p Percentage) DisplayPercent() (string, error) {
	hundred, _ := ParseDecimal("100")
	value, err := Multiply(p.ratio, hundred)
	if err != nil {
		return "", err
	}
	return value.String(), nil
}

// Duration is an integer number of milliseconds, not a display value.
type Duration int64

func ParseDuration(text string) (Duration, error) {
	if text == "" || strings.ContainsAny(text, ".eE") {
		return 0, fmt.Errorf("duration must be integer milliseconds")
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid duration: %w", err)
	}
	return Duration(n), nil
}
func (d Duration) String() string               { return strconv.FormatInt(int64(d), 10) }
func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

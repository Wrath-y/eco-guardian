package formula

import "testing"

func TestDecimalCanonicalAndBoundaries(t *testing.T) {
	d, err := ParseDecimal("001.2300")
	if err != nil || d.String() != "1.23" {
		t.Fatalf("canonical decimal: %v %q", err, d.String())
	}
	left, _ := ParseDecimal("1")
	right, _ := ParseDecimal("3")
	value, err := Divide(left, right)
	if err != nil || value.String() != "0.3333333333333333333333333333333333" {
		t.Fatalf("precision: %v %q", err, value.String())
	}
	if _, err := ParseDecimal("NaN"); err == nil {
		t.Fatal("NaN accepted")
	}
	if _, err := ParseDecimal("Infinity"); err == nil {
		t.Fatal("Infinity accepted")
	}
	if _, err := ParseDecimal("1e6145"); err == nil {
		t.Fatal("overflow accepted")
	}
	if _, err := ParseDecimal("1e-6143"); err != nil {
		t.Fatalf("minimum exponent rejected: %v", err)
	}
	tie, err := ParseDecimal("1.2345678901234567890123456789012345")
	if err != nil || tie.String() != "1.234567890123456789012345678901234" {
		t.Fatalf("half-even tie: %v %q", err, tie.String())
	}
	zero, _ := ParseDecimal("0")
	if _, err := Divide(left, zero); err == nil {
		t.Fatal("division by zero accepted")
	}
}
func TestQuantityCodecs(t *testing.T) {
	p, err := ParsePercentage("1")
	if err != nil || p.String() != "1" {
		t.Fatalf("ratio: %v %q", err, p.String())
	}
	d, err := ParseDuration("1000")
	if err != nil || d.String() != "1000" {
		t.Fatalf("duration: %v %q", err, d.String())
	}
	if _, err := ParseDuration("1.5"); err == nil {
		t.Fatal("fractional milliseconds accepted")
	}
	displayed, err := ParsePercentageDisplay("100")
	if err != nil || displayed.String() != "1" {
		t.Fatalf("display percentage: %v %q", err, displayed.String())
	}
	percent, err := displayed.DisplayPercent()
	if err != nil || percent != "100" {
		t.Fatalf("percentage round trip: %v %q", err, percent)
	}
}

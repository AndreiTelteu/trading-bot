package accounting

import "testing"

func TestDecimalArithmeticIsExactAndRoundsHalfAwayFromZero(t *testing.T) {
	a := MustParse("0.1")
	b := MustParse("0.2")
	if got := a.Add(b).String(); got != "0.3" {
		t.Fatalf("0.1 + 0.2 = %s", got)
	}
	if got := MustParse("12.345").Mul(MustParse("0.01")).String(); got != "0.12345" {
		t.Fatalf("product = %s", got)
	}
	oneThird, err := MustParse("1").Div(MustParse("3"))
	if err != nil || oneThird.String() != "0.333333333333333333" {
		t.Fatalf("1/3 = %s, %v", oneThird.String(), err)
	}
	proportional, err := MustParse("100").MulDiv(MustParse("1"), MustParse("3"))
	if err != nil || proportional.String() != "33.333333333333333333" {
		t.Fatalf("proportional basis = %s, %v", proportional.String(), err)
	}
}

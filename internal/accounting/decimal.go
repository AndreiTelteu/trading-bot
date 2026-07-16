package accounting

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// Decimal is the repository's persisted economic number. Values are fixed to
// 18 decimal places (the precision promised by tradingcore) and are never
// calculated through binary floating point.
type Decimal struct{ value *big.Int }

const Scale = 18

var factor = new(big.Int).Exp(big.NewInt(10), big.NewInt(Scale), nil)

func Zero() Decimal { return Decimal{value: new(big.Int)} }

func Parse(input string) (Decimal, error) {
	input = strings.TrimSpace(input)
	if input == "" || strings.ContainsAny(input, "eE") {
		return Decimal{}, fmt.Errorf("invalid decimal %q", input)
	}
	negative := strings.HasPrefix(input, "-")
	if negative || strings.HasPrefix(input, "+") {
		input = input[1:]
	}
	parts := strings.Split(input, ".")
	if len(parts) > 2 || parts[0] == "" {
		return Decimal{}, fmt.Errorf("invalid decimal %q", input)
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > Scale {
		return Decimal{}, fmt.Errorf("decimal scale exceeds %d", Scale)
	}
	digits := parts[0] + fraction + strings.Repeat("0", Scale-len(fraction))
	for _, digit := range digits {
		if digit < '0' || digit > '9' {
			return Decimal{}, fmt.Errorf("invalid decimal %q", input)
		}
	}
	value := new(big.Int)
	if _, ok := value.SetString(digits, 10); !ok {
		return Decimal{}, fmt.Errorf("invalid decimal %q", input)
	}
	if negative {
		value.Neg(value)
	}
	return Decimal{value: value}, nil
}

// FromFloat is only a compatibility adapter for existing float64 API/model
// fields. FormatFloat emits the exact shortest decimal that round-trips; all
// subsequent arithmetic remains decimal.
func FromFloat(value float64) (Decimal, error) {
	return Parse(strconv.FormatFloat(value, 'f', -1, 64))
}

func MustParse(value string) Decimal {
	d, err := Parse(value)
	if err != nil {
		panic(err)
	}
	return d
}

func (d Decimal) Valid() bool { return d.value != nil }
func (d Decimal) Sign() int {
	if d.value == nil {
		return 0
	}
	return d.value.Sign()
}
func (d Decimal) Cmp(other Decimal) int { return d.integer().Cmp(other.integer()) }
func (d Decimal) Add(other Decimal) Decimal {
	return Decimal{value: new(big.Int).Add(d.integer(), other.integer())}
}
func (d Decimal) Sub(other Decimal) Decimal {
	return Decimal{value: new(big.Int).Sub(d.integer(), other.integer())}
}
func (d Decimal) Neg() Decimal { return Decimal{value: new(big.Int).Neg(d.integer())} }
func (d Decimal) Mul(other Decimal) Decimal {
	product := new(big.Int).Mul(d.integer(), other.integer())
	return Decimal{value: roundQuotient(product, factor)}
}
func (d Decimal) Div(other Decimal) (Decimal, error) {
	if other.Sign() == 0 {
		return Decimal{}, fmt.Errorf("decimal division by zero")
	}
	numerator := new(big.Int).Mul(d.integer(), factor)
	return Decimal{value: roundQuotient(numerator, other.integer())}, nil
}

// MulDiv computes d*multiplier/divisor with one final rounding step. It is used
// for proportional cost-basis release so intermediate 18-place rounding cannot
// leak or duplicate basis across partial fills.
func (d Decimal) MulDiv(multiplier, divisor Decimal) (Decimal, error) {
	if divisor.Sign() == 0 {
		return Decimal{}, fmt.Errorf("decimal division by zero")
	}
	numerator := new(big.Int).Mul(d.integer(), multiplier.integer())
	return Decimal{value: roundQuotient(numerator, divisor.integer())}, nil
}

func roundQuotient(numerator, denominator *big.Int) *big.Int {
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	twice := new(big.Int).Abs(new(big.Int).Mul(remainder, big.NewInt(2)))
	if twice.Cmp(new(big.Int).Abs(denominator)) >= 0 {
		if numerator.Sign()*denominator.Sign() >= 0 {
			quotient.Add(quotient, big.NewInt(1))
		} else {
			quotient.Sub(quotient, big.NewInt(1))
		}
	}
	return quotient
}

func (d Decimal) Float64() float64 {
	value, _ := strconv.ParseFloat(d.String(), 64)
	return value
}

func (d Decimal) String() string {
	value := d.integer()
	negative := value.Sign() < 0
	digits := new(big.Int).Abs(value).String()
	for len(digits) <= Scale {
		digits = "0" + digits
	}
	point := len(digits) - Scale
	result := digits[:point] + "." + digits[point:]
	result = strings.TrimRight(strings.TrimRight(result, "0"), ".")
	if result == "" {
		result = "0"
	}
	if negative && result != "0" {
		result = "-" + result
	}
	return result
}

func (d Decimal) MarshalJSON() ([]byte, error) {
	if !d.Valid() {
		return []byte("null"), nil
	}
	return json.Marshal(d.String())
}

func (d Decimal) integer() *big.Int {
	if d.value == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(d.value)
}

func (d Decimal) Value() (driver.Value, error) {
	if !d.Valid() {
		return nil, nil
	}
	return d.String(), nil
}

func (d *Decimal) Scan(src interface{}) error {
	if src == nil {
		d.value = nil
		return nil
	}
	var input string
	switch value := src.(type) {
	case string:
		input = value
	case []byte:
		input = string(value)
	default:
		input = fmt.Sprint(value)
	}
	parsed, err := Parse(input)
	if err != nil {
		return err
	}
	d.value = parsed.integer()
	return nil
}

func (Decimal) GormDataType() string { return "numeric(38,18)" }

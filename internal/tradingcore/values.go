package tradingcore

import (
	"fmt"
	"strconv"
	"strings"
)

const MaxDecimalScale uint8 = 18

// Decimal is an exact base-10 scaled integer. Its zero value is invalid so
// economic values must be created through NewDecimal or ParseDecimal.
type Decimal struct {
	coefficient string
	scale       uint8
	valid       bool
}

func NewDecimal(coefficient int64, scale uint8) (Decimal, error) {
	return NewScaledDecimal(strconv.FormatInt(coefficient, 10), scale)
}

// NewScaledDecimal accepts an arbitrary-size signed base-10 integer string.
// Keeping the coefficient immutable and unbounded avoids float rounding and
// int64 range loss for high-precision exchange assets.
func NewScaledDecimal(coefficient string, scale uint8) (Decimal, error) {
	if scale > MaxDecimalScale {
		return Decimal{}, fmt.Errorf("decimal scale %d exceeds maximum %d", scale, MaxDecimalScale)
	}
	coefficient = strings.TrimSpace(coefficient)
	negative := strings.HasPrefix(coefficient, "-")
	if negative || strings.HasPrefix(coefficient, "+") {
		coefficient = coefficient[1:]
	}
	if coefficient == "" {
		return Decimal{}, fmt.Errorf("scaled decimal coefficient is required")
	}
	for _, char := range coefficient {
		if char < '0' || char > '9' {
			return Decimal{}, fmt.Errorf("invalid scaled decimal coefficient %q", coefficient)
		}
	}
	coefficient = strings.TrimLeft(coefficient, "0")
	if coefficient == "" {
		coefficient = "0"
	}
	if negative && coefficient != "0" {
		coefficient = "-" + coefficient
	}
	return Decimal{coefficient: coefficient, scale: scale, valid: true}, nil
}

func MustDecimal(coefficient int64, scale uint8) Decimal {
	value, err := NewDecimal(coefficient, scale)
	if err != nil {
		panic(err)
	}
	return value
}

func ParseDecimal(input string) (Decimal, error) {
	input = strings.TrimSpace(input)
	if input == "" || strings.ContainsAny(input, "eE") {
		return Decimal{}, fmt.Errorf("invalid decimal %q", input)
	}
	negative := strings.HasPrefix(input, "-")
	if negative || strings.HasPrefix(input, "+") {
		input = input[1:]
	}
	parts := strings.Split(input, ".")
	if len(parts) > 2 || parts[0] == "" || (len(parts) == 2 && parts[1] == "") {
		return Decimal{}, fmt.Errorf("invalid decimal %q", input)
	}
	scale := 0
	digits := parts[0]
	if len(parts) == 2 {
		scale = len(parts[1])
		digits += parts[1]
	}
	if scale > int(MaxDecimalScale) {
		return Decimal{}, fmt.Errorf("decimal scale %d exceeds maximum %d", scale, MaxDecimalScale)
	}
	for _, char := range digits {
		if char < '0' || char > '9' {
			return Decimal{}, fmt.Errorf("invalid decimal digits %q", digits)
		}
	}
	if negative {
		digits = "-" + digits
	}
	return NewScaledDecimal(digits, uint8(scale))
}

func (value Decimal) Valid() bool         { return value.valid }
func (value Decimal) Coefficient() string { return value.coefficient }
func (value Decimal) Scale() uint8        { return value.scale }
func (value Decimal) Sign() int {
	if !value.valid || value.coefficient == "0" {
		return 0
	}
	if strings.HasPrefix(value.coefficient, "-") {
		return -1
	}
	return 1
}

func (value Decimal) String() string {
	if !value.valid {
		return "<invalid>"
	}
	negative := strings.HasPrefix(value.coefficient, "-")
	digits := value.coefficient
	if negative {
		digits = strings.TrimPrefix(digits, "-")
	}
	if value.scale > 0 {
		for len(digits) <= int(value.scale) {
			digits = "0" + digits
		}
		point := len(digits) - int(value.scale)
		digits = digits[:point] + "." + digits[point:]
	}
	if negative {
		return "-" + digits
	}
	return digits
}

func (value Decimal) Float64() float64 {
	result, _ := strconv.ParseFloat(value.String(), 64)
	return result
}

type Quantity struct{ value Decimal }
type Price struct{ value Decimal }
type SignedAmount struct{ value Decimal }

func NewQuantity(value Decimal) (Quantity, error) {
	if !value.Valid() || value.Sign() <= 0 {
		return Quantity{}, fmt.Errorf("quantity must be a valid positive decimal")
	}
	return Quantity{value: value}, nil
}
func NewPrice(value Decimal) (Price, error) {
	if !value.Valid() || value.Sign() <= 0 {
		return Price{}, fmt.Errorf("price must be a valid positive decimal")
	}
	return Price{value: value}, nil
}
func NewSignedAmount(value Decimal) (SignedAmount, error) {
	if !value.Valid() {
		return SignedAmount{}, fmt.Errorf("amount must be a valid decimal")
	}
	return SignedAmount{value: value}, nil
}
func (value Quantity) Decimal() Decimal     { return value.value }
func (value Price) Decimal() Decimal        { return value.value }
func (value SignedAmount) Decimal() Decimal { return value.value }
func (value Quantity) Valid() bool          { return value.value.Valid() && value.value.Sign() > 0 }
func (value Price) Valid() bool             { return value.value.Valid() && value.value.Sign() > 0 }
func (value SignedAmount) Valid() bool      { return value.value.Valid() }

type AccountID struct{ value string }
type AssetID struct{ value string }
type InstrumentID struct{ value string }
type VenueID struct{ value string }
type PositionID struct{ value string }
type OrderID struct{ value string }
type FillID struct{ value string }
type EventID struct{ value string }
type StrategyID struct{ value string }
type IdempotencyKey struct{ value string }
type CorrelationID struct{ value string }
type CausationID struct{ value string }

func validateIdentity(kind, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", kind)
	}
	if len(value) > 200 {
		return "", fmt.Errorf("%s exceeds 200 characters", kind)
	}
	for _, char := range value {
		if char < 0x21 || char > 0x7e {
			return "", fmt.Errorf("%s contains unsupported characters", kind)
		}
	}
	return value, nil
}

func NewAccountID(v string) (AccountID, error) {
	x, e := validateIdentity("account id", v)
	return AccountID{x}, e
}
func NewAssetID(v string) (AssetID, error) {
	x, e := validateIdentity("asset id", v)
	return AssetID{x}, e
}
func NewInstrumentID(v string) (InstrumentID, error) {
	x, e := validateIdentity("instrument id", v)
	return InstrumentID{x}, e
}
func NewVenueID(v string) (VenueID, error) {
	x, e := validateIdentity("venue id", v)
	return VenueID{x}, e
}
func NewPositionID(v string) (PositionID, error) {
	x, e := validateIdentity("position id", v)
	return PositionID{x}, e
}
func NewOrderID(v string) (OrderID, error) {
	x, e := validateIdentity("order id", v)
	return OrderID{x}, e
}
func NewFillID(v string) (FillID, error) { x, e := validateIdentity("fill id", v); return FillID{x}, e }
func NewEventID(v string) (EventID, error) {
	x, e := validateIdentity("event id", v)
	return EventID{x}, e
}
func NewStrategyID(v string) (StrategyID, error) {
	x, e := validateIdentity("strategy id", v)
	return StrategyID{x}, e
}
func NewIdempotencyKey(v string) (IdempotencyKey, error) {
	x, e := validateIdentity("idempotency key", v)
	return IdempotencyKey{x}, e
}
func NewCorrelationID(v string) (CorrelationID, error) {
	x, e := validateIdentity("correlation id", v)
	return CorrelationID{x}, e
}
func NewCausationID(v string) (CausationID, error) {
	x, e := validateIdentity("causation id", v)
	return CausationID{x}, e
}

func (v AccountID) String() string      { return v.value }
func (v AssetID) String() string        { return v.value }
func (v InstrumentID) String() string   { return v.value }
func (v VenueID) String() string        { return v.value }
func (v PositionID) String() string     { return v.value }
func (v OrderID) String() string        { return v.value }
func (v FillID) String() string         { return v.value }
func (v EventID) String() string        { return v.value }
func (v StrategyID) String() string     { return v.value }
func (v IdempotencyKey) String() string { return v.value }
func (v CorrelationID) String() string  { return v.value }
func (v CausationID) String() string    { return v.value }

type VersionContext struct {
	Strategy, Settings, Policy, Model, FeatureSpec, Dataset string
	Engine, Universe, FlagSchema                            string
}

type Provenance struct {
	Source, Actor, Reason string
	CorrelationID         CorrelationID
	CausationID           CausationID
}

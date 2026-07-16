package tradingcore

import (
	"fmt"
	"math/big"
)

func cloneStrings(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func decimalRat(value Decimal) *big.Rat {
	n := new(big.Int)
	n.SetString(value.Coefficient(), 10)
	d := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(value.Scale())), nil)
	return new(big.Rat).SetFrac(n, d)
}

func ratDecimal(value *big.Rat) (Decimal, error) {
	scale := int(MaxDecimalScale)
	multiplier := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	numerator := new(big.Int).Mul(value.Num(), multiplier)
	quotient, remainder := new(big.Int).QuoRem(numerator, value.Denom(), new(big.Int))
	if new(big.Int).Mul(new(big.Int).Abs(remainder), big.NewInt(2)).Cmp(new(big.Int).Abs(value.Denom())) >= 0 {
		if value.Sign() < 0 {
			quotient.Sub(quotient, big.NewInt(1))
		} else {
			quotient.Add(quotient, big.NewInt(1))
		}
	}
	return NewScaledDecimal(quotient.String(), uint8(scale))
}

func amountFromRat(value *big.Rat) (SignedAmount, error) {
	d, err := ratDecimal(value)
	if err != nil {
		return SignedAmount{}, err
	}
	return NewSignedAmount(d)
}
func quantityFromRat(value *big.Rat) (Quantity, error) {
	d, err := ratDecimal(value)
	if err != nil {
		return Quantity{}, err
	}
	return NewQuantity(d)
}

func notional(quantity Quantity, price Price) *big.Rat {
	return new(big.Rat).Mul(decimalRat(quantity.Decimal()), decimalRat(price.Decimal()))
}

func normalizedQuantity(value *big.Rat, lot Quantity) (*big.Rat, error) {
	if value.Sign() <= 0 {
		return nil, fmt.Errorf("quantity is not positive")
	}
	if !lot.Valid() {
		return new(big.Rat).Set(value), nil
	}
	step := decimalRat(lot.Decimal())
	units := new(big.Rat).Quo(value, step)
	whole := new(big.Int).Quo(units.Num(), units.Denom())
	result := new(big.Rat).Mul(new(big.Rat).SetInt(whole), step)
	if result.Sign() <= 0 {
		return nil, fmt.Errorf("quantity_below_lot_size")
	}
	return result, nil
}

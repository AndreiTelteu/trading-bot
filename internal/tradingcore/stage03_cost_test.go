package tradingcore

import "testing"

func TestStage03AdverseTickRoundingBySide(t *testing.T) {
	decimal, _ := ParseDecimal("100.001")
	price, _ := NewPrice(decimal)
	buy, err := roundPriceAdverse(price, "0.01", Buy)
	if err != nil {
		t.Fatal(err)
	}
	sell, err := roundPriceAdverse(price, "0.01", Sell)
	if err != nil {
		t.Fatal(err)
	}
	if buy.Decimal().String() != "100.010000000000000000" || sell.Decimal().String() != "100.000000000000000000" {
		t.Fatalf("buy=%s sell=%s", buy.Decimal().String(), sell.Decimal().String())
	}
}

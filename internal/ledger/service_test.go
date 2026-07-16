package ledger

import (
	"testing"
	"trading-go/internal/accounting"
)

func TestCostedPaperFillUsesExactBasisPoints(t *testing.T) {
	fill, fee, err := CostedPaperFill("buy", accounting.MustParse("2"), accounting.MustParse("100"), 10, 5)
	if err != nil {
		t.Fatal(err)
	}
	if fill.String() != "100.05" {
		t.Fatalf("fill = %s", fill.String())
	}
	if fee.String() != "0.2001" {
		t.Fatalf("fee = %s", fee.String())
	}

	sell, _, err := CostedPaperFill("sell", accounting.MustParse("1"), accounting.MustParse("100"), 0, 5)
	if err != nil || sell.String() != "99.95" {
		t.Fatalf("sell = %s, err=%v", sell.String(), err)
	}
}

package backtest

import (
	"strings"
	"testing"

	"trading-go/internal/tradingcore"
)

func TestDecimalStringCapsAtMaxScale(t *testing.T) {
	// Binary float noise that FormatFloat(..., -1) can expand past MaxDecimalScale.
	// Example from the research init panic path: accumulated cash/mark values.
	noisy := 123.456789012345678901234 // more than 18 fraction digits in full expansion
	got := decimalString(noisy)
	if strings.Contains(got, ".") {
		frac := strings.SplitN(got, ".", 2)[1]
		if len(frac) > int(tradingcore.MaxDecimalScale) {
			t.Fatalf("fraction digits %d > max %d for %q", len(frac), tradingcore.MaxDecimalScale, got)
		}
	}
	if _, err := tradingcore.ParseDecimal(got); err != nil {
		t.Fatalf("ParseDecimal(%q): %v", got, err)
	}
}

func TestMustAmountAcceptsNoisyFloat(t *testing.T) {
	// Should not panic.
	_ = mustAmount(0.1 + 0.2)          // classic binary noise
	_ = mustAmount(1.0 / 3.0)          // repeating fraction
	_ = mustAmount(123.45678901234567) // near max scale
	_ = mustAmount(0)
}

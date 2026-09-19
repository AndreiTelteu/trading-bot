package pointintime

import (
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestConstraintAtUsesImmutableTimelineBoundaries(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	change := base.Add(24 * time.Hour)
	timeline := []Constraint{
		{ExchangeSymbolID: "symbol", EffectiveFrom: base, EffectiveTo: &change, AvailableAt: base, QuantityStep: .1},
		{ExchangeSymbolID: "symbol", EffectiveFrom: change, AvailableAt: change, QuantityStep: .01},
	}

	first, err := ConstraintAt(timeline, change.Add(-time.Nanosecond))
	if err != nil || first.QuantityStep != .1 {
		t.Fatalf("first constraint = %+v, %v", first, err)
	}
	second, err := ConstraintAt(timeline, change)
	if err != nil || second.QuantityStep != .01 {
		t.Fatalf("second constraint = %+v, %v", second, err)
	}
}

func TestConstraintAtRejectsUnavailableAndExpiredRows(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	expires := base.Add(time.Hour)
	timeline := []Constraint{{EffectiveFrom: base, EffectiveTo: &expires, AvailableAt: base.Add(30 * time.Minute)}}

	for _, at := range []time.Time{base, expires} {
		if _, err := ConstraintAt(timeline, at); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("ConstraintAt(%s) error = %v, want record not found", at, err)
		}
	}
}

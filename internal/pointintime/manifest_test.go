package pointintime

import (
	"testing"
	"time"

	"trading-go/internal/database"
)

func TestEffectiveAssetIdentityEvidenceUsesEarlierVersionedSymbolEvidence(t *testing.T) {
	assetAvailable := time.Date(2024, 11, 27, 0, 0, 0, 0, time.UTC)
	symbolAvailable := time.Date(2024, 9, 2, 0, 0, 0, 0, time.UTC)
	assetRetrieved := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	symbolRetrieved := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)

	available, retrieved := effectiveAssetIdentityEvidence(
		database.Asset{AvailableAt: assetAvailable, RetrievedAt: assetRetrieved},
		database.ExchangeSymbol{AvailableAt: symbolAvailable, RetrievedAt: symbolRetrieved},
	)
	if !available.Equal(symbolAvailable) || !retrieved.Equal(symbolRetrieved) {
		t.Fatalf("available=%s retrieved=%s", available, retrieved)
	}

	available, retrieved = effectiveAssetIdentityEvidence(
		database.Asset{AvailableAt: symbolAvailable, RetrievedAt: assetRetrieved},
		database.ExchangeSymbol{AvailableAt: assetAvailable, RetrievedAt: symbolRetrieved},
	)
	if !available.Equal(symbolAvailable) || !retrieved.Equal(assetRetrieved) {
		t.Fatalf("later symbol evidence replaced earlier asset evidence: available=%s retrieved=%s", available, retrieved)
	}
}

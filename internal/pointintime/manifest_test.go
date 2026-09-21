package pointintime

import (
	"encoding/json"
	"testing"
	"time"

	"trading-go/internal/database"
)

func TestJSONArrayDigestMatchesCanonicalManifestEncoding(t *testing.T) {
	values := []canonicalBar{
		{SymbolID: "asset\\\"one", Role: RoleDecision, Timeframe: "15m", OpenTime: "2026-01-01T00:00:00Z", Provenance: `{"source":"x"}`},
		{SymbolID: "asset-two", Role: RoleExecution, Timeframe: "1m", OpenTime: "2026-01-01T00:01:00Z", TradeCount: 42},
	}
	streamed := newJSONArrayDigest()
	for _, value := range values {
		streamed.add(value)
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := streamed.sum(), digest(json.RawMessage(encoded)); got != want {
		t.Fatalf("streamed digest=%s canonical digest=%s", got, want)
	}

	empty := newJSONArrayDigest()
	encoded, err = json.Marshal([]canonicalBar{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := empty.sum(), digest(json.RawMessage(encoded)); got != want {
		t.Fatalf("empty streamed digest=%s canonical digest=%s", got, want)
	}
}

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

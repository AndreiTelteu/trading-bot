package services

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"trading-go/internal/accounting"
	"trading-go/internal/database"
	ledgerpkg "trading-go/internal/ledger"
	"trading-go/internal/testutil"

	"gorm.io/gorm"
)

func TestExecuteBuyFromTrendingInitializesAtrTrailingWithoutVolSizing(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	database.DB = db

	if err := database.SeedData(); err != nil {
		t.Fatal(err)
	}
	if _, err := ledgerpkg.New(database.DB).ApplyAdjustment(context.Background(), ledgerpkg.AdjustmentCommand{IdempotencyKey: "trending-test-deposit", Type: ledgerpkg.EventCapitalDeposit, Amount: accounting.MustParse("600"), Currency: "USDT", Actor: "test", Reason: "fixture funding"}); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"entry_percent": "10", "vol_sizing_enabled": "false", "atr_trailing_enabled": "true", "atr_trailing_mult": "1", "atr_trailing_period": "14", "atr_annualization_enabled": "false", "atr_annualization_days": "365"} {
		if err := database.DB.Model(&database.Setting{}).Where("key = ?", key).Update("value", value).Error; err != nil {
			t.Fatal(err)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/ticker/24hr":
			payload := map[string]string{
				"symbol":             r.URL.Query().Get("symbol"),
				"lastPrice":          "150",
				"priceChange":        "0",
				"priceChangePercent": "0",
				"highPrice":          "150",
				"lowPrice":           "100",
				"volume":             "0",
				"quoteVolume":        "0",
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(payload)
		case "/api/v3/klines":
			klines := make([][]interface{}, 20)
			base := time.Now().Add(-20 * time.Minute).UnixMilli()
			for i := 0; i < 20; i++ {
				openTime := base + int64(i*60000)
				closeTime := openTime + 60000
				klines[i] = []interface{}{
					float64(openTime),
					"100",
					"110",
					"90",
					"100",
					"10",
					float64(closeTime),
				}
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(klines)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	exchange = &ExchangeService{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	success, err := executeBuyFromTrending("ETHUSDT")
	if err != nil {
		t.Fatalf("executeBuyFromTrending() error = %v", err)
	}
	if !success {
		t.Fatal("executeBuyFromTrending() expected success")
	}

	var position database.Position
	if err := database.DB.Where("symbol = ?", "ETH").First(&position).Error; err != nil {
		t.Fatalf("Failed to load position: %v", err)
	}
	if position.TrailingStopPrice == nil {
		t.Fatal("TrailingStopPrice should be initialized when ATR trailing is enabled")
	}
	if position.LastAtrValue == nil {
		t.Fatal("LastAtrValue should be stored when ATR trailing is enabled")
	}
}

func TestExecuteBuyFromTrendingRejectsUnreconciledClosedLegacyPosition(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	database.DB = db

	if err := database.SeedData(); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"entry_percent": "10", "vol_sizing_enabled": "false", "atr_trailing_enabled": "false"} {
		if err := database.DB.Model(&database.Setting{}).Where("key = ?", key).Update("value", value).Error; err != nil {
			t.Fatal(err)
		}
	}

	entry := 90.0
	closedAt := time.Now().Add(-48 * time.Hour)
	closeReason := "manual"
	existing := database.Position{
		Symbol:       "ZEC",
		Amount:       1.0,
		AvgPrice:     entry,
		EntryPrice:   &entry,
		CurrentPrice: &entry,
		Pnl:          10,
		PnlPercent:   5,
		Status:       "closed",
		OpenedAt:     time.Now().Add(-72 * time.Hour),
		ClosedAt:     &closedAt,
		CloseReason:  &closeReason,
	}
	testutil.WithLedgerProjectionWrites(t, database.DB, func(tx *gorm.DB) error { return tx.Create(&existing).Error })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/ticker/24hr":
			payload := map[string]string{
				"symbol":             r.URL.Query().Get("symbol"),
				"lastPrice":          "150",
				"priceChange":        "0",
				"priceChangePercent": "0",
				"highPrice":          "150",
				"lowPrice":           "100",
				"volume":             "0",
				"quoteVolume":        "0",
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(payload)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	exchange = &ExchangeService{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	success, err := executeBuyFromTrending("ZECUSDT")
	if success || !errors.Is(err, ledgerpkg.ErrUnreconciledLegacyState) {
		t.Fatalf("success=%v error=%v, want explicit unresolved migration fence", success, err)
	}

	var positions []database.Position
	if err := database.DB.Where("symbol = ?", "ZEC").Find(&positions).Error; err != nil {
		t.Fatalf("Failed to load positions: %v", err)
	}
	if len(positions) != 1 {
		t.Fatalf("Expected 1 ZEC position row, got %d", len(positions))
	}

	position := positions[0]
	if position.Status != "closed" || position.ClosedAt == nil || position.CloseReason == nil {
		t.Fatalf("legacy position was mutated: %+v", position)
	}
}

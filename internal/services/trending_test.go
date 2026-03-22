package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/testutil"
)

func TestExecuteBuyFromTrendingInitializesAtrTrailingWithoutVolSizing(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	database.DB = db

	database.DB.Create(&database.Wallet{Balance: 1000.0, Currency: "USDT"})
	database.DB.Create(&database.Setting{Key: "entry_percent", Value: "10"})
	database.DB.Create(&database.Setting{Key: "vol_sizing_enabled", Value: "false"})
	database.DB.Create(&database.Setting{Key: "atr_trailing_enabled", Value: "true"})
	database.DB.Create(&database.Setting{Key: "atr_trailing_mult", Value: "1"})
	database.DB.Create(&database.Setting{Key: "atr_trailing_period", Value: "14"})
	database.DB.Create(&database.Setting{Key: "atr_annualization_enabled", Value: "false"})
	database.DB.Create(&database.Setting{Key: "atr_annualization_days", Value: "365"})

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

func TestExecuteBuyFromTrendingReopensClosedPosition(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	database.DB = db

	database.DB.Create(&database.Wallet{Balance: 1000.0, Currency: "USDT"})
	database.DB.Create(&database.Setting{Key: "entry_percent", Value: "10"})
	database.DB.Create(&database.Setting{Key: "vol_sizing_enabled", Value: "false"})
	database.DB.Create(&database.Setting{Key: "atr_trailing_enabled", Value: "false"})

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
	if err := database.DB.Create(&existing).Error; err != nil {
		t.Fatalf("Failed to seed closed position: %v", err)
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
	if err != nil {
		t.Fatalf("executeBuyFromTrending() error = %v", err)
	}
	if !success {
		t.Fatal("executeBuyFromTrending() expected success")
	}

	var positions []database.Position
	if err := database.DB.Where("symbol = ?", "ZEC").Find(&positions).Error; err != nil {
		t.Fatalf("Failed to load positions: %v", err)
	}
	if len(positions) != 1 {
		t.Fatalf("Expected 1 ZEC position row, got %d", len(positions))
	}

	position := positions[0]
	if position.Status != "open" {
		t.Fatalf("Position status = %s, want open", position.Status)
	}
	if position.ClosedAt != nil {
		t.Fatal("ClosedAt should be cleared when reopening a position")
	}
	if position.CloseReason != nil {
		t.Fatal("CloseReason should be cleared when reopening a position")
	}
	if position.Pnl != 0 || position.PnlPercent != 0 {
		t.Fatalf("Expected pnl fields reset, got pnl=%v pnlPercent=%v", position.Pnl, position.PnlPercent)
	}
	if position.AvgPrice != 150 {
		t.Fatalf("AvgPrice = %v, want 150", position.AvgPrice)
	}
}

package services

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"trading-go/internal/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestResolveCloseReasonPrecedence(t *testing.T) {
	stop := 95.0
	takeProfit := 130.0
	atrTrailing := 100.0
	entry := 120.0

	reason := resolveCloseReason(94, -5, &stop, &takeProfit, true, &atrTrailing, true, &entry, 10, 5, 30)
	if reason != "stop_loss" {
		t.Errorf("resolveCloseReason() = %v, want stop_loss", reason)
	}
}

func TestResolveCloseReasonAtrTrailingOverPercentTrailing(t *testing.T) {
	atrTrailing := 105.0
	entry := 120.0

	reason := resolveCloseReason(100, -10, nil, nil, true, &atrTrailing, true, &entry, 10, 5, 30)
	if reason != "atr_trailing_stop" {
		t.Errorf("resolveCloseReason() = %v, want atr_trailing_stop", reason)
	}
}

func TestResolveCloseReasonPercentTrailing(t *testing.T) {
	entry := 120.0

	reason := resolveCloseReason(100, -10, nil, nil, false, nil, true, &entry, 10, 5, 30)
	if reason != "trailing_stop" {
		t.Errorf("resolveCloseReason() = %v, want trailing_stop", reason)
	}
}

func TestUpdatePositionsPricesAtrTrailingStopRatchet(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to connect to test database: %v", err)
	}
	database.DB = db
	database.DB.AutoMigrate(
		&database.Wallet{},
		&database.Position{},
		&database.Order{},
		&database.Setting{},
		&database.IndicatorWeight{},
		&database.ActivityLog{},
		&database.PortfolioSnapshot{},
	)

	database.DB.Create(&database.Wallet{Balance: 1000.0, Currency: "USDT"})
	database.DB.Create(&database.Setting{Key: "atr_trailing_enabled", Value: "true"})
	database.DB.Create(&database.Setting{Key: "atr_trailing_mult", Value: "1"})
	database.DB.Create(&database.Setting{Key: "atr_trailing_period", Value: "14"})
	database.DB.Create(&database.Setting{Key: "take_profit_percent", Value: "200"})

	entry := 100.0
	trailingOne := 120.0
	trailingTwo := 140.0
	now := time.Now()

	database.DB.Create(&database.Position{
		Symbol:            "BTC",
		Amount:            1.0,
		AvgPrice:          100.0,
		EntryPrice:        &entry,
		TrailingStopPrice: &trailingOne,
		Status:            "open",
		OpenedAt:          now,
	})
	database.DB.Create(&database.Position{
		Symbol:            "ETH",
		Amount:            1.0,
		AvgPrice:          100.0,
		EntryPrice:        &entry,
		TrailingStopPrice: &trailingTwo,
		Status:            "open",
		OpenedAt:          now,
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/ticker/24hr":
			symbol := r.URL.Query().Get("symbol")
			payload := map[string]string{
				"symbol":             symbol,
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

	_, err = UpdatePositionsPrices()
	if err != nil {
		t.Fatalf("UpdatePositionsPrices() error = %v", err)
	}

	var updatedBTC database.Position
	if err := database.DB.Where("symbol = ?", "BTC").First(&updatedBTC).Error; err != nil {
		t.Fatalf("Failed to load BTC position: %v", err)
	}
	if updatedBTC.TrailingStopPrice == nil {
		t.Fatalf("BTC trailing stop should be set")
	}
	if math.Abs(*updatedBTC.TrailingStopPrice-130.0) > 0.0001 {
		t.Errorf("BTC trailing stop = %v, want 130", *updatedBTC.TrailingStopPrice)
	}

	var updatedETH database.Position
	if err := database.DB.Where("symbol = ?", "ETH").First(&updatedETH).Error; err != nil {
		t.Fatalf("Failed to load ETH position: %v", err)
	}
	if updatedETH.TrailingStopPrice == nil {
		t.Fatalf("ETH trailing stop should be set")
	}
	if math.Abs(*updatedETH.TrailingStopPrice-140.0) > 0.0001 {
		t.Errorf("ETH trailing stop = %v, want 140", *updatedETH.TrailingStopPrice)
	}
}

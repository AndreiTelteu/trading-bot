package services

import (
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/testutil"
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
	db := testutil.SetupPostgresDB(t)
	database.DB = db

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

	_, err := UpdatePositionsPrices()
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

func TestUpdatePositionsPricesCreatesSnapshotWithoutOpenPositions(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	database.DB = db

	database.DB.Create(&database.Wallet{Balance: 1234.5, Currency: "USDT"})

	result, err := UpdatePositionsPrices()
	if err != nil {
		t.Fatalf("UpdatePositionsPrices() error = %v", err)
	}

	resultMap, ok := result.(fiber.Map)
	if !ok {
		t.Fatalf("UpdatePositionsPrices() result type = %T, want fiber.Map", result)
	}
	if updated, ok := resultMap["updated"].(int); !ok || updated != 0 {
		t.Fatalf("UpdatePositionsPrices() updated = %#v, want 0", resultMap["updated"])
	}

	var snapshots []database.PortfolioSnapshot
	if err := database.DB.Order("timestamp asc").Find(&snapshots).Error; err != nil {
		t.Fatalf("Failed to load snapshots: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(snapshots))
	}
	if math.Abs(snapshots[0].TotalValue-1234.5) > 0.0001 {
		t.Fatalf("snapshot total_value = %v, want 1234.5", snapshots[0].TotalValue)
	}
	if snapshots[0].VolatilityAnnualized != nil {
		t.Fatalf("snapshot volatility should be nil when no open positions")
	}
	if snapshots[0].Timestamp.IsZero() {
		t.Fatalf("snapshot timestamp should be set")
	}
	var openPositions int64
	if err := database.DB.Model(&database.Position{}).Where("status = ?", "open").Count(&openPositions).Error; err != nil {
		t.Fatalf("Failed to count positions: %v", err)
	}
	if openPositions != 0 {
		t.Fatalf("open positions = %d, want 0", openPositions)
	}
}

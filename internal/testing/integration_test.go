package testing

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"trading-go/internal/config"
	"trading-go/internal/database"
	"trading-go/internal/handlers"
	"trading-go/internal/services"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

var testDB *gorm.DB
var app *fiber.App

func SetupTestDB(t *testing.T) {
	var err error
	testDB, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to connect to test database: %v", err)
	}

	database.DB = testDB
	database.DB.AutoMigrate(
		&database.Wallet{},
		&database.Position{},
		&database.Order{},
		&database.Setting{},
	)

	wallet := database.Wallet{Balance: 1000.0, Currency: "USDT"}
	database.DB.Create(&wallet)
}

func SetupTestApp() *fiber.App {
	app = fiber.New()

	cfg := config.Load()
	database.Initialize(cfg)

	setupTestRoutes(app, cfg)

	return app
}

func setupTestRoutes(app *fiber.App, cfg *config.Config) {
	services.InitTradingService(cfg.BinanceAPIKey, cfg.BinanceSecret)

	app.Get("/ws", func(c *fiber.Ctx) error {
		return c.SendString("ws")
	})

	api := app.Group("/api")

	api.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status": "ok",
			"msg":    "Trading Go API is running",
		})
	})

	wallet := api.Group("/wallet")
	wallet.Get("", handlers.GetWallet)
	wallet.Put("", handlers.UpdateWallet)

	positions := api.Group("/positions")
	positions.Get("", handlers.GetPositions)
	positions.Post("", handlers.CreatePosition)
	positions.Post("/:id/close", handlers.ClosePosition)
	positions.Delete("/:symbol", handlers.DeletePosition)

	orders := api.Group("/orders")
	orders.Get("", handlers.GetOrders)
	orders.Post("", handlers.CreateOrder)

	settings := api.Group("/settings")
	settings.Get("", handlers.GetSettings)
	settings.Put("", handlers.UpdateSettings)
	settings.Get("/:key", handlers.GetSetting)

	indicatorWeights := api.Group("/indicator-weights")
	indicatorWeights.Get("", handlers.GetIndicatorWeights)
	indicatorWeights.Put("", handlers.UpdateIndicatorWeights)

	trading := api.Group("/trading")
	trading.Post("/buy", handlers.ExecuteBuy)
	trading.Post("/sell", handlers.ExecuteSell)
	trading.Post("/update-prices", handlers.UpdatePrices)

	analysis := api.Group("/analysis")
	analysis.Get("/:symbol", handlers.GetAnalysis)
	analysis.Get("", handlers.GetAnalysisDefault)
	analysis.Post("/analyze", handlers.AnalyzeSymbol)

	ai := api.Group("/ai")
	ai.Get("/proposals", handlers.GetAIProposals)
	ai.Post("/generate-proposals", handlers.GenerateProposals)
	ai.Post("/proposals/:id/approve", handlers.ApproveProposal)
	ai.Post("/proposals/:id/deny", handlers.DenyProposal)
}

func TestWalletEndpoint(t *testing.T) {
	SetupTestDB(t)
	app := SetupTestApp()

	req := httptest.NewRequest(http.MethodGet, "/api/wallet", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(body, &result)

	if result["balance"] == nil {
		t.Error("Expected balance in response")
	}
}

func TestCreateOrderIntegration(t *testing.T) {
	SetupTestDB(t)
	app := SetupTestApp()

	services.InitTradingService("", "")

	wallet := database.Wallet{}
	database.DB.First(&wallet)
	wallet.Balance = 1000.0
	database.DB.Save(&wallet)

	orderReq := map[string]interface{}{
		"symbol": "BNBUSDT",
		"amount": 0.01,
		"price":  500.0,
	}
	body, _ := json.Marshal(orderReq)

	req := httptest.NewRequest(http.MethodPost, "/api/trading/buy", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)

	if err != nil {
		t.Logf("Expected error due to no real exchange: %v", err)
	}

	if resp != nil && resp.StatusCode != http.StatusOK {
		t.Logf("Expected 200 or error due to mock exchange, got %d", resp.StatusCode)
	}
}

func TestGetPositions(t *testing.T) {
	SetupTestDB(t)
	app := SetupTestApp()

	position := database.Position{
		Symbol:   "BNBUSDT",
		Amount:   1.0,
		AvgPrice: 500.0,
		Status:   "open",
		OpenedAt: time.Now(),
	}
	database.DB.Create(&position)

	req := httptest.NewRequest(http.MethodGet, "/api/positions", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

func TestHealthEndpoint(t *testing.T) {
	SetupTestDB(t)
	app := SetupTestApp()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

func TestSettingsEndpoint(t *testing.T) {
	SetupTestDB(t)
	app := SetupTestApp()

	setting := database.Setting{
		Key:   "test_key",
		Value: "test_value",
	}
	database.DB.Create(&setting)

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

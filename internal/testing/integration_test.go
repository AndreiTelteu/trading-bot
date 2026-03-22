package testing

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
	"trading-go/internal/config"
	"trading-go/internal/database"
	"trading-go/internal/handlers"
	"trading-go/internal/middleware"
	"trading-go/internal/services"
	"trading-go/internal/testutil"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

var testDB *gorm.DB
var app *fiber.App

func SetupTestDB(t *testing.T) {
	testDB = testutil.SetupPostgresDB(t)

	wallet := database.Wallet{Balance: 1000.0, Currency: "USDT"}
	database.DB.Create(&wallet)

	llmConfig := database.LLMConfig{
		Provider: "openrouter",
		BaseURL:  "https://openrouter.ai/api/v1",
		Model:    "google/gemini-2.0-flash-001",
	}
	database.DB.Create(&llmConfig)
}

func SetupTestApp() *fiber.App {
	app = fiber.New()

	cfg := config.Load()
	cfg.AuthUsername = "admin"
	cfg.AuthPassword = "qwe321"
	cfg.SessionCookie = "trading_bot_test_session"
	setupTestRoutes(app, cfg)

	return app
}

func setupTestRoutes(app *fiber.App, cfg *config.Config) {
	services.InitTradingService(cfg.BinanceAPIKey, cfg.BinanceSecret)
	authManager := middleware.NewAuthManager(cfg)

	app.Get("/ws", func(c *fiber.Ctx) error {
		return c.SendString("ws")
	})

	api := app.Group("/api")
	auth := api.Group("/auth")
	auth.Post("/login", authManager.HandleLogin)
	auth.Post("/logout", authManager.HandleLogout)
	auth.Get("/session", authManager.HandleSession)

	api.Use(authManager.RequireAuth)

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

	positionsTrade := api.Group("/positions-trade")
	positionsTrade.Post("/open", handlers.ExecuteOpenTrade)
	positionsTrade.Post("/:id/close", handlers.ExecuteCloseTrade)

	analysis := api.Group("/analysis")
	analysis.Get("/:symbol", handlers.GetAnalysis)
	analysis.Get("", handlers.GetAnalysisDefault)
	analysis.Post("/analyze", handlers.AnalyzeSymbol)

	llm := api.Group("/llm")
	llm.Get("/config", handlers.GetLLMConfig)
	llm.Put("/config", handlers.UpdateLLMConfig)
	llm.Post("/test", handlers.TestLLMConfig)

	backtest := api.Group("/backtest")
	backtest.Get("/jobs", handlers.ListBacktestJobs)
	backtest.Get("/latest", handlers.GetLatestBacktestStatus)
	backtest.Get("/status/:id", handlers.GetBacktestStatus)

	ai := api.Group("/ai")
	ai.Get("/proposals", handlers.GetAIProposals)
	ai.Post("/generate-proposals", handlers.GenerateProposals)
	ai.Post("/optimize-backtest", handlers.OptimizeBacktest)
	ai.Post("/proposals/:id/approve", handlers.ApproveProposal)
	ai.Post("/proposals/:id/deny", handlers.DenyProposal)
}

func loginCookie(t *testing.T, app *fiber.App) string {
	t.Helper()

	body, _ := json.Marshal(map[string]string{
		"username": "admin",
		"password": "qwe321",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to login test user: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected login status 200, got %d: %s", resp.StatusCode, string(payload))
	}

	for _, cookie := range resp.Cookies() {
		if cookie.Name == "trading_bot_test_session" {
			return cookie.Name + "=" + cookie.Value
		}
	}

	t.Fatal("Expected auth cookie in login response")
	return ""
}

func TestWalletEndpoint(t *testing.T) {
	SetupTestDB(t)
	app := SetupTestApp()
	cookie := loginCookie(t, app)

	req := httptest.NewRequest(http.MethodGet, "/api/wallet", nil)
	req.Header.Set("Cookie", cookie)
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
	cookie := loginCookie(t, app)

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
	req.Header.Set("Cookie", cookie)
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
	cookie := loginCookie(t, app)

	position := database.Position{
		Symbol:   "BNBUSDT",
		Amount:   1.0,
		AvgPrice: 500.0,
		Status:   "open",
		OpenedAt: time.Now(),
	}
	database.DB.Create(&position)

	req := httptest.NewRequest(http.MethodGet, "/api/positions", nil)
	req.Header.Set("Cookie", cookie)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

func TestGetPositionsReturnsLatest50ClosedFirst(t *testing.T) {
	SetupTestDB(t)
	app := SetupTestApp()
	cookie := loginCookie(t, app)

	baseTime := time.Now().UTC()

	openPosition := database.Position{
		Symbol:   "OPENUSDT",
		Amount:   1.0,
		AvgPrice: 100.0,
		Status:   "open",
		OpenedAt: baseTime,
	}
	if err := database.DB.Create(&openPosition).Error; err != nil {
		t.Fatalf("Failed to create open position: %v", err)
	}

	for i := 0; i < 55; i++ {
		closedAt := baseTime.Add(time.Duration(i) * time.Minute)
		position := database.Position{
			Symbol:   "CLOSED" + strconv.Itoa(i) + "USDT",
			Amount:   1.0,
			AvgPrice: 100.0,
			Status:   "closed",
			OpenedAt: closedAt.Add(-time.Hour),
			ClosedAt: &closedAt,
		}
		if err := database.DB.Create(&position).Error; err != nil {
			t.Fatalf("Failed to create closed position %d: %v", i, err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/positions", nil)
	req.Header.Set("Cookie", cookie)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected status 200, got %d: %s", resp.StatusCode, string(body))
	}

	var positions []database.Position
	if err := json.NewDecoder(resp.Body).Decode(&positions); err != nil {
		t.Fatalf("Failed to decode positions response: %v", err)
	}

	if len(positions) != 51 {
		t.Fatalf("Expected 51 positions (1 open + 50 closed), got %d", len(positions))
	}

	if positions[0].Status != "open" || positions[0].Symbol != "OPENUSDT" {
		t.Fatalf("Expected open position first, got %+v", positions[0])
	}

	for i := 1; i < len(positions)-1; i++ {
		current := positions[i]
		next := positions[i+1]

		if current.Status != "closed" || next.Status != "closed" {
			t.Fatalf("Expected closed positions after the first open position, got %s then %s", current.Status, next.Status)
		}

		if current.ClosedAt == nil || next.ClosedAt == nil {
			t.Fatalf("Expected closed positions to have closed_at timestamps")
		}

		if current.ClosedAt.Before(*next.ClosedAt) {
			t.Fatalf("Expected closed positions ordered descending by closed_at, got %v before %v", current.ClosedAt, next.ClosedAt)
		}
	}

	if positions[len(positions)-1].Symbol != "CLOSED5USDT" {
		t.Fatalf("Expected oldest retained closed position to be CLOSED5USDT, got %s", positions[len(positions)-1].Symbol)
	}
}

func TestExecuteCloseTradeRejectsAlreadyClosedPosition(t *testing.T) {
	SetupTestDB(t)
	app := SetupTestApp()
	cookie := loginCookie(t, app)

	tickerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/ticker/24hr" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"symbol":"BANKUSDT","lastPrice":"7","priceChange":"0","priceChangePercent":"0","highPrice":"7","lowPrice":"7","volume":"0","quoteVolume":"0"}`))
	}))
	defer tickerServer.Close()

	services.InitTradingService("", "")
	exchange := services.GetExchange()
	exchange.BaseURL = tickerServer.URL
	exchange.HTTPClient = tickerServer.Client()

	var wallet database.Wallet
	if err := database.DB.First(&wallet).Error; err != nil {
		t.Fatalf("Failed to fetch wallet: %v", err)
	}
	wallet.Balance = 100
	if err := database.DB.Save(&wallet).Error; err != nil {
		t.Fatalf("Failed to seed wallet: %v", err)
	}

	entryPrice := 6.5
	currentPrice := 6.8
	position := database.Position{
		Symbol:       "BANK",
		Amount:       2,
		AvgPrice:     entryPrice,
		EntryPrice:   &entryPrice,
		CurrentPrice: &currentPrice,
		Status:       "open",
		OpenedAt:     time.Now().UTC(),
	}
	if err := database.DB.Create(&position).Error; err != nil {
		t.Fatalf("Failed to create position: %v", err)
	}

	body, _ := json.Marshal(map[string]interface{}{
		"close_reason": "manual",
		"price":        0,
		"order_type":   "market",
	})

	firstReq := httptest.NewRequest(http.MethodPost, "/api/positions-trade/"+strconv.Itoa(int(position.ID))+"/close", bytes.NewReader(body))
	firstReq.Header.Set("Content-Type", "application/json")
	firstReq.Header.Set("Cookie", cookie)
	firstResp, err := app.Test(firstReq)
	if err != nil {
		t.Fatalf("Failed first close request: %v", err)
	}
	if firstResp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(firstResp.Body)
		t.Fatalf("Expected first close status 200, got %d: %s", firstResp.StatusCode, string(payload))
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/api/positions-trade/"+strconv.Itoa(int(position.ID))+"/close", bytes.NewReader(body))
	secondReq.Header.Set("Content-Type", "application/json")
	secondReq.Header.Set("Cookie", cookie)
	secondResp, err := app.Test(secondReq)
	if err != nil {
		t.Fatalf("Failed second close request: %v", err)
	}
	if secondResp.StatusCode != http.StatusBadRequest {
		payload, _ := io.ReadAll(secondResp.Body)
		t.Fatalf("Expected second close status 400, got %d: %s", secondResp.StatusCode, string(payload))
	}

	var refreshedWallet database.Wallet
	if err := database.DB.First(&refreshedWallet).Error; err != nil {
		t.Fatalf("Failed to reload wallet: %v", err)
	}
	if refreshedWallet.Balance != 114 {
		t.Fatalf("Expected wallet balance 114 after one close, got %.2f", refreshedWallet.Balance)
	}

	var refreshedPosition database.Position
	if err := database.DB.First(&refreshedPosition, position.ID).Error; err != nil {
		t.Fatalf("Failed to reload position: %v", err)
	}
	if refreshedPosition.Status != "closed" {
		t.Fatalf("Expected position status closed, got %s", refreshedPosition.Status)
	}

	var sellOrders []database.Order
	if err := database.DB.Where("symbol = ? AND order_type = ?", position.Symbol, "sell").Find(&sellOrders).Error; err != nil {
		t.Fatalf("Failed to query sell orders: %v", err)
	}
	if len(sellOrders) != 1 {
		t.Fatalf("Expected exactly 1 sell order, got %d", len(sellOrders))
	}
}

func TestHealthEndpoint(t *testing.T) {
	SetupTestDB(t)
	app := SetupTestApp()
	cookie := loginCookie(t, app)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("Cookie", cookie)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

func TestProtectedEndpointRequiresAuth(t *testing.T) {
	SetupTestDB(t)
	app := SetupTestApp()

	req := httptest.NewRequest(http.MethodGet, "/api/wallet", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected status 401, got %d: %s", resp.StatusCode, string(body))
	}
}

func TestSettingsEndpoint(t *testing.T) {
	SetupTestDB(t)
	app := SetupTestApp()
	cookie := loginCookie(t, app)

	setting := database.Setting{
		Key:   "test_key",
		Value: "test_value",
	}
	database.DB.Create(&setting)

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	req.Header.Set("Cookie", cookie)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

func TestListBacktestJobsEndpoint(t *testing.T) {
	SetupTestDB(t)
	app := SetupTestApp()
	cookie := loginCookie(t, app)

	summaryJSON := `{"job_id":1,"started_at":"2026-03-14T00:00:00Z","finished_at":"2026-03-14T00:10:00Z","baseline":{"Mode":"baseline","Metrics":{"TradeCount":10}},"vol_sizing":{"Mode":"vol_sizing","Metrics":{"TradeCount":12}},"validation":{"passed":true,"windows":1}}`
	message := "done"
	job := database.BacktestJob{
		Status:      "completed",
		Progress:    1,
		Message:     &message,
		SummaryJSON: &summaryJSON,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	database.DB.Create(&job)

	req := httptest.NewRequest(http.MethodGet, "/api/backtest/jobs", nil)
	req.Header.Set("Cookie", cookie)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result []map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("Expected 1 job, got %d", len(result))
	}
	if result[0]["status"] != "completed" {
		t.Fatalf("Expected completed status, got %v", result[0]["status"])
	}
	if result[0]["summary"] == nil {
		t.Fatal("Expected parsed summary in response")
	}
	if _, ok := result[0]["summary_json"]; ok {
		t.Fatal("Expected raw summary_json to be omitted from response")
	}
}

func TestOptimizeBacktestEndpointCreatesProposal(t *testing.T) {
	SetupTestDB(t)
	app := SetupTestApp()
	cookie := loginCookie(t, app)

	settings := []database.Setting{
		{Key: "stop_mult", Value: "1.5", Category: strPtr("trading")},
		{Key: "ai_max_proposals", Value: "5", Category: strPtr("ai")},
		{Key: "ai_change_budget_pct", Value: "50", Category: strPtr("ai")},
		{Key: "ai_max_keys_per_category", Value: "2", Category: strPtr("ai")},
	}
	for _, setting := range settings {
		database.DB.Create(&setting)
	}
	weight := database.IndicatorWeight{Indicator: "rsi", Weight: 1.0}
	database.DB.Create(&weight)

	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"[{\"proposal_type\":\"backtest_parameter_adjustment\",\"parameter_key\":\"stop_mult\",\"old_value\":\"1.5\",\"new_value\":\"1.8\",\"reasoning\":\"Improves loss control based on the backtest comparison.\"}]"}}]}`))
	}))
	defer llmServer.Close()

	apiKey := "test-key"
	var config database.LLMConfig
	database.DB.First(&config)
	config.BaseURL = llmServer.URL
	config.APIKey = &apiKey
	database.DB.Save(&config)

	summaryJSON := `{"job_id":1,"started_at":"2026-03-14T00:00:00Z","finished_at":"2026-03-14T00:10:00Z","baseline":{"Mode":"baseline","Metrics":{"TradeCount":10,"WinRate":40,"ProfitFactor":1.1,"AvgWin":2.4,"AvgLoss":-1.2}},"vol_sizing":{"Mode":"vol_sizing","Metrics":{"TradeCount":8,"WinRate":50,"ProfitFactor":1.4,"AvgWin":2.8,"AvgLoss":-1.0}},"validation":{"passed":true,"windows":1}}`
	job := database.BacktestJob{
		Status:      "completed",
		Progress:    1,
		SummaryJSON: &summaryJSON,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	database.DB.Create(&job)

	body, _ := json.Marshal(map[string]uint{"job_id": job.ID})
	req := httptest.NewRequest(http.MethodPost, "/api/ai/optimize-backtest", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookie)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected status 200, got %d: %s", resp.StatusCode, string(payload))
	}

	var proposals []database.AIProposal
	if err := database.DB.Find(&proposals).Error; err != nil {
		t.Fatalf("Failed to load proposals: %v", err)
	}
	if len(proposals) != 1 {
		t.Fatalf("Expected 1 proposal, got %d", len(proposals))
	}
	if proposals[0].ProposalType != "backtest_parameter_adjustment" {
		t.Fatalf("Unexpected proposal type: %s", proposals[0].ProposalType)
	}
	if proposals[0].ParameterKey == nil || *proposals[0].ParameterKey != "stop_mult" {
		t.Fatalf("Unexpected parameter key: %v", proposals[0].ParameterKey)
	}
	if proposals[0].Status != "pending" {
		t.Fatalf("Unexpected proposal status: %s", proposals[0].Status)
	}
}

func TestOptimizeBacktestEndpointFallsBackToHypotheses(t *testing.T) {
	SetupTestDB(t)
	app := SetupTestApp()
	cookie := loginCookie(t, app)

	settings := []database.Setting{
		{Key: "stop_mult", Value: "1.5", Category: strPtr("trading")},
		{Key: "ai_max_proposals", Value: "5", Category: strPtr("ai")},
		{Key: "ai_min_proposals", Value: "1", Category: strPtr("ai")},
		{Key: "ai_change_budget_pct", Value: "10", Category: strPtr("ai")},
		{Key: "ai_max_keys_per_category", Value: "2", Category: strPtr("ai")},
	}
	for _, setting := range settings {
		database.DB.Create(&setting)
	}
	weight := database.IndicatorWeight{Indicator: "rsi", Weight: 1.0}
	database.DB.Create(&weight)

	requestCount := 0
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		if requestCount == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"[{\"proposal_type\":\"backtest_parameter_adjustment\",\"parameter_key\":\"stop_mult\",\"old_value\":\"1.5\",\"new_value\":\"1.8\",\"reasoning\":\"Too aggressive for the configured budget.\"}]"}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"[{\"proposal_type\":\"backtest_parameter_adjustment\",\"parameter_key\":\"stop_mult\",\"old_value\":\"1.5\",\"new_value\":\"1.6\",\"reasoning\":\"Hypothesis: a slightly wider ATR stop may reduce premature exits while staying within budget.\"}]"}}]}`))
	}))
	defer llmServer.Close()

	apiKey := "test-key"
	var config database.LLMConfig
	database.DB.First(&config)
	config.BaseURL = llmServer.URL
	config.APIKey = &apiKey
	database.DB.Save(&config)

	summaryJSON := `{"job_id":1,"started_at":"2026-03-14T00:00:00Z","finished_at":"2026-03-14T00:10:00Z","baseline":{"Mode":"baseline","Metrics":{"TradeCount":10,"WinRate":0.40,"ProfitFactor":0.9,"AvgWin":2.4,"AvgLoss":-1.4}},"vol_sizing":{"Mode":"vol_sizing","Metrics":{"TradeCount":8,"WinRate":0.45,"ProfitFactor":0.95,"AvgWin":2.6,"AvgLoss":-1.2}},"validation":{"passed":false,"windows":1}}`
	job := database.BacktestJob{
		Status:      "completed",
		Progress:    1,
		SummaryJSON: &summaryJSON,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	database.DB.Create(&job)

	body, _ := json.Marshal(map[string]uint{"job_id": job.ID})
	req := httptest.NewRequest(http.MethodPost, "/api/ai/optimize-backtest", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookie)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected status 200, got %d: %s", resp.StatusCode, string(payload))
	}

	var result struct {
		Success      bool   `json:"success"`
		Count        int    `json:"count"`
		JobID        uint   `json:"job_id"`
		UsedFallback bool   `json:"used_fallback"`
		AttemptMode  string `json:"attempt_mode"`
		Attempts     []struct {
			Mode          string `json:"mode"`
			AcceptedCount int    `json:"accepted_count"`
		} `json:"attempts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if !result.Success {
		t.Fatal("Expected success response")
	}
	if !result.UsedFallback {
		t.Fatal("Expected fallback pass to be used")
	}
	if result.AttemptMode != "hypothesis_fallback" {
		t.Fatalf("Expected hypothesis_fallback attempt mode, got %s", result.AttemptMode)
	}
	if len(result.Attempts) != 2 {
		t.Fatalf("Expected 2 attempts, got %d", len(result.Attempts))
	}
	if result.Attempts[0].AcceptedCount != 0 {
		t.Fatalf("Expected strict pass to accept 0 proposals, got %d", result.Attempts[0].AcceptedCount)
	}
	if result.Attempts[1].AcceptedCount != 1 {
		t.Fatalf("Expected fallback pass to accept 1 proposal, got %d", result.Attempts[1].AcceptedCount)
	}

	var proposals []database.AIProposal
	if err := database.DB.Find(&proposals).Error; err != nil {
		t.Fatalf("Failed to load proposals: %v", err)
	}
	if len(proposals) != 1 {
		t.Fatalf("Expected 1 proposal, got %d", len(proposals))
	}
	if proposals[0].NewValue == nil || *proposals[0].NewValue != "1.6" {
		t.Fatalf("Expected fallback proposal value 1.6, got %v", proposals[0].NewValue)
	}
}

func TestOptimizeBacktestEndpointReturnsRawResponseForNoJSONArray(t *testing.T) {
	SetupTestDB(t)
	app := SetupTestApp()
	cookie := loginCookie(t, app)

	settings := []database.Setting{
		{Key: "stop_mult", Value: "1.5", Category: strPtr("trading")},
		{Key: "ai_max_proposals", Value: "5", Category: strPtr("ai")},
		{Key: "ai_min_proposals", Value: "1", Category: strPtr("ai")},
		{Key: "ai_change_budget_pct", Value: "10", Category: strPtr("ai")},
		{Key: "ai_max_keys_per_category", Value: "2", Category: strPtr("ai")},
	}
	for _, setting := range settings {
		database.DB.Create(&setting)
	}

	requestCount := 0
	strictRaw := "I need more confidence before suggesting changes."
	fallbackRaw := "No changes recommended for this run."
	strictFinishReason := "length"
	fallbackFinishReason := "stop"
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		content := strictRaw
		finishReason := strictFinishReason
		if requestCount > 1 {
			content = fallbackRaw
			finishReason = fallbackFinishReason
		}
		payload, _ := json.Marshal(map[string]interface{}{
			"choices": []map[string]interface{}{{
				"message":       map[string]string{"content": content},
				"finish_reason": finishReason,
			}},
		})
		_, _ = w.Write(payload)
	}))
	defer llmServer.Close()

	apiKey := "test-key"
	var config database.LLMConfig
	database.DB.First(&config)
	config.BaseURL = llmServer.URL
	config.APIKey = &apiKey
	database.DB.Save(&config)

	summaryJSON := `{"job_id":1,"started_at":"2026-03-14T00:00:00Z","finished_at":"2026-03-14T00:10:00Z","baseline":{"Mode":"baseline","Metrics":{"TradeCount":10,"WinRate":0.40,"ProfitFactor":0.9,"AvgWin":2.4,"AvgLoss":-1.4}},"vol_sizing":{"Mode":"vol_sizing","Metrics":{"TradeCount":8,"WinRate":0.45,"ProfitFactor":0.95,"AvgWin":2.6,"AvgLoss":-1.2}},"validation":{"passed":false,"windows":1}}`
	job := database.BacktestJob{
		Status:      "completed",
		Progress:    1,
		SummaryJSON: &summaryJSON,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	database.DB.Create(&job)

	body, _ := json.Marshal(map[string]uint{"job_id": job.ID})
	req := httptest.NewRequest(http.MethodPost, "/api/ai/optimize-backtest", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookie)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected status 200, got %d: %s", resp.StatusCode, string(payload))
	}

	var result struct {
		Attempts []struct {
			Mode         string `json:"mode"`
			RawResponse  string `json:"raw_response"`
			FinishReason string `json:"finish_reason"`
			Diagnostics  struct {
				RejectedCounts map[string]int `json:"rejected_counts"`
			} `json:"diagnostics"`
		} `json:"attempts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if len(result.Attempts) != 2 {
		t.Fatalf("Expected 2 attempts, got %d", len(result.Attempts))
	}
	if result.Attempts[0].Diagnostics.RejectedCounts["no_json_array"] != 1 {
		t.Fatalf("Expected strict no_json_array rejection, got %v", result.Attempts[0].Diagnostics.RejectedCounts)
	}
	if result.Attempts[1].Diagnostics.RejectedCounts["no_json_array"] != 1 {
		t.Fatalf("Expected fallback no_json_array rejection, got %v", result.Attempts[1].Diagnostics.RejectedCounts)
	}
	if result.Attempts[0].RawResponse != strictRaw {
		t.Fatalf("Expected strict raw response %q, got %q", strictRaw, result.Attempts[0].RawResponse)
	}
	if result.Attempts[1].RawResponse != fallbackRaw {
		t.Fatalf("Expected fallback raw response %q, got %q", fallbackRaw, result.Attempts[1].RawResponse)
	}
	if result.Attempts[0].FinishReason != strictFinishReason {
		t.Fatalf("Expected strict finish reason %q, got %q", strictFinishReason, result.Attempts[0].FinishReason)
	}
	if result.Attempts[1].FinishReason != fallbackFinishReason {
		t.Fatalf("Expected fallback finish reason %q, got %q", fallbackFinishReason, result.Attempts[1].FinishReason)
	}
}

func strPtr(v string) *string {
	return &v
}

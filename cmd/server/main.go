package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"trading-go/internal/config"
	"trading-go/internal/database"
	"trading-go/internal/handlers"
	"trading-go/internal/middleware"
	"trading-go/internal/services"
	ws "trading-go/internal/websocket"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
)

func main() {
	cfg := config.Load()

	if err := database.Initialize(cfg); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	app := fiber.New(fiber.Config{
		AppName:      "Trading Go",
		ErrorHandler: customErrorHandler,
	})

	middleware.SetupCORS(app)
	middleware.SetupLogger(app)

	frontendPath := filepath.Join(".", "frontend", "dist")
	if _, err := os.Stat(frontendPath); err == nil {
		app.Static("/", frontendPath)
		app.Get("*", func(c *fiber.Ctx) error {
			return c.SendFile(filepath.Join(frontendPath, "index.html"))
		})
	}

	setupRoutes(app, cfg)

	addr := fmt.Sprintf(":%s", cfg.ServerPort)
	log.Printf("Server starting on %s", addr)
	log.Fatal(app.Listen(addr))
}

func setupRoutes(app *fiber.App, cfg *config.Config) {
	hub := ws.NewHub()
	handlers.InitWebSocket(hub)

	app.Use("/ws", func(c *fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			c.Locals("allowed", true)
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})

	// Initialize trading service
	services.InitTradingService(cfg.BinanceAPIKey, cfg.BinanceSecret)

	app.Get("/ws", websocket.New(func(c *websocket.Conn) {
		handlers.HandleWebSocketConn(c, hub)
	}))

	api := app.Group("/api")

	api.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status": "ok",
			"msg":    "Trading Go API is running",
		})
	})

	api.Get("/config", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"server_port":      cfg.ServerPort,
			"default_balance":  cfg.DefaultBalance,
			"default_currency": cfg.DefaultCurrency,
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

	// Trending endpoints (for frontend compatibility)
	trending := api.Group("/trending")
	trending.Get("", handlers.GetAnalysisDefault)
	trending.Post("/analyze", handlers.AnalyzeSymbol)

	// Activity logs endpoints
	activity := api.Group("/activity-logs")
	activity.Get("", handlers.GetActivityLogs)
	activity.Post("", handlers.CreateActivityLog)

	// AI routes
	ai := api.Group("/ai")
	ai.Get("/proposals", handlers.GetAIProposals)
	ai.Post("/generate-proposals", handlers.GenerateProposals)
	ai.Post("/proposals/:id/approve", handlers.ApproveProposal)
	ai.Post("/proposals/:id/deny", handlers.DenyProposal)
}

func customErrorHandler(c *fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	if e, ok := err.(*fiber.Error); ok {
		code = e.Code
	}
	return c.Status(code).JSON(fiber.Map{
		"error": err.Error(),
	})
}

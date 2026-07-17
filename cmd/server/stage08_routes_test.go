package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"trading-go/internal/config"
	"trading-go/internal/middleware"

	"github.com/gofiber/fiber/v2"
)

func TestOperationalStatusRequiresAuthentication(t *testing.T) {
	app := fiber.New()
	cfg := &config.Config{AuthUsername: "operator", AuthPassword: "secret", SessionCookie: "test_session"}
	auth := middleware.NewAuthManager(cfg)
	setupRoutes(app, cfg, auth)
	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/operations/status", nil))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.StatusCode)
	}
}

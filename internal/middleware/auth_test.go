package middleware

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"trading-go/internal/config"

	"github.com/gofiber/fiber/v2"
)

func TestLoginSessionCookieIsAvailableToWebSocketRoute(t *testing.T) {
	authManager := NewAuthManager(&config.Config{
		AuthUsername:  "admin",
		AuthPassword:  "secret",
		SessionCookie: "trading_bot_session",
	})

	app := fiber.New()
	app.Post("/api/auth/login", authManager.HandleLogin)

	body, err := json.Marshal(map[string]string{
		"username": "admin",
		"password": "secret",
	})
	if err != nil {
		t.Fatalf("marshal login body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("login request failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	setCookie := resp.Header.Get("Set-Cookie")
	if !strings.Contains(strings.ToLower(setCookie), "path=/") {
		t.Fatalf("session cookie path must include Path=/ for websocket auth, got %q", setCookie)
	}
	if !strings.Contains(setCookie, "HttpOnly") {
		t.Fatalf("session cookie must be HttpOnly, got %q", setCookie)
	}
	if !strings.Contains(setCookie, "SameSite=Lax") {
		t.Fatalf("session cookie must be SameSite=Lax, got %q", setCookie)
	}
}

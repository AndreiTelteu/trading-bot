package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestStage01UnsafeCreationAndDeleteEndpointsAreFenced(t *testing.T) {
	app := fiber.New()
	app.Post("/positions", CreatePosition)
	app.Delete("/positions/:symbol", DeletePosition)
	app.Post("/orders", CreateOrder)
	for _, fixture := range []struct{ method, path string }{{http.MethodPost, "/positions"}, {http.MethodDelete, "/positions/BTC"}, {http.MethodPost, "/orders"}} {
		response, err := app.Test(httptest.NewRequest(fixture.method, fixture.path, nil))
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != fiber.StatusConflict {
			t.Fatalf("%s %s status=%d want 409", fixture.method, fixture.path, response.StatusCode)
		}
	}
}

func TestExchangeExecutionEndpointIsStablyFenced(t *testing.T) {
	app := fiber.New()
	app.Post("/buy", ExecuteBuy)
	body := bytes.NewBufferString(`{"symbol":"BTCUSDT","amount":1,"price":10}`)
	request := httptest.NewRequest(http.MethodPost, "/buy", body)
	request.Header.Set("Content-Type", "application/json")
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != fiber.StatusServiceUnavailable {
		t.Fatalf("status=%d", response.StatusCode)
	}
	var payload map[string]interface{}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["code"] != "exchange_execution_fenced" {
		t.Fatalf("payload=%v", payload)
	}
}

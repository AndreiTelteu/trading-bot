package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestDispatchBacktestExperimentsRejectsInvalidBaseBeforeLLM(t *testing.T) {
	app := fiber.New()
	app.Post("/dispatch", DispatchBacktestExperiments)
	payload := map[string]any{
		"hypothesis":       "bounded test",
		"strategy_id":      "trend_momentum_candidate",
		"strategy_version": "1.0.0",
		"count":            2,
		"base_parameters": map[string]string{
			"execution_intent": "backtest",
			"max_gross":        "0.75",
			"max_net":          "1",
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/dispatch", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d want=%d", response.StatusCode, http.StatusBadRequest)
	}
}

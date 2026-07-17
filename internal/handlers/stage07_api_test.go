package handlers

import (
	"bytes"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestStage07GenericSettingsAPICannotBypassGovernance(t *testing.T) {
	app := fiber.New()
	app.Put("/settings", UpdateSettings)
	for _, payload := range []string{`[{"key":"model_rollout_state","value":"full_live"}]`, `[{"key":"active_model_version","value":"forged"}]`, `[{"key":"model_rollback_target","value":"forged"}]`, `[{"key":"selection_policy_top_k","value":"999"}]`, `[{"key":"risk_per_trade","value":"99"}]`, `[{"key":"universe_top_k","value":"1"}]`, `[{"key":"paper_fee_bps","value":"0"}]`, `[{"key":"strategy_version","value":"forged"}]`} {
		request := httptest.NewRequest("PUT", "/settings", bytes.NewBufferString(payload))
		request.Header.Set("Content-Type", "application/json")
		response, err := app.Test(request)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != fiber.StatusConflict {
			t.Fatalf("payload %s status=%d", payload, response.StatusCode)
		}
	}
	app.Put("/weights", UpdateIndicatorWeights)
	request := httptest.NewRequest("PUT", "/weights", bytes.NewBufferString(`[{"indicator":"rsi","weight":99}]`))
	request.Header.Set("Content-Type", "application/json")
	response, err := app.Test(request)
	if err != nil || response.StatusCode != fiber.StatusConflict {
		t.Fatalf("indicator weight mutation status=%d err=%v", response.StatusCode, err)
	}
}

func TestStage07ApprovalAPIBindsAuthenticatedActor(t *testing.T) {
	app := fiber.New()
	app.Post("/approve", ApproveGovernanceTransition)
	request := httptest.NewRequest("POST", "/approve", bytes.NewBufferString(`{"idempotency_key":"x","approver":"forged"}`))
	request.Header.Set("Content-Type", "application/json")
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("status=%d", response.StatusCode)
	}
}

func TestStage07AuthenticatedResearcherCannotSpoofApproverRole(t *testing.T) {
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("authenticated_actor", "researcher")
		c.Locals("governance_capabilities", []string{"research"})
		return c.Next()
	})
	app.Post("/approve", ApproveGovernanceTransition)
	request := httptest.NewRequest("POST", "/approve", bytes.NewBufferString(`{"idempotency_key":"forged","approver":"admin","target_state":"full_live"}`))
	request.Header.Set("Content-Type", "application/json")
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("role spoof status=%d", response.StatusCode)
	}
}

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
	for _, payload := range []string{`[{"key":"model_rollout_state","value":"full_live"}]`, `[{"key":"active_model_version","value":"forged"}]`, `[{"key":"model_rollback_target","value":"forged"}]`} {
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

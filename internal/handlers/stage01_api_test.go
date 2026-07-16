package handlers

import (
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

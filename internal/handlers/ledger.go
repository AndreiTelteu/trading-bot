package handlers

import (
	"time"
	"trading-go/internal/database"
	ledgerpkg "trading-go/internal/ledger"

	"github.com/gofiber/fiber/v2"
)

func GetLedgerReconciliation(c *fiber.Ctx) error {
	asOf := time.Now().UTC()
	if value := c.Query("as_of"); value != "" {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "as_of must be RFC3339"})
		}
		asOf = parsed
	}
	report, err := ledgerpkg.New(database.DB).Reconcile(c.UserContext(), ledgerpkg.DefaultAccountID, asOf)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(report)
}

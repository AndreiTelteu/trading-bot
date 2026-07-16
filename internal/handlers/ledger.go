package handlers

import (
	"time"
	"trading-go/internal/database"
	ledgerpkg "trading-go/internal/ledger"

	"github.com/gofiber/fiber/v2"
)

func GetLedgerReconciliation(c *fiber.Ctx) error {
	if c.Query("as_of") != "" {
		return writeLedgerError(c, ledgerpkg.ErrHistoricalReconciliationUnsupported)
	}
	report, err := ledgerpkg.New(database.DB).Reconcile(c.UserContext(), ledgerpkg.DefaultAccountID, time.Time{})
	if err != nil {
		return writeLedgerError(c, err)
	}
	return c.JSON(report)
}

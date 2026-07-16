package handlers

import (
	"github.com/gofiber/fiber/v2"
	ledgerpkg "trading-go/internal/ledger"
)

func writeLedgerError(c *fiber.Ctx, err error) error {
	kind, code := ledgerpkg.ErrorDetails(err)
	status := fiber.StatusInternalServerError
	switch kind {
	case ledgerpkg.KindValidation:
		status = fiber.StatusBadRequest
	case ledgerpkg.KindConflict:
		status = fiber.StatusConflict
	case ledgerpkg.KindUnavailable:
		status = fiber.StatusServiceUnavailable
	case ledgerpkg.KindIndeterminate:
		status = fiber.StatusAccepted
	}
	return c.Status(status).JSON(fiber.Map{"error": err.Error(), "code": code, "kind": kind})
}

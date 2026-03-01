package handlers

import (
	"strings"
	"time"
	"trading-go/internal/database"
	ws "trading-go/internal/websocket"

	"github.com/gofiber/fiber/v2"
)

func GetWallet(c *fiber.Ctx) error {
	var wallet database.Wallet
	if err := database.DB.First(&wallet).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Wallet not found"})
	}
	return c.JSON(wallet)
}

func UpdateWallet(c *fiber.Ctx) error {
	var wallet database.Wallet
	if err := database.DB.First(&wallet).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Wallet not found"})
	}

	type UpdateWalletRequest struct {
		Balance  *float64 `json:"balance"`
		Currency *string  `json:"currency"`
	}

	var req UpdateWalletRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if req.Balance != nil {
		wallet.Balance = *req.Balance
	}
	if req.Currency != nil {
		wallet.Currency = *req.Currency
	}

	if err := database.DB.Save(&wallet).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to update wallet"})
	}

	if wsHub != nil {
		wsHub.BroadcastMsg(&ws.Message{
			Type:    "balance_update",
			Payload: wallet,
		})
	}

	return c.JSON(wallet)
}

func GetPortfolioSnapshots(c *fiber.Ctx) error {
	var snapshots []database.PortfolioSnapshot

	period := strings.ToLower(strings.TrimSpace(c.Query("period", "4h")))
	var duration time.Duration
	switch period {
	case "1h":
		duration = time.Hour
	case "4h":
		duration = 4 * time.Hour
	case "6h":
		duration = 6 * time.Hour
	case "12h":
		duration = 12 * time.Hour
	case "24h":
		duration = 24 * time.Hour
	case "3d", "3days", "3 days":
		duration = 72 * time.Hour
	default:
		duration = 4 * time.Hour
	}

	since := time.Now().Add(-duration)
	if err := database.DB.Where("timestamp >= ?", since).Order("timestamp asc").Find(&snapshots).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch snapshots"})
	}

	return c.JSON(snapshots)
}

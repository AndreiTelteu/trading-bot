package handlers

import (
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
	// Return the most recent 100 snapshots, could be configured later
	if err := database.DB.Order("timestamp desc").Limit(100).Find(&snapshots).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch snapshots"})
	}

	// Reverse the order so chronological is ascending
	for i, j := 0, len(snapshots)-1; i < j; i, j = i+1, j-1 {
		snapshots[i], snapshots[j] = snapshots[j], snapshots[i]
	}

	return c.JSON(snapshots)
}

package handlers

import (
	"trading-go/internal/database"

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

	return c.JSON(wallet)
}

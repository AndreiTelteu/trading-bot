package handlers

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"trading-go/internal/accounting"
	"trading-go/internal/database"
	ledgerpkg "trading-go/internal/ledger"
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
		Type           string          `json:"type"`
		Amount         json.RawMessage `json:"amount"`
		Reason         string          `json:"reason"`
		Actor          string          `json:"actor"`
		IdempotencyKey string          `json:"idempotency_key"`
		Balance        *float64        `json:"balance"`
		Currency       *string         `json:"currency"`
	}

	var req UpdateWalletRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if req.Balance != nil || req.Currency != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Absolute balance/currency edits are disabled; use a typed deposit, withdrawal, or administrative correction"})
	}
	amountText := strings.Trim(string(req.Amount), `"`)
	amount, err := accounting.Parse(amountText)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": fmt.Sprintf("Invalid exact amount: %v", err)})
	}
	result, err := ledgerpkg.New(database.DB).ApplyAdjustment(c.UserContext(), ledgerpkg.AdjustmentCommand{IdempotencyKey: req.IdempotencyKey, Type: req.Type, Amount: amount, Currency: wallet.Currency, Actor: req.Actor, Reason: req.Reason})
	if err != nil {
		status := fiber.StatusBadRequest
		if ledgerpkg.IsConflict(err) {
			status = fiber.StatusConflict
		}
		return c.Status(status).JSON(fiber.Map{"error": err.Error()})
	}
	wallet = result.Wallet

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

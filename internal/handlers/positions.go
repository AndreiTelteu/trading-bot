package handlers

import (
	"time"
	"trading-go/internal/database"

	"github.com/gofiber/fiber/v2"
)

func GetPositions(c *fiber.Ctx) error {
	var positions []database.Position
	if err := database.DB.Find(&positions).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch positions"})
	}
	if positions == nil {
		positions = []database.Position{}
	}
	return c.JSON(positions)
}

func CreatePosition(c *fiber.Ctx) error {
	type CreatePositionRequest struct {
		Symbol     string   `json:"symbol"`
		Amount     float64  `json:"amount"`
		AvgPrice   float64  `json:"avg_price"`
		EntryPrice *float64 `json:"entry_price"`
	}

	var req CreatePositionRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if req.Symbol == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Symbol is required"})
	}

	existing := database.Position{}
	if err := database.DB.Where("symbol = ? AND status = ?", req.Symbol, "open").First(&existing).Error; err == nil {
		return c.Status(400).JSON(fiber.Map{"error": "Position already exists for this symbol"})
	}

	position := database.Position{
		Symbol:     req.Symbol,
		Amount:     req.Amount,
		AvgPrice:   req.AvgPrice,
		EntryPrice: req.EntryPrice,
		Status:     "open",
		OpenedAt:   time.Now(),
	}

	if err := database.DB.Create(&position).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to create position"})
	}

	return c.Status(201).JSON(position)
}

func ClosePosition(c *fiber.Ctx) error {
	id := c.Params("id")

	type ClosePositionRequest struct {
		CloseReason *string `json:"close_reason"`
	}

	var req ClosePositionRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	var position database.Position
	if err := database.DB.First(&position, id).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Position not found"})
	}

	now := time.Now()
	position.Status = "closed"
	position.ClosedAt = &now
	if req.CloseReason != nil {
		position.CloseReason = req.CloseReason
	}

	if err := database.DB.Save(&position).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to close position"})
	}

	return c.JSON(position)
}

func DeletePosition(c *fiber.Ctx) error {
	symbol := c.Params("symbol")

	var position database.Position
	if err := database.DB.Where("symbol = ?", symbol).First(&position).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Position not found"})
	}

	if err := database.DB.Delete(&position).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to delete position"})
	}

	return c.JSON(fiber.Map{"message": "Position deleted successfully"})
}

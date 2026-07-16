package handlers

import (
	"trading-go/internal/database"

	"github.com/gofiber/fiber/v2"
)

func GetOrders(c *fiber.Ctx) error {
	var orders []database.Order
	if err := database.DB.Order("executed_at DESC").Find(&orders).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch orders"})
	}
	if orders == nil {
		orders = []database.Order{}
	}
	return c.JSON(orders)
}

func CreateOrder(c *fiber.Ctx) error {
	return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Direct order creation is disabled; orders are created atomically from execution results"})
}

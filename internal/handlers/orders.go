package handlers

import (
	"time"
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
	type CreateOrderRequest struct {
		OrderType    string  `json:"order_type"`
		Symbol       string  `json:"symbol"`
		AmountCrypto float64 `json:"amount_crypto"`
		AmountUsdt   float64 `json:"amount_usdt"`
		Price        float64 `json:"price"`
	}

	var req CreateOrderRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if req.OrderType == "" || req.Symbol == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Order type and symbol are required"})
	}

	order := database.Order{
		OrderType:    req.OrderType,
		Symbol:       req.Symbol,
		AmountCrypto: req.AmountCrypto,
		AmountUsdt:   req.AmountUsdt,
		Price:        req.Price,
		ExecutedAt:   time.Now(),
	}

	if err := database.DB.Create(&order).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to create order"})
	}

	return c.Status(201).JSON(order)
}

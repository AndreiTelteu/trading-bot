package handlers

import (
	"trading-go/internal/database"

	"github.com/gofiber/fiber/v2"
)

func GetOrders(c *fiber.Ctx) error {
	limit, err := pageLimit(c, 200, 1000)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	query := database.DB.Order("executed_at DESC,id DESC")
	if raw := c.Query("cursor"); raw != "" {
		var cursor timeIDCursor
		if err := decodeCursor(raw, &cursor); err != nil || cursor.Time.IsZero() || cursor.ID == 0 {
			return c.Status(400).JSON(fiber.Map{"error": "invalid cursor"})
		}
		query = query.Where("executed_at < ? OR (executed_at = ? AND id < ?)", cursor.Time.UTC(), cursor.Time.UTC(), cursor.ID)
	}
	var orders []database.Order
	if err := query.Limit(limit + 1).Find(&orders).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch orders"})
	}
	next := ""
	if len(orders) > limit {
		orders = orders[:limit]
		last := orders[len(orders)-1]
		next = encodeCursor(timeIDCursor{Time: last.ExecutedAt.UTC(), ID: last.ID})
	}
	advertiseNext(c, next)
	if orders == nil {
		orders = []database.Order{}
	}
	return c.JSON(orders)
}

func CreateOrder(c *fiber.Ctx) error {
	return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Direct order creation is disabled; orders are created atomically from execution results"})
}

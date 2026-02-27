package handlers

import (
	"strconv"
	"trading-go/internal/database"

	"github.com/gofiber/fiber/v2"
)

func GetActivityLogs(c *fiber.Ctx) error {
	limitStr := c.Query("limit", "50")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 50
	}

	logType := c.Query("log_type", "")

	var logs []database.ActivityLog
	query := database.DB.Order("timestamp DESC").Limit(limit)

	if logType != "" {
		query = query.Where("log_type = ?", logType)
	}

	if err := query.Find(&logs).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": "Failed to fetch activity logs",
		})
	}

	return c.JSON(logs)
}

func CreateActivityLog(c *fiber.Ctx) error {
	type CreateLogRequest struct {
		LogType string `json:"log_type"`
		Message string `json:"message"`
		Details string `json:"details"`
	}

	var req CreateLogRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	log := database.ActivityLog{
		LogType:   req.LogType,
		Message:   req.Message,
	}

	if req.Details != "" {
		log.Details = &req.Details
	}

	if err := database.DB.Create(&log).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{
			"error": "Failed to create activity log",
		})
	}

	return c.JSON(log)
}

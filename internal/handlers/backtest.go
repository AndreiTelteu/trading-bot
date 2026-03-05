package handlers

import (
	"strconv"
	"trading-go/internal/backtest"

	"github.com/gofiber/fiber/v2"
)

func StartBacktest(c *fiber.Ctx) error {
	job, err := backtest.StartBacktestJob()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to start backtest"})
	}
	return c.JSON(job)
}

func GetBacktestStatus(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid job id"})
	}
	job, err := backtest.GetBacktestJob(uint(id))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Backtest job not found"})
	}
	return c.JSON(job)
}

func GetLatestBacktestStatus(c *fiber.Ctx) error {
	job, err := backtest.GetLatestBacktestJob()
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Backtest job not found"})
	}
	return c.JSON(job)
}

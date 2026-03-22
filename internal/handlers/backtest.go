package handlers

import (
	"strconv"
	"trading-go/internal/backtest"

	"github.com/gofiber/fiber/v2"
)

func ListBacktestJobs(c *fiber.Ctx) error {
	jobs, err := backtest.ListBacktestJobResponses()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch backtest jobs"})
	}
	return c.JSON(jobs)
}

func StartBacktest(c *fiber.Ctx) error {
	job, err := backtest.StartBacktestJob()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to start backtest"})
	}
	response, err := backtest.BuildBacktestJobResponse(job)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to prepare backtest response"})
	}
	return c.JSON(response)
}

func GetBacktestStatus(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid job id"})
	}
	response, err := backtest.GetBacktestJobResponse(uint(id))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Backtest job not found"})
	}
	return c.JSON(response)
}

func GetLatestBacktestStatus(c *fiber.Ctx) error {
	response, err := backtest.GetLatestBacktestJobResponse()
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Backtest job not found"})
	}
	return c.JSON(response)
}

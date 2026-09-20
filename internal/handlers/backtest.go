package handlers

import (
	"strconv"
	"trading-go/internal/backtest"
	"trading-go/internal/cutover"

	"github.com/gofiber/fiber/v2"
)

func ListBacktestJobs(c *fiber.Ctx) error {
	limit, err := pageLimit(c, 200, 1000)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	var cursor timeIDCursor
	if raw := c.Query("cursor"); raw != "" {
		if err := decodeCursor(raw, &cursor); err != nil || cursor.Time.IsZero() || cursor.ID == 0 {
			return c.Status(400).JSON(fiber.Map{"error": "invalid cursor"})
		}
	}
	jobs, next, err := backtest.ListBacktestJobResponsePage(cursor.Time, cursor.ID, limit)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch backtest jobs"})
	}
	nextCursor := ""
	if next != nil {
		nextCursor = encodeCursor(timeIDCursor{Time: next.CreatedAt.UTC(), ID: next.ID})
	}
	advertiseNext(c, nextCursor)
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

func ListBacktestStrategies(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"schema_version": backtest.StrategyDescriptorSchemaVersion, "strategies": backtest.DefaultStrategyRegistry.List()})
}

func StartStage05Comparison(c *fiber.Ctx) error {
	if flags, active := cutover.Active(); active && flags.NewBacktest != "research" {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Stage 08 new backtest research mode is disabled"})
	}
	type requestBody struct {
		backtest.Stage05RunRequest
		Overrides map[string]string `json:"overrides,omitempty"`
	}
	var request requestBody
	if err := c.BodyParser(&request); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid Stage 05 comparison request"})
	}
	job, err := backtest.StartStage05ComparisonJob(request.Stage05RunRequest, request.Overrides)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	response, err := backtest.BuildBacktestJobResponse(job)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to prepare Stage 05 job response"})
	}
	return c.Status(fiber.StatusAccepted).JSON(response)
}

func StartStage05ComparisonBatch(c *fiber.Ctx) error {
	if flags, active := cutover.Active(); active && flags.NewBacktest != "research" {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Stage 08 new backtest research mode is disabled"})
	}
	type experiment struct {
		backtest.Stage05RunRequest
		Overrides map[string]string `json:"overrides,omitempty"`
	}
	var request struct {
		Experiments []experiment `json:"experiments"`
	}
	if err := c.BodyParser(&request); err != nil || len(request.Experiments) < 1 || len(request.Experiments) > 8 {
		return c.Status(400).JSON(fiber.Map{"error": "Between 1 and 8 experiments are required"})
	}
	// Validate the complete batch before creating the first job. This prevents
	// malformed AI drafts from producing a partially submitted experiment set.
	for _, item := range request.Experiments {
		parameters := make(map[string]string, len(item.Parameters)+2)
		for key, value := range item.Parameters {
			parameters[key] = value
		}
		if item.TargetGrossExposure != "" {
			parameters["target_gross"] = item.TargetGrossExposure
		}
		if item.FinalPolicy != "" {
			parameters["final_policy"] = item.FinalPolicy
		}
		if _, _, _, err := backtest.DefaultStrategyRegistry.ResolveExecutable(item.StrategyID, item.StrategyVersion, parameters); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": err.Error()})
		}
	}
	responses := make([]*backtest.BacktestJobResponse, 0, len(request.Experiments))
	for _, item := range request.Experiments {
		job, err := backtest.StartStage05ComparisonJob(item.Stage05RunRequest, item.Overrides)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Batch submission stopped: " + err.Error(), "accepted": responses})
		}
		response, err := backtest.BuildBacktestJobResponse(job)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Failed to prepare batch job response", "accepted": responses})
		}
		responses = append(responses, response)
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"jobs": responses, "count": len(responses), "concurrency_limit": stage05ConcurrencyForResponse()})
}

func stage05ConcurrencyForResponse() int {
	// The runtime limit is deliberately not controlled by the request. Expose a
	// conservative operator-facing value without leaking unrelated environment.
	return backtest.Stage05ConcurrencyLimit()
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

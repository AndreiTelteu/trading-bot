package handlers

import (
	"strconv"
	"trading-go/internal/database"

	"github.com/gofiber/fiber/v2"
)

// GetLatestUniverseSnapshot returns the most recent universe snapshot with all members.
// GET /api/universe/latest
func GetLatestUniverseSnapshot(c *fiber.Ctx) error {
	var snapshot database.UniverseSnapshot
	result := database.DB.Preload("Members").Order("snapshot_time DESC").First(&snapshot)
	if result.Error != nil {
		return c.Status(404).JSON(fiber.Map{"error": "No universe snapshot found"})
	}

	return c.JSON(snapshot)
}

// ListUniverseSnapshots returns the last 50 snapshots without members.
// GET /api/universe/snapshots
func ListUniverseSnapshots(c *fiber.Ctx) error {
	var snapshots []database.UniverseSnapshot
	result := database.DB.
		Select("id, snapshot_time, regime_state, breadth_ratio, eligible_count, candidate_count, ranked_count, shortlist_count, rebalance_interval, created_at, updated_at").
		Order("snapshot_time DESC").
		Limit(50).
		Find(&snapshots)
	if result.Error != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch universe snapshots"})
	}

	return c.JSON(snapshots)
}

// GetUniverseSnapshotDetail returns a specific snapshot by ID with all members.
// GET /api/universe/snapshots/:id
func GetUniverseSnapshotDetail(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid snapshot ID"})
	}

	var snapshot database.UniverseSnapshot
	result := database.DB.Preload("Members").First(&snapshot, id)
	if result.Error != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Universe snapshot not found"})
	}

	return c.JSON(snapshot)
}

// GetUniverseSymbols returns universe symbols with optional filtering.
// GET /api/universe/symbols
// Query params:
//   - eligible=true: only non-excluded, spot-tradable, TRADING status
//   - excluded=true: only excluded symbols with reasons
//   - default: return all
func GetUniverseSymbols(c *fiber.Ctx) error {
	var symbols []database.UniverseSymbol

	query := database.DB.Model(&database.UniverseSymbol{})

	if c.Query("eligible") == "true" {
		query = query.Where("is_excluded = ? AND spot_tradable = ? AND status = ?", false, true, "TRADING")
	} else if c.Query("excluded") == "true" {
		query = query.Where("is_excluded = ?", true)
	}

	result := query.Order("symbol ASC").Find(&symbols)
	if result.Error != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch universe symbols"})
	}

	return c.JSON(symbols)
}

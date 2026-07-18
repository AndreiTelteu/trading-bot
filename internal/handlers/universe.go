package handlers

import (
	"strconv"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/pointintime"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

// GetLatestUniverseSnapshot returns the most recent universe snapshot with all members.
// GET /api/universe/latest
func GetLatestUniverseSnapshot(c *fiber.Ctx) error {
	var snapshot database.UniverseSnapshot
	query := database.DB.Preload("Members", func(db *gorm.DB) *gorm.DB { return db.Order("symbol ASC,id ASC") }).Order("snapshot_time DESC,id DESC")
	if asOf, err := parseUniverseAsOf(c); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid as_of"})
	} else if asOf != nil {
		query = query.Where("snapshot_time <= ?", *asOf)
	}
	result := query.First(&snapshot)
	if result.Error != nil {
		return c.Status(404).JSON(fiber.Map{"error": "No universe snapshot found"})
	}

	return c.JSON(snapshot)
}

// ListUniverseSnapshots returns the last 50 snapshots without members.
// GET /api/universe/snapshots
func ListUniverseSnapshots(c *fiber.Ctx) error {
	var snapshots []database.UniverseSnapshot
	query := database.DB.
		Select("id, snapshot_time, policy_version, dataset_manifest_id, coverage_state, benchmark_asset_id, benchmark_symbol_id, regime_state, breadth_ratio, eligible_count, candidate_count, ranked_count, shortlist_count, rebalance_interval, created_at, updated_at").
		Order("snapshot_time DESC,id DESC").Limit(50)
	if asOf, err := parseUniverseAsOf(c); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid as_of"})
	} else if asOf != nil {
		query = query.Where("snapshot_time <= ?", *asOf)
	}
	result := query.Find(&snapshots)
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
	result := database.DB.Preload("Members", func(db *gorm.DB) *gorm.DB { return db.Order("symbol ASC,id ASC") }).First(&snapshot, id)
	if result.Error != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Universe snapshot not found"})
	}
	if asOf, err := parseUniverseAsOf(c); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid as_of"})
	} else if asOf != nil && snapshot.SnapshotTime.After(*asOf) {
		return c.Status(404).JSON(fiber.Map{"error": "Universe snapshot not effective as of requested time"})
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
	limit, limitErr := pageLimit(c, 250, 1000)
	if limitErr != nil {
		return c.Status(400).JSON(fiber.Map{"error": limitErr.Error()})
	}
	if asOf, err := parseUniverseAsOf(c); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid as_of"})
	} else if asOf != nil {
		manifestID := c.Query("manifest_id")
		if manifestID == "" {
			return c.Status(400).JSON(fiber.Map{"error": "manifest_id is required with as_of"})
		}
		cursor := pointintime.SymbolCursor{}
		if raw := c.Query("cursor"); raw != "" {
			if err := decodeCursor(raw, &cursor); err != nil || cursor.AssetID == "" || cursor.ID == "" {
				return c.Status(400).JSON(fiber.Map{"error": "invalid cursor"})
			}
		}
		symbols, next, err := (pointintime.Repository{DB: database.DB}).SymbolsAsOfPage(manifestID, *asOf, c.Query("eligible") == "true", cursor, limit)
		if err != nil {
			return c.Status(422).JSON(fiber.Map{"error": err.Error()})
		}
		nextCursor := ""
		if next != nil {
			nextCursor = encodeCursor(*next)
		}
		advertiseNext(c, nextCursor)
		return c.JSON(symbols)
	}
	var symbols []database.UniverseSymbol

	query := database.DB.Model(&database.UniverseSymbol{})

	if c.Query("eligible") == "true" {
		query = query.Where("is_excluded = ? AND spot_tradable = ? AND status = ?", false, true, "TRADING")
	} else if c.Query("excluded") == "true" {
		query = query.Where("is_excluded = ?", true)
	}

	if raw := c.Query("cursor"); raw != "" {
		var cursor stringIDCursor
		if err := decodeCursor(raw, &cursor); err != nil || cursor.Value == "" || cursor.ID == 0 {
			return c.Status(400).JSON(fiber.Map{"error": "invalid cursor"})
		}
		query = query.Where("symbol > ? OR (symbol = ? AND id > ?)", cursor.Value, cursor.Value, cursor.ID)
	}
	result := query.Order("symbol ASC,id ASC").Limit(limit + 1).Find(&symbols)
	if result.Error != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch universe symbols"})
	}

	next := ""
	if len(symbols) > limit {
		symbols = symbols[:limit]
		last := symbols[len(symbols)-1]
		next = encodeCursor(stringIDCursor{Value: last.Symbol, ID: last.ID})
	}
	advertiseNext(c, next)
	return c.JSON(symbols)
}

func parseUniverseAsOf(c *fiber.Ctx) (*time.Time, error) {
	value := c.Query("as_of")
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

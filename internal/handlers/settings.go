package handlers

import (
	"trading-go/internal/database"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func GetSettings(c *fiber.Ctx) error {
	var settings []database.Setting
	if err := database.DB.Find(&settings).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch settings"})
	}
	if settings == nil {
		settings = []database.Setting{}
	}
	return c.JSON(settings)
}

func UpdateSettings(c *fiber.Ctx) error {
	type UpdateSettingRequest struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}

	var requests []UpdateSettingRequest
	if err := c.BodyParser(&requests); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if err := database.DB.Transaction(func(tx *gorm.DB) error {
		for _, req := range requests {
			setting := database.Setting{Key: req.Key, Value: req.Value}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "key"}},
				DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
			}).Create(&setting).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to update settings"})
	}

	var settings []database.Setting
	database.DB.Find(&settings)
	return c.JSON(settings)
}

func GetSetting(c *fiber.Ctx) error {
	key := c.Params("key")

	var setting database.Setting
	if err := database.DB.First(&setting, "key = ?", key).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Setting not found"})
	}
	return c.JSON(setting)
}

func GetIndicatorWeights(c *fiber.Ctx) error {
	var weights []database.IndicatorWeight
	if err := database.DB.Find(&weights).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch indicator weights"})
	}
	if weights == nil {
		weights = []database.IndicatorWeight{}
	}
	return c.JSON(weights)
}

func UpdateIndicatorWeights(c *fiber.Ctx) error {
	type UpdateWeightRequest struct {
		Indicator string  `json:"indicator"`
		Weight    float64 `json:"weight"`
	}

	var requests []UpdateWeightRequest
	if err := c.BodyParser(&requests); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if err := database.DB.Transaction(func(tx *gorm.DB) error {
		for _, req := range requests {
			weight := database.IndicatorWeight{Indicator: req.Indicator, Weight: req.Weight}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "indicator"}},
				DoUpdates: clause.AssignmentColumns([]string{"weight"}),
			}).Create(&weight).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to update indicator weights"})
	}

	var weights []database.IndicatorWeight
	database.DB.Find(&weights)
	return c.JSON(weights)
}

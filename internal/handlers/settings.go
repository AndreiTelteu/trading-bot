package handlers

import (
	"strings"
	"trading-go/internal/database"
	"trading-go/internal/services"

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
	for _, req := range requests {
		if stage07GovernanceSetting(req.Key) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Stage 07 governance settings are immutable through the generic settings API; use a validated, human-approved governance transition"})
		}
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

func stage07GovernanceSetting(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "active_model_version", "model_rollout_state", "model_fallback_mode", "model_rollback_target", "model_experiment_id", "rollout_policy_version":
		return true
	default:
		return false
	}
}

func GetGovernanceOverview(c *fiber.Ctx) error {
	overview, err := services.GetGovernanceOverview()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch governance overview"})
	}
	return c.JSON(overview)
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

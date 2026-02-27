package handlers

import (
	"trading-go/internal/database"

	"github.com/gofiber/fiber/v2"
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
		var setting database.Setting
		if err := database.DB.First(&setting, "key = ?", req.Key).Error; err != nil {
			setting = database.Setting{Key: req.Key}
			if err := database.DB.Create(&setting).Error; err != nil {
				return c.Status(500).JSON(fiber.Map{"error": "Failed to create setting: " + req.Key})
			}
		}
		setting.Value = req.Value
		if err := database.DB.Save(&setting).Error; err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Failed to update setting: " + req.Key})
		}
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

	for _, req := range requests {
		var weight database.IndicatorWeight
		if err := database.DB.First(&weight, "indicator = ?", req.Indicator).Error; err != nil {
			weight = database.IndicatorWeight{Indicator: req.Indicator}
			if err := database.DB.Create(&weight).Error; err != nil {
				return c.Status(500).JSON(fiber.Map{"error": "Failed to create indicator weight: " + req.Indicator})
			}
		}
		weight.Weight = req.Weight
		if err := database.DB.Save(&weight).Error; err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Failed to update indicator weight: " + req.Indicator})
		}
	}

	var weights []database.IndicatorWeight
	database.DB.Find(&weights)
	return c.JSON(weights)
}

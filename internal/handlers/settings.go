package handlers

import (
	"fmt"
	"strings"
	"trading-go/internal/database"
	"trading-go/internal/operations"
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
		if err := validateGenericSettingMutation(req.Key, req.Value); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if authorityAffectingSetting(req.Key) {
			operations.RecordGovernanceBypass(fmt.Errorf("generic settings mutation attempted for %s", req.Key))
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "authority-affecting settings are immutable through the generic API; create a new research experiment"})
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

func authorityAffectingSetting(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return true
	}
	// This is an authenticated operational enable/kill switch, not a strategy,
	// risk, model, rollout, or live-promotion policy. Live exchange submission
	// remains independently fenced and governance-controlled.
	if key == "auto_trade_enabled" {
		return false
	}
	for _, prefix := range []string{"active_", "selection_", "model_", "universe_", "risk_", "portfolio_", "entry_", "rebuy_", "pyramid", "max_position", "max_trade", "max_order", "auto_trade", "buy_only", "min_confidence", "paper_", "backtest_", "exchange_", "execution_", "trading_engine", "stage08_", "stop_", "tp_", "trailing_", "time_stop", "position_", "strategy_", "indicator_", "regime_", "cash_", "turnover_", "fee_", "slippage_", "rollout_", "vol_", "atr_", "sell_", "allow_sell", "stream_exit"} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	if strings.HasPrefix(key, "ai_") {
		return false
	}
	return governanceDeploymentExists()
}

func validateGenericSettingMutation(key, value string) error {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return fmt.Errorf("setting key is required")
	}
	if key == "auto_trade_enabled" {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "true" && value != "false" {
			return fmt.Errorf("auto_trade_enabled must be true or false")
		}
	}
	return nil
}

func governanceDeploymentExists() bool {
	if database.DB == nil {
		return false
	}
	var count int64
	return database.DB.Model(&database.GovernanceDeployment{}).Limit(1).Count(&count).Error == nil && count > 0
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
	return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "indicator weights are authority-affecting; create a new research experiment"})
}

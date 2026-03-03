package handlers

import (
	"strconv"
	"strings"
	"trading-go/internal/database"
	"trading-go/internal/services"

	"github.com/gofiber/fiber/v2"
)

func GetAIProposals(c *fiber.Ctx) error {
	proposals, err := services.GetAllProposals()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch proposals: " + err.Error()})
	}
	return c.JSON(proposals)
}

func ApproveProposal(c *fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid proposal ID"})
	}

	result, err := services.ApproveProposal(uint(id))
	if err != nil {
		if fiberErr, ok := err.(*fiber.Error); ok {
			return c.Status(fiberErr.Code).JSON(fiber.Map{"error": fiberErr.Message})
		}
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(result)
}

func DenyProposal(c *fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid proposal ID"})
	}

	result, err := services.DenyProposal(uint(id))
	if err != nil {
		if fiberErr, ok := err.(*fiber.Error); ok {
			return c.Status(fiberErr.Code).JSON(fiber.Map{"error": fiberErr.Message})
		}
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(result)
}

func GenerateProposals(c *fiber.Ctx) error {
	result, err := services.GenerateProposals()
	if err != nil {
		if fiberErr, ok := err.(*fiber.Error); ok {
			return c.Status(fiberErr.Code).JSON(fiber.Map{"error": fiberErr.Message})
		}
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(result)
}

func GetLLMConfig(c *fiber.Ctx) error {
	var config database.LLMConfig
	if err := database.DB.First(&config).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "LLM config not found"})
	}
	return c.JSON(llmConfigResponse(config))
}

func UpdateLLMConfig(c *fiber.Ctx) error {
	type UpdateLLMConfigRequest struct {
		Provider string `json:"provider"`
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
		Model    string `json:"model"`
	}

	var req UpdateLLMConfigRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	var config database.LLMConfig
	if err := database.DB.First(&config).Error; err != nil {
		config = database.LLMConfig{}
	}

	config.Provider = req.Provider
	config.BaseURL = req.BaseURL
	config.Model = req.Model
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		config.APIKey = nil
	} else {
		config.APIKey = &apiKey
	}

	if config.ID == 0 {
		if err := database.DB.Create(&config).Error; err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Failed to save LLM config"})
		}
	} else {
		if err := database.DB.Save(&config).Error; err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Failed to save LLM config"})
		}
	}

	return c.JSON(llmConfigResponse(config))
}

func TestLLMConfig(c *fiber.Ctx) error {
	type LLMConfigRequest struct {
		Provider string `json:"provider"`
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
		Model    string `json:"model"`
	}

	var config database.LLMConfig
	if err := database.DB.First(&config).Error; err != nil {
		config = database.LLMConfig{}
	}

	if len(c.Body()) > 0 {
		var req LLMConfigRequest
		if err := c.BodyParser(&req); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
		}
		config.Provider = req.Provider
		config.BaseURL = req.BaseURL
		config.Model = req.Model
		apiKey := strings.TrimSpace(req.APIKey)
		if apiKey == "" {
			config.APIKey = nil
		} else {
			config.APIKey = &apiKey
		}
	}

	if strings.TrimSpace(config.Provider) == "" || strings.TrimSpace(config.BaseURL) == "" || strings.TrimSpace(config.Model) == "" {
		return c.Status(400).JSON(fiber.Map{"error": "LLM configuration incomplete"})
	}

	if config.APIKey == nil || strings.TrimSpace(*config.APIKey) == "" {
		return c.Status(400).JSON(fiber.Map{"error": "LLM API key not configured"})
	}

	return c.JSON(fiber.Map{"success": true, "message": "LLM configuration looks valid"})
}

func llmConfigResponse(config database.LLMConfig) fiber.Map {
	apiKey := ""
	if config.APIKey != nil {
		apiKey = *config.APIKey
	}
	return fiber.Map{
		"provider": config.Provider,
		"base_url": config.BaseURL,
		"api_key":  apiKey,
		"model":    config.Model,
	}
}

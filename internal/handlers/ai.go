package handlers

import (
	"encoding/json"
	"strconv"
	"strings"
	"trading-go/internal/backtest"
	"trading-go/internal/database"
	"trading-go/internal/services"

	"github.com/gofiber/fiber/v2"
)

func DispatchBacktestExperiments(c *fiber.Ctx) error {
	type requestBody struct {
		Hypothesis      string            `json:"hypothesis"`
		StrategyID      string            `json:"strategy_id"`
		StrategyVersion string            `json:"strategy_version"`
		Count           int               `json:"count"`
		BaseParameters  map[string]string `json:"base_parameters"`
	}
	var request requestBody
	if err := c.BodyParser(&request); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid AI experiment dispatch request"})
	}
	var descriptor *backtest.StrategyDescriptor
	for _, candidate := range backtest.DefaultStrategyRegistry.List() {
		if candidate.ID == request.StrategyID && candidate.Version == request.StrategyVersion && candidate.ResearchOnly {
			copy := candidate
			descriptor = &copy
			break
		}
	}
	if descriptor == nil {
		return c.Status(400).JSON(fiber.Map{"error": "Only a registered research strategy can be dispatched"})
	}
	specs := make([]services.ExperimentParameterSpec, 0, len(descriptor.Parameters))
	for _, spec := range descriptor.Parameters {
		specs = append(specs, services.ExperimentParameterSpec{Name: spec.Name, Type: spec.Type, Description: spec.Description, Default: spec.Default, Enum: spec.Enum, Minimum: spec.Minimum, Maximum: spec.Maximum})
	}
	drafts, err := services.GenerateExperimentDrafts(services.ExperimentDraftRequest{Hypothesis: strings.TrimSpace(request.Hypothesis), StrategyID: request.StrategyID, StrategyVersion: request.StrategyVersion, Count: request.Count, BaseParameters: request.BaseParameters, ParameterSpecs: specs})
	if err != nil {
		if fiberErr, ok := err.(*fiber.Error); ok {
			return c.Status(fiberErr.Code).JSON(fiber.Map{"error": fiberErr.Message})
		}
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	seen := map[string]bool{}
	baseSelected, _, _, baseErr := backtest.DefaultStrategyRegistry.ResolveExecutable(request.StrategyID, request.StrategyVersion, request.BaseParameters)
	if baseErr != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Base candidate is invalid: " + baseErr.Error()})
	}
	baseCanonical, _ := json.Marshal(baseSelected.Parameters)
	for i := range drafts {
		// AI output is advisory. Force the non-capital backtest intent and pass every draft
		// through the canonical registry before it can be shown or submitted.
		if drafts[i].Parameters == nil {
			drafts[i].Parameters = map[string]string{}
		}
		drafts[i].Name = strings.TrimSpace(drafts[i].Name)
		drafts[i].Hypothesis = strings.TrimSpace(drafts[i].Hypothesis)
		drafts[i].Rationale = strings.TrimSpace(drafts[i].Rationale)
		if drafts[i].Name == "" || drafts[i].Hypothesis == "" || drafts[i].Rationale == "" {
			return c.Status(422).JSON(fiber.Map{"error": "AI draft is missing its name, hypothesis, or rationale"})
		}
		drafts[i].Parameters["execution_intent"] = "backtest"
		selected, _, _, resolveErr := backtest.DefaultStrategyRegistry.ResolveExecutable(request.StrategyID, request.StrategyVersion, drafts[i].Parameters)
		if resolveErr != nil {
			return c.Status(422).JSON(fiber.Map{"error": "AI draft failed strategy validation: " + resolveErr.Error()})
		}
		drafts[i].Parameters = selected.Parameters
		canonical, _ := json.Marshal(selected.Parameters)
		key := string(canonical)
		if key == string(baseCanonical) {
			return c.Status(422).JSON(fiber.Map{"error": "AI returned an experiment identical to the base candidate"})
		}
		if seen[key] {
			return c.Status(422).JSON(fiber.Map{"error": "AI returned duplicate experiment configurations"})
		}
		seen[key] = true
	}
	return c.JSON(fiber.Map{"schema_version": "ai-experiment-dispatch-v1", "experiments": drafts, "count": len(drafts), "advisory_only": true})
}

func GetAIProposals(c *fiber.Ctx) error {
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
	proposals, next, err := services.GetProposalPage(cursor.Time, cursor.ID, limit)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch proposals: " + err.Error()})
	}
	nextCursor := ""
	if next != nil {
		nextCursor = encodeCursor(timeIDCursor{Time: next.CreatedAt.UTC(), ID: next.ID})
	}
	advertiseNext(c, nextCursor)
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

func OptimizeBacktest(c *fiber.Ctx) error {
	type OptimizeBacktestRequest struct {
		JobID uint `json:"job_id"`
	}

	var req OptimizeBacktestRequest
	if err := c.BodyParser(&req); err != nil || req.JobID == 0 {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	result, err := services.GenerateBacktestOptimizationProposals(services.BacktestOptimizationInput{
		JobID: req.JobID,
	})
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
	if apiKey != "" {
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
		if apiKey != "" {
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
	return fiber.Map{
		"provider":           config.Provider,
		"base_url":           config.BaseURL,
		"api_key":            "",
		"api_key_configured": config.APIKey != nil && strings.TrimSpace(*config.APIKey) != "",
		"model":              config.Model,
	}
}

package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"trading-go/internal/database"

	"github.com/gofiber/fiber/v2"
)

func GenerateProposals() (interface{}, error) {
	var llmConfig database.LLMConfig
	if err := database.DB.First(&llmConfig).Error; err != nil {
		return nil, fiber.NewError(500, "LLM config not found")
	}

	if llmConfig.APIKey == nil || *llmConfig.APIKey == "" {
		return nil, fiber.NewError(400, "LLM API key not configured")
	}

	analysisData, err := getRecentAnalysisData()
	if err != nil {
		return nil, fiber.NewError(500, "Failed to get analysis data: "+err.Error())
	}

	wallet, err := getWalletData()
	if err != nil {
		return nil, fiber.NewError(500, "Failed to get wallet data: "+err.Error())
	}

	positions, err := getOpenPositions()
	if err != nil {
		return nil, fiber.NewError(500, "Failed to get positions: "+err.Error())
	}

	settings, err := getSettingsMap()
	if err != nil {
		return nil, fiber.NewError(500, "Failed to get settings: "+err.Error())
	}

	prompt := buildProposalPrompt(analysisData, wallet, positions, settings)

	response, err := callLLM(&llmConfig, prompt)
	if err != nil {
		return nil, fiber.NewError(500, "Failed to call LLM: "+err.Error())
	}

	proposals, err := parseProposalsFromResponse(response)
	if err != nil {
		return nil, fiber.NewError(500, "Failed to parse proposals: "+err.Error())
	}

	if len(proposals) == 0 {
		return fiber.Map{
			"success":   true,
			"proposals": []database.AIProposal{},
			"message":   "No proposals generated based on current market conditions",
		}, nil
	}

	createdProposals := []database.AIProposal{}
	for _, proposal := range proposals {
		proposal.Status = "pending"
		proposal.CreatedAt = time.Now()
		if err := database.DB.Create(&proposal).Error; err != nil {
			continue
		}
		createdProposals = append(createdProposals, proposal)
	}

	return fiber.Map{
		"success":   true,
		"proposals": createdProposals,
		"count":     len(createdProposals),
	}, nil
}

type AnalysisSummary struct {
	Symbol       string  `json:"symbol"`
	Signal       string  `json:"signal"`
	Rating       float64 `json:"rating"`
	CurrentPrice float64 `json:"current_price"`
	Change24h    float64 `json:"change_24h"`
}

func getRecentAnalysisData() ([]AnalysisSummary, error) {
	var history []database.TrendAnalysisHistory
	if err := database.DB.Order("analyzed_at DESC").Limit(20).Find(&history).Error; err != nil {
		return nil, err
	}

	symbolMap := make(map[string]AnalysisSummary)
	for _, h := range history {
		if _, exists := symbolMap[h.Symbol]; !exists {
			var summary AnalysisSummary
			summary.Symbol = h.Symbol
			if h.FinalSignal != nil {
				summary.Signal = *h.FinalSignal
			}
			if h.FinalRating != nil {
				summary.Rating = *h.FinalRating
			}
			if h.CurrentPrice != nil {
				summary.CurrentPrice = *h.CurrentPrice
			}
			if h.Change24h != nil {
				summary.Change24h = *h.Change24h
			}
			symbolMap[h.Symbol] = summary
		}
	}

	result := make([]AnalysisSummary, 0, len(symbolMap))
	for _, v := range symbolMap {
		result = append(result, v)
	}
	return result, nil
}

func getWalletData() (map[string]interface{}, error) {
	var wallet database.Wallet
	if err := database.DB.First(&wallet).Error; err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"balance":  wallet.Balance,
		"currency": wallet.Currency,
	}, nil
}

func getOpenPositions() ([]map[string]interface{}, error) {
	var positions []database.Position
	if err := database.DB.Where("status = ?", "open").Find(&positions).Error; err != nil {
		return nil, err
	}

	result := make([]map[string]interface{}, len(positions))
	for i, pos := range positions {
		result[i] = map[string]interface{}{
			"symbol":        pos.Symbol,
			"amount":        pos.Amount,
			"avg_price":     pos.AvgPrice,
			"current_price": pos.CurrentPrice,
			"pnl":           pos.Pnl,
			"pnl_percent":   pos.PnlPercent,
		}
	}
	return result, nil
}

func getSettingsMap() (map[string]string, error) {
	var settings []database.Setting
	if err := database.DB.Find(&settings).Error; err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for _, s := range settings {
		result[s.Key] = s.Value
	}
	return result, nil
}

func buildProposalPrompt(analysisData []AnalysisSummary, wallet map[string]interface{}, positions []map[string]interface{}, settings map[string]string) string {
	analysisJSON, _ := json.Marshal(analysisData)
	positionsJSON, _ := json.Marshal(positions)

	var sb strings.Builder
	sb.WriteString("You are a trading AI assistant. Analyze the following data and suggest trading parameter adjustments or actions.\n\n")
	sb.WriteString("Current Market Analysis:\n")
	sb.WriteString(string(analysisJSON))
	sb.WriteString("\n\nWallet: ")
	sb.WriteString(fmt.Sprintf("Balance: %.2f %s", wallet["balance"].(float64), wallet["currency"].(string)))
	sb.WriteString("\n\nOpen Positions:\n")
	sb.WriteString(string(positionsJSON))
	sb.WriteString("\n\nCurrent Settings:\n")
	for k, v := range settings {
		sb.WriteString(fmt.Sprintf("%s: %s\n", k, v))
	}
	sb.WriteString("\n\nBased on this data, generate trading proposals. Each proposal should be one of these types:\n")
	sb.WriteString("- buy_signal: Suggest opening a new position\n")
	sb.WriteString("- sell_signal: Suggest closing a position\n")
	sb.WriteString("- parameter_adjustment: Suggest changing a trading parameter\n")
	sb.WriteString("- risk_management: Suggest risk management action\n\n")
	sb.WriteString("Return a JSON array of proposals with these fields:\n")
	sb.WriteString("- proposal_type: The type of proposal\n")
	sb.WriteString("- parameter_key: The setting key to change (for parameter_adjustment)\n")
	sb.WriteString("- old_value: Current value\n")
	sb.WriteString("- new_value: Proposed new value\n")
	sb.WriteString("- reasoning: Why this proposal makes sense\n\n")
	sb.WriteString("Example: [{\"proposal_type\":\"parameter_adjustment\",\"parameter_key\":\"rsi_period\",\"old_value\":\"14\",\"new_value\":\"20\",\"reasoning\":\"Longer period reduces noise\"}]\n\n")
	sb.WriteString("Respond with only valid JSON array, no other text:")

	return sb.String()
}

func callLLM(config *database.LLMConfig, prompt string) (string, error) {
	payload := map[string]interface{}{
		"model": config.Model,
		"messages": []map[string]interface{}{
			{"role": "user", "content": prompt},
		},
		"max_tokens": 2000,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", config.BaseURL+"/chat/completions", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+*config.APIKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	type LLMResponse struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	var llmResp LLMResponse
	if err := json.Unmarshal(body, &llmResp); err != nil {
		return "", err
	}

	if len(llmResp.Choices) == 0 {
		return "", fmt.Errorf("no response from LLM")
	}

	return llmResp.Choices[0].Message.Content, nil
}

func parseProposalsFromResponse(response string) ([]database.AIProposal, error) {
	response = strings.TrimSpace(response)

	start := -1
	end := -1
	for i := 0; i < len(response); i++ {
		if response[i] == '[' {
			start = i
			break
		}
	}
	for i := len(response) - 1; i >= 0; i-- {
		if response[i] == ']' {
			end = i
			break
		}
	}

	if start == -1 || end == -1 || start > end {
		return []database.AIProposal{}, nil
	}

	jsonStr := response[start : end+1]

	type RawProposal struct {
		ProposalType string  `json:"proposal_type"`
		ParameterKey *string `json:"parameter_key"`
		OldValue     *string `json:"old_value"`
		NewValue     *string `json:"new_value"`
		Reasoning    string  `json:"reasoning"`
	}

	var rawProposals []RawProposal
	if err := json.Unmarshal([]byte(jsonStr), &rawProposals); err != nil {
		return []database.AIProposal{}, nil
	}

	proposals := make([]database.AIProposal, len(rawProposals))
	for i, raw := range rawProposals {
		proposals[i] = database.AIProposal{
			ProposalType: raw.ProposalType,
			ParameterKey: raw.ParameterKey,
			OldValue:     raw.OldValue,
			NewValue:     raw.NewValue,
			Reasoning:    raw.Reasoning,
		}
	}

	return proposals, nil
}

func ApproveProposal(id uint) (interface{}, error) {
	var proposal database.AIProposal
	if err := database.DB.First(&proposal, id).Error; err != nil {
		return nil, fiber.NewError(404, "Proposal not found")
	}

	if proposal.Status != "pending" {
		return nil, fiber.NewError(400, "Proposal is not pending")
	}

	now := time.Now()
	proposal.Status = "approved"
	proposal.ResolvedAt = &now

	if err := database.DB.Save(&proposal).Error; err != nil {
		return nil, fiber.NewError(500, "Failed to approve proposal")
	}

	if proposal.ProposalType == "parameter_adjustment" && proposal.ParameterKey != nil && proposal.NewValue != nil {
		var setting database.Setting
		if err := database.DB.First(&setting, "key = ?", *proposal.ParameterKey).Error; err != nil {
			setting = database.Setting{Key: *proposal.ParameterKey}
			database.DB.Create(&setting)
		}
		setting.Value = *proposal.NewValue
		setting.UpdatedAt = time.Now()
		database.DB.Save(&setting)
	}

	return fiber.Map{
		"success":  true,
		"proposal": proposal,
		"message":  "Proposal approved and applied",
	}, nil
}

func DenyProposal(id uint) (interface{}, error) {
	var proposal database.AIProposal
	if err := database.DB.First(&proposal, id).Error; err != nil {
		return nil, fiber.NewError(404, "Proposal not found")
	}

	if proposal.Status != "pending" {
		return nil, fiber.NewError(400, "Proposal is not pending")
	}

	now := time.Now()
	proposal.Status = "denied"
	proposal.ResolvedAt = &now

	if err := database.DB.Save(&proposal).Error; err != nil {
		return nil, fiber.NewError(500, "Failed to deny proposal")
	}

	return fiber.Map{
		"success":  true,
		"proposal": proposal,
		"message":  "Proposal denied",
	}, nil
}

func GetAllProposals() ([]database.AIProposal, error) {
	var proposals []database.AIProposal
	if err := database.DB.Order("created_at DESC").Find(&proposals).Error; err != nil {
		return nil, err
	}
	if proposals == nil {
		proposals = []database.AIProposal{}
	}
	return proposals, nil
}

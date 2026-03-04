package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
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

	settings, err := getSettingsByCategory([]string{"trading", "indicators", "probabilistic", "ai"})
	if err != nil {
		return nil, fiber.NewError(500, "Failed to get settings: "+err.Error())
	}

	weights := GetIndicatorWeights()
	allowedKeys := buildAllowedParameterKeys(settings, weights)
	prompt := buildProposalPrompt(analysisData, wallet, positions, settings, weights)

	response, err := callLLM(&llmConfig, prompt)
	if err != nil {
		return nil, fiber.NewError(500, "Failed to call LLM: "+err.Error())
	}

	proposals, err := parseProposalsFromResponse(response, settings, weights, allowedKeys)
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
	Symbol              string  `json:"symbol"`
	Signal              string  `json:"signal"`
	Rating              float64 `json:"rating"`
	CurrentPrice        float64 `json:"current_price"`
	Change24h           float64 `json:"change_24h"`
	SignalQualifies     *bool   `json:"signal_qualifies,omitempty"`
	ConfidenceQualifies *bool   `json:"confidence_qualifies,omitempty"`
	RegimeOk            *bool   `json:"regime_ok,omitempty"`
	VolOk               *bool   `json:"vol_ok,omitempty"`
	ProbOk              *bool   `json:"prob_ok,omitempty"`
	Decision            *string `json:"decision,omitempty"`
	DecisionReason      *string `json:"decision_reason,omitempty"`
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
			if h.SignalQualifies != nil {
				val := *h.SignalQualifies
				summary.SignalQualifies = &val
			}
			if h.ConfidenceQualifies != nil {
				val := *h.ConfidenceQualifies
				summary.ConfidenceQualifies = &val
			}
			if h.RegimeOk != nil {
				val := *h.RegimeOk
				summary.RegimeOk = &val
			}
			if h.VolOk != nil {
				val := *h.VolOk
				summary.VolOk = &val
			}
			if h.ProbOk != nil {
				val := *h.ProbOk
				summary.ProbOk = &val
			}
			if h.Decision != nil {
				val := *h.Decision
				summary.Decision = &val
			}
			if h.DecisionReason != nil {
				val := *h.DecisionReason
				summary.DecisionReason = &val
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

func getSettingsByCategory(categories []string) (map[string]string, error) {
	var settings []database.Setting
	if err := database.DB.Where("category IN ?", categories).Find(&settings).Error; err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for _, s := range settings {
		result[s.Key] = s.Value
	}
	return result, nil
}

func buildAllowedParameterKeys(settings map[string]string, weights map[string]float64) map[string]bool {
	allowed := make(map[string]bool)
	for key := range settings {
		allowed[key] = true
	}
	for indicator := range weights {
		allowed["weight:"+indicator] = true
	}
	return allowed
}

func buildProposalPrompt(analysisData []AnalysisSummary, wallet map[string]interface{}, positions []map[string]interface{}, settings map[string]string, weights map[string]float64) string {
	analysisJSON, _ := json.Marshal(analysisData)
	positionsJSON, _ := json.Marshal(positions)

	var sb strings.Builder
	sb.WriteString("You are a trading AI assistant. Analyze the data and suggest parameter adjustments only.\n\n")
	sb.WriteString("Current Market Analysis:\n")
	sb.WriteString(string(analysisJSON))
	sb.WriteString("\n\nWallet: ")
	sb.WriteString(fmt.Sprintf("Balance: %.2f %s", wallet["balance"].(float64), wallet["currency"].(string)))
	sb.WriteString("\n\nOpen Positions:\n")
	sb.WriteString(string(positionsJSON))
	sb.WriteString("\n\nHow the trading logic uses settings:\n")
	sb.WriteString("- Auto-trade runs only if auto_trade_enabled is true and max_positions is not exceeded.\n")
	sb.WriteString("- Signal gate: buy_only_strong=true requires STRONG_BUY; false allows BUY or STRONG_BUY.\n")
	sb.WriteString("- Confidence gate: if prob_model_enabled=true, the model gate overrides min_confidence_to_buy.\n")
	sb.WriteString("- Prob gate uses prob_model_beta0..beta6 with features (RSI, MACD hist, BB %B, momentum %, volume ratio, volatility ratio) to compute p_up via sigmoid, then EV = p_up*prob_avg_gain - (1-p_up)*prob_avg_loss; buy only if p_up>prob_p_min and EV>prob_ev_min.\n")
	sb.WriteString("- Regime gate: if regime_gate_enabled=true, requires EMA(fast) > EMA(slow) on regime_timeframe.\n")
	sb.WriteString("- Vol gate: ATR/price on 15m must be between vol_ratio_min and vol_ratio_max.\n")
	sb.WriteString("- Vol sizing: if vol_sizing_enabled=true, risk_per_trade and stop_mult define position size via ATR stop distance; max_position_value caps order size.\n")
	sb.WriteString("- Exit logic uses stop_loss_percent/take_profit_percent unless per-position ATR stop/tp were set by vol sizing.\n")
	sb.WriteString("- Trailing stop uses trailing_stop_enabled and trailing_stop_percent.\n")
	sb.WriteString("- Time stop uses time_stop_bars if > 0 and exits only when PnL <= 0.\n")
	sb.WriteString("\nImportant interactions and failure modes:\n")
	sb.WriteString("- If prob_model_enabled=true and betas are all 0, p_up defaults to 0.5 and may fail prob_p_min.\n")
	sb.WriteString("- Tight vol_ratio_min/max can block trades in low or high volatility regimes.\n")
	sb.WriteString("- High min_confidence_to_buy combined with buy_only_strong can suppress trades.\n")
	sb.WriteString("\n\nCurrent Settings (allowed keys):\n")
	for k, v := range settings {
		sb.WriteString(fmt.Sprintf("%s: %s\n", k, v))
	}
	sb.WriteString("\n\nCurrent Indicator Weights (allowed keys use prefix weight:):\n")
	for k, v := range weights {
		sb.WriteString(fmt.Sprintf("weight:%s: %g\n", k, v))
	}
	sb.WriteString("\n\nOnly return proposals of type parameter_adjustment. Each proposal must change exactly one allowed key.\n")
	sb.WriteString("Allowed keys are the keys listed above in Current Settings plus the weight:* keys listed above.\n")
	sb.WriteString("Do not return buy/sell/risk proposals. Do not return null values.\n\n")
	sb.WriteString("Return a JSON array of proposals with these fields:\n")
	sb.WriteString("- proposal_type: must be parameter_adjustment\n")
	sb.WriteString("- parameter_key: one allowed key\n")
	sb.WriteString("- old_value: current value for that key\n")
	sb.WriteString("- new_value: proposed value for that key\n")
	sb.WriteString("- reasoning: why this adjustment makes sense\n\n")
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

func parseProposalsFromResponse(response string, settings map[string]string, weights map[string]float64, allowedKeys map[string]bool) ([]database.AIProposal, error) {
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

	proposals := make([]database.AIProposal, 0, len(rawProposals))
	for _, raw := range rawProposals {
		if strings.TrimSpace(raw.ProposalType) != "parameter_adjustment" {
			continue
		}
		if raw.ParameterKey == nil || raw.NewValue == nil {
			continue
		}
		paramKey := strings.TrimSpace(*raw.ParameterKey)
		if paramKey == "" || !allowedKeys[paramKey] {
			continue
		}
		if strings.TrimSpace(*raw.NewValue) == "" {
			continue
		}
		oldValue := ""
		if strings.HasPrefix(paramKey, "weight:") {
			indicator := strings.TrimPrefix(paramKey, "weight:")
			if weight, ok := weights[indicator]; ok {
				oldValue = fmt.Sprintf("%g", weight)
			}
		} else if current, ok := settings[paramKey]; ok {
			oldValue = current
		}
		if oldValue == "" {
			continue
		}
		oldValueCopy := oldValue
		proposals = append(proposals, database.AIProposal{
			ProposalType: "parameter_adjustment",
			ParameterKey: &paramKey,
			OldValue:     &oldValueCopy,
			NewValue:     raw.NewValue,
			Reasoning:    raw.Reasoning,
		})
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
		paramKey := strings.TrimSpace(*proposal.ParameterKey)
		if strings.HasPrefix(paramKey, "weight:") {
			indicator := strings.TrimSpace(strings.TrimPrefix(paramKey, "weight:"))
			if indicator != "" {
				if weightValue, err := strconv.ParseFloat(strings.TrimSpace(*proposal.NewValue), 64); err == nil {
					var weight database.IndicatorWeight
					if err := database.DB.First(&weight, "indicator = ?", indicator).Error; err != nil {
						weight = database.IndicatorWeight{Indicator: indicator}
						database.DB.Create(&weight)
					}
					weight.Weight = weightValue
					database.DB.Save(&weight)
				}
			}
		} else {
			var setting database.Setting
			if err := database.DB.First(&setting, "key = ?", paramKey).Error; err != nil {
				setting = database.Setting{Key: paramKey}
				database.DB.Create(&setting)
			}
			setting.Value = *proposal.NewValue
			setting.UpdatedAt = time.Now()
			database.DB.Save(&setting)
		}
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

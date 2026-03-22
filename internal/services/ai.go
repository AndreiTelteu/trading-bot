package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"trading-go/internal/database"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

	settings, settingCategories, err := getSettingsByCategory([]string{"trading", "universe", "model", "backtest", "ai"})
	if err != nil {
		return nil, fiber.NewError(500, "Failed to get settings: "+err.Error())
	}

	weights := GetIndicatorWeights()
	lockedKeys := parseLockedKeys(settings)
	allowedKeys := buildAllowedParameterKeys(settings, weights, lockedKeys)

	gateLimit := getSettingIntFromMap(settings, "ai_gate_metrics_limit", 200)
	gateMetrics, _ := getGateMetrics(gateLimit)
	decisionLimit := getSettingIntFromMap(settings, "ai_recent_decisions_limit", 10)
	recentDecisions, _ := getRecentDecisionSummaries(decisionLimit)
	governanceOverview, _ := GetGovernanceOverview()

	prompt := buildProposalPrompt(analysisData, wallet, positions, settings, weights, gateMetrics, recentDecisions, lockedKeys, governanceOverview)

	llmResult, err := callLLM(&llmConfig, prompt)
	if err != nil {
		return nil, fiber.NewError(500, "Failed to call LLM: "+err.Error())
	}
	response := llmResult.Content

	proposals, err := parseProposalsFromResponse(response, settings, weights, allowedKeys, settingCategories)
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

type BacktestOptimizationInput struct {
	JobID       uint       `json:"job_id"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	SummaryJSON string     `json:"summary_json"`
}

type ProposalParseDiagnostics struct {
	FoundJSONArray   bool           `json:"found_json_array"`
	RawProposalCount int            `json:"raw_proposal_count"`
	AcceptedCount    int            `json:"accepted_count"`
	RejectedCounts   map[string]int `json:"rejected_counts,omitempty"`
}

type backtestProposalParseResult struct {
	Proposals   []database.AIProposal
	Diagnostics ProposalParseDiagnostics
}

type BacktestOptimizationAttempt struct {
	Mode          string                   `json:"mode"`
	AcceptedCount int                      `json:"accepted_count"`
	Diagnostics   ProposalParseDiagnostics `json:"diagnostics"`
	FinishReason  string                   `json:"finish_reason,omitempty"`
	Error         string                   `json:"error,omitempty"`
	RawResponse   string                   `json:"raw_response,omitempty"`
}

type LLMCallResult struct {
	Content      string
	FinishReason string
}

type BacktestOptimizationPromptOptions struct {
	Mode                string
	MinProposals        int
	PreviousResponse    string
	PreviousDiagnostics *ProposalParseDiagnostics
}

func GenerateBacktestOptimizationProposals(input BacktestOptimizationInput) (interface{}, error) {
	var llmConfig database.LLMConfig
	if err := database.DB.First(&llmConfig).Error; err != nil {
		return nil, fiber.NewError(500, "LLM config not found")
	}

	if llmConfig.APIKey == nil || *llmConfig.APIKey == "" {
		return nil, fiber.NewError(400, "LLM API key not configured")
	}

	if strings.TrimSpace(input.SummaryJSON) == "" {
		return nil, fiber.NewError(400, "Selected backtest has no stored summary")
	}

	settings, settingCategories, err := getSettingsByCategory([]string{"trading", "universe", "model", "backtest", "ai"})
	if err != nil {
		return nil, fiber.NewError(500, "Failed to get optimization settings: "+err.Error())
	}
	backtestSettings, _, err := getSettingsByCategory([]string{"backtest"})
	if err != nil {
		return nil, fiber.NewError(500, "Failed to get backtest settings: "+err.Error())
	}

	weights := GetIndicatorWeights()
	lockedKeys := parseLockedKeys(settings)
	allowedKeys := buildAllowedParameterKeys(settings, weights, lockedKeys)
	minProposals := maxInt(1, getSettingIntFromMap(settings, "ai_min_proposals", 1))
	maxProposals := getSettingIntFromMap(settings, "ai_max_proposals", 5)
	if maxProposals > 0 {
		minProposals = minInt(minProposals, maxProposals)
	}

	prompt, err := buildBacktestOptimizationPrompt(input, settings, backtestSettings, weights, allowedKeys, settingCategories, lockedKeys, BacktestOptimizationPromptOptions{Mode: "strict"})
	if err != nil {
		return nil, fiber.NewError(500, "Failed to build backtest optimization prompt: "+err.Error())
	}

	llmResult, err := callLLM(&llmConfig, prompt)
	if err != nil {
		return nil, fiber.NewError(500, "Failed to call LLM: "+err.Error())
	}
	response := llmResult.Content

	strictResult, err := parseProposalsFromResponseWithTypeDetailed(response, settings, weights, allowedKeys, settingCategories, "backtest_parameter_adjustment")
	if err != nil {
		return nil, fiber.NewError(500, "Failed to parse proposals: "+err.Error())
	}
	attempts := []BacktestOptimizationAttempt{{
		Mode:          "strict",
		AcceptedCount: len(strictResult.Proposals),
		Diagnostics:   strictResult.Diagnostics,
		FinishReason:  llmResult.FinishReason,
		RawResponse:   buildAttemptRawResponse(response, strictResult.Diagnostics),
	}}
	proposals := strictResult.Proposals
	attemptMode := "strict"
	usedFallback := false

	if len(proposals) == 0 {
		usedFallback = true
		fallbackPrompt, err := buildBacktestOptimizationPrompt(input, settings, backtestSettings, weights, allowedKeys, settingCategories, lockedKeys, BacktestOptimizationPromptOptions{
			Mode:                "hypothesis_fallback",
			MinProposals:        minProposals,
			PreviousResponse:    response,
			PreviousDiagnostics: &strictResult.Diagnostics,
		})
		if err != nil {
			return nil, fiber.NewError(500, "Failed to build fallback backtest optimization prompt: "+err.Error())
		}

		fallbackLLMResult, fallbackErr := callLLM(&llmConfig, fallbackPrompt)
		if fallbackErr != nil {
			attempts = append(attempts, BacktestOptimizationAttempt{Mode: "hypothesis_fallback", Error: fallbackErr.Error()})
			return fiber.Map{
				"success":       true,
				"proposals":     []database.AIProposal{},
				"count":         0,
				"job_id":        input.JobID,
				"message":       buildBacktestOptimizationNoProposalMessage(attempts),
				"attempts":      attempts,
				"attempt_mode":  "none",
				"used_fallback": usedFallback,
			}, nil
		}
		fallbackResponse := fallbackLLMResult.Content

		fallbackResult, err := parseProposalsFromResponseWithTypeDetailed(fallbackResponse, settings, weights, allowedKeys, settingCategories, "backtest_parameter_adjustment")
		if err != nil {
			return nil, fiber.NewError(500, "Failed to parse fallback proposals: "+err.Error())
		}

		attempts = append(attempts, BacktestOptimizationAttempt{
			Mode:          "hypothesis_fallback",
			AcceptedCount: len(fallbackResult.Proposals),
			Diagnostics:   fallbackResult.Diagnostics,
			FinishReason:  fallbackLLMResult.FinishReason,
			RawResponse:   buildAttemptRawResponse(fallbackResponse, fallbackResult.Diagnostics),
		})
		proposals = fallbackResult.Proposals
		if len(proposals) > 0 {
			attemptMode = "hypothesis_fallback"
		}
	}

	if len(proposals) == 0 {
		return fiber.Map{
			"success":       true,
			"proposals":     []database.AIProposal{},
			"count":         0,
			"job_id":        input.JobID,
			"message":       buildBacktestOptimizationNoProposalMessage(attempts),
			"attempts":      attempts,
			"attempt_mode":  "none",
			"used_fallback": usedFallback,
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

	logActivity("ai", "Backtest optimization proposals generated", fmt.Sprintf("Job %d created %d proposals via %s", input.JobID, len(createdProposals), attemptMode))

	return fiber.Map{
		"success":       true,
		"proposals":     createdProposals,
		"count":         len(createdProposals),
		"job_id":        input.JobID,
		"message":       buildBacktestOptimizationSuccessMessage(len(createdProposals), attemptMode),
		"attempts":      attempts,
		"attempt_mode":  attemptMode,
		"used_fallback": usedFallback,
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

type GateMetrics struct {
	Total                int            `json:"total"`
	SignalPassRate       float64        `json:"signal_pass_rate"`
	ConfidencePassRate   float64        `json:"confidence_pass_rate"`
	RegimePassRate       float64        `json:"regime_pass_rate"`
	VolPassRate          float64        `json:"vol_pass_rate"`
	ProbPassRate         float64        `json:"prob_pass_rate"`
	DecisionCounts       map[string]int `json:"decision_counts"`
	MostCommonSkipReason string         `json:"most_common_skip_reason"`
}

type DecisionSummary struct {
	Symbol   string  `json:"symbol"`
	Decision string  `json:"decision"`
	Reason   string  `json:"reason"`
	Signal   string  `json:"signal"`
	Rating   float64 `json:"rating"`
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

func getGateMetrics(limit int) (GateMetrics, error) {
	if limit <= 0 {
		limit = 200
	}

	var history []database.TrendAnalysisHistory
	if err := database.DB.Order("analyzed_at DESC").Limit(limit).Find(&history).Error; err != nil {
		return GateMetrics{}, err
	}

	total := len(history)
	if total == 0 {
		return GateMetrics{DecisionCounts: map[string]int{}}, nil
	}

	signalCount := 0
	confidenceCount := 0
	regimeCount := 0
	volCount := 0
	probCount := 0
	decisionCounts := make(map[string]int)
	skipReasonCounts := make(map[string]int)

	for _, row := range history {
		if row.SignalQualifies != nil && *row.SignalQualifies {
			signalCount++
		}
		if row.ConfidenceQualifies != nil && *row.ConfidenceQualifies {
			confidenceCount++
		}
		if row.RegimeOk != nil && *row.RegimeOk {
			regimeCount++
		}
		if row.VolOk != nil && *row.VolOk {
			volCount++
		}
		if row.ProbOk != nil && *row.ProbOk {
			probCount++
		}
		if row.Decision != nil {
			decisionCounts[*row.Decision]++
			if *row.Decision == "skip" && row.DecisionReason != nil && *row.DecisionReason != "" {
				skipReasonCounts[*row.DecisionReason]++
			}
		}
	}

	mostCommonSkip := ""
	maxSkip := 0
	for reason, count := range skipReasonCounts {
		if count > maxSkip {
			maxSkip = count
			mostCommonSkip = reason
		}
	}

	return GateMetrics{
		Total:                total,
		SignalPassRate:       (float64(signalCount) / float64(total)) * 100,
		ConfidencePassRate:   (float64(confidenceCount) / float64(total)) * 100,
		RegimePassRate:       (float64(regimeCount) / float64(total)) * 100,
		VolPassRate:          (float64(volCount) / float64(total)) * 100,
		ProbPassRate:         (float64(probCount) / float64(total)) * 100,
		DecisionCounts:       decisionCounts,
		MostCommonSkipReason: mostCommonSkip,
	}, nil
}

func getRecentDecisionSummaries(limit int) ([]DecisionSummary, error) {
	if limit <= 0 {
		limit = 10
	}
	var history []database.TrendAnalysisHistory
	if err := database.DB.Order("analyzed_at DESC").Limit(limit).Find(&history).Error; err != nil {
		return nil, err
	}
	result := make([]DecisionSummary, 0, len(history))
	for _, row := range history {
		signal := ""
		if row.FinalSignal != nil {
			signal = *row.FinalSignal
		}
		rating := 0.0
		if row.FinalRating != nil {
			rating = *row.FinalRating
		}
		decision := ""
		if row.Decision != nil {
			decision = *row.Decision
		}
		reason := ""
		if row.DecisionReason != nil {
			reason = *row.DecisionReason
		}
		result = append(result, DecisionSummary{
			Symbol:   row.Symbol,
			Decision: decision,
			Reason:   reason,
			Signal:   signal,
			Rating:   rating,
		})
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

func getSettingsByCategory(categories []string) (map[string]string, map[string]string, error) {
	var settings []database.Setting
	if err := database.DB.Where("category IN ?", categories).Find(&settings).Error; err != nil {
		return nil, nil, err
	}

	result := make(map[string]string)
	categoriesMap := make(map[string]string)
	for _, s := range settings {
		result[s.Key] = s.Value
		if s.Category != nil {
			categoriesMap[s.Key] = *s.Category
		}
	}
	return result, categoriesMap, nil
}

func parseLockedKeys(settings map[string]string) map[string]bool {
	locked := make(map[string]bool)
	raw, ok := settings["ai_locked_keys"]
	if !ok {
		return locked
	}
	parts := strings.Split(raw, ",")
	for _, p := range parts {
		key := strings.TrimSpace(p)
		if key != "" {
			locked[key] = true
		}
	}
	return locked
}

func buildAllowedParameterKeys(settings map[string]string, weights map[string]float64, lockedKeys map[string]bool) map[string]bool {
	allowed := make(map[string]bool)
	for key := range settings {
		if !lockedKeys[key] && isAIAdjustableSettingKey(key) {
			allowed[key] = true
		}
	}
	_ = weights
	return allowed
}

func isAIAdjustableSettingKey(key string) bool {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "prob_") || strings.HasPrefix(trimmed, "rsi_") || strings.HasPrefix(trimmed, "macd_") || strings.HasPrefix(trimmed, "bb_") || strings.HasPrefix(trimmed, "volume_ma_") || strings.HasPrefix(trimmed, "momentum_") {
		return false
	}
	switch trimmed {
	case "prob_model_enabled", "buy_only_strong", "min_confidence_to_buy", "backtest_symbols", "backtest_start", "backtest_end":
		return false
	default:
		return true
	}
}

func buildProposalPrompt(analysisData []AnalysisSummary, wallet map[string]interface{}, positions []map[string]interface{}, settings map[string]string, weights map[string]float64, gateMetrics GateMetrics, recentDecisions []DecisionSummary, lockedKeys map[string]bool, governanceOverview GovernanceOverview) string {
	analysisJSON, _ := json.Marshal(analysisData)
	positionsJSON, _ := json.Marshal(positions)
	gateMetricsJSON, _ := json.Marshal(gateMetrics)
	recentDecisionsJSON, _ := json.Marshal(recentDecisions)
	governanceJSON, _ := json.Marshal(governanceOverview)

	var sb strings.Builder
	sb.WriteString("You are a trading governance assistant. Analyze the data and suggest safe policy adjustments only.\n\n")
	sb.WriteString("Current Market Analysis:\n")
	sb.WriteString(string(analysisJSON))
	sb.WriteString("\n\nWallet: ")
	sb.WriteString(fmt.Sprintf("Balance: %.2f %s", wallet["balance"].(float64), wallet["currency"].(string)))
	sb.WriteString("\n\nOpen Positions:\n")
	sb.WriteString(string(positionsJSON))
	if goal := strings.TrimSpace(settings["ai_goal"]); goal != "" {
		sb.WriteString("\n\nDesired Outcome:\n")
		sb.WriteString(goal)
	}
	sb.WriteString("\n\nRecent Decisions:\n")
	sb.WriteString(string(recentDecisionsJSON))
	sb.WriteString("\n\nGovernance Overview:\n")
	sb.WriteString(string(governanceJSON))
	sb.WriteString("\n\nGate Pass Rates:\n")
	sb.WriteString(string(gateMetricsJSON))
	sb.WriteString("\n\nHow the trading logic uses settings:\n")
	sb.WriteString("- Auto-trade runs only if auto_trade_enabled is true and max_positions is not exceeded.\n")
	sb.WriteString("- The learned signal model loads one immutable artifact by active_model_version and computes calibrated probability plus expected value on each shortlisted candidate.\n")
	sb.WriteString("- selection_policy_top_k, selection_policy_min_prob, and selection_policy_min_ev define the ranked entry policy.\n")
	sb.WriteString("- model_rollout_state governs whether the model is research_only, shadow, paper, limited_live, full_live, or rollback.\n")
	sb.WriteString("- model_fallback_mode and model_rollback_target define how rollback behaves while preserving prediction logging.\n")
	sb.WriteString("- min_confidence_to_sell still uses the legacy 1 to 5 confidence scale for discretionary sell signals and must stay within that range.\n")
	sb.WriteString("- Regime gate: if regime_gate_enabled=true, requires EMA(fast) > EMA(slow) on regime_timeframe.\n")
	sb.WriteString("- Vol gate: ATR/price on 15m must be between vol_ratio_min and vol_ratio_max.\n")
	sb.WriteString("- Vol sizing: if vol_sizing_enabled=true, risk_per_trade and stop_mult define position size via ATR stop distance; max_position_value caps order size.\n")
	sb.WriteString("- Exit logic uses stop_loss_percent/take_profit_percent unless per-position ATR stop/tp were set by vol sizing.\n")
	sb.WriteString("- Trailing stop uses trailing_stop_enabled and trailing_stop_percent.\n")
	sb.WriteString("- Time stop uses time_stop_bars if > 0 and exits only when PnL <= 0.\n")
	sb.WriteString("\nImportant interactions and failure modes:\n")
	sb.WriteString("- Tight selection_policy_min_prob or selection_policy_min_ev can suppress all entries even with a healthy shortlist.\n")
	sb.WriteString("- Tight vol_ratio_min/max can block trades in low or high volatility regimes.\n")
	sb.WriteString("- Rollout changes must follow the latest validation and monitoring evidence, not one attractive backtest number.\n")
	sb.WriteString("- Prefer governance, validation, and risk-policy changes over legacy indicator tuning.\n")
	sb.WriteString("\nConstraints:\n")
	sb.WriteString(fmt.Sprintf("- Max proposals per run: %d\n", getSettingIntFromMap(settings, "ai_max_proposals", 5)))
	sb.WriteString(fmt.Sprintf("- Max numeric change per proposal: %.2f%%\n", getSettingFloatFromMap(settings, "ai_change_budget_pct", 10)))
	sb.WriteString(fmt.Sprintf("- Max keys per category: %d\n", getSettingIntFromMap(settings, "ai_max_keys_per_category", 2)))
	if len(lockedKeys) > 0 {
		lockedList := make([]string, 0, len(lockedKeys))
		for key := range lockedKeys {
			lockedList = append(lockedList, key)
		}
		sort.Strings(lockedList)
		sb.WriteString("\nLocked Keys (do not change):\n")
		sb.WriteString(strings.Join(lockedList, ", "))
		sb.WriteString("\n")
	}
	sb.WriteString("\n\nCurrent Settings (allowed keys):\n")
	for k, v := range settings {
		sb.WriteString(fmt.Sprintf("%s: %s\n", k, v))
	}
	sb.WriteString("\n\nCurrent Indicator Weights (legacy context only; do not optimize these):\n")
	for k, v := range weights {
		sb.WriteString(fmt.Sprintf("weight:%s: %g\n", k, v))
	}
	sb.WriteString("\n\nOnly return proposals of type parameter_adjustment. Each proposal must change exactly one allowed governance or policy key.\n")
	sb.WriteString("Allowed keys are only the adjustable settings listed above in Current Settings. Indicator weights, indicator periods, and retired probability-beta controls are context only.\n")
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

func buildBacktestOptimizationPrompt(input BacktestOptimizationInput, settings map[string]string, backtestSettings map[string]string, weights map[string]float64, allowedKeys map[string]bool, settingCategories map[string]string, lockedKeys map[string]bool, options BacktestOptimizationPromptOptions) (string, error) {
	var summary map[string]interface{}
	if err := json.Unmarshal([]byte(input.SummaryJSON), &summary); err != nil {
		return "", err
	}

	comparison := buildBacktestMetricComparison(summary)
	comparisonJSON, _ := json.Marshal(comparison)
	settingsJSON, _ := json.Marshal(settings)
	runSettings := getNestedMap(summary, "settings_snapshot")
	backtestSettingsJSON, _ := json.Marshal(backtestSettings)
	runSettingsJSON, _ := json.Marshal(runSettings)
	weightsJSON, _ := json.Marshal(weights)
	allowedKeysJSON, _ := json.Marshal(sortedAllowedParameterKeys(allowedKeys))
	allowedKeySummaryJSON, _ := json.Marshal(buildAllowedKeySummary(settings, weights, allowedKeys, settingCategories))
	compactSummaryJSON, err := buildCompactBacktestSummaryJSON(summary)
	if err != nil {
		return "", err
	}

	jobContext := map[string]interface{}{
		"job_id":      input.JobID,
		"status":      input.Status,
		"created_at":  input.CreatedAt,
		"started_at":  input.StartedAt,
		"finished_at": input.FinishedAt,
	}
	jobContextJSON, _ := json.Marshal(jobContext)

	var sb strings.Builder
	if options.Mode == "hypothesis_fallback" {
		sb.WriteString("You are a trading strategy optimization AI performing a repair pass after a previous response produced zero accepted proposals.\n\n")
	} else {
		sb.WriteString("You are a trading strategy optimization AI. Review the selected backtest and suggest parameter adjustments only.\n\n")
	}
	if goal := strings.TrimSpace(settings["ai_goal"]); goal != "" {
		sb.WriteString("Optimization Goal:\n")
		sb.WriteString(goal)
		sb.WriteString("\n\n")
	}
	sb.WriteString("Selected Backtest Job:\n")
	sb.WriteString(string(jobContextJSON))
	sb.WriteString("\n\nBacktest Comparison Summary:\n")
	sb.WriteString(string(comparisonJSON))
	sb.WriteString("\n\nCurrent Adjustable Settings:\n")
	sb.WriteString(string(settingsJSON))
	if len(runSettings) > 0 {
		sb.WriteString("\n\nStored Run Settings Snapshot:\n")
		sb.WriteString(string(runSettingsJSON))
	}
	sb.WriteString("\n\nCurrent Backtest Settings (context only, do not optimize these unless explicitly allowed elsewhere):\n")
	sb.WriteString(string(backtestSettingsJSON))
	sb.WriteString("\n\nCurrent Indicator Weights:\n")
	sb.WriteString(string(weightsJSON))
	sb.WriteString("\n\nAllowed parameter_key values (use one exactly as written):\n")
	sb.WriteString(string(allowedKeysJSON))
	sb.WriteString("\n\nAllowed parameter summary (current values and categories):\n")
	sb.WriteString(string(allowedKeySummaryJSON))
	sb.WriteString("\n\nHow to interpret the backtest:\n")
	sb.WriteString("- baseline represents the fixed-size baseline strategy.\n")
	sb.WriteString("- vol_sizing represents volatility-adjusted sizing and exits.\n")
	sb.WriteString("- When active_model_version is set, both strategies use the learned model ranking policy for entries while differing on sizing/exits.\n")
	sb.WriteString("- validation contains walk-forward confidence checks plus ranking, regime-slice, symbol-cohort, and promotion-readiness diagnostics.\n")
	sb.WriteString("- Use model_rollout_state, model_fallback_mode, and model_rollback_target only when the evidence clearly supports promotion or rollback.\n")
	sb.WriteString("- min_confidence_to_sell still uses a 1 to 5 scale and must never exceed 5.\n")
	sb.WriteString("- Optimize for stronger risk-adjusted performance, better profit factor, better win/loss profile, and lower drawdown when possible.\n")
	sb.WriteString("- Prefer settings that are already part of the governance surface and avoid inventing new parameters.\n")
	sb.WriteString("\nConstraints:\n")
	sb.WriteString(fmt.Sprintf("- Max proposals per run: %d\n", getSettingIntFromMap(settings, "ai_max_proposals", 5)))
	sb.WriteString(fmt.Sprintf("- Max numeric change per proposal: %.2f%%\n", getSettingFloatFromMap(settings, "ai_change_budget_pct", 10)))
	sb.WriteString(fmt.Sprintf("- Max keys per category: %d\n", getSettingIntFromMap(settings, "ai_max_keys_per_category", 2)))
	sb.WriteString("- Only propose changes to allowed setting keys or indicator weights.\n")
	sb.WriteString("- Do not propose backtest_* keys; they are context only.\n")
	sb.WriteString("- Prefer trading, universe, model selection, rollout, and validation adjustments over ai_* operational settings.\n")
	sb.WriteString("- Do not propose manual trades or portfolio actions.\n")
	sb.WriteString("- Use the backtest evidence to justify each change.\n")
	if options.Mode == "hypothesis_fallback" {
		minProposals := maxInt(1, options.MinProposals)
		maxProposals := getSettingIntFromMap(settings, "ai_max_proposals", 5)
		if maxProposals > 0 {
			minProposals = minInt(minProposals, maxProposals)
		} else {
			maxProposals = minProposals
		}
		sb.WriteString(fmt.Sprintf("- Return %d to %d best-effort hypotheses even if confidence is moderate.\n", minProposals, maxProposals))
		sb.WriteString("- If evidence is mixed, still propose the most plausible small adjustments and begin the reasoning with 'Hypothesis:'.\n")
		sb.WriteString("- Repair any previous validation failure by staying within the numeric budget and using only exact allowed parameter_key values.\n")
	} else {
		sb.WriteString("- If no grounded, budget-compliant adjustment is available, return an empty JSON array.\n")
	}
	if len(lockedKeys) > 0 {
		lockedList := make([]string, 0, len(lockedKeys))
		for key := range lockedKeys {
			lockedList = append(lockedList, key)
		}
		sort.Strings(lockedList)
		sb.WriteString("- Locked keys (never change): ")
		sb.WriteString(strings.Join(lockedList, ", "))
		sb.WriteString("\n")
	}
	if options.Mode == "hypothesis_fallback" {
		if options.PreviousDiagnostics != nil {
			sb.WriteString("- Previous validation diagnostics:\n")
			sb.WriteString(prettyJSON(options.PreviousDiagnostics))
			sb.WriteString("\n")
		}
		if previousResponse := strings.TrimSpace(options.PreviousResponse); previousResponse != "" {
			sb.WriteString("- Previous response to repair:\n")
			sb.WriteString(truncateString(previousResponse, 1200))
			sb.WriteString("\n")
		}
	}
	sb.WriteString("\nCompact Backtest Summary JSON:\n")
	sb.WriteString(compactSummaryJSON)
	sb.WriteString("\n\nReturn a JSON array only. Each item must contain:\n")
	sb.WriteString("- proposal_type: backtest_parameter_adjustment\n")
	sb.WriteString("- parameter_key: one allowed adjustable setting key\n")
	sb.WriteString("- old_value: current value\n")
	sb.WriteString("- new_value: proposed value\n")
	sb.WriteString("- reasoning: concise explanation grounded in the backtest evidence\n\n")
	sb.WriteString("Example: [{\"proposal_type\":\"backtest_parameter_adjustment\",\"parameter_key\":\"selection_policy_min_prob\",\"old_value\":\"0.53\",\"new_value\":\"0.55\",\"reasoning\":\"Hypothesis: the selected backtest shows weak lower-ranked trades, so a slightly higher probability floor may improve quality while staying inside the configured change budget.\"}]\n\n")
	sb.WriteString("Respond with only valid JSON array, no markdown or extra text:")

	return sb.String(), nil
}

func sortedAllowedParameterKeys(allowedKeys map[string]bool) []string {
	keys := make([]string, 0, len(allowedKeys))
	for key, allowed := range allowedKeys {
		if allowed {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func buildAllowedKeySummary(settings map[string]string, weights map[string]float64, allowedKeys map[string]bool, categories map[string]string) []map[string]string {
	keys := sortedAllowedParameterKeys(allowedKeys)
	summary := make([]map[string]string, 0, len(keys))
	for _, key := range keys {
		entry := map[string]string{"parameter_key": key}
		if strings.HasPrefix(key, "weight:") {
			indicator := strings.TrimPrefix(key, "weight:")
			entry["category"] = "weights"
			if current, ok := weights[indicator]; ok {
				entry["current_value"] = fmt.Sprintf("%g", current)
			}
		} else {
			if category, ok := categories[key]; ok && category != "" {
				entry["category"] = category
			}
			if current, ok := settings[key]; ok {
				entry["current_value"] = current
			}
		}
		summary = append(summary, entry)
	}
	return summary
}

func truncateString(value string, maxLen int) string {
	trimmed := strings.TrimSpace(value)
	if maxLen <= 0 || len(trimmed) <= maxLen {
		return trimmed
	}
	if maxLen <= 3 {
		return trimmed[:maxLen]
	}
	return trimmed[:maxLen-3] + "..."
}

func buildBacktestOptimizationSuccessMessage(count int, attemptMode string) string {
	if attemptMode == "hypothesis_fallback" {
		return fmt.Sprintf("Created %d backtest optimization proposal(s) using the best-effort fallback pass.", count)
	}
	return fmt.Sprintf("Created %d backtest optimization proposal(s) from the selected backtest.", count)
}

func buildBacktestOptimizationNoProposalMessage(attempts []BacktestOptimizationAttempt) string {
	message := "The AI responded, but no backtest optimization proposal passed validation."
	if len(attempts) > 1 {
		message = "The AI responded, but no backtest optimization proposal passed validation after strict and fallback passes."
	}
	if reasons := formatRejectedCounts(collectRejectedCounts(attempts)); reasons != "" {
		message += " Main filter reasons: " + reasons + "."
	}
	for _, attempt := range attempts {
		if strings.TrimSpace(attempt.Error) != "" {
			message += " Fallback error: " + attempt.Error
			break
		}
	}
	return message
}

func buildAttemptRawResponse(response string, diagnostics ProposalParseDiagnostics) string {
	if diagnostics.AcceptedCount != 0 {
		return ""
	}
	if diagnostics.RejectedCounts["no_json_array"] == 0 {
		return ""
	}
	return strings.TrimSpace(response)
}

func collectRejectedCounts(attempts []BacktestOptimizationAttempt) map[string]int {
	combined := make(map[string]int)
	for _, attempt := range attempts {
		for key, count := range attempt.Diagnostics.RejectedCounts {
			combined[key] += count
		}
	}
	if len(combined) == 0 {
		return nil
	}
	return combined
}

func formatRejectedCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return ""
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s (%d)", key, counts[key]))
	}
	return strings.Join(parts, ", ")
}

func buildBacktestMetricComparison(summary map[string]interface{}) map[string]interface{} {
	metrics := []string{"TradeCount", "WinRate", "ProfitFactor", "AvgWin", "AvgLoss", "Sharpe", "MaxDrawdown", "ReturnVolatility"}
	baselineMetrics := map[string]float64{}
	volMetrics := map[string]float64{}
	deltaMetrics := map[string]float64{}

	for _, metric := range metrics {
		baselineValue := getBacktestMetricValue(summary, "baseline", metric)
		volValue := getBacktestMetricValue(summary, "vol_sizing", metric)
		baselineMetrics[metric] = baselineValue
		volMetrics[metric] = volValue
		deltaMetrics[metric] = volValue - baselineValue
	}

	validation := buildValidationSummaryForLLM(getNestedMap(summary, "validation"))
	return map[string]interface{}{
		"baseline":   baselineMetrics,
		"vol_sizing": volMetrics,
		"delta":      deltaMetrics,
		"validation": validation,
	}
}

func buildCompactBacktestSummaryJSON(summary map[string]interface{}) (string, error) {
	compact := map[string]interface{}{
		"job_id":            summary["job_id"],
		"started_at":        summary["started_at"],
		"finished_at":       summary["finished_at"],
		"backtest_mode":     summary["backtest_mode"],
		"model_version":     summary["model_version"],
		"policy_version":    summary["policy_version"],
		"policy_context":    summary["policy_context"],
		"experiment_id":     summary["experiment_id"],
		"settings_snapshot": getNestedMap(summary, "settings_snapshot"),
		"baseline":          summarizeBacktestStrategyForLLM(getNestedMap(summary, "baseline")),
		"vol_sizing":        summarizeBacktestStrategyForLLM(getNestedMap(summary, "vol_sizing")),
		"validation":        buildValidationSummaryForLLM(getNestedMap(summary, "validation")),
	}

	payload, err := json.MarshalIndent(compact, "", "  ")
	if err != nil {
		return "", err
	}

	return string(payload), nil
}

func summarizeBacktestStrategyForLLM(strategy map[string]interface{}) map[string]interface{} {
	if strategy == nil {
		return nil
	}

	summary := map[string]interface{}{
		"mode":           valueFromMap(strategy, "Mode", "mode"),
		"model_version":  valueFromMap(strategy, "ModelVersion", "model_version"),
		"policy_version": valueFromMap(strategy, "PolicyVersion", "policy_version"),
		"rollout_state":  valueFromMap(strategy, "RolloutState", "rollout_state"),
		"metrics":        getNestedMap(strategy, "Metrics", "metrics"),
	}

	if rankingMetrics := valueFromMap(strategy, "RankingMetrics", "ranking_metrics"); rankingMetrics != nil {
		summary["ranking_metrics"] = rankingMetrics
	}
	if diagnostics := valueFromMap(strategy, "Diagnostics", "diagnostics"); diagnostics != nil {
		summary["diagnostics"] = diagnostics
	}

	if tradeSummary := buildTradeSummaryForLLM(strategy); tradeSummary != nil {
		summary["trade_summary"] = tradeSummary
	}
	if equitySummary := buildEquitySummaryForLLM(strategy); equitySummary != nil {
		summary["equity_summary"] = equitySummary
	}
	if symbolSummary := buildEquityBySymbolSummaryForLLM(strategy); symbolSummary != nil {
		summary["equity_by_symbol_summary"] = symbolSummary
	}

	return summary
}

func buildTradeSummaryForLLM(strategy map[string]interface{}) map[string]interface{} {
	trades := getArrayValue(strategy, "Trades", "trades")
	if len(trades) == 0 {
		return nil
	}

	type tradeSnapshot struct {
		Pnl  float64
		Data map[string]interface{}
	}

	reasonCounts := map[string]int{}
	tradeSnapshots := make([]tradeSnapshot, 0, len(trades))
	winCount := 0
	lossCount := 0
	breakevenCount := 0
	totalPnl := 0.0
	totalPnlPercent := 0.0

	for _, trade := range trades {
		tradeMap, ok := trade.(map[string]interface{})
		if !ok {
			continue
		}

		pnl := getFloatValue(tradeMap, "Pnl", "pnl")
		pnlPercent := getFloatValue(tradeMap, "PnlPercent", "pnl_percent")
		reason := strings.TrimSpace(stringValue(valueFromMap(tradeMap, "Reason", "reason")))
		if reason == "" {
			reason = "unknown"
		}

		totalPnl += pnl
		totalPnlPercent += pnlPercent
		reasonCounts[reason]++

		switch {
		case pnl > 0:
			winCount++
		case pnl < 0:
			lossCount++
		default:
			breakevenCount++
		}

		tradeSnapshots = append(tradeSnapshots, tradeSnapshot{
			Pnl: pnl,
			Data: map[string]interface{}{
				"symbol":      valueFromMap(tradeMap, "Symbol", "symbol"),
				"entry_time":  valueFromMap(tradeMap, "EntryTime", "entry_time"),
				"exit_time":   valueFromMap(tradeMap, "ExitTime", "exit_time"),
				"entry_price": valueFromMap(tradeMap, "EntryPrice", "entry_price"),
				"exit_price":  valueFromMap(tradeMap, "ExitPrice", "exit_price"),
				"pnl":         pnl,
				"pnl_percent": pnlPercent,
				"reason":      reason,
			},
		})
	}

	if len(tradeSnapshots) == 0 {
		return nil
	}

	sort.Slice(tradeSnapshots, func(i, j int) bool {
		return tradeSnapshots[i].Pnl > tradeSnapshots[j].Pnl
	})

	bestTrades := make([]map[string]interface{}, 0, minInt(3, len(tradeSnapshots)))
	for _, trade := range tradeSnapshots[:minInt(3, len(tradeSnapshots))] {
		bestTrades = append(bestTrades, trade.Data)
	}

	worstTrades := make([]map[string]interface{}, 0, minInt(3, len(tradeSnapshots)))
	for i := len(tradeSnapshots) - 1; i >= maxInt(len(tradeSnapshots)-3, 0); i-- {
		worstTrades = append(worstTrades, tradeSnapshots[i].Data)
	}

	return map[string]interface{}{
		"count":           len(tradeSnapshots),
		"win_count":       winCount,
		"loss_count":      lossCount,
		"breakeven_count": breakevenCount,
		"total_pnl":       totalPnl,
		"avg_pnl":         totalPnl / float64(len(tradeSnapshots)),
		"avg_pnl_percent": totalPnlPercent / float64(len(tradeSnapshots)),
		"reason_counts":   reasonCounts,
		"best_trades":     bestTrades,
		"worst_trades":    worstTrades,
	}
}

func buildEquitySummaryForLLM(strategy map[string]interface{}) map[string]interface{} {
	equity := getArrayValue(strategy, "Equity", "equity")
	if len(equity) == 0 {
		return nil
	}

	values := make([]float64, 0, len(equity))
	for _, point := range equity {
		pointMap, ok := point.(map[string]interface{})
		if !ok {
			continue
		}
		values = append(values, getFloatValue(pointMap, "Value", "value"))
	}

	if len(values) == 0 {
		return nil
	}

	minValue := values[0]
	maxValue := values[0]
	for _, value := range values[1:] {
		minValue = math.Min(minValue, value)
		maxValue = math.Max(maxValue, value)
	}

	startValue := values[0]
	endValue := values[len(values)-1]
	changePct := 0.0
	if startValue != 0 {
		changePct = ((endValue - startValue) / startValue) * 100
	}

	return map[string]interface{}{
		"points":        len(values),
		"start_value":   startValue,
		"end_value":     endValue,
		"change":        endValue - startValue,
		"change_pct":    changePct,
		"min_value":     minValue,
		"max_value":     maxValue,
		"sample_points": sampleEquityPointsForLLM(equity, 5),
	}
}

func sampleEquityPointsForLLM(equity []interface{}, limit int) []map[string]interface{} {
	if len(equity) == 0 || limit <= 0 {
		return nil
	}
	if limit == 1 {
		limit = 2
	}

	indexes := map[int]struct{}{0: {}}
	if len(equity) > 1 {
		indexes[len(equity)-1] = struct{}{}
	}
	for step := 1; len(indexes) < limit && len(indexes) < len(equity); step++ {
		index := int(math.Round(float64(step) * float64(len(equity)-1) / float64(limit-1)))
		indexes[index] = struct{}{}
	}

	orderedIndexes := make([]int, 0, len(indexes))
	for index := range indexes {
		orderedIndexes = append(orderedIndexes, index)
	}
	sort.Ints(orderedIndexes)

	samples := make([]map[string]interface{}, 0, len(orderedIndexes))
	for _, index := range orderedIndexes {
		pointMap, ok := equity[index].(map[string]interface{})
		if !ok {
			continue
		}
		samples = append(samples, map[string]interface{}{
			"time":  valueFromMap(pointMap, "Time", "time"),
			"value": getFloatValue(pointMap, "Value", "value"),
		})
	}

	return samples
}

func buildEquityBySymbolSummaryForLLM(strategy map[string]interface{}) map[string]interface{} {
	equityBySymbol := getMapValue(strategy, "EquityBySymbol", "equity_by_symbol")
	if len(equityBySymbol) == 0 {
		return nil
	}

	type symbolSummary struct {
		AbsChange float64
		Data      map[string]interface{}
	}

	summaries := make([]symbolSummary, 0, len(equityBySymbol))
	for symbol, rawSeries := range equityBySymbol {
		series, ok := rawSeries.([]interface{})
		if !ok || len(series) == 0 {
			continue
		}

		values := make([]float64, 0, len(series))
		for _, point := range series {
			pointMap, ok := point.(map[string]interface{})
			if !ok {
				continue
			}
			values = append(values, getFloatValue(pointMap, "Value", "value"))
		}
		if len(values) == 0 {
			continue
		}

		startValue := values[0]
		endValue := values[len(values)-1]
		change := endValue - startValue
		minValue := values[0]
		maxValue := values[0]
		for _, value := range values[1:] {
			minValue = math.Min(minValue, value)
			maxValue = math.Max(maxValue, value)
		}

		summaries = append(summaries, symbolSummary{
			AbsChange: math.Abs(change),
			Data: map[string]interface{}{
				"symbol":      symbol,
				"points":      len(values),
				"start_value": startValue,
				"end_value":   endValue,
				"change":      change,
				"min_value":   minValue,
				"max_value":   maxValue,
			},
		})
	}

	if len(summaries) == 0 {
		return nil
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].AbsChange > summaries[j].AbsChange
	})

	topSymbols := make([]map[string]interface{}, 0, minInt(5, len(summaries)))
	for _, summary := range summaries[:minInt(5, len(summaries))] {
		topSymbols = append(topSymbols, summary.Data)
	}

	return map[string]interface{}{
		"symbol_count": len(summaries),
		"top_symbols":  topSymbols,
	}
}

func buildValidationSummaryForLLM(validation map[string]interface{}) map[string]interface{} {
	if validation == nil {
		return nil
	}

	summary := map[string]interface{}{
		"backtest_mode":                        valueFromMap(validation, "backtest_mode"),
		"model_version":                        valueFromMap(validation, "model_version"),
		"universe_mode":                        valueFromMap(validation, "universe_mode"),
		"policy_version":                       valueFromMap(validation, "policy_version"),
		"rollout_state":                        valueFromMap(validation, "rollout_state"),
		"windows":                              valueFromMap(validation, "windows"),
		"train_windows":                        valueFromMap(validation, "train_windows"),
		"test_windows":                         valueFromMap(validation, "test_windows"),
		"training_accepted_metrics":            valueFromMap(validation, "training_accepted_metrics"),
		"accepted_metrics":                     valueFromMap(validation, "accepted_metrics"),
		"training_passed":                      valueFromMap(validation, "training_passed"),
		"passed":                               valueFromMap(validation, "passed"),
		"training_sharpe_baseline_ci":          valueFromMap(validation, "training_sharpe_baseline_ci"),
		"training_sharpe_vol_sizing_ci":        valueFromMap(validation, "training_sharpe_vol_sizing_ci"),
		"sharpe_baseline_ci":                   valueFromMap(validation, "sharpe_baseline_ci"),
		"sharpe_vol_sizing_ci":                 valueFromMap(validation, "sharpe_vol_sizing_ci"),
		"training_max_drawdown_baseline_ci":    valueFromMap(validation, "training_max_drawdown_baseline_ci"),
		"training_max_drawdown_vol_sizing_ci":  valueFromMap(validation, "training_max_drawdown_vol_sizing_ci"),
		"max_drawdown_baseline_ci":             valueFromMap(validation, "max_drawdown_baseline_ci"),
		"max_drawdown_vol_sizing_ci":           valueFromMap(validation, "max_drawdown_vol_sizing_ci"),
		"training_profit_factor_baseline_ci":   valueFromMap(validation, "training_profit_factor_baseline_ci"),
		"training_profit_factor_vol_sizing_ci": valueFromMap(validation, "training_profit_factor_vol_sizing_ci"),
		"profit_factor_baseline_ci":            valueFromMap(validation, "profit_factor_baseline_ci"),
		"profit_factor_vol_sizing_ci":          valueFromMap(validation, "profit_factor_vol_sizing_ci"),
		"baseline_ranking_diagnostics":         valueFromMap(validation, "baseline_ranking_diagnostics"),
		"vol_sizing_ranking_diagnostics":       valueFromMap(validation, "vol_sizing_ranking_diagnostics"),
		"baseline_regime_slices":               valueFromMap(validation, "baseline_regime_slices"),
		"vol_sizing_regime_slices":             valueFromMap(validation, "vol_sizing_regime_slices"),
		"baseline_symbol_cohorts":              valueFromMap(validation, "baseline_symbol_cohorts"),
		"vol_sizing_symbol_cohorts":            valueFromMap(validation, "vol_sizing_symbol_cohorts"),
		"window_summaries":                     valueFromMap(validation, "window_summaries"),
		"promotion_readiness":                  valueFromMap(validation, "promotion_readiness"),
	}

	if metricsSummary := summarizeMetricsSeries(getArrayValue(validation, "training_baseline_metrics")); metricsSummary != nil {
		summary["training_baseline_metrics_summary"] = metricsSummary
	}
	if metricsSummary := summarizeMetricsSeries(getArrayValue(validation, "training_vol_sizing_metrics")); metricsSummary != nil {
		summary["training_vol_sizing_metrics_summary"] = metricsSummary
	}
	if metricsSummary := summarizeMetricsSeries(getArrayValue(validation, "baseline_metrics")); metricsSummary != nil {
		summary["baseline_metrics_summary"] = metricsSummary
	}
	if metricsSummary := summarizeMetricsSeries(getArrayValue(validation, "vol_sizing_metrics")); metricsSummary != nil {
		summary["vol_sizing_metrics_summary"] = metricsSummary
	}

	return summary
}

func summarizeMetricsSeries(series []interface{}) map[string]interface{} {
	if len(series) == 0 {
		return nil
	}

	keys := []string{"Sharpe", "MaxDrawdown", "WinRate", "ProfitFactor", "AvgWin", "AvgLoss", "ReturnVolatility", "TradeCount"}
	totals := map[string]float64{}
	count := 0

	for _, item := range series {
		metricMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		count++
		for _, key := range keys {
			totals[key] += getFloatValue(metricMap, key, strings.ToLower(key[:1])+key[1:])
		}
	}

	if count == 0 {
		return nil
	}

	averages := map[string]float64{}
	for _, key := range keys {
		averages[key] = totals[key] / float64(count)
	}

	return map[string]interface{}{
		"count":    count,
		"averages": averages,
	}
}

func getBacktestMetricValue(summary map[string]interface{}, strategyKey string, metric string) float64 {
	strategy := getNestedMap(summary, strategyKey)
	if strategy == nil {
		return 0
	}
	metrics := getNestedMap(strategy, "Metrics", "metrics")
	if metrics == nil {
		return 0
	}
	return getFloatValue(metrics, metric, strings.ToLower(metric[:1])+metric[1:])
}

func getNestedMap(source map[string]interface{}, keys ...string) map[string]interface{} {
	for _, key := range keys {
		if value, ok := source[key]; ok {
			if nested, ok := value.(map[string]interface{}); ok {
				return nested
			}
		}
	}
	return nil
}

func getArrayValue(source map[string]interface{}, keys ...string) []interface{} {
	for _, key := range keys {
		if value, ok := source[key]; ok {
			if items, ok := value.([]interface{}); ok {
				return items
			}
		}
	}
	return nil
}

func getMapValue(source map[string]interface{}, keys ...string) map[string]interface{} {
	for _, key := range keys {
		if value, ok := source[key]; ok {
			if nested, ok := value.(map[string]interface{}); ok {
				return nested
			}
		}
	}
	return nil
}

func valueFromMap(source map[string]interface{}, keys ...string) interface{} {
	for _, key := range keys {
		if value, ok := source[key]; ok {
			return value
		}
	}
	return nil
}

func stringValue(value interface{}) string {
	if value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	return fmt.Sprint(value)
}

func getFloatValue(source map[string]interface{}, keys ...string) float64 {
	for _, key := range keys {
		if value, ok := source[key]; ok {
			switch v := value.(type) {
			case float64:
				return v
			case float32:
				return float64(v)
			case int:
				return float64(v)
			case int64:
				return float64(v)
			case json.Number:
				parsed, err := v.Float64()
				if err == nil {
					return parsed
				}
			}
		}
	}
	return 0
}

func prettyJSON(value interface{}) string {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(payload)
}

func callLLM(config *database.LLMConfig, prompt string) (LLMCallResult, error) {
	timeoutSeconds := 300
	payload := map[string]interface{}{
		"model": config.Model,
		"messages": []map[string]interface{}{
			{"role": "user", "content": prompt},
		},
		"max_tokens": 4000,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return LLMCallResult{}, err
	}

	req, err := http.NewRequest("POST", config.BaseURL+"/chat/completions", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return LLMCallResult{}, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+*config.APIKey)

	client := &http.Client{Timeout: time.Duration(timeoutSeconds) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return LLMCallResult{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return LLMCallResult{}, err
	}

	if resp.StatusCode != http.StatusOK {
		// Try to extract the provider's error message
		var errResp struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
			detail := errResp.Error.Message
			if errResp.Error.Type != "" {
				detail = errResp.Error.Type + ": " + detail
			}
			return LLMCallResult{}, fmt.Errorf("LLM API error (HTTP %d): %s", resp.StatusCode, detail)
		}
		return LLMCallResult{}, fmt.Errorf("LLM API error (HTTP %d): %s", resp.StatusCode, string(body))
	}

	type LLMResponse struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}

	var llmResp LLMResponse
	if err := json.Unmarshal(body, &llmResp); err != nil {
		return LLMCallResult{}, fmt.Errorf("failed to parse LLM response: %v - raw body: %.500s", err, string(body))
	}

	if len(llmResp.Choices) == 0 {
		return LLMCallResult{}, fmt.Errorf("LLM returned empty choices array - raw body: %.500s", string(body))
	}

	return LLMCallResult{
		Content:      llmResp.Choices[0].Message.Content,
		FinishReason: strings.TrimSpace(llmResp.Choices[0].FinishReason),
	}, nil
}

func parseProposalsFromResponse(response string, settings map[string]string, weights map[string]float64, allowedKeys map[string]bool, categories map[string]string) ([]database.AIProposal, error) {
	return parseProposalsFromResponseWithType(response, settings, weights, allowedKeys, categories, "parameter_adjustment")
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func parseProposalsFromResponseWithType(response string, settings map[string]string, weights map[string]float64, allowedKeys map[string]bool, categories map[string]string, proposalType string) ([]database.AIProposal, error) {
	result, err := parseProposalsFromResponseWithTypeDetailed(response, settings, weights, allowedKeys, categories, proposalType)
	if err != nil {
		return nil, err
	}
	return result.Proposals, nil
}

func parseProposalsFromResponseWithTypeDetailed(response string, settings map[string]string, weights map[string]float64, allowedKeys map[string]bool, categories map[string]string, proposalType string) (backtestProposalParseResult, error) {
	response = strings.TrimSpace(response)
	diagnostics := ProposalParseDiagnostics{RejectedCounts: make(map[string]int)}
	reject := func(reason string) {
		diagnostics.RejectedCounts[reason]++
	}

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
		reject("no_json_array")
		diagnostics.AcceptedCount = 0
		return backtestProposalParseResult{Proposals: []database.AIProposal{}, Diagnostics: diagnostics}, nil
	}
	diagnostics.FoundJSONArray = true

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
		reject("invalid_json")
		diagnostics.AcceptedCount = 0
		return backtestProposalParseResult{Proposals: []database.AIProposal{}, Diagnostics: diagnostics}, nil
	}
	diagnostics.RawProposalCount = len(rawProposals)

	proposals := make([]database.AIProposal, 0, len(rawProposals))
	maxPerCategory := getSettingIntFromMap(settings, "ai_max_keys_per_category", 2)
	maxProposals := getSettingIntFromMap(settings, "ai_max_proposals", 5)
	changeBudgetPct := getSettingFloatFromMap(settings, "ai_change_budget_pct", 10)
	categoryCounts := make(map[string]int)
	for _, raw := range rawProposals {
		if strings.TrimSpace(raw.ProposalType) != proposalType {
			reject("wrong_type")
			continue
		}
		if raw.ParameterKey == nil || raw.NewValue == nil {
			reject("missing_required_fields")
			continue
		}
		paramKey := strings.TrimSpace(*raw.ParameterKey)
		if paramKey == "" || !allowedKeys[paramKey] {
			reject("disallowed_key")
			continue
		}
		if strings.TrimSpace(*raw.NewValue) == "" {
			reject("missing_new_value")
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
			reject("missing_current_value")
			continue
		}
		if changeBudgetPct > 0 {
			oldNum, oldErr := strconv.ParseFloat(strings.TrimSpace(oldValue), 64)
			newNum, newErr := strconv.ParseFloat(strings.TrimSpace(*raw.NewValue), 64)
			if oldErr == nil && newErr == nil && oldNum != 0 {
				pctChange := math.Abs((newNum-oldNum)/oldNum) * 100
				if pctChange > changeBudgetPct {
					reject("change_over_budget")
					continue
				}
			}
		}

		category := "unknown"
		if strings.HasPrefix(paramKey, "weight:") {
			category = "weights"
		} else if cat, ok := categories[paramKey]; ok && cat != "" {
			category = cat
		}
		if maxPerCategory > 0 && categoryCounts[category] >= maxPerCategory {
			reject("category_limit")
			continue
		}

		oldValueCopy := oldValue
		proposals = append(proposals, database.AIProposal{
			ProposalType: proposalType,
			ParameterKey: &paramKey,
			OldValue:     &oldValueCopy,
			NewValue:     raw.NewValue,
			Reasoning:    raw.Reasoning,
		})
		categoryCounts[category]++
		if maxProposals > 0 && len(proposals) >= maxProposals {
			break
		}
	}

	diagnostics.AcceptedCount = len(proposals)
	if len(diagnostics.RejectedCounts) == 0 {
		diagnostics.RejectedCounts = nil
	}
	return backtestProposalParseResult{Proposals: proposals, Diagnostics: diagnostics}, nil
}

func getSettingIntFromMap(settings map[string]string, key string, defaultVal int) int {
	val, ok := settings[key]
	if !ok {
		return defaultVal
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil {
		return defaultVal
	}
	return parsed
}

func getSettingFloatFromMap(settings map[string]string, key string, defaultVal float64) float64 {
	val, ok := settings[key]
	if !ok {
		return defaultVal
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
	if err != nil {
		return defaultVal
	}
	return parsed
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

	if err := database.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&proposal).Error; err != nil {
			return err
		}

		if !isAdjustmentProposalType(proposal.ProposalType) || proposal.ParameterKey == nil || proposal.NewValue == nil {
			return nil
		}

		paramKey := strings.TrimSpace(*proposal.ParameterKey)
		if strings.HasPrefix(paramKey, "weight:") {
			indicator := strings.TrimSpace(strings.TrimPrefix(paramKey, "weight:"))
			if indicator == "" {
				return nil
			}

			weightValue, err := strconv.ParseFloat(strings.TrimSpace(*proposal.NewValue), 64)
			if err != nil {
				return err
			}

			weight := database.IndicatorWeight{Indicator: indicator, Weight: weightValue}
			return tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "indicator"}},
				DoUpdates: clause.AssignmentColumns([]string{"weight"}),
			}).Create(&weight).Error
		}

		setting := database.Setting{Key: paramKey, Value: *proposal.NewValue, UpdatedAt: time.Now()}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
		}).Create(&setting).Error
	}); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fiber.NewError(404, "Proposal not found")
		}
		return nil, fiber.NewError(500, "Failed to approve proposal")
	}

	return fiber.Map{
		"success":  true,
		"proposal": proposal,
		"message":  "Proposal approved and applied",
	}, nil
}

func isAdjustmentProposalType(proposalType string) bool {
	return proposalType == "parameter_adjustment" || proposalType == "backtest_parameter_adjustment"
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

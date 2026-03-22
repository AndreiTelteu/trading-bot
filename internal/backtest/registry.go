package backtest

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"trading-go/internal/database"

	"gorm.io/gorm/clause"
)

func RegisterExperimentRun(jobID uint, summary *BacktestRunSummary) (string, error) {
	if summary == nil || database.DB == nil {
		return "", nil
	}

	experimentID := summary.ExperimentID
	if strings.TrimSpace(experimentID) == "" {
		experimentID = buildExperimentID(summary)
	}

	validationJSON, err := json.Marshal(summary.Validation)
	if err != nil {
		return "", err
	}
	rankingJSON, err := json.Marshal(map[string]interface{}{
		"baseline":   summary.Baseline.RankingMetrics,
		"vol_sizing": summary.VolSizing.RankingMetrics,
	})
	if err != nil {
		return "", err
	}

	record := database.ExperimentRun{
		ExperimentID:                experimentID,
		BacktestMode:                string(summary.BacktestMode),
		ModelVersion:                summary.ModelVersion,
		FeatureSpecVersion:          summary.PolicyContext.FeatureSpecVersion,
		LabelSpecVersion:            summary.PolicyContext.LabelSpecVersion,
		UniverseMode:                string(summary.UniverseMode),
		PolicyVersion:               summary.PolicyVersion,
		ExecutionPolicyVersion:      summary.PolicyContext.PolicyVersions.ExecutionPolicyVersion,
		UniversePolicyVersion:       summary.PolicyContext.PolicyVersions.UniversePolicyVersion,
		ModelSelectionPolicyVersion: summary.PolicyContext.PolicyVersions.ModelSelectionPolicyVersion,
		EntrySelectionPolicyVersion: summary.PolicyContext.PolicyVersions.EntrySelectionPolicyVersion,
		PortfolioRiskPolicyVersion:  summary.PolicyContext.PolicyVersions.PortfolioRiskPolicyVersion,
		RolloutPolicyVersion:        summary.PolicyContext.PolicyVersions.RolloutPolicyVersion,
		RolloutState:                summary.PolicyContext.RolloutState,
		ValidationPassed:            summary.Validation.Passed,
		ValidationSummaryJSON:       string(validationJSON),
		RankingSummaryJSON:          string(rankingJSON),
		PromotionDecision:           summary.Validation.PromotionReadiness.RecommendedStage,
	}
	if jobID > 0 {
		record.BacktestJobID = &jobID
	}

	if err := database.DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "experiment_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"backtest_job_id",
			"backtest_mode",
			"model_version",
			"feature_spec_version",
			"label_spec_version",
			"universe_mode",
			"policy_version",
			"execution_policy_version",
			"universe_policy_version",
			"model_selection_policy_version",
			"entry_selection_policy_version",
			"portfolio_risk_policy_version",
			"rollout_policy_version",
			"rollout_state",
			"validation_passed",
			"validation_summary_json",
			"ranking_summary_json",
			"promotion_decision",
			"updated_at",
		}),
	}).Create(&record).Error; err != nil {
		return "", err
	}

	if strings.TrimSpace(summary.ModelVersion) != "" {
		updates := map[string]interface{}{
			"active_experiment_id":    experimentID,
			"promotion_metadata_json": string(validationJSON),
		}
		if err := database.DB.Model(&database.ModelArtifact{}).
			Where("version = ?", summary.ModelVersion).
			Updates(updates).Error; err != nil {
			return "", err
		}
	}

	return experimentID, nil
}

func buildExperimentID(summary *BacktestRunSummary) string {
	timestamp := time.Now().UTC().Format("20060102T150405")
	mode := strings.TrimSpace(string(summary.BacktestMode))
	if mode == "" {
		mode = "backtest"
	}
	modelVersion := strings.TrimSpace(summary.ModelVersion)
	if modelVersion == "" {
		modelVersion = "rule_rank"
	}
	return fmt.Sprintf("exp_%s_%s_%s", mode, modelVersion, timestamp)
}

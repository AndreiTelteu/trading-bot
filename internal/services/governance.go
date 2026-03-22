package services

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"trading-go/internal/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	PolicyTypeExecution      = "execution_policy"
	PolicyTypeUniverse       = "universe_policy"
	PolicyTypeModelSelection = "model_selection_policy"
	PolicyTypeEntrySelection = "entry_selection_policy"
	PolicyTypePortfolioRisk  = "portfolio_risk_policy"
	PolicyTypeRollout        = "rollout_policy"
)

type PolicyVersionSet struct {
	ExecutionPolicyVersion      string `json:"execution_policy_version"`
	UniversePolicyVersion       string `json:"universe_policy_version"`
	ModelSelectionPolicyVersion string `json:"model_selection_policy_version"`
	EntrySelectionPolicyVersion string `json:"entry_selection_policy_version"`
	PortfolioRiskPolicyVersion  string `json:"portfolio_risk_policy_version"`
	RolloutPolicyVersion        string `json:"rollout_policy_version"`
	CompositeVersion            string `json:"composite_version"`
}

type GovernanceContext struct {
	ModelVersion       string           `json:"model_version"`
	FeatureSpecVersion string           `json:"feature_spec_version,omitempty"`
	LabelSpecVersion   string           `json:"label_spec_version,omitempty"`
	UniverseMode       string           `json:"universe_mode"`
	RolloutState       string           `json:"rollout_state"`
	FallbackMode       string           `json:"fallback_mode"`
	RollbackTarget     string           `json:"rollback_target,omitempty"`
	EffectiveEntryMode string           `json:"effective_entry_mode"`
	ExperimentID       string           `json:"experiment_id,omitempty"`
	PolicyVersions     PolicyVersionSet `json:"policy_versions"`
}

type GovernanceOverview struct {
	Context             GovernanceContext            `json:"context"`
	ActivePolicies      []database.PolicyConfig      `json:"active_policies,omitempty"`
	LatestExperiment    *database.ExperimentRun      `json:"latest_experiment,omitempty"`
	RecentRolloutEvents []database.RolloutEvent      `json:"recent_rollout_events,omitempty"`
	LatestMonitoring    *database.MonitoringSnapshot `json:"latest_monitoring,omitempty"`
}

func ResolveGovernanceContext(settings map[string]string, universeMode string) (GovernanceContext, error) {
	versions, err := EnsurePolicyConfigs(settings)
	if err != nil {
		return GovernanceContext{}, err
	}

	policy := GetModelSelectionPolicy(settings)
	policy.PolicyVersion = versions.ModelSelectionPolicyVersion

	context := GovernanceContext{
		ModelVersion:       policy.ActiveModelVersion,
		UniverseMode:       strings.TrimSpace(universeMode),
		RolloutState:       policy.rolloutLabel(),
		FallbackMode:       policy.FallbackMode,
		RollbackTarget:     policy.RollbackTarget,
		EffectiveEntryMode: policy.EffectiveEntryMode(),
		PolicyVersions:     versions,
	}

	if database.DB == nil || strings.TrimSpace(policy.ActiveModelVersion) == "" {
		return context, nil
	}

	var artifact database.ModelArtifact
	if err := database.DB.Where("version = ?", policy.ActiveModelVersion).First(&artifact).Error; err == nil {
		context.FeatureSpecVersion = artifact.FeatureSpecVersion
		context.LabelSpecVersion = artifact.LabelSpecVersion
		if artifact.ActiveExperimentID != nil {
			context.ExperimentID = strings.TrimSpace(*artifact.ActiveExperimentID)
		}
	}

	return context, nil
}

func EnsurePolicyConfigs(settings map[string]string) (PolicyVersionSet, error) {
	payloads := buildPolicyPayloads(settings)
	versions := PolicyVersionSet{
		ExecutionPolicyVersion:      policyVersion(PolicyTypeExecution, payloads[PolicyTypeExecution]),
		UniversePolicyVersion:       policyVersion(PolicyTypeUniverse, payloads[PolicyTypeUniverse]),
		ModelSelectionPolicyVersion: policyVersion(PolicyTypeModelSelection, payloads[PolicyTypeModelSelection]),
		EntrySelectionPolicyVersion: policyVersion(PolicyTypeEntrySelection, payloads[PolicyTypeEntrySelection]),
		PortfolioRiskPolicyVersion:  policyVersion(PolicyTypePortfolioRisk, payloads[PolicyTypePortfolioRisk]),
		RolloutPolicyVersion:        policyVersion(PolicyTypeRollout, payloads[PolicyTypeRollout]),
	}
	versions.CompositeVersion = compositePolicyVersion(versions)

	if database.DB == nil {
		return versions, nil
	}

	err := database.DB.Transaction(func(tx *gorm.DB) error {
		for policyType, payload := range payloads {
			version := policyVersion(policyType, payload)
			payloadBytes, err := json.Marshal(payload)
			if err != nil {
				return err
			}

			record := database.PolicyConfig{
				PolicyType:  policyType,
				Version:     version,
				IsActive:    true,
				Source:      "settings",
				PayloadJSON: string(payloadBytes),
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "policy_type"}, {Name: "version"}},
				DoUpdates: clause.AssignmentColumns([]string{"is_active", "source", "payload_json", "updated_at"}),
			}).Create(&record).Error; err != nil {
				return err
			}

			if err := tx.Model(&database.PolicyConfig{}).
				Where("policy_type = ? AND version <> ?", policyType, version).
				Update("is_active", false).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return PolicyVersionSet{}, err
	}

	return versions, nil
}

func SyncGovernanceState(settings map[string]string, source string) (GovernanceContext, error) {
	context, err := ResolveGovernanceContext(settings, getSettingString(settings, "universe_mode", "dynamic"))
	if err != nil {
		return GovernanceContext{}, err
	}
	if database.DB == nil || strings.TrimSpace(context.ModelVersion) == "" {
		return context, nil
	}

	var artifact database.ModelArtifact
	err = database.DB.Where("version = ?", context.ModelVersion).First(&artifact).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return GovernanceContext{}, err
	}

	gateSummaryJSON := "{}"
	if context.ExperimentID != "" {
		var experiment database.ExperimentRun
		if err := database.DB.Where("experiment_id = ?", context.ExperimentID).First(&experiment).Error; err == nil {
			gateSummary := map[string]interface{}{
				"validation_passed":  experiment.ValidationPassed,
				"promotion_decision": experiment.PromotionDecision,
				"rollout_state":      experiment.RolloutState,
			}
			if payload, err := json.Marshal(gateSummary); err == nil {
				gateSummaryJSON = string(payload)
			}
		}
	}

	metadataPayload, _ := json.Marshal(map[string]interface{}{
		"effective_entry_mode": context.EffectiveEntryMode,
		"fallback_mode":        context.FallbackMode,
		"policy_versions":      context.PolicyVersions,
		"synced_at":            time.Now().UTC(),
	})

	fromState := ""
	if strings.TrimSpace(artifact.RolloutState) != "" {
		fromState = artifact.RolloutState
	}
	if fromState == "" {
		fromState = ModelRolloutResearchOnly
	}

	if artifact.ID != 0 {
		artifact.RolloutState = context.RolloutState
		artifact.PromotionMetadataJSON = string(metadataPayload)
		if context.ExperimentID != "" {
			experimentID := context.ExperimentID
			artifact.ActiveExperimentID = &experimentID
		}
		if context.RollbackTarget != "" {
			rollbackTarget := context.RollbackTarget
			artifact.RollbackTarget = &rollbackTarget
		} else {
			artifact.RollbackTarget = nil
		}
		if err := database.DB.Save(&artifact).Error; err != nil {
			return GovernanceContext{}, err
		}
	}

	var latestEvent database.RolloutEvent
	hasLatestEvent := database.DB.Where("model_version = ?", context.ModelVersion).Order("created_at DESC").First(&latestEvent).Error == nil
	shouldCreateEvent := !hasLatestEvent || latestEvent.ToState != context.RolloutState || latestEvent.PolicyVersion != context.PolicyVersions.CompositeVersion || latestEvent.FallbackMode != context.FallbackMode || stringValuePtr(latestEvent.RollbackTarget) != context.RollbackTarget
	if shouldCreateEvent {
		event := database.RolloutEvent{
			ModelVersion:    context.ModelVersion,
			PolicyVersion:   context.PolicyVersions.CompositeVersion,
			FromState:       fromState,
			ToState:         context.RolloutState,
			FallbackMode:    context.FallbackMode,
			Source:          defaultString(source, "settings"),
			GateSummaryJSON: gateSummaryJSON,
			MetadataJSON:    string(metadataPayload),
		}
		if context.ExperimentID != "" {
			experimentID := context.ExperimentID
			event.ExperimentID = &experimentID
		}
		if context.RollbackTarget != "" {
			rollbackTarget := context.RollbackTarget
			event.RollbackTarget = &rollbackTarget
		}
		if err := database.DB.Create(&event).Error; err != nil {
			return GovernanceContext{}, err
		}
	}

	return context, nil
}

func GetGovernanceOverview() (GovernanceOverview, error) {
	settings := GetAllSettings()
	context, err := ResolveGovernanceContext(settings, getSettingString(settings, "universe_mode", "dynamic"))
	if err != nil {
		return GovernanceOverview{}, err
	}

	overview := GovernanceOverview{Context: context}
	if database.DB == nil {
		return overview, nil
	}

	if err := database.DB.Where("is_active = ?", true).Order("policy_type ASC").Find(&overview.ActivePolicies).Error; err != nil {
		return GovernanceOverview{}, err
	}

	if context.ExperimentID != "" {
		var experiment database.ExperimentRun
		if err := database.DB.Where("experiment_id = ?", context.ExperimentID).First(&experiment).Error; err == nil {
			overview.LatestExperiment = &experiment
		}
	}
	if overview.LatestExperiment == nil {
		var experiment database.ExperimentRun
		if err := database.DB.Order("created_at DESC").First(&experiment).Error; err == nil {
			overview.LatestExperiment = &experiment
		}
	}

	if strings.TrimSpace(context.ModelVersion) != "" {
		database.DB.Where("model_version = ?", context.ModelVersion).Order("created_at DESC").Limit(5).Find(&overview.RecentRolloutEvents)
		var monitoring database.MonitoringSnapshot
		if err := database.DB.Where("model_version = ?", context.ModelVersion).Order("snapshot_time DESC").First(&monitoring).Error; err == nil {
			overview.LatestMonitoring = &monitoring
		}
	}

	return overview, nil
}

func buildPolicyPayloads(settings map[string]string) map[string]map[string]string {
	return map[string]map[string]string{
		PolicyTypeExecution: pickSettings(settings,
			"sell_on_signal", "min_confidence_to_sell", "allow_sell_at_loss", "stream_exit_enabled",
			"trailing_stop_enabled", "trailing_stop_percent", "atr_trailing_enabled", "atr_trailing_mult",
			"atr_trailing_period", "atr_annualization_enabled", "atr_annualization_days", "time_stop_bars",
		),
		PolicyTypeUniverse: pickSettings(settings,
			"universe_mode", "universe_rebalance_interval", "universe_min_listing_days", "universe_min_daily_quote_volume",
			"universe_min_intraday_quote_volume", "universe_max_gap_ratio", "universe_vol_ratio_min",
			"universe_vol_ratio_max", "universe_max_24h_move", "universe_top_k", "universe_analyze_top_n",
		),
		PolicyTypeModelSelection: pickSettings(settings,
			"active_model_version", "selection_policy_top_k", "selection_policy_min_prob", "selection_policy_min_ev",
			"monitoring_window_days", "monitoring_min_outcomes",
		),
		PolicyTypeEntrySelection: pickSettings(settings,
			"auto_trade_enabled", "max_positions", "buy_only_strong", "min_confidence_to_buy", "trending_coins_to_analyze",
		),
		PolicyTypePortfolioRisk: pickSettings(settings,
			"entry_percent", "rebuy_percent", "stop_loss_percent", "take_profit_percent", "vol_sizing_enabled",
			"risk_per_trade", "stop_mult", "tp_mult", "max_position_value", "pyramiding_enabled",
			"max_pyramid_layers", "position_scale_percent",
		),
		PolicyTypeRollout: pickSettings(settings,
			"active_model_version", "model_rollout_state", "model_fallback_mode", "model_rollback_target",
		),
	}
}

func pickSettings(settings map[string]string, keys ...string) map[string]string {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		result[key] = strings.TrimSpace(settings[key])
	}
	return result
}

func policyVersion(policyType string, payload map[string]string) string {
	payloadBytes, _ := json.Marshal(payload)
	sum := sha256.Sum256(payloadBytes)
	return fmt.Sprintf("%s_%s", policyType, hex.EncodeToString(sum[:])[:10])
}

func compositePolicyVersion(versions PolicyVersionSet) string {
	parts := []string{
		versions.ExecutionPolicyVersion,
		versions.UniversePolicyVersion,
		versions.ModelSelectionPolicyVersion,
		versions.EntrySelectionPolicyVersion,
		versions.PortfolioRiskPolicyVersion,
		versions.RolloutPolicyVersion,
	}
	sort.Strings(parts)
	joined := strings.Join(parts, "|")
	sum := sha256.Sum256([]byte(joined))
	return "policy_bundle_" + hex.EncodeToString(sum[:])[:10]
}

func stringValuePtr(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

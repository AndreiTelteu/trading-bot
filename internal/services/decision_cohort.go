package services

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"trading-go/internal/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var sha256DigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

const (
	DecisionOutcomePending      = "pending"
	DecisionOutcomeUnavailable  = "unavailable"
	DecisionOutcomeLabeled      = "labeled"
	defaultDecisionLabelHorizon = 24 * time.Hour
)

// decisionLabelPolicy is captured in every cohort. It is deliberately derived
// from authority-affecting settings once, before any row is persisted, so a
// later settings change cannot rewrite how an old opportunity is scored.
type decisionLabelPolicy struct {
	Horizon          time.Duration
	RoundTripCostBPS int64
	CostModelVersion string
}

func decisionLabelPolicyFromSettings(settings map[string]string) (decisionLabelPolicy, error) {
	horizon := defaultDecisionLabelHorizon
	if raw := strings.TrimSpace(getSettingString(settings, "model_label_horizon", "24h")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 || parsed > 30*24*time.Hour || parsed%time.Minute != 0 {
			return decisionLabelPolicy{}, fmt.Errorf("model_label_horizon must be a positive whole-minute duration no longer than 30 days")
		}
		horizon = parsed
	}
	fee := getSettingInt(settings, "paper_fee_bps", 10)
	slippage := getSettingInt(settings, "paper_slippage_bps", 5)
	if fee < 0 || slippage < 0 || fee+slippage > 10000 {
		return decisionLabelPolicy{}, fmt.Errorf("paper label cost settings are invalid")
	}
	roundTrip := int64(2 * (fee + slippage))
	return decisionLabelPolicy{Horizon: horizon, RoundTripCostBPS: roundTrip, CostModelVersion: "paper-round-trip-bps:" + strconv.FormatInt(roundTrip, 10)}, nil
}

func decisionCohortIdentity(decisionTime time.Time, symbol string, horizon time.Duration, policyVersion, artifactDigest, costModelVersion string) string {
	canonical := strings.Join([]string{
		decisionTime.UTC().Format(time.RFC3339Nano), strings.ToUpper(strings.TrimSpace(symbol)),
		strconv.FormatInt(int64(horizon/time.Second), 10), strings.TrimSpace(policyVersion),
		strings.TrimSpace(artifactDigest), strings.TrimSpace(costModelVersion),
	}, "|")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

func modelArtifactDigest(version string) (string, error) {
	if database.DB == nil {
		return "", nil
	}
	var artifact database.ModelArtifact
	if err := database.DB.Where("version = ?", strings.TrimSpace(version)).First(&artifact).Error; err != nil {
		return "", fmt.Errorf("load model artifact identity: %w", err)
	}
	if !sha256DigestPattern.MatchString(artifact.ModelDigest) {
		return "", fmt.Errorf("model artifact %s has no durable digest", version)
	}
	return artifact.ModelDigest, nil
}

// ProcessMatureDecisionCohorts is a retryable observer worker. It only uses
// persisted, point-in-time historical bars. A missing bar is recorded as
// unavailable and remains eligible for a later retry; it is never coerced to a
// losing label or replaced with a current exchange quote.
func ProcessMatureDecisionCohorts(now time.Time, limit int) (int, error) {
	if database.DB == nil {
		return 0, nil
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	now = now.UTC()
	var candidates []database.DecisionCohort
	if err := database.DB.Where("maturity_time <= ? AND outcome_status IN ?", now, []string{DecisionOutcomePending, DecisionOutcomeUnavailable}).Order("maturity_time, decision_id").Limit(limit).Find(&candidates).Error; err != nil {
		return 0, err
	}
	processed := 0
	for _, candidate := range candidates {
		if err := processMatureDecisionCohort(candidate.DecisionID, now); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

func processMatureDecisionCohort(decisionID string, now time.Time) error {
	return database.DB.Transaction(func(tx *gorm.DB) error {
		var cohort database.DecisionCohort
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&cohort, "decision_id = ?", decisionID).Error; err != nil {
			return err
		}
		if cohort.OutcomeStatus == DecisionOutcomeLabeled || cohort.MaturityTime.After(now) {
			return nil
		}
		price, err := fixedHorizonOutcomePrice(tx, cohort, now)
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			return tx.Model(&database.DecisionCohort{}).Where("decision_id = ? AND outcome_status <> ?", cohort.DecisionID, DecisionOutcomeLabeled).Updates(map[string]interface{}{
				"outcome_status":   DecisionOutcomeUnavailable,
				"label_attempts":   gorm.Expr("label_attempts + 1"),
				"last_label_error": "fixed_horizon_bar_unavailable",
			}).Error
		}
		if !finiteOutcomePrice(price) || !finiteOutcomePrice(cohort.EntryReferencePrice) {
			return fmt.Errorf("decision cohort %s has invalid label price", cohort.DecisionID)
		}
		outcome := (price/cohort.EntryReferencePrice - 1) - float64(cohort.RoundTripCostBPS)/10000
		if math.IsNaN(outcome) || math.IsInf(outcome, 0) {
			return fmt.Errorf("decision cohort %s has invalid after-cost outcome", cohort.DecisionID)
		}
		profitable := outcome > 0
		return tx.Model(&database.DecisionCohort{}).Where("decision_id = ? AND outcome_status <> ?", cohort.DecisionID, DecisionOutcomeLabeled).Updates(map[string]interface{}{
			"outcome_status":      DecisionOutcomeLabeled,
			"outcome_return":      outcome,
			"outcome_profitable":  profitable,
			"outcome_price":       price,
			"outcome_recorded_at": now,
			"label_attempts":      gorm.Expr("label_attempts + 1"),
			"last_label_error":    "",
		}).Error
	})
}

func fixedHorizonOutcomePrice(tx *gorm.DB, cohort database.DecisionCohort, now time.Time) (float64, error) {
	var bar database.HistoricalBar
	end := cohort.MaturityTime.Add(15 * time.Minute)
	err := tx.Table("historical_bars AS b").Select("b.*").
		Joins("JOIN exchange_symbols AS s ON s.id = b.exchange_symbol_id").
		Where("s.ticker = ? AND b.role = ? AND b.timeframe = ? AND b.quality_status = ? AND b.open_time >= ? AND b.open_time < ? AND b.available_at <= ?", cohort.Symbol, "decision", "15m", "valid", cohort.MaturityTime, end, now).
		Order("b.open_time, b.id").First(&bar).Error
	if err != nil {
		return 0, err
	}
	price, err := strconv.ParseFloat(bar.Close, 64)
	if err != nil || !finiteOutcomePrice(price) {
		return 0, fmt.Errorf("invalid fixed-horizon bar close for %s", cohort.DecisionID)
	}
	return price, nil
}

func finiteOutcomePrice(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

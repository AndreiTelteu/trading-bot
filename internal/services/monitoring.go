package services

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"
	"trading-go/internal/database"
)

type RankBucketSelectionSummary struct {
	Bucket                  string  `json:"bucket"`
	Predictions             int     `json:"predictions"`
	Selected                int     `json:"selected"`
	SelectionRate           float64 `json:"selection_rate"`
	AvgPredictedProbability float64 `json:"avg_predicted_probability"`
	AvgRealizedReturn       float64 `json:"avg_realized_return"`
}

type ProbabilityCalibrationBucket struct {
	Bucket                  string  `json:"bucket"`
	Count                   int     `json:"count"`
	AvgPredictedProbability float64 `json:"avg_predicted_probability"`
	ProfitableRate          float64 `json:"profitable_rate"`
	AvgRealizedReturn       float64 `json:"avg_realized_return"`
}

type FeatureDriftMetric struct {
	Feature      string  `json:"feature"`
	SampleCount  int     `json:"sample_count"`
	BaselineMean float64 `json:"baseline_mean"`
	CurrentMean  float64 `json:"current_mean"`
	ZScore       float64 `json:"z_score"`
}

type MonitoringSummary struct {
	SnapshotTime            time.Time                      `json:"snapshot_time"`
	ModelVersion            string                         `json:"model_version"`
	PolicyVersion           string                         `json:"policy_version"`
	RolloutState            string                         `json:"rollout_state"`
	UniverseMode            string                         `json:"universe_mode"`
	ExperimentID            string                         `json:"experiment_id,omitempty"`
	PredictionCount         int                            `json:"prediction_count"`
	DecisionCount           int                            `json:"decision_count"`
	MaturedLabelCount       int                            `json:"matured_label_count"`
	PendingLabelCount       int                            `json:"pending_label_count"`
	UnavailableLabelCount   int                            `json:"unavailable_label_count"`
	LabelCoverage           float64                        `json:"label_coverage"`
	SelectionRate           float64                        `json:"selection_rate"`
	PredictionCountsByModel map[string]int                 `json:"prediction_counts_by_model"`
	RankBuckets             []RankBucketSelectionSummary   `json:"rank_buckets,omitempty"`
	Calibration             []ProbabilityCalibrationBucket `json:"calibration,omitempty"`
	FeatureDrift            []FeatureDriftMetric           `json:"feature_drift,omitempty"`
	RegimeSummary           map[string]interface{}         `json:"regime_summary,omitempty"`
}

func RefreshMonitoringSnapshot(settings map[string]string) error {
	windowDays := getSettingInt(settings, "monitoring_window_days", 30)
	summary, err := BuildMonitoringSummary(windowDays)
	if err != nil {
		return err
	}
	return persistMonitoringSnapshot(summary)
}

func BuildMonitoringSummary(windowDays int) (MonitoringSummary, error) {
	settings := GetAllSettings()
	context, err := ResolveGovernanceContext(settings, getSettingString(settings, "universe_mode", "dynamic"))
	if err != nil {
		return MonitoringSummary{}, err
	}
	if database.DB == nil {
		return MonitoringSummary{SnapshotTime: time.Now().UTC(), ModelVersion: context.ModelVersion, PolicyVersion: context.PolicyVersions.CompositeVersion}, nil
	}
	if windowDays <= 0 {
		windowDays = 30
	}

	since := time.Now().UTC().AddDate(0, 0, -windowDays)
	var cohorts []database.DecisionCohort
	query := database.DB.Where("decision_time >= ?", since).Order("decision_time DESC")
	if strings.TrimSpace(context.ModelVersion) != "" {
		query = query.Where("model_version = ?", context.ModelVersion)
	}
	if err := query.Limit(1000).Find(&cohorts).Error; err != nil {
		return MonitoringSummary{}, err
	}

	predictionCountsByModel := make(map[string]int)
	selectedCount := 0
	maturedLabels, pendingLabels, unavailableLabels := 0, 0, 0
	rankTotals := make(map[string]*bucketTotals)
	calibrationTotals := make(map[string]*bucketTotals)
	for _, cohort := range cohorts {
		predictionCountsByModel[cohort.ModelVersion]++
		if cohort.Accepted {
			selectedCount++
		}
		rankBucket := defaultString(cohort.RankBucket, rankBucket(cohort.Rank))
		rankTotal := ensureBucket(rankTotals, rankBucket)
		rankTotal.Predictions++
		rankTotal.ProbTotal += cohort.PredictedProbability
		if cohort.Accepted {
			rankTotal.Selected++
		}
		if cohort.OutcomeStatus == DecisionOutcomeLabeled && cohort.OutcomeReturn != nil {
			maturedLabels++
			rankTotal.OutcomeSum += *cohort.OutcomeReturn
			rankTotal.OutcomeCnt++
			probBucket := defaultString(cohort.ProbabilityBucket, probabilityBucket(cohort.PredictedProbability))
			calibrationTotal := ensureBucket(calibrationTotals, probBucket)
			calibrationTotal.Predictions++
			calibrationTotal.ProbTotal += cohort.PredictedProbability
			if cohort.OutcomeProfitable != nil && *cohort.OutcomeProfitable {
				calibrationTotal.Selected++
			}
			calibrationTotal.OutcomeSum += *cohort.OutcomeReturn
			calibrationTotal.OutcomeCnt++
		} else if cohort.OutcomeStatus == DecisionOutcomeUnavailable {
			unavailableLabels++
		} else {
			pendingLabels++
		}
	}

	rankBuckets := make([]RankBucketSelectionSummary, 0, len(rankTotals))
	for bucket, totals := range rankTotals {
		entry := RankBucketSelectionSummary{Bucket: bucket, Predictions: totals.Predictions, Selected: totals.Selected}
		if totals.Predictions > 0 {
			entry.SelectionRate = float64(totals.Selected) / float64(totals.Predictions)
			entry.AvgPredictedProbability = totals.ProbTotal / float64(totals.Predictions)
		}
		if totals.OutcomeCnt > 0 {
			entry.AvgRealizedReturn = totals.OutcomeSum / float64(totals.OutcomeCnt)
		}
		rankBuckets = append(rankBuckets, entry)
	}
	sort.Slice(rankBuckets, func(i, j int) bool { return rankBuckets[i].Bucket < rankBuckets[j].Bucket })

	calibration := make([]ProbabilityCalibrationBucket, 0, len(calibrationTotals))
	for bucket, totals := range calibrationTotals {
		entry := ProbabilityCalibrationBucket{Bucket: bucket, Count: totals.Predictions}
		if totals.Predictions > 0 {
			entry.AvgPredictedProbability = totals.ProbTotal / float64(totals.Predictions)
			entry.ProfitableRate = float64(totals.Selected) / float64(totals.Predictions)
		}
		if totals.OutcomeCnt > 0 {
			entry.AvgRealizedReturn = totals.OutcomeSum / float64(totals.OutcomeCnt)
		}
		calibration = append(calibration, entry)
	}
	sort.Slice(calibration, func(i, j int) bool { return calibration[i].Bucket < calibration[j].Bucket })

	featureDrift, _ := buildFeatureDriftSummary(context.ModelVersion, since)
	regimeSummary, _ := buildRegimeSummary(since)

	selectionRate, labelCoverage := 0.0, 0.0
	if len(cohorts) > 0 {
		selectionRate = float64(selectedCount) / float64(len(cohorts))
		labelCoverage = float64(maturedLabels) / float64(len(cohorts))
	}

	return MonitoringSummary{
		SnapshotTime:            time.Now().UTC(),
		ModelVersion:            context.ModelVersion,
		PolicyVersion:           context.PolicyVersions.CompositeVersion,
		RolloutState:            context.RolloutState,
		UniverseMode:            context.UniverseMode,
		ExperimentID:            context.ExperimentID,
		PredictionCount:         len(cohorts),
		DecisionCount:           len(cohorts),
		MaturedLabelCount:       maturedLabels,
		PendingLabelCount:       pendingLabels,
		UnavailableLabelCount:   unavailableLabels,
		LabelCoverage:           labelCoverage,
		SelectionRate:           selectionRate,
		PredictionCountsByModel: predictionCountsByModel,
		RankBuckets:             rankBuckets,
		Calibration:             calibration,
		FeatureDrift:            featureDrift,
		RegimeSummary:           regimeSummary,
	}, nil
}

func persistMonitoringSnapshot(summary MonitoringSummary) error {
	if database.DB == nil {
		return nil
	}
	calibrationJSON, _ := json.Marshal(summary.Calibration)
	rankJSON, _ := json.Marshal(summary.RankBuckets)
	driftJSON, _ := json.Marshal(summary.FeatureDrift)
	regimeJSON, _ := json.Marshal(summary.RegimeSummary)
	record := database.MonitoringSnapshot{
		SnapshotTime:      summary.SnapshotTime,
		ModelVersion:      summary.ModelVersion,
		PolicyVersion:     summary.PolicyVersion,
		RolloutState:      summary.RolloutState,
		UniverseMode:      summary.UniverseMode,
		ExperimentID:      stringPtr(summary.ExperimentID),
		PredictionCount:   summary.PredictionCount,
		DecisionCount:     summary.DecisionCount,
		MaturedLabelCount: summary.MaturedLabelCount,
		PendingLabelCount: summary.PendingLabelCount,
		UnavailableCount:  summary.UnavailableLabelCount,
		LabelCoverage:     summary.LabelCoverage,
		SelectionRate:     summary.SelectionRate,
		CalibrationJSON:   string(calibrationJSON),
		RankBucketJSON:    string(rankJSON),
		FeatureDriftJSON:  string(driftJSON),
		RegimeSummaryJSON: string(regimeJSON),
	}
	return database.DB.Create(&record).Error
}

func ensureBucket(target map[string]*bucketTotals, bucket string) *bucketTotals {
	if target[bucket] == nil {
		target[bucket] = &bucketTotals{}
	}
	return target[bucket]
}

type bucketTotals struct {
	Predictions int
	Selected    int
	ProbTotal   float64
	OutcomeSum  float64
	OutcomeCnt  int
}

func buildFeatureDriftSummary(modelVersion string, since time.Time) ([]FeatureDriftMetric, error) {
	if database.DB == nil || strings.TrimSpace(modelVersion) == "" {
		return nil, nil
	}
	artifact, err := LoadModelArtifact(modelVersion)
	if err != nil || artifact == nil {
		return nil, err
	}
	var snapshots []database.FeatureSnapshot
	if err := database.DB.Where("model_version = ? AND snapshot_time >= ?", modelVersion, since).Order("snapshot_time DESC").Limit(200).Find(&snapshots).Error; err != nil {
		return nil, err
	}
	if len(snapshots) == 0 {
		return nil, nil
	}
	type featureAggregate struct {
		Sum   float64
		Count int
	}
	aggregates := make(map[string]*featureAggregate)
	for _, snapshot := range snapshots {
		values := map[string]float64{}
		if err := json.Unmarshal([]byte(snapshot.FeaturesJSON), &values); err != nil {
			continue
		}
		for _, feature := range artifact.Features {
			value, ok := values[feature.Name]
			if !ok {
				continue
			}
			agg := aggregates[feature.Name]
			if agg == nil {
				agg = &featureAggregate{}
				aggregates[feature.Name] = agg
			}
			agg.Sum += value
			agg.Count++
		}
	}
	results := make([]FeatureDriftMetric, 0, len(artifact.Features))
	for _, feature := range artifact.Features {
		agg := aggregates[feature.Name]
		if agg == nil || agg.Count == 0 {
			continue
		}
		currentMean := agg.Sum / float64(agg.Count)
		zScore := 0.0
		if feature.Std != 0 {
			zScore = (currentMean - feature.Mean) / feature.Std
		}
		results = append(results, FeatureDriftMetric{
			Feature:      feature.Name,
			SampleCount:  agg.Count,
			BaselineMean: feature.Mean,
			CurrentMean:  currentMean,
			ZScore:       zScore,
		})
	}
	return selectLargestAbsoluteDrift(results, 8), nil
}

func selectLargestAbsoluteDrift(results []FeatureDriftMetric, limit int) []FeatureDriftMetric {
	sort.Slice(results, func(i, j int) bool {
		left, right := math.Abs(results[i].ZScore), math.Abs(results[j].ZScore)
		if left == right {
			return results[i].Feature < results[j].Feature
		}
		return left > right
	})
	if limit > 0 && len(results) > limit {
		return results[:limit]
	}
	return results
}

func buildRegimeSummary(since time.Time) (map[string]interface{}, error) {
	if database.DB == nil {
		return nil, nil
	}
	var snapshots []database.UniverseSnapshot
	if err := database.DB.Where("snapshot_time >= ?", since).Order("snapshot_time DESC").Limit(500).Find(&snapshots).Error; err != nil {
		return nil, err
	}
	if len(snapshots) == 0 {
		return nil, nil
	}
	counts := make(map[string]int)
	breadthValues := make([]float64, 0, len(snapshots))
	for _, snapshot := range snapshots {
		counts[snapshot.RegimeState]++
		breadthValues = append(breadthValues, snapshot.BreadthRatio)
	}
	return map[string]interface{}{
		"counts":        counts,
		"avg_breadth":   meanValue(breadthValues),
		"snapshots":     len(snapshots),
		"latest_regime": snapshots[0].RegimeState,
	}, nil
}

func meanValue(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

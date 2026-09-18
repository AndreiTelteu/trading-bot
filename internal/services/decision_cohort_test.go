package services

import (
	"testing"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/testutil"

	"gorm.io/gorm"
)

func TestDecisionCohortCapturesRejectedOpportunityAndLateFixedHorizonLabel(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	database.DB = db
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	cohort := fixtureDecisionCohort(base, false)
	if err := db.Create(&cohort).Error; err != nil {
		t.Fatal(err)
	}
	createDecisionCohortAsset(t, db, base)
	if _, err := ProcessMatureDecisionCohorts(base.Add(23*time.Hour), 10); err != nil {
		t.Fatalf("unmatured processing error = %v", err)
	}
	var pending database.DecisionCohort
	if err := db.First(&pending, "decision_id = ?", cohort.DecisionID).Error; err != nil {
		t.Fatal(err)
	}
	if pending.OutcomeStatus != DecisionOutcomePending || pending.OutcomeReturn != nil {
		t.Fatalf("unmatured cohort was labeled: %+v", pending)
	}

	if err := db.Create(&database.ExchangeSymbol{ID: "binance:ETHUSDT", VenueID: "binance", Ticker: "ETHUSDT", AssetID: "ETH", BaseAssetID: "ETH", QuoteAssetID: "USDT", ListedAt: base.Add(-24 * time.Hour), Version: 1, Source: "test", ProvenanceJSON: "{}", AvailableAt: base.Add(-24 * time.Hour), RetrievedAt: base.Add(-24 * time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.HistoricalBar{ExchangeSymbolID: "binance:ETHUSDT", Timeframe: "15m", OpenTime: base.Add(24 * time.Hour), DatasetVersion: "runtime-label-v1", Role: "decision", Open: "110", High: "111", Low: "109", Close: "110", Volume: "1", QuoteVolume: "110", QualityStatus: "valid", QualityFlagsJSON: "[]", Source: "test", ProvenanceJSON: "{}", AvailableAt: base.Add(24*time.Hour + 15*time.Minute), RetrievedAt: base.Add(24*time.Hour + 15*time.Minute), ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessMatureDecisionCohorts(base.Add(24*time.Hour+16*time.Minute), 10); err != nil {
		t.Fatalf("late-label processing error = %v", err)
	}
	var labeled database.DecisionCohort
	if err := db.First(&labeled, "decision_id = ?", cohort.DecisionID).Error; err != nil {
		t.Fatal(err)
	}
	if labeled.Accepted || labeled.OutcomeStatus != DecisionOutcomeLabeled || labeled.OutcomeReturn == nil || *labeled.OutcomeReturn <= 0 || labeled.OutcomeProfitable == nil || !*labeled.OutcomeProfitable {
		t.Fatalf("rejected cohort/late label = %+v", labeled)
	}
}

func TestDecisionCohortRetryIsIdempotentAfterUnavailableOutcome(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	database.DB = db
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	cohort := fixtureDecisionCohort(base, true)
	if err := db.Create(&cohort).Error; err != nil {
		t.Fatal(err)
	}
	createDecisionCohortAsset(t, db, base)
	if _, err := ProcessMatureDecisionCohorts(base.Add(25*time.Hour), 10); err != nil {
		t.Fatal(err)
	}
	var unavailable database.DecisionCohort
	if err := db.First(&unavailable, "decision_id = ?", cohort.DecisionID).Error; err != nil {
		t.Fatal(err)
	}
	if unavailable.OutcomeStatus != DecisionOutcomeUnavailable || unavailable.LabelAttempts != 1 || unavailable.OutcomeReturn != nil {
		t.Fatalf("missing label state = %+v", unavailable)
	}
	if err := db.Create(&database.ExchangeSymbol{ID: "binance:ETHUSDT", VenueID: "binance", Ticker: "ETHUSDT", AssetID: "ETH", BaseAssetID: "ETH", QuoteAssetID: "USDT", ListedAt: base.Add(-24 * time.Hour), Version: 1, Source: "test", ProvenanceJSON: "{}", AvailableAt: base.Add(-24 * time.Hour), RetrievedAt: base.Add(-24 * time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.HistoricalBar{ExchangeSymbolID: "binance:ETHUSDT", Timeframe: "15m", OpenTime: base.Add(24 * time.Hour), DatasetVersion: "runtime-label-v1", Role: "decision", Open: "90", High: "91", Low: "89", Close: "90", Volume: "1", QuoteVolume: "90", QualityStatus: "valid", QualityFlagsJSON: "[]", Source: "test", ProvenanceJSON: "{}", AvailableAt: base.Add(24*time.Hour + 15*time.Minute), RetrievedAt: base.Add(24*time.Hour + 15*time.Minute), ContentHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := ProcessMatureDecisionCohorts(base.Add(26*time.Hour), 10); err != nil {
			t.Fatal(err)
		}
	}
	var got database.DecisionCohort
	if err := db.First(&got, "decision_id = ?", cohort.DecisionID).Error; err != nil {
		t.Fatal(err)
	}
	if got.OutcomeStatus != DecisionOutcomeLabeled || got.LabelAttempts != 2 || got.OutcomeReturn == nil {
		t.Fatalf("retry did not produce one durable label: %+v", got)
	}
	var count int64
	if err := db.Model(&database.DecisionCohort{}).Where("decision_id = ?", cohort.DecisionID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("duplicate retry cohort count=%d err=%v", count, err)
	}
}

func TestDecisionCohortCreationIsAtomicAndReusesDuplicateDecisionIdentity(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	database.DB = db
	digest := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if err := db.Create(&database.ModelArtifact{Version: "cohort-model-v1", ModelDigest: digest, ArtifactChecksum: digest}).Error; err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	observations := []modelObservation{{Symbol: "ETHUSDT", Prediction: ModelPrediction{ModelVersion: "cohort-model-v1", Probability: .6, ExpectedValue: .01, RawScore: .2}, Rank: 1, Selected: true, DecisionResult: "selected", DecisionTime: at, EntryPrice: 100, FeatureSpec: ModelFeatureSpecVersion}}
	governance := GovernanceContext{UniverseMode: UniverseModeDynamic, RolloutState: ModelRolloutShadow}
	governance.PolicyVersions.CompositeVersion = "policy-v1"
	policy := decisionLabelPolicy{Horizon: 24 * time.Hour, RoundTripCostBPS: 30, CostModelVersion: "paper-round-trip-bps:30"}
	if _, err := persistPredictionLogs(observations, nil, governance, policy); err != nil {
		t.Fatal(err)
	}
	if _, err := persistPredictionLogs(observations, nil, governance, policy); err != nil {
		t.Fatalf("idempotent duplicate decision cohort error = %v", err)
	}
	var predictions, cohorts int64
	if err := db.Model(&database.PredictionLog{}).Count(&predictions).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&database.DecisionCohort{}).Count(&cohorts).Error; err != nil {
		t.Fatal(err)
	}
	if predictions != 1 || cohorts != 1 {
		t.Fatalf("duplicate write was not atomic: predictions=%d cohorts=%d", predictions, cohorts)
	}
}

func TestMonitoringUsesOnlyMaturedLabelsAndKeepsNegativeDrift(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	database.DB = db
	base := time.Now().UTC().Add(-time.Hour)
	for _, cohort := range []database.DecisionCohort{
		fixtureLabeledCohort(base, "one", .8, true, .02),
		fixtureLabeledCohort(base.Add(time.Minute), "two", .2, false, -.02),
		fixtureDecisionCohort(base.Add(2*time.Minute), false),
	} {
		if err := db.Create(&cohort).Error; err != nil {
			t.Fatal(err)
		}
	}
	summary, err := BuildMonitoringSummary(30)
	if err != nil {
		t.Fatal(err)
	}
	if summary.DecisionCount != 3 || summary.MaturedLabelCount != 2 || summary.LabelCoverage != 2.0/3.0 {
		t.Fatalf("coverage = %+v", summary)
	}
	if len(summary.Calibration) != 2 {
		t.Fatalf("calibration included unmatured cohort: %+v", summary.Calibration)
	}
	drift := selectLargestAbsoluteDrift([]FeatureDriftMetric{{Feature: "positive", ZScore: 2}, {Feature: "negative", ZScore: -5}}, 1)
	if len(drift) != 1 || drift[0].Feature != "negative" {
		t.Fatalf("negative drift was hidden: %+v", drift)
	}
}

func fixtureDecisionCohort(at time.Time, accepted bool) database.DecisionCohort {
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	return database.DecisionCohort{DecisionID: decisionCohortIdentity(at, "ETHUSDT", 24*time.Hour, "policy-v1", digest, "paper-round-trip-bps:30"), DecisionTime: at, Symbol: "ETHUSDT", HorizonSeconds: int64((24 * time.Hour).Seconds()), MaturityTime: at.Add(24 * time.Hour), ModelVersion: DefaultActiveModelVersion, ModelArtifactDigest: digest, PolicyVersion: "policy-v1", CostModelVersion: "paper-round-trip-bps:30", FeatureSpecVersion: ModelFeatureSpecVersion, EntryReferencePrice: 100, RoundTripCostBPS: 30, PredictedProbability: .6, PredictedEV: .01, RawScore: .2, Rank: 2, RankBucket: "rank_2_3", ProbabilityBucket: "0_60_to_0_69", Accepted: accepted, DecisionResult: "rejected", RolloutState: ModelRolloutShadow, OutcomeStatus: DecisionOutcomePending, PolicyContextJSON: "{}"}
}

func fixtureLabeledCohort(at time.Time, suffix string, probability float64, profitable bool, outcome float64) database.DecisionCohort {
	c := fixtureDecisionCohort(at, profitable)
	_ = suffix // The timestamp is part of the deterministic identity.
	c.PredictedProbability = probability
	c.ProbabilityBucket = probabilityBucket(probability)
	c.OutcomeStatus = DecisionOutcomeLabeled
	c.OutcomeReturn = &outcome
	c.OutcomeProfitable = &profitable
	price := 100 * (1 + outcome)
	c.OutcomePrice = &price
	recordedAt := at.Add(24 * time.Hour)
	c.OutcomeRecordedAt = &recordedAt
	return c
}

func createDecisionCohortAsset(t *testing.T, db *gorm.DB, at time.Time) {
	t.Helper()
	assets := []database.Asset{
		{ID: "ETH", CanonicalCode: "ETH", Name: "Ether", Source: "test", ProvenanceJSON: "{}", AvailableAt: at.Add(-24 * time.Hour), RetrievedAt: at.Add(-24 * time.Hour)},
		{ID: "USDT", CanonicalCode: "USDT", Name: "Tether", Source: "test", ProvenanceJSON: "{}", AvailableAt: at.Add(-24 * time.Hour), RetrievedAt: at.Add(-24 * time.Hour)},
	}
	if err := db.Create(&assets).Error; err != nil {
		t.Fatal(err)
	}
}

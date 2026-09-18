package research

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/services"
	"trading-go/internal/testutil"
)

func TestBuildProposalDatasetUsesOnlyLabeledManifestBoundCohorts(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	at := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	manifestID := strings.Repeat("a", 64)
	otherManifestID := strings.Repeat("b", 64)
	if err := db.Create(&database.DatasetManifest{ID: manifestID, ContentHash: manifestID, SchemaVersion: "point-in-time-dataset-manifest-v2", DatasetVersion: "research-v1", RequestedStart: at.Add(-time.Hour), RequestedEnd: at.Add(time.Hour), EffectiveStart: at.Add(-time.Hour), EffectiveEnd: at.Add(time.Hour), KnowledgeCutoff: at, Source: "test", BuildVersion: "test"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.DatasetManifest{ID: otherManifestID, ContentHash: otherManifestID, SchemaVersion: "point-in-time-dataset-manifest-v2", DatasetVersion: "research-v2", RequestedStart: at.Add(-time.Hour), RequestedEnd: at.Add(time.Hour), EffectiveStart: at.Add(-time.Hour), EffectiveEnd: at.Add(time.Hour), KnowledgeCutoff: at, Source: "test", BuildVersion: "test"}).Error; err != nil {
		t.Fatal(err)
	}
	bound := database.UniverseSnapshot{SnapshotTime: at, PolicyVersion: "policy-v1", DatasetManifestID: &manifestID, CoverageState: "complete", CoverageJSON: "[]", CandidatePoolJSON: "[]", RebalanceInterval: "24h", RegimeState: "risk_on"}
	other := database.UniverseSnapshot{SnapshotTime: at, PolicyVersion: "policy-v1", DatasetManifestID: &otherManifestID, CoverageState: "complete", CoverageJSON: "[]", CandidatePoolJSON: "[]", RebalanceInterval: "24h", RegimeState: "risk_on"}
	if err := db.Create(&bound).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	features := modelFeaturesJSON(t)
	quality := "[]"
	good := database.FeatureSnapshot{SnapshotTime: at, Symbol: "AAAUSDT", UniverseSnapshotID: &bound.ID, ModelVersion: "model-v1", PolicyVersion: "policy-v1", UniverseMode: "dynamic_replay", FeatureSpecVersion: services.ModelFeatureSpecVersion, LastPrice: 100, FeaturesJSON: features, QualityFlagsJSON: quality}
	notBound := database.FeatureSnapshot{SnapshotTime: at, Symbol: "BBBUSDT", UniverseSnapshotID: &other.ID, ModelVersion: "model-v1", PolicyVersion: "policy-v1", UniverseMode: "dynamic_replay", FeatureSpecVersion: services.ModelFeatureSpecVersion, LastPrice: 100, FeaturesJSON: features, QualityFlagsJSON: quality}
	if err := db.Create(&good).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&notBound).Error; err != nil {
		t.Fatal(err)
	}
	returnValue := 0.02
	profitable := true
	outcomePrice := 102.0
	recordedAt := at.Add(24 * time.Hour)
	for _, cohort := range []database.DecisionCohort{
		{DecisionID: strings.Repeat("c", 64), FeatureSnapshotID: &good.ID, UniverseSnapshotID: &bound.ID, DecisionTime: at, Symbol: good.Symbol, HorizonSeconds: 24 * 60 * 60, MaturityTime: at.Add(24 * time.Hour), ModelVersion: "model-v1", ModelArtifactDigest: strings.Repeat("d", 64), PolicyVersion: "policy-v1", CostModelVersion: "paper-round-trip-bps:30", FeatureSpecVersion: services.ModelFeatureSpecVersion, EntryReferencePrice: 100, PredictedProbability: .6, PredictedEV: .01, Rank: 1, RankBucket: "rank_1", ProbabilityBucket: "0_60_to_0_69", DecisionResult: "accepted", RolloutState: "shadow", UniverseMode: "dynamic_replay", PolicyContextJSON: "{}", OutcomeStatus: "labeled", OutcomeReturn: &returnValue, OutcomeProfitable: &profitable, OutcomePrice: &outcomePrice, OutcomeRecordedAt: &recordedAt},
		{DecisionID: strings.Repeat("e", 64), FeatureSnapshotID: &notBound.ID, UniverseSnapshotID: &other.ID, DecisionTime: at, Symbol: notBound.Symbol, HorizonSeconds: 24 * 60 * 60, MaturityTime: at.Add(24 * time.Hour), ModelVersion: "model-v1", ModelArtifactDigest: strings.Repeat("d", 64), PolicyVersion: "policy-v1", CostModelVersion: "paper-round-trip-bps:30", FeatureSpecVersion: services.ModelFeatureSpecVersion, EntryReferencePrice: 100, PredictedProbability: .6, PredictedEV: .01, Rank: 1, RankBucket: "rank_1", ProbabilityBucket: "0_60_to_0_69", DecisionResult: "accepted", RolloutState: "shadow", UniverseMode: "dynamic_replay", PolicyContextJSON: "{}", OutcomeStatus: "labeled", OutcomeReturn: &returnValue, OutcomeProfitable: &profitable, OutcomePrice: &outcomePrice, OutcomeRecordedAt: &recordedAt},
		{DecisionID: strings.Repeat("1", 64), FeatureSnapshotID: &good.ID, UniverseSnapshotID: &other.ID, DecisionTime: at, Symbol: good.Symbol, HorizonSeconds: 24 * 60 * 60, MaturityTime: at.Add(24 * time.Hour), ModelVersion: "model-v1", ModelArtifactDigest: strings.Repeat("d", 64), PolicyVersion: "policy-v1", CostModelVersion: "paper-round-trip-bps:30", FeatureSpecVersion: services.ModelFeatureSpecVersion, EntryReferencePrice: 100, PredictedProbability: .6, PredictedEV: .01, Rank: 1, RankBucket: "rank_1", ProbabilityBucket: "0_60_to_0_69", DecisionResult: "accepted", RolloutState: "shadow", UniverseMode: "dynamic_replay", PolicyContextJSON: "{}", OutcomeStatus: "labeled", OutcomeReturn: &returnValue, OutcomeProfitable: &profitable, OutcomePrice: &outcomePrice, OutcomeRecordedAt: &recordedAt},
		{DecisionID: strings.Repeat("f", 64), FeatureSnapshotID: &good.ID, UniverseSnapshotID: &bound.ID, DecisionTime: at, Symbol: good.Symbol, HorizonSeconds: 24 * 60 * 60, MaturityTime: at.Add(24 * time.Hour), ModelVersion: "model-v1", ModelArtifactDigest: strings.Repeat("d", 64), PolicyVersion: "policy-v1", CostModelVersion: "paper-round-trip-bps:30", FeatureSpecVersion: services.ModelFeatureSpecVersion, EntryReferencePrice: 100, PredictedProbability: .6, PredictedEV: .01, Rank: 1, RankBucket: "rank_1", ProbabilityBucket: "0_60_to_0_69", DecisionResult: "accepted", RolloutState: "shadow", UniverseMode: "dynamic_replay", PolicyContextJSON: "{}", OutcomeStatus: "unavailable"},
	} {
		if err := db.Create(&cohort).Error; err != nil {
			t.Fatal(err)
		}
	}

	dataset, err := BuildProposalDataset(db, ProposalDatasetRequest{DatasetManifestID: manifestID, Start: at.Add(-time.Minute), End: at.Add(time.Minute), LabelHorizon: 24 * time.Hour, FeatureSpecVersion: services.ModelFeatureSpecVersion, LabelSpecVersion: FixedHorizonAfterCostLabelSpecVersion, PolicyVersion: "policy-v1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(dataset.Rows) != 1 || dataset.Rows[0].Symbol != good.Symbol || dataset.Rows[0].OutcomeReturn != returnValue {
		t.Fatalf("dataset rows = %+v", dataset.Rows)
	}
	if dataset.Manifest.DatasetManifestID != manifestID || dataset.Manifest.RowCount != 1 {
		t.Fatalf("manifest = %+v", dataset.Manifest)
	}
}

func TestWriteProposalDatasetIsImmutableAndHashBound(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	dataset := ProposalDataset{Manifest: ProposalDatasetManifest{SchemaVersion: ProposalDatasetSchemaVersion, DatasetManifestID: strings.Repeat("a", 64), DatasetManifestHash: strings.Repeat("a", 64), FeatureSpecVersion: services.ModelFeatureSpecVersion, LabelSpecVersion: FixedHorizonAfterCostLabelSpecVersion, LabelHorizonSeconds: 86400, PolicyVersion: "policy-v1", Start: at, End: at.Add(time.Hour), FeatureNames: services.ModelFeatureNames(), RowsFile: ProposalDatasetRowsFilename, RowCount: 1}, Rows: []ProposalDatasetRow{{SchemaVersion: ProposalDatasetRowSchemaVersion, DecisionID: strings.Repeat("b", 64), DecisionTime: at, Symbol: "AAAUSDT", FeatureSnapshotID: 1, UniverseSnapshotID: 1, PolicyVersion: "policy-v1", FeatureSpecVersion: services.ModelFeatureSpecVersion, LabelSpecVersion: FixedHorizonAfterCostLabelSpecVersion, LabelHorizonSecs: 86400, CostModelVersion: "paper-round-trip-bps:30", OutcomeReturn: .01, Profitable: true, Values: modelFeaturesMap()}}}
	dir := filepath.Join(t.TempDir(), "export")
	manifest, err := WriteProposalDataset(dir, dataset)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.RowCount != 1 || manifest.RowsSHA256 == "" || manifest.ContentDigest == "" {
		t.Fatalf("manifest = %+v", manifest)
	}
	if _, err := os.Stat(filepath.Join(dir, ProposalDatasetRowsFilename)); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteProposalDataset(dir, dataset); err == nil {
		t.Fatal("rewriting immutable export unexpectedly succeeded")
	}
}

func modelFeaturesJSON(t *testing.T) string {
	t.Helper()
	bytes, err := json.Marshal(modelFeaturesMap())
	if err != nil {
		t.Fatal(err)
	}
	return string(bytes)
}

func modelFeaturesMap() map[string]float64 {
	values := make(map[string]float64, len(services.ModelFeatureNames()))
	for i, name := range services.ModelFeatureNames() {
		values[name] = float64(i + 1)
	}
	return values
}

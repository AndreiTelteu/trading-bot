package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"trading-go/internal/config"
	"trading-go/internal/cutover"
	"trading-go/internal/database"
	"trading-go/internal/pointintime"
	"trading-go/internal/services"
)

type publicClient struct{}

func (publicClient) FetchBars(ctx context.Context, ticker, frame string, start, end time.Time, limit int) ([]pointintime.Bar, error) {
	values, err := services.GetExchange().FetchOHLCVPage(ticker, frame, start, end, limit)
	if err != nil {
		return nil, err
	}
	out := make([]pointintime.Bar, len(values))
	for idx, v := range values {
		out[idx] = pointintime.Bar{OpenTime: time.UnixMilli(v.OpenTime), Open: fmt.Sprint(v.Open), High: fmt.Sprint(v.High), Low: fmt.Sprint(v.Low), Close: fmt.Sprint(v.Close), Volume: fmt.Sprint(v.Volume), QuoteVolume: fmt.Sprint(v.Close * v.Volume), Quality: "valid", Provenance: map[string]string{"endpoint": "public_klines"}}
	}
	return out, nil
}

func main() {
	action := flag.String("action", "coverage", "ingest|import-metadata|build-manifest|coverage|build-universe|build-universe-range")
	manifestID := flag.String("manifest-id", "", "")
	dataset := flag.String("dataset-version", "", "")
	symbolID := flag.String("symbol-id", "", "")
	ticker := flag.String("symbol", "", "")
	symbols := flag.String("symbols", "", "")
	frame := flag.String("timeframe", "15m", "")
	role := flag.String("role", pointintime.RoleDecision, "")
	startText := flag.String("start", "", "")
	endText := flag.String("end", "", "")
	source := flag.String("source", "binance-public", "")
	dryRun := flag.Bool("dry-run", true, "")
	retries := flag.Int("retries", 3, "")
	rate := flag.Duration("rate-limit", 250*time.Millisecond, "")
	policyVersion := flag.String("policy-version", "", "")
	benchmarkID := flag.String("benchmark-symbol-id", "", "")
	benchmarkAsset := flag.String("benchmark-asset-id", "", "")
	metadataFile := flag.String("metadata-file", "", "JSON envelope containing assets, symbols, tradability_intervals, and constraints")
	knowledgeCutoffText := flag.String("knowledge-cutoff", "", "deterministic retrieval cutoff (RFC3339)")
	step := flag.Duration("step", 24*time.Hour, "snapshot range step")
	flag.Parse()
	cfg, loadErr := config.LoadValidated()
	if loadErr != nil {
		fatal(loadErr)
	}
	if err := cutover.Activate(cfg.Stage08Flags); err != nil {
		fatal(err)
	}
	if *action != "coverage" && cfg.Stage08Flags.PointInTime == "off" {
		fatal(fmt.Errorf("Stage 04 mutation/build requires STAGE08_POINT_IN_TIME_UNIVERSE=research or authoritative"))
	}
	if err := database.Initialize(cfg); err != nil {
		fatal(err)
	}
	services.InitTradingService(cfg.BinanceAPIKey, cfg.BinanceSecret)
	start := parseTime(*startText)
	end := parseTime(*endText)
	knowledgeCutoff := parseTime(*knowledgeCutoffText)
	switch *action {
	case "ingest":
		result, err := (pointintime.Ingester{DB: database.DB, Client: publicClient{}}).Run(context.Background(), pointintime.IngestRequest{DatasetVersion: *dataset, ExchangeSymbolID: *symbolID, Ticker: strings.ToUpper(*ticker), Timeframe: *frame, Role: *role, Source: *source, Start: start, End: end, DryRun: *dryRun, MaxRetries: *retries, RateLimit: *rate})
		output(result, err)
	case "import-metadata":
		if *metadataFile == "" {
			fatal(fmt.Errorf("metadata-file is required"))
		}
		payload, err := os.ReadFile(*metadataFile)
		if err != nil {
			fatal(err)
		}
		var envelope struct {
			Assets      []database.Asset                   `json:"assets"`
			Symbols     []database.ExchangeSymbol          `json:"symbols"`
			Tradability []database.TradabilityInterval     `json:"tradability_intervals"`
			Constraints []database.SymbolConstraintVersion `json:"constraints"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			fatal(err)
		}
		err = pointintime.IngestMetadata(database.DB, pointintime.MetadataIngestRequest{Assets: envelope.Assets, Symbols: envelope.Symbols, Tradability: envelope.Tradability, Constraints: envelope.Constraints, Start: start, End: end, DryRun: *dryRun})
		output(map[string]any{"schema_version": "point-in-time-metadata-import-v1", "dry_run": *dryRun, "assets": len(envelope.Assets), "symbols": len(envelope.Symbols), "tradability_intervals": len(envelope.Tradability), "constraints": len(envelope.Constraints)}, err)
	case "coverage":
		exact := []pointintime.SeriesKey{}
		if *symbolID != "" {
			exact = append(exact, pointintime.SeriesKey{ExchangeSymbolID: *symbolID, Role: *role, Timeframe: *frame})
		}
		_, report, err := pointintime.ValidateManifest(database.DB, pointintime.ManifestRequirement{ManifestID: *manifestID, DatasetVersion: *dataset, Start: start, End: end, Symbols: split(*symbols), Series: exact, Roles: map[string]string{*role: *frame}, RequireComplete: true})
		output(report, err)
	case "build-manifest":
		manifest, err := pointintime.BuildManifest(database.DB, pointintime.BuildRequest{DatasetVersion: *dataset, RequestedStart: start, RequestedEnd: end, KnowledgeCutoff: knowledgeCutoff, SymbolIDs: splitRaw(*symbols), Source: *source})
		output(manifest, err)
	case "build-universe":
		result, err := pointintime.BuildUniverseSnapshot(database.DB, pointintime.UniverseBuildRequest{ManifestID: *manifestID, EffectiveAt: end, PolicyVersion: *policyVersion, Policy: services.GetUniversePolicy(services.GetAllSettings()), BenchmarkSymbolID: *benchmarkID, BenchmarkAssetID: *benchmarkAsset})
		output(result, err)
	case "build-universe-range":
		result, err := pointintime.BuildUniverseSnapshotRange(database.DB, pointintime.UniverseRangeRequest{Start: start, End: end, Step: *step, DryRun: *dryRun, Build: pointintime.UniverseBuildRequest{ManifestID: *manifestID, PolicyVersion: *policyVersion, Policy: services.GetUniversePolicy(services.GetAllSettings()), BenchmarkSymbolID: *benchmarkID, BenchmarkAssetID: *benchmarkAsset}})
		output(result, err)
	default:
		fatal(fmt.Errorf("unknown action %q", *action))
	}
}
func parseTime(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, e := time.Parse(time.RFC3339, v)
	if e != nil {
		fatal(e)
	}
	return t
}
func split(v string) []string {
	r := []string{}
	for _, x := range strings.Split(v, ",") {
		if x = strings.TrimSpace(strings.ToUpper(x)); x != "" {
			r = append(r, x)
		}
	}
	return r
}
func splitRaw(v string) []string {
	r := []string{}
	for _, x := range strings.Split(v, ",") {
		if x = strings.TrimSpace(x); x != "" {
			r = append(r, x)
		}
	}
	return r
}
func output(v any, err error) {
	payload, _ := json.Marshal(v)
	fmt.Println(string(payload))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }

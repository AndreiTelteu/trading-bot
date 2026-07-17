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
	"trading-go/internal/database"
	"trading-go/internal/pointintime"
	"trading-go/internal/services"
)

type publicClient struct{}

func (publicClient) FetchBars(ctx context.Context, ticker, frame string, start, end time.Time, limit int) ([]pointintime.Bar, error) {
	values, err := services.GetExchange().FetchOHLCVRange(ticker, frame, start, end)
	if err != nil {
		return nil, err
	}
	if len(values) > limit {
		values = values[:limit]
	}
	out := make([]pointintime.Bar, len(values))
	for idx, v := range values {
		out[idx] = pointintime.Bar{OpenTime: time.UnixMilli(v.OpenTime), Open: fmt.Sprint(v.Open), High: fmt.Sprint(v.High), Low: fmt.Sprint(v.Low), Close: fmt.Sprint(v.Close), Volume: fmt.Sprint(v.Volume), QuoteVolume: fmt.Sprint(v.Close * v.Volume), Quality: "valid", Provenance: map[string]string{"endpoint": "public_klines"}}
	}
	return out, nil
}

func main() {
	action := flag.String("action", "coverage", "ingest|build-manifest|coverage|build-universe")
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
	flag.Parse()
	cfg := config.Load()
	if err := database.Initialize(cfg); err != nil {
		fatal(err)
	}
	services.InitTradingService(cfg.BinanceAPIKey, cfg.BinanceSecret)
	start := parseTime(*startText)
	end := parseTime(*endText)
	switch *action {
	case "ingest":
		result, err := (pointintime.Ingester{DB: database.DB, Client: publicClient{}}).Run(context.Background(), pointintime.IngestRequest{DatasetVersion: *dataset, ExchangeSymbolID: *symbolID, Ticker: strings.ToUpper(*ticker), Timeframe: *frame, Role: *role, Source: *source, Start: start, End: end, DryRun: *dryRun, MaxRetries: *retries, RateLimit: *rate})
		output(result, err)
	case "coverage":
		_, report, err := pointintime.ValidateManifest(database.DB, pointintime.ManifestRequirement{ManifestID: *manifestID, DatasetVersion: *dataset, Start: start, End: end, Symbols: split(*symbols), Roles: map[string]string{*role: *frame}, RequireComplete: true})
		output(report, err)
	case "build-manifest":
		manifest, err := pointintime.BuildManifest(database.DB, pointintime.BuildRequest{DatasetVersion: *dataset, RequestedStart: start, RequestedEnd: end, SymbolIDs: splitRaw(*symbols), Source: *source})
		output(manifest, err)
	case "build-universe":
		result, err := pointintime.BuildUniverseSnapshot(database.DB, pointintime.UniverseBuildRequest{ManifestID: *manifestID, EffectiveAt: end, PolicyVersion: *policyVersion, Policy: services.GetUniversePolicy(services.GetAllSettings()), BenchmarkSymbolID: *benchmarkID, BenchmarkAssetID: *benchmarkAsset})
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

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"strconv"
	"strings"

	"trading-go/internal/backtest"
	"trading-go/internal/config"
	"trading-go/internal/cutover"
	"trading-go/internal/database"
	"trading-go/internal/services"
)

func main() {
	cfg, loadErr := config.LoadValidated()
	if loadErr != nil {
		log.Fatalf("Invalid startup configuration: %v", loadErr)
	}
	if err := cutover.Activate(cfg.Stage08Flags); err != nil {
		log.Fatal(err)
	}
	if err := database.Initialize(cfg); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	services.InitTradingService(cfg.BinanceAPIKey, cfg.BinanceSecret)

	symbols := flag.String("symbols", "", "")
	start := flag.String("start", "", "")
	end := flag.String("end", "", "")
	feeBps := flag.Float64("fee-bps", -1, "")
	slippageBps := flag.Float64("slippage-bps", -1, "")
	universeMode := flag.String("universe-mode", "", "")
	strategyID := flag.String("strategy", "", "Stage 05 registered candidate strategy ID")
	strategyVersion := flag.String("strategy-version", "", "Stage 05 strategy version")
	strategyParams := flag.String("strategy-params", "", "comma-separated key=value Stage 05 parameters")
	targetGross := flag.String("target-gross", "1", "normalized Stage 05 gross exposure decimal")
	maxNet := flag.String("max-net", "1", "normalized Stage 05 maximum net exposure decimal")
	finalPolicy := flag.String("final-policy", "liquidate", "liquidate or mark_to_market")
	flag.Parse()

	overrides := map[string]string{}
	if *symbols != "" {
		overrides["backtest_symbols"] = *symbols
	}
	if *start != "" {
		overrides["backtest_start"] = *start
	}
	if *end != "" {
		overrides["backtest_end"] = *end
	}
	if *feeBps >= 0 {
		overrides["backtest_fee_bps"] = strconv.FormatFloat(*feeBps, 'f', -1, 64)
	}
	if *slippageBps >= 0 {
		overrides["backtest_slippage_bps"] = strconv.FormatFloat(*slippageBps, 'f', -1, 64)
	}
	if *universeMode != "" {
		overrides["backtest_universe_mode"] = *universeMode
	}

	if *strategyID != "" {
		if cfg.Stage08Flags.NewBacktest != "research" {
			log.Fatal("Stage 05/06 strategy comparison requires STAGE08_NEW_BACKTEST=research")
		}
		parameters := map[string]string{}
		for _, item := range strings.Split(*strategyParams, ",") {
			if strings.TrimSpace(item) == "" {
				continue
			}
			parts := strings.SplitN(item, "=", 2)
			if len(parts) != 2 {
				log.Fatalf("Invalid strategy parameter %q", item)
			}
			parameters[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
		comparison, compareErr := backtest.RunStage05ComparisonSyncWithOverrides(backtest.Stage05RunRequest{StrategyID: *strategyID, StrategyVersion: *strategyVersion, Parameters: parameters, TargetGrossExposure: *targetGross, MaxNetExposure: *maxNet, FinalPolicy: *finalPolicy}, overrides)
		if compareErr != nil {
			log.Fatalf("Stage 05 comparison failed: %v", compareErr)
		}
		payload, marshalErr := json.MarshalIndent(comparison, "", "  ")
		if marshalErr != nil {
			log.Fatalf("Failed to encode comparison: %v", marshalErr)
		}
		fmt.Println(string(payload))
		return
	}

	summary, err := backtest.RunBacktestSyncWithOverrides(overrides)
	if err != nil {
		log.Fatalf("Backtest failed: %v", err)
	}

	payload, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		log.Fatalf("Failed to encode summary: %v", err)
	}

	fmt.Println(string(payload))
}

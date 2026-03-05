package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"strconv"

	"trading-go/internal/backtest"
	"trading-go/internal/config"
	"trading-go/internal/database"
	"trading-go/internal/services"
)

func main() {
	cfg := config.Load()
	if err := database.Initialize(cfg); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	services.InitTradingService(cfg.BinanceAPIKey, cfg.BinanceSecret)

	symbols := flag.String("symbols", "", "")
	start := flag.String("start", "", "")
	end := flag.String("end", "", "")
	feeBps := flag.Float64("fee-bps", -1, "")
	slippageBps := flag.Float64("slippage-bps", -1, "")
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

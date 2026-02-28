package cron

import (
	"log"
	"strconv"
	"strings"

	"trading-go/internal/database"
	"trading-go/internal/services"

	"github.com/robfig/cron/v3"
)

var (
	scheduler     *cron.Cron
	priceJobID    cron.EntryID
	trendingJobID cron.EntryID
	proposalJobID cron.EntryID
)

func Start() {
	scheduler = cron.New()

	// Price update every 1 minute
	scheduler.AddFunc("@every 1m", func() {
		if err := runPriceUpdate(); err != nil {
			log.Printf("Price update job failed: %v", err)
		}
	})

	// Trending analysis every 15 minutes
	scheduler.AddFunc("0 */15 * * * *", func() {
		if err := runTrendingAnalysis(); err != nil {
			log.Printf("Trending analysis job failed: %v", err)
		}
	})

	scheduler.Start()
	log.Println("Cron scheduler started")
}

func Stop() {
	if scheduler != nil {
		scheduler.Stop()
		log.Println("Cron scheduler stopped")
	}
}

func runPriceUpdate() error {
	result, err := services.UpdatePositionsPrices()
	if err != nil {
		return err
	}
	log.Printf("Price update completed: %v", result)
	return nil
}

func runTrendingAnalysis() error {
	result, err := services.AnalyzeTrendingCoins()
	if err != nil {
		return err
	}
	log.Printf("Trending analysis completed: analyzed %d coins, opened %d trades", len(result.Analyzed), result.TradesOpened)
	return nil
}

func runGenerateProposals() error {
	result, err := services.GenerateProposals()
	if err != nil {
		return err
	}
	log.Printf("AI proposal generation completed: %v", result)
	return nil
}

func getProposalInterval() string {
	var setting database.Setting
	if err := database.DB.First(&setting, "key = ?", "ai_analysis_interval").Error; err != nil {
		return "disabled"
	}

	interval := setting.Value
	interval = strings.TrimSpace(interval)

	if interval == "" || interval == "0" || interval == "disabled" {
		return "disabled"
	}

	if strings.HasPrefix(interval, "@every ") {
		return interval
	}

	if strings.HasSuffix(interval, "m") {
		minutes, err := strconv.Atoi(strings.TrimSuffix(interval, "m"))
		if err != nil || minutes <= 0 {
			return "disabled"
		}
		return "0 */" + strconv.Itoa(minutes) + " * * * *"
	}

	return "disabled"
}

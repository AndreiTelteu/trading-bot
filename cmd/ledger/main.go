package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"
	"trading-go/internal/config"
	ledgerpkg "trading-go/internal/ledger"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	action := flag.String("action", "reconcile", "reconcile or backfill")
	jsonOutput := flag.Bool("json", false, "emit machine-readable JSON")
	dryRun := flag.Bool("dry-run", true, "inspect backfill without mutation (default true)")
	approval := flag.String("approval", "", "required approval phrase when dry-run=false")
	approvedBy := flag.String("approved-by", "", "human/operator approving a backfill")
	flag.Parse()

	cfg := config.Load()
	dsn, err := cfg.DatabaseDSN()
	if err != nil {
		log.Fatal(err)
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		log.Fatal(err)
	}
	service := ledgerpkg.New(db)
	ctx := context.Background()
	var output interface{}
	switch *action {
	case "reconcile":
		report, runErr := service.Reconcile(ctx, ledgerpkg.DefaultAccountID, time.Time{})
		if runErr != nil {
			log.Fatal(runErr)
		}
		if !*jsonOutput {
			fmt.Print(report.String())
			return
		}
		output = report
	case "backfill":
		report, runErr := service.Backfill(ctx, ledgerpkg.BackfillOptions{Apply: !*dryRun, Approval: *approval, ApprovedBy: *approvedBy})
		if runErr != nil {
			log.Fatal(runErr)
		}
		output = report
	default:
		fmt.Fprintln(os.Stderr, "action must be reconcile or backfill")
		os.Exit(2)
	}
	encoded, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(encoded))
}

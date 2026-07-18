package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"
	"trading-go/internal/accounting"
	"trading-go/internal/config"
	"trading-go/internal/database"
	ledgerpkg "trading-go/internal/ledger"
	"trading-go/internal/operations"
)

func main() {
	action := flag.String("action", "reconcile", "reconcile or backfill")
	jsonOutput := flag.Bool("json", false, "emit machine-readable JSON")
	dryRun := flag.Bool("dry-run", true, "inspect backfill without mutation (default true)")
	approval := flag.String("approval", "", "required approval phrase when dry-run=false")
	approvedBy := flag.String("approved-by", "", "human/operator approving a backfill")
	symbol := flag.String("symbol", "", "asset symbol for an approved correction")
	quantity := flag.String("quantity", "", "exact asset quantity")
	costBasis := flag.String("cost-basis", "", "exact settlement-currency cost basis")
	reason := flag.String("reason", "", "required correction/reversal reason")
	idempotencyKey := flag.String("idempotency-key", "", "stable command idempotency key")
	originalEvent := flag.String("original-event", "", "event id to reverse")
	flag.Parse()

	cfg, err := config.LoadValidated()
	if err != nil {
		log.Fatal(err)
	}
	if err := database.OpenCommandPools(cfg, ledgerPoolRequirements(*action)); err != nil {
		log.Fatal(err)
	}
	stage08 := operations.New(database.DB, cfg.Stage08Flags)
	if _, err := stage08.Initialize(context.Background()); err != nil {
		log.Fatalf("Stage 08 startup reconciliation failed: %v", err)
	}
	if err := database.SeedDataWithDefaults(cfg.DefaultBalance, cfg.DefaultCurrency); err != nil {
		log.Fatal(err)
	}
	if _, err := stage08.Initialize(context.Background()); err != nil {
		log.Fatal(err)
	}
	db := database.DB
	service := ledgerpkg.New(db)
	ctx := context.Background()
	var output interface{}
	exitCode := 0
	switch *action {
	case "reconcile":
		report, runErr := service.Reconcile(ctx, ledgerpkg.DefaultAccountID, time.Time{})
		if runErr != nil {
			log.Fatal(runErr)
		}
		exitCode = reconciliationExitCode(report.Balanced)
		if !*jsonOutput {
			fmt.Print(report.String())
			if exitCode != 0 {
				os.Exit(exitCode)
			}
			return
		}
		output = report
	case "backfill":
		report, runErr := service.Backfill(ctx, ledgerpkg.BackfillOptions{Apply: !*dryRun, Approval: *approval, ApprovedBy: *approvedBy})
		if runErr != nil {
			log.Fatal(runErr)
		}
		output = report
	case "asset-correction":
		if *approval != "APPROVE_LEDGER_ASSET_CORRECTION" || *approvedBy == "" {
			log.Fatal("asset correction requires approval APPROVE_LEDGER_ASSET_CORRECTION and approved-by")
		}
		qty, parseErr := accounting.Parse(*quantity)
		if parseErr != nil {
			log.Fatal(parseErr)
		}
		basis, parseErr := accounting.Parse(*costBasis)
		if parseErr != nil {
			log.Fatal(parseErr)
		}
		var wallet database.Wallet
		if queryErr := db.First(&wallet).Error; queryErr != nil {
			log.Fatal(queryErr)
		}
		event, runErr := service.ApplyAssetCorrection(ctx, ledgerpkg.AssetCorrectionCommand{IdempotencyKey: *idempotencyKey, Symbol: *symbol, Quantity: qty, CostBasis: basis, Currency: wallet.Currency, Actor: *approvedBy, Reason: *reason, Evidence: map[string]interface{}{"operator_approval": *approval}})
		if runErr != nil {
			log.Fatal(runErr)
		}
		output = event
	case "reverse":
		if *approvedBy == "" {
			log.Fatal("reversal requires approved-by")
		}
		event, runErr := service.ReverseCashEvent(ctx, ledgerpkg.ReversalCommand{IdempotencyKey: *idempotencyKey, OriginalEventID: *originalEvent, Actor: *approvedBy, Reason: *reason})
		if runErr != nil {
			log.Fatal(runErr)
		}
		output = event
	default:
		fmt.Fprintln(os.Stderr, "action must be reconcile, backfill, asset-correction, or reverse")
		os.Exit(2)
	}
	encoded, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(encoded))
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

func ledgerPoolRequirements(_ string) database.CommandPoolRequirements {
	// All ledger actions seed the opening economic boundary before execution.
	return database.CommandPoolRequirements{Migrate: true, ValidateRuntime: true, LedgerWriter: true}
}

func reconciliationExitCode(balanced bool) int {
	if balanced {
		return 0
	}
	return 2
}

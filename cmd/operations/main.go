package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"
	"trading-go/internal/config"
	"trading-go/internal/cutover"
	"trading-go/internal/database"
	"trading-go/internal/ledger"
	"trading-go/internal/operations"
)

func main() {
	action := flag.String("action", "verify", "verify or status")
	flag.Parse()
	if *action != "verify" && *action != "status" {
		fmt.Fprintln(os.Stderr, "action must be verify or status")
		os.Exit(2)
	}
	cfg, err := config.LoadValidated()
	if err != nil {
		fatal(err)
	}
	if err := cutover.Activate(cfg.Stage08Flags); err != nil {
		fatal(err)
	}
	if err := database.Initialize(cfg); err != nil {
		fatal(err)
	}
	service := operations.New(database.DB, cfg.Stage08Flags)
	if _, err := service.Initialize(context.Background()); err != nil {
		fatal(err)
	}
	if *action == "verify" {
		report, err := ledger.New(database.DB).Reconcile(context.Background(), ledger.DefaultAccountID, time.Time{})
		if err != nil {
			fatal(err)
		}
		if !report.Balanced {
			fatal(fmt.Errorf("restored ledger is unreconciled: %v", report.ActionableIssues))
		}
	}
	status := service.Status(context.Background())
	payload, _ := json.Marshal(status)
	fmt.Println(string(payload))
	if status.Status != "ok" && *action == "status" {
		os.Exit(2)
	}
}
func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }

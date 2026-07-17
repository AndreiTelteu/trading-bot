package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"
	"trading-go/internal/config"
	"trading-go/internal/database"
	"trading-go/internal/ledger"
	"trading-go/internal/operations"
)

func main() {
	action := flag.String("action", "verify", "verify, status, fingerprint, or record-backup")
	manifestPath := flag.String("manifest", "", "backup verification manifest path")
	principal := flag.String("principal", "", "trusted operations principal")
	flag.Parse()
	if *action != "verify" && *action != "status" && *action != "fingerprint" && *action != "restore-verify" && *action != "record-backup" {
		fmt.Fprintln(os.Stderr, "action must be verify, status, fingerprint, restore-verify, or record-backup")
		os.Exit(2)
	}
	cfg, err := config.LoadValidated()
	if err != nil {
		fatal(err)
	}
	open := database.OpenAndMigrate
	if *action == "fingerprint" {
		open = database.Open
	}
	if err := open(cfg); err != nil {
		fatal(err)
	}
	service := operations.New(database.DB, cfg.Stage08Flags)
	service.ReadOnly = *action == "fingerprint" || *action == "restore-verify" || *action == "record-backup"
	if _, err := service.Initialize(context.Background()); err != nil {
		fatal(err)
	}
	if *action == "fingerprint" {
		value, err := operations.FingerprintDatabase(context.Background(), database.DB)
		if err != nil {
			fatal(err)
		}
		payload, _ := json.Marshal(value)
		fmt.Println(string(payload))
		return
	}
	if *action == "restore-verify" {
		report, err := ledger.New(database.DB).Reconcile(context.Background(), ledger.DefaultAccountID, time.Time{})
		if err != nil || !report.Balanced {
			if err == nil {
				err = fmt.Errorf("restored ledger is unreconciled: %v", report.ActionableIssues)
			}
			fatal(err)
		}
		fmt.Println(`{"status":"restore_verified"}`)
		return
	}
	if *action == "record-backup" {
		if *manifestPath == "" || *principal == "" {
			fatal(fmt.Errorf("manifest and principal are required"))
		}
		payload, err := os.ReadFile(*manifestPath)
		if err != nil {
			fatal(err)
		}
		if len(payload) > 64<<10 {
			fatal(fmt.Errorf("manifest exceeds 64KiB"))
		}
		var manifest operations.BackupVerificationManifest
		if err := json.Unmarshal(payload, &manifest); err != nil {
			fatal(err)
		}
		row, err := service.RecordBackupVerification(context.Background(), manifest, *principal)
		if err != nil {
			fatal(err)
		}
		encoded, _ := json.Marshal(row)
		fmt.Println(string(encoded))
		return
	}
	if err := database.SeedDataWithDefaults(cfg.DefaultBalance, cfg.DefaultCurrency); err != nil {
		fatal(err)
	}
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

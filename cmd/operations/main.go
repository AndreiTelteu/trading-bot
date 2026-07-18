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
	var cfg *config.Config
	var err error
	if actionIgnoresLocalStage08Flags(*action) {
		cfg, err = config.LoadValidatedFromPersistedStage08Authority()
	} else {
		cfg, err = config.LoadValidated()
	}
	if err != nil {
		fatal(err)
	}
	if err := database.OpenCommandPools(cfg, operationsPoolRequirements(*action)); err != nil {
		fatal(err)
	}
	// A fingerprint is an inventory of the database as it exists.  In
	// particular, it must remain available when the cutover authority is
	// damaged, so that restore verification can report the resulting drift.
	// Do not construct or initialize an operations service on this path: either
	// initializer validates (and the normal one can bootstrap) Stage 08
	// authority.
	if stage08InitializationFor(*action) == stage08InitializationNone {
		value, err := operations.FingerprintDatabase(context.Background(), database.DB)
		if err != nil {
			fatal(err)
		}
		payload, _ := json.Marshal(value)
		fmt.Println(string(payload))
		return
	}
	service := operations.New(database.DB, cfg.Stage08Flags)
	service.ReadOnly = *action == "restore-verify" || *action == "record-backup"
	if stage08InitializationFor(*action) == stage08InitializationPersisted {
		_, err = service.InitializeFromPersistedAuthority(context.Background())
	} else {
		_, err = service.Initialize(context.Background())
	}
	if err != nil {
		fatal(err)
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

func operationsPoolRequirements(action string) database.CommandPoolRequirements {
	switch action {
	case "fingerprint", "restore-verify":
		// These are intentionally trusted operator reads. They may use an
		// administrative/service DSN and must not be constrained as runtime.
		return database.CommandPoolRequirements{Migrate: action == "restore-verify", TrustedOperator: true}
	case "record-backup":
		return database.CommandPoolRequirements{ValidateRuntime: true}
	default: // verify and status retain seeding behavior.
		return database.CommandPoolRequirements{Migrate: true, ValidateRuntime: true, LedgerWriter: true}
	}
}
func actionMigratesTarget(action string) bool {
	return action == "verify" || action == "status" || action == "restore-verify"
}
func actionUsesPersistedAuthority(action string) bool {
	return action == "restore-verify" || action == "record-backup"
}

// actionIgnoresLocalStage08Flags is intentionally broader than persisted
// initialization: fingerprint needs a valid connection configuration, but no
// local or persisted Stage 08 authority to inventory a database.
func actionIgnoresLocalStage08Flags(action string) bool {
	return action == "fingerprint" || actionUsesPersistedAuthority(action)
}

type stage08Initialization int

const (
	stage08InitializationNone stage08Initialization = iota
	stage08InitializationNormal
	stage08InitializationPersisted
)

func stage08InitializationFor(action string) stage08Initialization {
	if action == "fingerprint" {
		return stage08InitializationNone
	}
	if actionUsesPersistedAuthority(action) {
		return stage08InitializationPersisted
	}
	return stage08InitializationNormal
}
func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }

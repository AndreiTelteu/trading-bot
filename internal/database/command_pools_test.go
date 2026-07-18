package database

import (
	"strings"
	"testing"
	"trading-go/internal/config"

	"gorm.io/gorm"
)

func TestCommandPoolRequirementsFailClosedForProtectedWrites(t *testing.T) {
	cfg := &config.Config{DatabaseURL: "postgres://trading_bot_app_runtime@db/trading", MigrationDatabaseURL: "postgres://migration@db/trading"}
	if err := (CommandPoolRequirements{ValidateRuntime: true, LedgerWriter: true}).Validate(cfg); err == nil || !strings.Contains(err.Error(), "LEDGER_DATABASE_URL") {
		t.Fatalf("ledger writer requirement error = %v", err)
	}
	if err := (CommandPoolRequirements{ValidateRuntime: true, ParityWriter: true}).Validate(cfg); err == nil || !strings.Contains(err.Error(), "PARITY_DATABASE_URL") {
		t.Fatalf("parity writer requirement error = %v", err)
	}
}

func TestWriterAccessorsNeverFallBackToRuntimeDB(t *testing.T) {
	previousRuntime, previousLedger, previousParity := DB, ledgerWriterDB, parityWriterDB
	t.Cleanup(func() {
		DB, ledgerWriterDB, parityWriterDB = previousRuntime, previousLedger, previousParity
	})
	DB = &gorm.DB{}
	ledgerWriterDB, parityWriterDB = nil, nil
	if LedgerWriter() != nil || ParityWriter() != nil {
		t.Fatal("protected writer accessor fell back to runtime database")
	}
}

func TestCommandPoolRequirementsAllowTrustedReadWithoutWriterDSNs(t *testing.T) {
	cfg := &config.Config{DatabaseURL: "postgres://operator@db/trading", MigrationDatabaseURL: "postgres://migration@db/trading"}
	if err := (CommandPoolRequirements{TrustedOperator: true}).Validate(cfg); err != nil {
		t.Fatal(err)
	}
	if err := (CommandPoolRequirements{TrustedOperator: true, Migrate: true}).Validate(cfg); err != nil {
		t.Fatal(err)
	}
	if err := (CommandPoolRequirements{TrustedOperator: true, ValidateRuntime: true}).Validate(cfg); err == nil {
		t.Fatal("trusted operator flow must not silently become a runtime flow")
	}
	if err := (CommandPoolRequirements{}).Validate(cfg); err == nil || !strings.Contains(err.Error(), "runtime principal validation") {
		t.Fatalf("ordinary long-lived connection must fail closed, got %v", err)
	}
}

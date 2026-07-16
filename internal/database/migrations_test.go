package database_test

import (
	"testing"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/testutil"
)

func TestFreshLedgerMigrationAndSeedCreatesOpeningCapital(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	database.DB = db
	if err := database.SeedData(); err != nil {
		t.Fatal(err)
	}
	for _, model := range []interface{}{&database.LedgerBatch{}, &database.Fill{}, &database.LedgerEvent{}, &database.LedgerMigrationState{}} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("missing table for %T", model)
		}
	}
	var state database.LedgerMigrationState
	if err := db.First(&state, "account_id = ?", "primary").Error; err != nil {
		t.Fatal(err)
	}
	if state.Status != "ready" || state.OpeningEventID == nil {
		t.Fatalf("state=%+v", state)
	}
}

func TestUpgradeFixtureMigratesWithoutFabricatingEconomicHistory(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	database.DB = db
	if err := db.Create(&database.Wallet{ID: 1, Balance: 777, Currency: "USDT"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.Position{Symbol: "OLD", Amount: 3, AvgPrice: 9, Status: "open", OpenedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DROP TABLE ledger_events, fills, ledger_batches, ledger_migration_states CASCADE").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DELETE FROM schema_migrations WHERE id = ?", "202607160100_immutable_ledger").Error; err != nil {
		t.Fatal(err)
	}
	if err := database.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := database.SeedData(); err != nil {
		t.Fatal(err)
	}
	var events int64
	db.Model(&database.LedgerEvent{}).Count(&events)
	if events != 0 {
		t.Fatalf("upgrade fabricated %d events", events)
	}
	var state database.LedgerMigrationState
	if err := db.First(&state, "account_id = ?", "primary").Error; err != nil {
		t.Fatal(err)
	}
	if state.Status != "pending_approval" {
		t.Fatalf("status=%s", state.Status)
	}
}

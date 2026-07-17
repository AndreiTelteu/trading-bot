package database_test

import (
	"gorm.io/gorm"
	"testing"
	"trading-go/internal/database"
	"trading-go/internal/testutil"
)

func TestFreshLedgerMigrationAndSeedCreatesOpeningCapital(t *testing.T) {
	db := testutil.OpenPostgresDB(t)
	testutil.ResetPublicSchema(t, db)
	if err := database.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	database.DB = db
	if err := database.SeedDataWithDefaults(123.45, "EUR"); err != nil {
		t.Fatal(err)
	}
	var wallet database.Wallet
	if err := db.First(&wallet).Error; err != nil {
		t.Fatal(err)
	}
	if wallet.BalanceExact == nil || wallet.BalanceExact.String() != "123.45" || wallet.Currency != "EUR" {
		t.Fatalf("wallet=%+v", wallet)
	}
	var event database.LedgerEvent
	if err := db.First(&event).Error; err != nil {
		t.Fatal(err)
	}
	if event.EventType != "capital_deposit" || event.CashDelta.String() != "123.45" || event.RecordedAt.Before(event.OccurredAt) {
		t.Fatalf("event=%+v", event)
	}
	assertTrigger(t, db, "ledger_batches_immutable")
	assertTrigger(t, db, "positions_economic_guard")
	assertTrigger(t, db, "wallets_economic_guard")
}

func TestGenuinePreLedgerPopulatedSchemaUpgradeDoesNotFabricateHistory(t *testing.T) {
	db := testutil.OpenPostgresDB(t)
	testutil.ResetPublicSchema(t, db)
	baseline := []string{
		`CREATE TABLE schema_migrations (id varchar(255) PRIMARY KEY)`,
		`CREATE TABLE wallets (id bigserial PRIMARY KEY,balance double precision,currency varchar(20),created_at timestamptz,updated_at timestamptz)`,
		`CREATE TABLE positions (id bigserial PRIMARY KEY,symbol varchar(20),amount double precision,avg_price double precision,entry_price double precision,current_price double precision,pnl double precision,pnl_percent double precision,status varchar(20),opened_at timestamptz,closed_at timestamptz,close_reason varchar(50))`,
		`CREATE UNIQUE INDEX idx_positions_symbol ON positions(symbol)`,
		`CREATE INDEX idx_positions_status ON positions(status)`,
		`CREATE TABLE orders (id bigserial PRIMARY KEY,order_type varchar(10) NOT NULL,symbol varchar(20) NOT NULL,amount_crypto double precision,amount_usdt double precision,price double precision,fee double precision,executed_at timestamptz)`,
		`INSERT INTO wallets(id,balance,currency,created_at,updated_at) VALUES(1,777.125,'USDT',now(),now())`,
		`INSERT INTO positions(symbol,amount,avg_price,status,opened_at) VALUES('OPEN',3.25,9.125,'open',now()),('CLOSED',2,4,'closed',now())`,
		`UPDATE positions SET closed_at=now(),close_reason='legacy' WHERE symbol='CLOSED'`,
		`INSERT INTO orders(order_type,symbol,amount_crypto,amount_usdt,price,fee,executed_at) VALUES('buy','OPEN',3.25,29.65625,9.125,0.1,now())`,
	}
	for _, sql := range baseline {
		if err := db.Exec(sql).Error; err != nil {
			t.Fatalf("baseline %q: %v", sql, err)
		}
	}
	for _, id := range []string{"202603221700_initial_postgres_schema", "202603221830_backtest_job_summary_compact_json", "202603222100_execution_parity_fields", "202603230100_universe_selection_tables", "202603230400_learned_model_entities", "202603231200_governance_tracking_entities"} {
		if err := db.Exec("INSERT INTO schema_migrations(id) VALUES(?)", id).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := database.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	database.DB = db
	if err := database.SeedData(); err != nil {
		t.Fatal(err)
	}
	var events int64
	if err := db.Model(&database.LedgerEvent{}).Count(&events).Error; err != nil || events != 0 {
		t.Fatalf("events=%d err=%v", events, err)
	}
	var fills int64
	if err := db.Model(&database.Fill{}).Count(&fills).Error; err != nil || fills != 0 {
		t.Fatalf("fills=%d err=%v", fills, err)
	}
	var state database.LedgerMigrationState
	if err := db.First(&state, "account_id = ?", "primary").Error; err != nil {
		t.Fatal(err)
	}
	if state.Status != "pending_approval" {
		t.Fatalf("state=%+v", state)
	}
	var open, closed database.Position
	db.Where("symbol='OPEN'").First(&open)
	db.Where("symbol='CLOSED'").First(&closed)
	if open.AmountExact != nil || closed.AmountExact != nil || open.Amount != 3.25 || closed.CloseReason == nil {
		t.Fatalf("positions changed open=%+v closed=%+v", open, closed)
	}
	var order database.Order
	if err := db.First(&order).Error; err != nil || order.Symbol != "OPEN" || order.AmountCrypto != 3.25 || order.AmountUsdt != 29.65625 {
		t.Fatalf("legacy order changed: %+v err=%v", order, err)
	}
	var indexCount int64
	if err := db.Raw(`SELECT count(*) FROM pg_indexes WHERE indexname='idx_positions_symbol'`).Scan(&indexCount).Error; err != nil || indexCount != 1 {
		t.Fatalf("baseline index was not preserved: count=%d err=%v", indexCount, err)
	}
	assertTrigger(t, db, "ledger_events_immutable")
	assertTrigger(t, db, "ledger_batches_immutable")
	assertTrigger(t, db, "positions_economic_guard")
}

func TestStage03ShapedSchemaUpgradesToPointInTimeWithoutDestructiveRewrite(t *testing.T) {
	db := testutil.OpenPostgresDB(t)
	t.Cleanup(func() {
		testutil.ResetPublicSchema(t, db)
		if err := database.RunMigrations(db); err != nil {
			t.Fatalf("restore full schema after Stage 03 upgrade fixture: %v", err)
		}
	})
	testutil.ResetPublicSchema(t, db)
	for _, statement := range []string{
		`CREATE TABLE schema_migrations (id varchar(255) PRIMARY KEY)`,
		`CREATE TABLE backtest_jobs (id bigserial PRIMARY KEY,status varchar(20),progress double precision,created_at timestamptz,updated_at timestamptz)`,
		`CREATE TABLE universe_snapshots (id bigserial PRIMARY KEY,snapshot_time timestamptz,regime_state varchar(20),breadth_ratio double precision,eligible_count bigint,candidate_count bigint,ranked_count bigint,shortlist_count bigint,rebalance_interval varchar(20),created_at timestamptz,updated_at timestamptz)`,
		`CREATE TABLE universe_members (id bigserial PRIMARY KEY,universe_snapshot_id bigint,symbol varchar(20),stage varchar(20),rank_score double precision,shortlisted boolean,created_at timestamptz,updated_at timestamptz)`,
		`INSERT INTO universe_snapshots(id,snapshot_time,regime_state,created_at,updated_at) VALUES(7,'2024-01-01T00:00:00Z','neutral',now(),now())`,
		`INSERT INTO universe_members(universe_snapshot_id,symbol,stage,rank_score,shortlisted,created_at,updated_at) VALUES(7,'LEGACYUSDT','active',1,true,now(),now())`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"202603221700_initial_postgres_schema", "202603221830_backtest_job_summary_compact_json", "202603222100_execution_parity_fields", "202603230100_universe_selection_tables", "202603230400_learned_model_entities", "202603231200_governance_tracking_entities", "202607160100_immutable_ledger", "202607160200_stage01_review_remediation", "202607170200_shared_broker_outcomes"} {
		if err := db.Exec("INSERT INTO schema_migrations(id) VALUES(?)", id).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := database.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	for _, model := range []any{&database.Asset{}, &database.ExchangeSymbol{}, &database.HistoricalBar{}, &database.DatasetManifest{}, &database.IngestionCheckpoint{}} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("missing upgraded table for %T", model)
		}
	}
	var legacy database.UniverseMember
	if err := db.First(&legacy, "symbol=?", "LEGACYUSDT").Error; err != nil || legacy.UniverseSnapshotID != 7 {
		t.Fatalf("legacy snapshot member was not preserved: %+v %v", legacy, err)
	}
}

func assertTrigger(t *testing.T, db interface {
	Raw(string, ...interface{}) *gorm.DB
}, name string) {
	t.Helper()
	var count int64
	if err := db.Raw(`SELECT count(*) FROM pg_trigger WHERE tgname=? AND NOT tgisinternal`, name).Scan(&count).Error; err != nil || count != 1 {
		t.Fatalf("trigger %s count=%d err=%v", name, count, err)
	}
}

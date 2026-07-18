package database_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"trading-go/internal/accounting"
	"trading-go/internal/config"
	"trading-go/internal/database"
	ledgerpkg "trading-go/internal/ledger"
	"trading-go/internal/testutil"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMigrateRequiresDedicatedAdminURL(t *testing.T) {
	err := database.Migrate(&config.Config{})
	if err == nil || !strings.Contains(err.Error(), "MIGRATION_DATABASE_URL is required") {
		t.Fatalf("expected missing migration URL rejection, got %v", err)
	}
}

func TestRealLoginRolesEnforceRuntimeLifecycleAndWriterBoundary(t *testing.T) {
	admin := testutil.SetupPostgresDB(t)
	if err := database.SeedData(); err != nil {
		t.Fatal(err)
	}
	const runtimeLogin = "trading_bot_c1_runtime_login_test"
	const writerLogin = "trading_bot_c1_writer_login_test"
	const parityLogin = "trading_bot_c1_parity_login_test"
	const password = "role-boundary-test-only"
	for _, role := range []string{runtimeLogin, writerLogin, parityLogin} {
		_ = admin.Exec("DROP ROLE IF EXISTS " + role).Error
		inheritance := "NOINHERIT"
		if role == runtimeLogin || role == parityLogin {
			inheritance = "INHERIT"
		}
		if err := admin.Exec(fmt.Sprintf("CREATE ROLE %s LOGIN %s NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD '%s'", role, inheritance, password)).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, role := range []string{runtimeLogin, writerLogin, parityLogin} {
			_ = admin.Exec("DROP ROLE IF EXISTS " + role).Error
		}
	})
	if err := admin.Exec("GRANT trading_bot_runtime TO " + runtimeLogin + "; GRANT trading_bot_ledger_writer TO " + writerLogin + "; GRANT trading_bot_parity_writer TO " + parityLogin).Error; err != nil {
		t.Fatal(err)
	}

	runtime := openRoleLogin(t, runtimeLogin, password)
	writer := openRoleLogin(t, writerLogin, password)
	parity := openRoleLogin(t, parityLogin, password)
	defer closeRoleLogin(runtime)
	defer closeRoleLogin(writer)
	defer closeRoleLogin(parity)
	if err := database.ValidateRuntimePrincipalFor(runtime, runtimeLogin); err != nil {
		t.Fatalf("valid genuine runtime login was rejected: %v", err)
	}
	if err := database.ValidateLedgerWriterPrincipalFor(writer, writerLogin); err != nil {
		t.Fatalf("valid genuine ledger login was rejected: %v", err)
	}
	if err := database.ValidateParityWriterPrincipalFor(parity, parityLogin); err != nil {
		t.Fatalf("valid genuine parity login was rejected: %v", err)
	}

	// Exercise the complete ledger path through the genuine non-superuser login,
	// including the second fill that updates an existing position. This catches
	// omissions in the column-level writer grant without assuming synthetic ORM
	// timestamp columns that the physical Position schema does not contain.
	opened, err := ledgerpkg.New(writer).ApplyFill(context.Background(), ledgerpkg.FillCommand{IdempotencyKey: "role-boundary-open", Symbol: "ROLE", Side: "buy", Quantity: accounting.MustParse("1"), RequestedPrice: accounting.MustParse("1"), FillPrice: accounting.MustParse("1"), Fee: accounting.Zero(), FeeType: ledgerpkg.EventTradingFee, Currency: "USDT", ExecutionMode: "paper", Actor: "test", Reason: "role boundary", OccurredAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("ledger persistence through genuine writer login failed: %v", err)
	}
	if _, err := ledgerpkg.New(writer).ApplyFill(context.Background(), ledgerpkg.FillCommand{IdempotencyKey: "role-boundary-add", Symbol: "ROLE", Side: "buy", Quantity: accounting.MustParse("1"), RequestedPrice: accounting.MustParse("1"), FillPrice: accounting.MustParse("1"), Fee: accounting.Zero(), FeeType: ledgerpkg.EventTradingFee, Currency: "USDT", ExecutionMode: "paper", Actor: "test", Reason: "role boundary", OccurredAt: time.Now().UTC()}); err != nil {
		t.Fatalf("ledger update through genuine writer login failed: %v", err)
	}
	position := opened.Position

	if err := runtime.Exec("UPDATE positions SET exit_pending=true WHERE id=?", position.ID).Error; err != nil {
		t.Fatalf("runtime close claim denied: %v", err)
	}
	if err := runtime.Exec("UPDATE positions SET exit_pending=false WHERE id=?", position.ID).Error; err != nil {
		t.Fatalf("runtime close rollback denied: %v", err)
	}
	if err := runtime.Exec("UPDATE positions SET amount=999 WHERE id=?", position.ID).Error; err == nil {
		t.Fatal("runtime changed economic quantity")
	}
	if err := writer.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL ROLE trading_bot_ledger_writer").Error; err != nil {
			return err
		}
		return tx.Exec("INSERT INTO settings(key,value) VALUES('forged','true')").Error
	}); err == nil {
		t.Fatal("ledger writer fabricated settings/governance state")
	}
	var canAssumeParity bool
	if err := writer.Raw("SELECT pg_has_role(session_user, 'trading_bot_parity_writer', 'member')").Scan(&canAssumeParity).Error; err != nil {
		t.Fatal(err)
	}
	if canAssumeParity {
		t.Fatal("ledger writer login can assume parity authority")
	}
	if err := writer.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL ROLE trading_bot_ledger_writer").Error; err != nil {
			return err
		}
		return tx.Model(&database.Position{}).Where("id=?", position.ID).Update("model_version", "forged-provenance").Error
	}); err == nil {
		t.Fatal("ledger writer changed position provenance without immutable ledger evidence")
	}
	var unrestrictedPositionUpdate bool
	if err := writer.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL ROLE trading_bot_ledger_writer").Error; err != nil {
			return err
		}
		return tx.Raw("SELECT has_table_privilege(current_user,'positions','UPDATE')").Scan(&unrestrictedPositionUpdate).Error
	}); err != nil {
		t.Fatal(err)
	}
	if unrestrictedPositionUpdate {
		t.Fatal("ledger writer retained unrestricted position update authority")
	}
	for _, column := range []string{"amount_exact", "cost_basis_exact", "fees_exact", "realized_pn_l_exact"} {
		t.Run("forged_"+column, func(t *testing.T) {
			err := writer.Transaction(func(tx *gorm.DB) error {
				if err := tx.Exec("SET LOCAL ROLE trading_bot_ledger_writer").Error; err != nil {
					return err
				}
				return tx.Exec(fmt.Sprintf("UPDATE positions SET %s=999 WHERE id=?", column), position.ID).Error
			})
			if err == nil || !strings.Contains(err.Error(), "position economic projection must equal immutable ledger events written by the same transaction") {
				t.Fatalf("forged %s was not rejected by ledger coupling: %v", column, err)
			}
		})
	}
	if err := writer.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL ROLE trading_bot_ledger_writer").Error; err != nil {
			return err
		}
		return tx.Model(&database.LedgerMigrationState{}).Where("account_id='primary'").Update("unresolved_json", `["forged"]`).Error
	}); err == nil {
		t.Fatal("ledger writer changed migration state without same-transaction immutable ledger evidence")
	}
	if err := writer.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL ROLE trading_bot_ledger_writer").Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := tx.Create(&database.LedgerBatch{ID: "forged-batch", AccountID: "primary", PayloadHash: strings.Repeat("f", 64), CreatedAt: now}).Error; err != nil {
			return err
		}
		zero := accounting.Zero()
		return tx.Create(&database.LedgerEvent{ID: "forged-event", LedgerBatchID: "forged-batch", Sequence: 1, IdempotencyKey: "forged-event", EventType: ledgerpkg.EventCapitalDeposit, AccountID: "primary", VenueID: "internal", Currency: "USDT", CashDelta: accounting.MustParse("1"), AssetDelta: zero, ExecutionMode: "administrative", Actor: "forger", Reason: "unprojected", RealizedPnL: zero, CostBasisDelta: zero, FeeDelta: zero, MetadataJSON: "{}", Stage08ContextJSON: "{}", OccurredAt: now, RecordedAt: now}).Error
	}); err == nil {
		t.Fatal("ledger writer fabricated immutable economics without a coupled projection")
	}
}

func TestGenuineRuntimeLoginRejectsIndependentEconomicAuthority(t *testing.T) {
	admin := testutil.SetupPostgresDB(t)
	const login, password = "trading_bot_c1_runtime_audit_test", "runtime-audit-test-only"
	_ = admin.Exec("DROP ROLE IF EXISTS " + login).Error
	if err := admin.Exec("CREATE ROLE " + login + " LOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '" + password + "'; GRANT trading_bot_runtime TO " + login).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = admin.Exec("REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM " + login).Error
		_ = admin.Exec("REVOKE trading_bot_parity_writer FROM " + login).Error
		_ = admin.Exec("DROP ROLE IF EXISTS " + login).Error
	})
	runtime := openRoleLogin(t, login, password)
	defer closeRoleLogin(runtime)
	assertRejected := func(label string) {
		t.Helper()
		if err := database.ValidateRuntimePrincipalFor(runtime, login); err == nil {
			t.Fatalf("%s authority was accepted", label)
		}
	}
	if err := database.ValidateRuntimePrincipalFor(runtime, login); err != nil {
		t.Fatalf("valid login rejected: %v", err)
	}
	var owner string
	if err := admin.Raw(`SELECT tableowner FROM pg_tables WHERE schemaname='public' AND tablename='settings'`).Scan(&owner).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Exec("ALTER TABLE settings OWNER TO " + login).Error; err != nil {
		t.Fatal(err)
	}
	assertRejected("table ownership")
	if err := admin.Exec(fmt.Sprintf(`ALTER TABLE settings OWNER TO "%s"`, owner)).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Exec("GRANT UPDATE (amount_exact) ON positions TO " + login).Error; err != nil {
		t.Fatal(err)
	}
	assertRejected("direct economic DML")
	if err := admin.Exec("REVOKE UPDATE (amount_exact) ON positions FROM " + login).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Exec("GRANT CREATE ON SCHEMA public TO " + login).Error; err != nil {
		t.Fatal(err)
	}
	assertRejected("schema DDL")
	if err := admin.Exec("REVOKE CREATE ON SCHEMA public FROM " + login).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Exec("ALTER ROLE " + login + " CREATEDB").Error; err != nil {
		t.Fatal(err)
	}
	assertRejected("dangerous role attribute")
	if err := admin.Exec("ALTER ROLE " + login + " NOCREATEDB; GRANT trading_bot_parity_writer TO " + login).Error; err != nil {
		t.Fatal(err)
	}
	assertRejected("cross-writer membership")
}

func openRoleLogin(t *testing.T, user, password string) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5433/trading_bot_test?sslmode=disable"
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(user, password)
	db, err := gorm.Open(postgres.Open(u.String()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open real PostgreSQL login %s: %v", user, err)
	}
	return db
}

func closeRoleLogin(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

const (
	testRuntimePrincipal = "trading_bot_c1_runtime_test"
	testWriterPrincipal  = "trading_bot_c1_writer_test"
)

func TestPrincipalValidationUsesPostgreSQLSetRoleCapabilities(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	for _, role := range []string{testRuntimePrincipal, testWriterPrincipal} {
		if err := db.Exec("DROP ROLE IF EXISTS " + role).Error; err != nil {
			t.Fatal(err)
		}
		inheritance := "NOINHERIT"
		if role == testRuntimePrincipal {
			inheritance = "INHERIT"
		}
		if err := db.Exec("CREATE ROLE " + role + " LOGIN " + inheritance + " NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS").Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, role := range []string{testRuntimePrincipal, testWriterPrincipal} {
			_ = db.Exec("DROP ROLE IF EXISTS " + role).Error
		}
	})

	if err := db.Exec("GRANT trading_bot_runtime TO " + testRuntimePrincipal + "; GRANT trading_bot_ledger_writer TO " + testWriterPrincipal).Error; err != nil {
		t.Fatal(err)
	}
	if err := asPrincipal(db, testRuntimePrincipal, database.ValidateRuntimePrincipal); err != nil {
		t.Fatalf("restricted runtime principal was rejected: %v", err)
	}
	if err := db.Exec("GRANT trading_bot_ledger_writer TO " + testRuntimePrincipal).Error; err != nil {
		t.Fatal(err)
	}
	if err := asPrincipal(db, testRuntimePrincipal, database.ValidateRuntimePrincipal); err == nil || !strings.Contains(err.Error(), "protected writer role") {
		t.Fatalf("runtime principal with ledger SET ROLE capability was accepted: %v", err)
	}
	if err := db.Exec("REVOKE trading_bot_ledger_writer FROM " + testRuntimePrincipal + "; GRANT trading_bot_parity_writer TO " + testRuntimePrincipal).Error; err != nil {
		t.Fatal(err)
	}
	if err := asPrincipal(db, testRuntimePrincipal, database.ValidateRuntimePrincipal); err == nil || !strings.Contains(err.Error(), "protected writer role") {
		t.Fatalf("runtime principal with parity SET ROLE capability was accepted: %v", err)
	}
	if err := db.Exec("REVOKE trading_bot_parity_writer FROM " + testRuntimePrincipal + "; GRANT trading_bot_migration_admin TO " + testRuntimePrincipal).Error; err != nil {
		t.Fatal(err)
	}
	if err := asPrincipal(db, testRuntimePrincipal, database.ValidateRuntimePrincipal); err == nil || !strings.Contains(err.Error(), "protected writer role") {
		t.Fatalf("runtime principal with migration-admin SET ROLE capability was accepted: %v", err)
	}

	if err := asPrincipal(db, testWriterPrincipal, database.ValidateLedgerWriterPrincipal); err != nil {
		t.Fatalf("ledger principal with SET ROLE capability was rejected: %v", err)
	}
	if err := db.Exec("GRANT trading_bot_parity_writer TO " + testWriterPrincipal).Error; err != nil {
		t.Fatal(err)
	}
	if err := asPrincipal(db, testWriterPrincipal, database.ValidateLedgerWriterPrincipal); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("ledger principal with parity SET ROLE capability was accepted: %v", err)
	}
}

func TestRuntimePrincipalRejectsSuperuser(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	var isSuperuser bool
	if err := db.Raw("SELECT rolsuper FROM pg_catalog.pg_roles WHERE rolname = session_user").Scan(&isSuperuser).Error; err != nil {
		t.Fatal(err)
	}
	if !isSuperuser {
		t.Skip("TEST_DATABASE_URL does not use a superuser principal")
	}
	if err := database.ValidateRuntimePrincipal(db); err == nil || !strings.Contains(err.Error(), "superuser") {
		t.Fatalf("expected administrative test principal to be rejected, got %v", err)
	}
}

func asPrincipal(db *gorm.DB, principal string, validate func(*gorm.DB) error) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL SESSION AUTHORIZATION " + principal).Error; err != nil {
			return err
		}
		return validate(tx)
	})
}

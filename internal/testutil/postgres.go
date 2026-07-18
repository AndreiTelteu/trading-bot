package testutil

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"trading-go/internal/database"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const defaultTestDatabaseURL = "postgres://postgres:postgres@localhost:5433/trading_bot_test?sslmode=disable"

func SetupPostgresDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	allowSkip := false
	if dsn == "" {
		dsn = defaultTestDatabaseURL
		allowSkip = true
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		if allowSkip {
			t.Skipf("PostgreSQL test database unavailable at %s: %v", dsn, err)
		}
		t.Fatalf("Failed to connect to PostgreSQL test database: %v", err)
	}

	if err := database.RunMigrations(db); err != nil {
		if allowSkip {
			t.Skipf("PostgreSQL test migrations unavailable at %s: %v", dsn, err)
		}
		t.Fatalf("Failed to run PostgreSQL migrations: %v", err)
	}

	if err := truncatePublicTables(db); err != nil {
		if allowSkip {
			t.Skipf("PostgreSQL test reset unavailable at %s: %v", dsn, err)
		}
		t.Fatalf("Failed to reset PostgreSQL test database: %v", err)
	}

	database.DB = db
	database.ConfigureWriterPoolsForTest(db, db)
	return db
}

func OpenPostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = defaultTestDatabaseURL
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("Failed to connect to PostgreSQL test database: %v", err)
	}
	database.ConfigureWriterPoolsForTest(db, db)
	return db
}

func ResetPublicSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Exec("DROP SCHEMA public CASCADE; CREATE SCHEMA public").Error; err != nil {
		t.Fatalf("reset public schema: %v", err)
	}
}

// WithLedgerProjectionWrites stages an explicitly unresolved legacy fixture.
// It uses the non-login migration role, marks the ledger state unresolved in
// the same transaction, and cannot create exact economic projections. Tests
// needing authoritative economics must call the ledger service instead.
func WithLedgerProjectionWrites(t *testing.T, db *gorm.DB, arrange func(*gorm.DB) error) {
	t.Helper()
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL ROLE trading_bot_migration_admin").Error; err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO ledger_migration_states(account_id,status,unresolved_json,created_at,updated_at)
			VALUES('primary','pending_resolution',?::jsonb,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)
			ON CONFLICT(account_id) DO UPDATE SET status=EXCLUDED.status,unresolved_json=EXCLUDED.unresolved_json,updated_at=EXCLUDED.updated_at`, `["test-arranged unresolved legacy projection"]`).Error; err != nil {
			return err
		}
		return arrange(tx)
	}); err != nil {
		t.Fatalf("arrange ledger projection fixture: %v", err)
	}
}

// WithCorruptedLedgerStorage is restricted to tests that prove reconciliation
// detects storage corruption. It requires the PostgreSQL test superuser and
// bypasses triggers only for the scoped transaction; application fixtures must
// use the ledger or migration helpers above.
func WithCorruptedLedgerStorage(t *testing.T, db *gorm.DB, corrupt func(*gorm.DB) error) {
	t.Helper()
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL session_replication_role='replica'").Error; err != nil {
			return err
		}
		return corrupt(tx)
	}); err != nil {
		t.Fatalf("arrange corrupted ledger storage fixture: %v", err)
	}
}

func truncatePublicTables(db *gorm.DB) error {
	var tables []string
	if err := db.Raw(`
		SELECT tablename
		FROM pg_tables
		WHERE schemaname = 'public' AND tablename <> 'schema_migrations'
		ORDER BY tablename
	`).Scan(&tables).Error; err != nil {
		return err
	}

	if len(tables) == 0 {
		return nil
	}

	quoted := make([]string, 0, len(tables))
	for _, table := range tables {
		quoted = append(quoted, fmt.Sprintf(`"public"."%s"`, strings.ReplaceAll(table, `"`, `""`)))
	}

	return db.Exec("TRUNCATE TABLE " + strings.Join(quoted, ", ") + " RESTART IDENTITY CASCADE").Error
}

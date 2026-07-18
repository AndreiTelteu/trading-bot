package database

import (
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestFormatBootstrapRoleStatementUsesTypedParametersOnPostgres exercises the
// extended-query path that PostgreSQL uses in production. Without the explicit
// text casts, PostgreSQL 16 rejects format's variadic parameters as unknown.
func TestFormatBootstrapRoleStatementUsesTypedParametersOnPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5433/trading_bot_test?sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Skipf("PostgreSQL test database unavailable: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	var statement string
	verb := "CREATE ROLE %I LOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD %L"
	if err := formatBootstrapRoleStatement(db, verb, "bootstrap format role", "bootstrap'password", &statement); err != nil {
		t.Fatalf("format bootstrap role statement: %v", err)
	}
	const want = `CREATE ROLE "bootstrap format role" LOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD 'bootstrap''password'`
	if statement != want {
		t.Fatalf("formatted statement = %q, want %q", statement, want)
	}
}

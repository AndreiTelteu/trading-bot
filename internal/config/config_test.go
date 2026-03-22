package config

import (
	"os"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	os.Setenv("PORT", "8080")
	os.Setenv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/trading_bot?sslmode=disable")
	os.Setenv("POSTGRES_HOST", "db")
	os.Setenv("POSTGRES_PORT", "5433")
	os.Setenv("POSTGRES_DB", "bot")
	os.Setenv("POSTGRES_USER", "botuser")
	os.Setenv("POSTGRES_PASSWORD", "secret")
	os.Setenv("POSTGRES_SSLMODE", "require")
	os.Setenv("DB_MAX_OPEN_CONNS", "50")
	os.Setenv("DB_MAX_IDLE_CONNS", "10")
	os.Setenv("DB_CONN_MAX_LIFETIME", "45m")
	os.Setenv("DB_CONN_MAX_IDLE_TIME", "10m")
	os.Setenv("SECRET_KEY", "test-secret")
	os.Setenv("AUTH_USERNAME", "admin")
	os.Setenv("AUTH_PASSWORD", "qwe321")
	os.Setenv("BINANCE_API_KEY", "test-key")
	os.Setenv("BINANCE_SECRET", "test-secret")

	cfg := Load()

	if cfg.ServerPort != "8080" {
		t.Errorf("Load() ServerPort = %v, want 8080", cfg.ServerPort)
	}
	if cfg.DatabaseURL != "postgres://postgres:postgres@localhost:5432/trading_bot?sslmode=disable" {
		t.Errorf("Load() DatabaseURL = %v, want postgres://postgres:postgres@localhost:5432/trading_bot?sslmode=disable", cfg.DatabaseURL)
	}
	if cfg.PostgresHost != "db" || cfg.PostgresPort != "5433" || cfg.PostgresDB != "bot" || cfg.PostgresUser != "botuser" || cfg.PostgresPassword != "secret" || cfg.PostgresSSLMode != "require" {
		t.Errorf("Load() postgres fields not loaded correctly: %+v", cfg)
	}
	if cfg.DBMaxOpenConns != 50 || cfg.DBMaxIdleConns != 10 {
		t.Errorf("Load() pool settings = (%d,%d), want (50,10)", cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)
	}
	if cfg.DBConnMaxLifetime != 45*time.Minute || cfg.DBConnMaxIdleTime != 10*time.Minute {
		t.Errorf("Load() connection durations = (%v,%v), want (45m,10m)", cfg.DBConnMaxLifetime, cfg.DBConnMaxIdleTime)
	}
	if cfg.SecretKey != "test-secret" {
		t.Errorf("Load() SecretKey = %v, want test-secret", cfg.SecretKey)
	}
	if cfg.AuthUsername != "admin" {
		t.Errorf("Load() AuthUsername = %v, want admin", cfg.AuthUsername)
	}
	if cfg.AuthPassword != "qwe321" {
		t.Errorf("Load() AuthPassword = %v, want qwe321", cfg.AuthPassword)
	}
	if cfg.BinanceAPIKey != "test-key" {
		t.Errorf("Load() BinanceAPIKey = %v, want test-key", cfg.BinanceAPIKey)
	}
	if cfg.DefaultBalance != 400.0 {
		t.Errorf("Load() DefaultBalance = %v, want 400.0", cfg.DefaultBalance)
	}
	if cfg.DefaultCurrency != "USDT" {
		t.Errorf("Load() DefaultCurrency = %v, want USDT", cfg.DefaultCurrency)
	}

	os.Unsetenv("PORT")
	os.Unsetenv("DATABASE_URL")
	os.Unsetenv("POSTGRES_HOST")
	os.Unsetenv("POSTGRES_PORT")
	os.Unsetenv("POSTGRES_DB")
	os.Unsetenv("POSTGRES_USER")
	os.Unsetenv("POSTGRES_PASSWORD")
	os.Unsetenv("POSTGRES_SSLMODE")
	os.Unsetenv("DB_MAX_OPEN_CONNS")
	os.Unsetenv("DB_MAX_IDLE_CONNS")
	os.Unsetenv("DB_CONN_MAX_LIFETIME")
	os.Unsetenv("DB_CONN_MAX_IDLE_TIME")
	os.Unsetenv("SECRET_KEY")
	os.Unsetenv("AUTH_USERNAME")
	os.Unsetenv("AUTH_PASSWORD")
	os.Unsetenv("BINANCE_API_KEY")
	os.Unsetenv("BINANCE_SECRET")
}

func TestLoadDefaults(t *testing.T) {
	os.Unsetenv("PORT")
	os.Unsetenv("DATABASE_URL")
	os.Unsetenv("POSTGRES_HOST")
	os.Unsetenv("POSTGRES_PORT")
	os.Unsetenv("POSTGRES_DB")
	os.Unsetenv("POSTGRES_USER")
	os.Unsetenv("POSTGRES_PASSWORD")
	os.Unsetenv("POSTGRES_SSLMODE")
	os.Unsetenv("DB_MAX_OPEN_CONNS")
	os.Unsetenv("DB_MAX_IDLE_CONNS")
	os.Unsetenv("DB_CONN_MAX_LIFETIME")
	os.Unsetenv("DB_CONN_MAX_IDLE_TIME")
	os.Unsetenv("SECRET_KEY")
	os.Unsetenv("AUTH_USERNAME")
	os.Unsetenv("AUTH_PASSWORD")
	os.Unsetenv("BINANCE_API_KEY")
	os.Unsetenv("BINANCE_SECRET")

	cfg := Load()

	if cfg.ServerPort != "5001" {
		t.Errorf("Load() default ServerPort = %v, want 5001", cfg.ServerPort)
	}
	if cfg.DatabaseURL != "" {
		t.Errorf("Load() default DatabaseURL = %v, want empty string", cfg.DatabaseURL)
	}
	if cfg.PostgresHost != "localhost" || cfg.PostgresPort != "5432" || cfg.PostgresDB != "trading_bot" || cfg.PostgresUser != "postgres" || cfg.PostgresPassword != "postgres" || cfg.PostgresSSLMode != "disable" {
		t.Errorf("Load() default postgres fields not set correctly: %+v", cfg)
	}
	if cfg.DBMaxOpenConns != 25 || cfg.DBMaxIdleConns != 5 {
		t.Errorf("Load() default pool settings = (%d,%d), want (25,5)", cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)
	}
	if cfg.DBConnMaxLifetime != 30*time.Minute || cfg.DBConnMaxIdleTime != 5*time.Minute {
		t.Errorf("Load() default connection durations = (%v,%v), want (30m,5m)", cfg.DBConnMaxLifetime, cfg.DBConnMaxIdleTime)
	}
	if cfg.DefaultBalance != 400.0 {
		t.Errorf("Load() default DefaultBalance = %v, want 400.0", cfg.DefaultBalance)
	}
	if cfg.AuthUsername != "" {
		t.Errorf("Load() default AuthUsername = %v, want empty string", cfg.AuthUsername)
	}
	if cfg.AuthPassword != "" {
		t.Errorf("Load() default AuthPassword = %v, want empty string", cfg.AuthPassword)
	}
}

func TestGetEnv(t *testing.T) {
	os.Setenv("TEST_KEY", "test_value")

	key := getEnv("TEST_KEY", "default")
	if key != "test_value" {
		t.Errorf("getEnv() = %v, want test_value", key)
	}

	key = getEnv("NON_EXISTENT", "default")
	if key != "default" {
		t.Errorf("getEnv() with default = %v, want default", key)
	}

	os.Unsetenv("TEST_KEY")
}

func TestDatabaseDSN(t *testing.T) {
	cfg := &Config{DatabaseURL: "postgres://override"}
	dsn, err := cfg.DatabaseDSN()
	if err != nil {
		t.Fatalf("DatabaseDSN() unexpected error: %v", err)
	}
	if dsn != "postgres://override" {
		t.Fatalf("DatabaseDSN() = %v, want postgres://override", dsn)
	}

	cfg = &Config{
		PostgresHost:     "db",
		PostgresPort:     "5432",
		PostgresDB:       "trading_bot",
		PostgresUser:     "postgres",
		PostgresPassword: "postgres",
		PostgresSSLMode:  "disable",
	}
	dsn, err = cfg.DatabaseDSN()
	if err != nil {
		t.Fatalf("DatabaseDSN() unexpected error: %v", err)
	}
	if dsn != "host=db port=5432 user=postgres password=postgres dbname=trading_bot sslmode=disable TimeZone=UTC" {
		t.Fatalf("DatabaseDSN() = %v", dsn)
	}

	_, err = (&Config{}).DatabaseDSN()
	if err == nil {
		t.Fatal("DatabaseDSN() expected error for incomplete config")
	}
}

func TestGetWSPingInterval(t *testing.T) {
	interval := GetWSPingInterval()
	if interval != 30*time.Second {
		t.Errorf("GetWSPingInterval() = %v, want 30s", interval)
	}
}

func TestGetWSPingTimeout(t *testing.T) {
	timeout := GetWSPingTimeout()
	if timeout != 10*time.Second {
		t.Errorf("GetWSPingTimeout() = %v, want 10s", timeout)
	}
}

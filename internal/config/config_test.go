package config

import (
	"os"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	os.Setenv("PORT", "8080")
	os.Setenv("DATABASE_PATH", "/tmp/test.db")
	os.Setenv("SECRET_KEY", "test-secret")
	os.Setenv("AUTH_USERNAME", "admin")
	os.Setenv("AUTH_PASSWORD", "qwe321")
	os.Setenv("BINANCE_API_KEY", "test-key")
	os.Setenv("BINANCE_SECRET", "test-secret")

	cfg := Load()

	if cfg.ServerPort != "8080" {
		t.Errorf("Load() ServerPort = %v, want 8080", cfg.ServerPort)
	}
	if cfg.DatabasePath != "/tmp/test.db" {
		t.Errorf("Load() DatabasePath = %v, want /tmp/test.db", cfg.DatabasePath)
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
	os.Unsetenv("DATABASE_PATH")
	os.Unsetenv("SECRET_KEY")
	os.Unsetenv("AUTH_USERNAME")
	os.Unsetenv("AUTH_PASSWORD")
	os.Unsetenv("BINANCE_API_KEY")
	os.Unsetenv("BINANCE_SECRET")
}

func TestLoadDefaults(t *testing.T) {
	os.Unsetenv("PORT")
	os.Unsetenv("DATABASE_PATH")
	os.Unsetenv("SECRET_KEY")
	os.Unsetenv("AUTH_USERNAME")
	os.Unsetenv("AUTH_PASSWORD")
	os.Unsetenv("BINANCE_API_KEY")
	os.Unsetenv("BINANCE_SECRET")

	cfg := Load()

	if cfg.ServerPort != "5001" {
		t.Errorf("Load() default ServerPort = %v, want 5001", cfg.ServerPort)
	}
	if cfg.DatabasePath != "./trading.db" {
		t.Errorf("Load() default DatabasePath = %v, want ./trading.db", cfg.DatabasePath)
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

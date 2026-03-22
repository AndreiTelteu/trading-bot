package config

import (
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
)

var Log zerolog.Logger

type Config struct {
	ServerPort      string
	DatabasePath    string
	SecretKey       string
	AuthUsername    string
	AuthPassword    string
	SessionCookie   string
	DefaultBalance  float64
	DefaultCurrency string
	BinanceAPIKey   string
	BinanceSecret   string
	RedisAddr       string
	RedisPassword   string
	RedisDB         int
}

func Load() *Config {
	godotenv.Load()

	Log = zerolog.New(os.Stdout).
		Level(zerolog.InfoLevel).
		With().
		Timestamp().
		Logger()

	return &Config{
		ServerPort:      getEnv("PORT", "5001"),
		DatabasePath:    getEnv("DATABASE_PATH", "./trading.db"),
		SecretKey:       getEnv("SECRET_KEY", "default-secret-key"),
		AuthUsername:    getEnv("AUTH_USERNAME", ""),
		AuthPassword:    getEnv("AUTH_PASSWORD", ""),
		SessionCookie:   getEnv("SESSION_COOKIE_NAME", "trading_bot_session"),
		DefaultBalance:  400.0,
		DefaultCurrency: "USDT",
		BinanceAPIKey:   getEnv("BINANCE_API_KEY", ""),
		BinanceSecret:   getEnv("BINANCE_SECRET", ""),
		RedisAddr:       getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:   getEnv("REDIS_PASSWORD", ""),
		RedisDB:         0,
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func GetWSPingInterval() time.Duration {
	return 30 * time.Second
}

func GetWSPingTimeout() time.Duration {
	return 10 * time.Second
}

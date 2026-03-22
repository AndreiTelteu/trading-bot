package config

import (
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
)

var Log zerolog.Logger

type Config struct {
	ServerPort        string
	DatabaseURL       string
	PostgresHost      string
	PostgresPort      string
	PostgresDB        string
	PostgresUser      string
	PostgresPassword  string
	PostgresSSLMode   string
	DBMaxOpenConns    int
	DBMaxIdleConns    int
	DBConnMaxLifetime time.Duration
	DBConnMaxIdleTime time.Duration
	SecretKey         string
	AuthUsername      string
	AuthPassword      string
	SessionCookie     string
	DefaultBalance    float64
	DefaultCurrency   string
	BinanceAPIKey     string
	BinanceSecret     string
	RedisAddr         string
	RedisPassword     string
	RedisDB           int
}

func Load() *Config {
	godotenv.Load()

	Log = zerolog.New(os.Stdout).
		Level(zerolog.InfoLevel).
		With().
		Timestamp().
		Logger()

	return &Config{
		ServerPort:        getEnv("PORT", "5001"),
		DatabaseURL:       getEnv("DATABASE_URL", ""),
		PostgresHost:      getEnv("POSTGRES_HOST", "localhost"),
		PostgresPort:      getEnv("POSTGRES_PORT", "5432"),
		PostgresDB:        getEnv("POSTGRES_DB", "trading_bot"),
		PostgresUser:      getEnv("POSTGRES_USER", "postgres"),
		PostgresPassword:  getEnv("POSTGRES_PASSWORD", "postgres"),
		PostgresSSLMode:   getEnv("POSTGRES_SSLMODE", "disable"),
		DBMaxOpenConns:    getEnvInt("DB_MAX_OPEN_CONNS", 25),
		DBMaxIdleConns:    getEnvInt("DB_MAX_IDLE_CONNS", 5),
		DBConnMaxLifetime: getEnvDuration("DB_CONN_MAX_LIFETIME", 30*time.Minute),
		DBConnMaxIdleTime: getEnvDuration("DB_CONN_MAX_IDLE_TIME", 5*time.Minute),
		SecretKey:         getEnv("SECRET_KEY", "default-secret-key"),
		AuthUsername:      getEnv("AUTH_USERNAME", ""),
		AuthPassword:      getEnv("AUTH_PASSWORD", ""),
		SessionCookie:     getEnv("SESSION_COOKIE_NAME", "trading_bot_session"),
		DefaultBalance:    400.0,
		DefaultCurrency:   "USDT",
		BinanceAPIKey:     getEnv("BINANCE_API_KEY", ""),
		BinanceSecret:     getEnv("BINANCE_SECRET", ""),
		RedisAddr:         getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:     getEnv("REDIS_PASSWORD", ""),
		RedisDB:           0,
	}
}

func (c *Config) DatabaseDSN() (string, error) {
	if c.DatabaseURL != "" {
		return c.DatabaseURL, nil
	}

	if c.PostgresHost == "" || c.PostgresPort == "" || c.PostgresDB == "" || c.PostgresUser == "" {
		return "", fmt.Errorf("postgres configuration is incomplete")
	}

	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s TimeZone=UTC",
		c.PostgresHost,
		c.PostgresPort,
		c.PostgresUser,
		c.PostgresPassword,
		c.PostgresDB,
		c.PostgresSSLMode,
	), nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		var parsed int
		if _, err := fmt.Sscanf(value, "%d", &parsed); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		parsed, err := time.ParseDuration(value)
		if err == nil {
			return parsed
		}
	}
	return defaultValue
}

func GetWSPingInterval() time.Duration {
	return 30 * time.Second
}

func GetWSPingTimeout() time.Duration {
	return 10 * time.Second
}

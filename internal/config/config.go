package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
	"trading-go/internal/cutover"

	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
)

var Log zerolog.Logger

type Config struct {
	ServerPort           string
	DatabaseURL          string
	PostgresHost         string
	PostgresPort         string
	PostgresDB           string
	PostgresUser         string
	PostgresPassword     string
	PostgresSSLMode      string
	DBMaxOpenConns       int
	DBMaxIdleConns       int
	DBConnMaxLifetime    time.Duration
	DBConnMaxIdleTime    time.Duration
	SecretKey            string
	AuthUsername         string
	AuthPassword         string
	SessionCookie        string
	GovernanceAdminUsers string
	DefaultBalance       float64
	DefaultCurrency      string
	BinanceAPIKey        string
	BinanceSecret        string
	RedisAddr            string
	RedisPassword        string
	RedisDB              int
	Stage08Flags         cutover.Flags
}

func Load() *Config {
	cfg, _ := load(false)
	return cfg
}

// LoadValidated rejects malformed environment and Stage 08 authority before
// database connections, workers, listeners, or external clients are started.
func LoadValidated() (*Config, error) { return load(true) }

func load(strict bool) (*Config, error) {
	godotenv.Load()

	Log = zerolog.New(os.Stdout).
		Level(zerolog.InfoLevel).
		With().
		Timestamp().
		Logger()

	flags, flagErr := cutover.Parse(envValues([]string{"STAGE08_FLAG_SCHEMA_VERSION", "STAGE08_LEDGER_AUTHORITY", "STAGE08_SHARED_ENGINE", "STAGE08_NEW_BACKTEST", "STAGE08_POINT_IN_TIME_UNIVERSE", "STAGE08_CANDIDATE_STRATEGY", "STAGE08_DUAL_RUN", "STAGE08_STAGE07_CONTEXT"}))
	if strict && flagErr != nil {
		return nil, flagErr
	}
	if flagErr != nil {
		flags = cutover.SafeFlags()
	}
	cfg := &Config{
		ServerPort:           getEnv("PORT", "5001"),
		DatabaseURL:          getEnv("DATABASE_URL", ""),
		PostgresHost:         getEnv("POSTGRES_HOST", "localhost"),
		PostgresPort:         getEnv("POSTGRES_PORT", "5432"),
		PostgresDB:           getEnv("POSTGRES_DB", "trading_bot"),
		PostgresUser:         getEnv("POSTGRES_USER", "postgres"),
		PostgresPassword:     getEnv("POSTGRES_PASSWORD", "postgres"),
		PostgresSSLMode:      getEnv("POSTGRES_SSLMODE", "disable"),
		DBMaxOpenConns:       getEnvInt("DB_MAX_OPEN_CONNS", 25),
		DBMaxIdleConns:       getEnvInt("DB_MAX_IDLE_CONNS", 5),
		DBConnMaxLifetime:    getEnvDuration("DB_CONN_MAX_LIFETIME", 30*time.Minute),
		DBConnMaxIdleTime:    getEnvDuration("DB_CONN_MAX_IDLE_TIME", 5*time.Minute),
		SecretKey:            getEnv("SECRET_KEY", "default-secret-key"),
		AuthUsername:         getEnv("AUTH_USERNAME", ""),
		AuthPassword:         getEnv("AUTH_PASSWORD", ""),
		SessionCookie:        getEnv("SESSION_COOKIE_NAME", "trading_bot_session"),
		GovernanceAdminUsers: getEnv("GOVERNANCE_ADMIN_USERS", ""),
		DefaultBalance:       400.0,
		DefaultCurrency:      "USDT",
		BinanceAPIKey:        getEnv("BINANCE_API_KEY", ""),
		BinanceSecret:        getEnv("BINANCE_SECRET", ""),
		RedisAddr:            getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:        getEnv("REDIS_PASSWORD", ""),
		RedisDB:              0,
		Stage08Flags:         flags,
	}
	if strict {
		for _, item := range []struct {
			key      string
			value    int
			positive bool
		}{{"DB_MAX_OPEN_CONNS", cfg.DBMaxOpenConns, true}, {"DB_MAX_IDLE_CONNS", cfg.DBMaxIdleConns, false}} {
			if raw, ok := os.LookupEnv(item.key); ok {
				parsed, err := strconv.Atoi(raw)
				if err != nil || (item.positive && parsed <= 0) || (!item.positive && parsed < 0) {
					return nil, fmt.Errorf("malformed %s=%q", item.key, raw)
				}
				item.value = parsed
			}
		}
		for _, item := range []struct {
			key   string
			value time.Duration
		}{{"DB_CONN_MAX_LIFETIME", cfg.DBConnMaxLifetime}, {"DB_CONN_MAX_IDLE_TIME", cfg.DBConnMaxIdleTime}} {
			if raw, ok := os.LookupEnv(item.key); ok {
				parsed, err := time.ParseDuration(raw)
				if err != nil || parsed < 0 {
					return nil, fmt.Errorf("malformed %s=%q", item.key, raw)
				}
				item.value = parsed
			}
		}
		if cfg.DBMaxIdleConns > cfg.DBMaxOpenConns {
			return nil, fmt.Errorf("DB_MAX_IDLE_CONNS cannot exceed DB_MAX_OPEN_CONNS")
		}
	}
	return cfg, nil
}

func envValues(keys []string) map[string]string {
	result := map[string]string{}
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			result[key] = value
		}
	}
	return result
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

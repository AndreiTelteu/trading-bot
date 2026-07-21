package database

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"trading-go/internal/config"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB
var ledgerWriterDB *gorm.DB
var parityWriterDB *gorm.DB

type principalCapabilities struct {
	Principal       string
	Superuser       bool
	Login           bool
	Inherit         bool
	CreateDB        bool
	CreateRole      bool
	Replication     bool
	BypassRLS       bool
	CanSetLedger    bool
	CanSetParity    bool
	CanSetMigration bool
}

// LedgerWriter returns the dedicated economic writer pool. It never falls back
// to DB: a runtime connection must not acquire ledger authority by accident.
func LedgerWriter() *gorm.DB {
	return ledgerWriterDB
}

// ParityWriter uses the isolated writer connection. PersistParityBound assumes
// the narrower trading_bot_parity_writer role inside its transaction.
func ParityWriter() *gorm.DB {
	return parityWriterDB
}

// ConfigureWriterPoolsForTest is an explicit test-fixture seam. Production
// startup must use OpenLedgerWriter and OpenParityWriter, which validate the
// separate service logins before assigning either pool.
func ConfigureWriterPoolsForTest(ledger, parity *gorm.DB) {
	ledgerWriterDB = ledger
	parityWriterDB = parity
}

func OpenParityWriter(cfg *config.Config) error {
	if cfg == nil || strings.TrimSpace(cfg.ParityDatabaseURL) == "" {
		return fmt.Errorf("PARITY_DATABASE_URL is required for the isolated parity writer pool")
	}
	db, err := openPostgres(cfg.ParityDatabaseURL, cfg)
	if err != nil {
		return err
	}
	expected, err := loginFromDSN(cfg.ParityDatabaseURL)
	if err == nil {
		err = ValidateParityWriterPrincipalFor(db, expected)
	}
	if err != nil {
		closeDB(db)
		return err
	}
	parityWriterDB = db
	return nil
}

func OpenLedgerWriter(cfg *config.Config) error {
	if cfg == nil || strings.TrimSpace(cfg.LedgerDatabaseURL) == "" {
		return fmt.Errorf("LEDGER_DATABASE_URL is required for the isolated ledger writer pool")
	}
	db, err := openPostgres(cfg.LedgerDatabaseURL, cfg)
	if err != nil {
		return err
	}
	expected, err := loginFromDSN(cfg.LedgerDatabaseURL)
	if err != nil {
		closeDB(db)
		return err
	}
	if err := ValidateLedgerWriterPrincipalFor(db, expected); err != nil {
		closeDB(db)
		return err
	}
	ledgerWriterDB = db
	return nil
}

func Initialize(cfg *config.Config) error {
	if err := Migrate(cfg); err != nil {
		return err
	}
	if err := OpenRuntime(cfg); err != nil {
		return err
	}
	if err := OpenLedgerWriter(cfg); err != nil {
		return err
	}
	if err := OpenParityWriter(cfg); err != nil {
		return err
	}
	if err := SeedDataWithDefaults(cfg.DefaultBalance, cfg.DefaultCurrency); err != nil {
		return err
	}
	log.Println("Database initialized successfully")
	return nil
}

// OpenAndMigrate uses a short-lived migration-admin connection, closes it, and
// then opens the separately configured long-lived runtime connection.
func OpenAndMigrate(cfg *config.Config) error {
	if err := Migrate(cfg); err != nil {
		return err
	}
	return Open(cfg)
}

// Migrate performs schema work exclusively through MIGRATION_DATABASE_URL. The
// administrative pool is never assigned to DB and is closed before returning.
func Migrate(cfg *config.Config) error {
	if cfg == nil || strings.TrimSpace(cfg.MigrationDatabaseURL) == "" {
		return fmt.Errorf("MIGRATION_DATABASE_URL is required for schema migrations")
	}
	db, err := openPostgres(cfg.MigrationDatabaseURL, cfg)
	if err != nil {
		return fmt.Errorf("open migration database: %w", err)
	}
	defer closeDB(db)
	if err := RunMigrations(db); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

// BootstrapFreshDatabase is the one-shot fresh-install boundary. Passwords are
// supplied by the caller from secret files and SQL containing them is executed
// with logging disabled.
func BootstrapFreshDatabase(cfg *config.Config, passwords map[string]string) error {
	if cfg == nil || strings.TrimSpace(cfg.MigrationDatabaseURL) == "" {
		return fmt.Errorf("MIGRATION_DATABASE_URL is required for schema migrations")
	}
	db, err := openPostgres(cfg.MigrationDatabaseURL, cfg)
	if err != nil {
		return err
	}
	defer closeDB(db)
	if err := RunMigrations(db); err != nil {
		return err
	}
	roles := []struct{ login, group, password, inheritance string }{
		{"trading_bot_app_runtime", "trading_bot_runtime", passwords["runtime"], "INHERIT"},
		{"trading_bot_app_ledger", "trading_bot_ledger_writer", passwords["ledger"], "NOINHERIT"},
		{"trading_bot_app_parity", "trading_bot_parity_writer", passwords["parity"], "INHERIT"},
	}
	quiet := db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	for _, role := range roles {
		if strings.TrimSpace(role.password) == "" {
			return fmt.Errorf("password for %s is required", role.login)
		}
		var exists bool
		if err := db.Raw("SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=?)", role.login).Scan(&exists).Error; err != nil {
			return err
		}
		var statement string
		verb := "CREATE ROLE %I LOGIN " + role.inheritance + " NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD %L"
		if exists {
			verb = "ALTER ROLE %I LOGIN " + role.inheritance + " NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD %L"
		}
		if err := formatBootstrapRoleStatement(quiet, verb, role.login, role.password, &statement); err != nil {
			return err
		}
		if err := quiet.Exec(statement).Error; err != nil {
			return err
		}
		// Bootstrap is authoritative for these service logins. Remove stale or
		// accidental memberships before granting the one intended capability.
		cleanupMemberships := fmt.Sprintf(`DO $cleanup$ DECLARE inherited record; BEGIN
			FOR inherited IN
				SELECT parent.rolname
				FROM pg_catalog.pg_auth_members membership
				JOIN pg_catalog.pg_roles parent ON parent.oid=membership.roleid
				JOIN pg_catalog.pg_roles member ON member.oid=membership.member
				WHERE member.rolname=%s
			LOOP EXECUTE format('REVOKE %%I FROM %%I', inherited.rolname, %s); END LOOP;
		END $cleanup$`, quoteLiteral(role.login), quoteLiteral(role.login))
		if err := db.Exec(cleanupMemberships).Error; err != nil {
			return err
		}
		if err := db.Exec(fmt.Sprintf("GRANT %s TO %s", role.group, role.login)).Error; err != nil {
			return err
		}
	}
	return nil
}

// formatBootstrapRoleStatement lets PostgreSQL quote role names and passwords
// while keeping password-bearing SQL out of the normal query logger. The casts
// are required because format's variadic arguments otherwise remain unknown
// parameters under PostgreSQL's extended query protocol.
func formatBootstrapRoleStatement(db *gorm.DB, verb, login, password string, statement *string) error {
	return db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).
		Raw("SELECT format('"+verb+"', ?::text, ?::text)", login, password).
		Scan(statement).Error
}

func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// Open establishes and verifies the PostgreSQL connection without schema or
// economic writes. It is used by canonical source fingerprinting.
func Open(cfg *config.Config) error {
	dsn, err := cfg.DatabaseDSN()
	if err != nil {
		return err
	}

	db, err := openPostgres(dsn, cfg)
	if err != nil {
		return err
	}
	DB = db
	return nil
}

// OpenRuntime opens the long-lived application connection and proves that it
// is the constrained runtime service principal. Trusted operator flows that
// intentionally use an administrative/service DSN must call Open directly.
func OpenRuntime(cfg *config.Config) error {
	if err := Open(cfg); err != nil {
		return err
	}
	expected := ""
	if cfg != nil && strings.TrimSpace(cfg.DatabaseURL) != "" {
		var err error
		expected, err = loginFromDSN(cfg.DatabaseURL)
		if err != nil {
			return err
		}
	}
	if err := ValidateRuntimePrincipalFor(DB, expected); err != nil {
		return err
	}
	return nil
}

func openPostgres(dsn string, cfg *config.Config) (*gorm.DB, error) {
	// GORM_LOG_LEVEL: silent|error|warn|info (default info). CLI backtests set
	// silent so multi-hour PIT loads do not fill the disk with SQL tees.
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(gormLogLevel())})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	sqlDB.SetMaxOpenConns(cfg.DBMaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.DBMaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.DBConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.DBConnMaxIdleTime)

	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return db, nil
}

func gormLogLevel() logger.LogLevel {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GORM_LOG_LEVEL"))) {
	case "silent":
		return logger.Silent
	case "error":
		return logger.Error
	case "warn", "warning":
		return logger.Warn
	case "info", "":
		return logger.Info
	default:
		return logger.Info
	}
}

func closeDB(db *gorm.DB) {
	if db == nil {
		return
	}
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

func inspectPrincipal(db *gorm.DB) (principalCapabilities, error) {
	if db == nil {
		return principalCapabilities{}, fmt.Errorf("database connection is not initialized")
	}
	var capabilities principalCapabilities
	result := db.Raw(`
		SELECT session_user AS principal,
		       roles.rolsuper AS superuser,
		       roles.rolcanlogin AS login, roles.rolinherit AS inherit,
		       roles.rolcreatedb AS create_db, roles.rolcreaterole AS create_role,
		       roles.rolreplication AS replication, roles.rolbypassrls AS bypass_rls,
		       pg_catalog.pg_has_role(session_user, 'trading_bot_ledger_writer', 'SET') AS can_set_ledger,
		       pg_catalog.pg_has_role(session_user, 'trading_bot_parity_writer', 'SET') AS can_set_parity,
		       pg_catalog.pg_has_role(session_user, 'trading_bot_migration_admin', 'SET') AS can_set_migration
		FROM pg_catalog.pg_roles AS roles
		WHERE roles.rolname = session_user
	`).Scan(&capabilities)
	if result.Error != nil {
		return principalCapabilities{}, fmt.Errorf("inspect database principal: %w", result.Error)
	}
	if result.RowsAffected != 1 || capabilities.Principal == "" {
		return principalCapabilities{}, fmt.Errorf("inspect database principal: session principal was not found")
	}
	return capabilities, nil
}

type principalProfile struct {
	ExpectedLogin string
	Group         string
	Inherit       bool
}

func loginFromDSN(dsn string) (string, error) {
	dsn = strings.TrimSpace(dsn)
	if u, err := url.Parse(dsn); err == nil && (u.Scheme == "postgres" || u.Scheme == "postgresql") {
		if u.User != nil && strings.TrimSpace(u.User.Username()) != "" {
			return u.User.Username(), nil
		}
		return "", intendedLoginError()
	}

	values, err := parseLibpqKeywordDSN(dsn)
	if err != nil || strings.TrimSpace(values["user"]) == "" {
		return "", intendedLoginError()
	}
	return values["user"], nil
}

func intendedLoginError() error {
	return fmt.Errorf("database URL must name the intended non-secret login identity")
}

// parseLibpqKeywordDSN parses the keyword/value form accepted by libpq. It is
// deliberately limited to extracting explicit parameters: service expansion
// must not silently supply the login identity used for runtime validation.
func parseLibpqKeywordDSN(dsn string) (map[string]string, error) {
	values := make(map[string]string)
	for i := 0; i < len(dsn); {
		for i < len(dsn) && isLibpqSpace(dsn[i]) {
			i++
		}
		if i == len(dsn) {
			break
		}

		keyStart := i
		for i < len(dsn) && dsn[i] != '=' && !isLibpqSpace(dsn[i]) {
			i++
		}
		if keyStart == i || i == len(dsn) || dsn[i] != '=' {
			return nil, fmt.Errorf("invalid libpq keyword DSN")
		}
		key := dsn[keyStart:i]
		i++

		var value strings.Builder
		if i < len(dsn) && dsn[i] == '\'' {
			i++
			closed := false
			for i < len(dsn) {
				if dsn[i] == '\\' {
					i++
					if i == len(dsn) {
						return nil, fmt.Errorf("invalid libpq keyword DSN")
					}
					value.WriteByte(dsn[i])
					i++
					continue
				}
				if dsn[i] == '\'' {
					i++
					closed = true
					break
				}
				value.WriteByte(dsn[i])
				i++
			}
			if !closed || (i < len(dsn) && !isLibpqSpace(dsn[i])) {
				return nil, fmt.Errorf("invalid libpq keyword DSN")
			}
		} else {
			for i < len(dsn) && !isLibpqSpace(dsn[i]) {
				if dsn[i] == '\\' {
					i++
					if i == len(dsn) {
						return nil, fmt.Errorf("invalid libpq keyword DSN")
					}
				}
				value.WriteByte(dsn[i])
				i++
			}
		}
		values[key] = value.String()
	}
	return values, nil
}

func isLibpqSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f'
}

// validatePrincipalProfile ensures the login has no authority except the one
// bootstrap group.  Comparing effective privileges with that group catches
// grants inherited through non-protected roles; explicit ACL/ownership checks
// catch authority which PostgreSQL owners can use to disable safeguards.
func validatePrincipalProfile(db *gorm.DB, profile principalProfile) error {
	c, err := inspectPrincipal(db)
	if err != nil {
		return err
	}
	if profile.ExpectedLogin != "" && c.Principal != profile.ExpectedLogin {
		return fmt.Errorf("database principal %q does not match intended login %q", c.Principal, profile.ExpectedLogin)
	}
	if c.Superuser {
		return fmt.Errorf("database principal %q is a superuser", c.Principal)
	}
	if !c.Login {
		return fmt.Errorf("database principal %q is not a login role", c.Principal)
	}
	for attribute, enabled := range map[string]bool{
		"CREATEDB":    c.CreateDB,
		"CREATEROLE":  c.CreateRole,
		"REPLICATION": c.Replication,
		"BYPASSRLS":   c.BypassRLS,
	} {
		if enabled {
			return fmt.Errorf("database principal %q has dangerous %s attribute", c.Principal, attribute)
		}
	}
	if profile.Group == "trading_bot_runtime" && (c.CanSetLedger || c.CanSetParity || c.CanSetMigration) {
		return fmt.Errorf("database principal %q can assume a protected writer role", c.Principal)
	}
	if profile.Group == "trading_bot_ledger_writer" && (c.CanSetParity || c.CanSetMigration) {
		return fmt.Errorf("database principal %q can assume forbidden writer authority", c.Principal)
	}
	if profile.Group == "trading_bot_parity_writer" && (c.CanSetLedger || c.CanSetMigration) {
		return fmt.Errorf("database principal %q can assume forbidden writer authority", c.Principal)
	}
	if c.Inherit != profile.Inherit {
		return fmt.Errorf("database principal %q has unexpected INHERIT shape", c.Principal)
	}
	var bad int64
	// The service login must have exactly its bootstrap group; no side-channel
	// membership can add authority independently of the reviewed group ACL.
	err = db.Raw(`SELECT count(*) FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.roleid JOIN pg_roles u ON u.oid=m.member WHERE u.rolname=session_user AND (r.rolname<>? OR m.admin_option)`, profile.Group).Scan(&bad).Error
	if err != nil || bad != 0 {
		if err != nil {
			return err
		}
		return fmt.Errorf("database principal %q has unexpected protected-role or auxiliary membership", c.Principal)
	}
	var intended bool
	if err := db.Raw(`SELECT pg_has_role(session_user, ?, 'SET')`, profile.Group).Scan(&intended).Error; err != nil || !intended {
		if err != nil {
			return err
		}
		return fmt.Errorf("database principal %q lacks required %s membership", c.Principal, profile.Group)
	}
	// A login must never own protected objects or receive direct ACLs. Group ACLs
	// are the sole reviewed capability source.
	checks := []string{
		`SELECT count(*) FROM pg_namespace n JOIN pg_roles r ON r.oid=n.nspowner WHERE n.nspname='public' AND r.rolname=session_user`,
		`SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.oid=c.relowner WHERE n.nspname='public' AND r.rolname=session_user`,
		`SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner WHERE n.nspname='public' AND r.rolname=session_user`,
		`SELECT count(*) FROM pg_namespace n JOIN pg_roles u ON u.rolname=session_user, LATERAL aclexplode(n.nspacl) a WHERE n.nspname='public' AND a.grantee=u.oid`,
		`SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles u ON u.rolname=session_user, LATERAL aclexplode(c.relacl) a WHERE n.nspname='public' AND a.grantee=u.oid`,
		`SELECT count(*) FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles u ON u.rolname=session_user, LATERAL aclexplode(a.attacl) x WHERE n.nspname='public' AND a.attnum>0 AND NOT a.attisdropped AND x.grantee=u.oid`,
		`SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles u ON u.rolname=session_user, LATERAL aclexplode(p.proacl) a WHERE n.nspname='public' AND a.grantee=u.oid`,
	}
	for _, query := range checks {
		if err := db.Raw(query).Scan(&bad).Error; err != nil {
			return err
		}
		if bad != 0 {
			return fmt.Errorf("database principal %q has direct protected object authority", c.Principal)
		}
	}
	if !profile.Inherit {
		return nil
	}
	var mismatch bool
	if err := db.Raw(`SELECT EXISTS (
		SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace CROSS JOIN unnest(ARRAY['SELECT','INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER']) p
		WHERE n.nspname='public' AND c.relkind IN ('r','p','v','m','S','f') AND has_table_privilege(session_user,c.oid,p) IS DISTINCT FROM has_table_privilege(?,c.oid,p)
		UNION ALL SELECT 1 FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace CROSS JOIN unnest(ARRAY['SELECT','INSERT','UPDATE','REFERENCES']) p
		WHERE n.nspname='public' AND a.attnum>0 AND NOT a.attisdropped AND has_column_privilege(session_user,c.oid,a.attnum,p) IS DISTINCT FROM has_column_privilege(?,c.oid,a.attnum,p)
		UNION ALL SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public' AND has_function_privilege(session_user,p.oid,'EXECUTE') IS DISTINCT FROM has_function_privilege(?,p.oid,'EXECUTE')
		UNION ALL SELECT 1 FROM pg_namespace n WHERE n.nspname='public' AND (has_schema_privilege(session_user,n.oid,'USAGE') IS DISTINCT FROM has_schema_privilege(?,n.oid,'USAGE') OR has_schema_privilege(session_user,n.oid,'CREATE') IS DISTINCT FROM has_schema_privilege(?,n.oid,'CREATE'))
	)`, profile.Group, profile.Group, profile.Group, profile.Group, profile.Group).Scan(&mismatch).Error; err != nil {
		return err
	}
	if mismatch {
		return fmt.Errorf("database principal %q has effective authority beyond %s", c.Principal, profile.Group)
	}
	return nil
}

// ValidateRuntimePrincipal rejects an administrative or writer-capable runtime
// login. pg_has_role(..., 'SET') follows direct and transitive SET ROLE grants.
func ValidateRuntimePrincipal(db *gorm.DB) error {
	return ValidateRuntimePrincipalFor(db, "")
}

func ValidateRuntimePrincipalFor(db *gorm.DB, expectedLogin string) error {
	if err := validatePrincipalProfile(db, principalProfile{expectedLogin, "trading_bot_runtime", true}); err != nil {
		return fmt.Errorf("runtime %w", err)
	}
	return validateRequiredCapabilities(db, "trading_bot_runtime", []string{"public:USAGE", "settings:SELECT", "positions:SELECT"})
}

// ValidateLedgerWriterPrincipal verifies that the isolated connection can enter
// the non-login ledger writer role used by economic transactions.
func ValidateLedgerWriterPrincipal(db *gorm.DB) error {
	return ValidateLedgerWriterPrincipalFor(db, "")
}

func ValidateLedgerWriterPrincipalFor(db *gorm.DB, expectedLogin string) error {
	return validateWriterPrincipal(db, principalProfile{expectedLogin, "trading_bot_ledger_writer", false}, []string{"public:USAGE", "ledger_batches:INSERT", "ledger_events:INSERT"})
}
func ValidateParityWriterPrincipalFor(db *gorm.DB, expectedLogin string) error {
	return validateWriterPrincipal(db, principalProfile{expectedLogin, "trading_bot_parity_writer", true}, []string{"public:USAGE", "parity_populations:INSERT", "parity_observations:INSERT"})
}

func validateWriterPrincipal(db *gorm.DB, profile principalProfile, required []string) error {
	if err := validatePrincipalProfile(db, profile); err != nil {
		return fmt.Errorf("writer %w", err)
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL ROLE " + profile.Group).Error; err != nil {
			return err
		}
		return validateRequiredCapabilities(tx, profile.Group, required)
	})
}

func validateRequiredCapabilities(db *gorm.DB, group string, required []string) error {
	for _, capability := range required {
		parts := strings.SplitN(capability, ":", 2)
		var ok bool
		var err error
		if parts[0] == "public" {
			err = db.Raw(`SELECT has_schema_privilege(current_user,'public',?)`, parts[1]).Scan(&ok).Error
		} else {
			err = db.Raw(`SELECT has_table_privilege(current_user,?,?)`, parts[0], parts[1]).Scan(&ok).Error
		}
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%s lacks required %s", group, capability)
		}
	}
	return nil
}

func AutoMigrate() error {
	return migrateSchema(DB)
}

func SeedData() error {
	return SeedDataWithDefaults(400, "USDT")
}

func SeedDataWithDefaults(defaultBalance float64, defaultCurrency string) error {
	settings := []Setting{
		{Key: "entry_percent", Value: "5.0", Category: strPtr("trading")},
		{Key: "stop_loss_percent", Value: "5.0", Category: strPtr("trading")},
		{Key: "take_profit_percent", Value: "30.0", Category: strPtr("trading")},
		{Key: "rebuy_percent", Value: "2.5", Category: strPtr("trading")},
		{Key: "max_positions", Value: "5", Category: strPtr("trading")},
		{Key: "buy_only_strong", Value: "true", Category: strPtr("trading")}, // DEPRECATED: superseded by learned model selection policy
		{Key: "sell_on_signal", Value: "true", Category: strPtr("trading")},
		{Key: "min_confidence_to_buy", Value: "4.0", Category: strPtr("trading")}, // DEPRECATED: superseded by learned model selection policy (selection_policy_min_prob)
		{Key: "min_confidence_to_sell", Value: "3.5", Category: strPtr("trading")},
		{Key: "stream_exit_enabled", Value: "true", Category: strPtr("trading")},
		{Key: "allow_sell_at_loss", Value: "false", Category: strPtr("trading")},
		{Key: "trailing_stop_enabled", Value: "false", Category: strPtr("trading")},
		{Key: "trailing_stop_percent", Value: "10.0", Category: strPtr("trading")},
		{Key: "atr_trailing_enabled", Value: "false", Category: strPtr("trading")},
		{Key: "atr_trailing_mult", Value: "1.0", Category: strPtr("trading")},
		{Key: "atr_trailing_period", Value: "14", Category: strPtr("trading")},
		{Key: "atr_annualization_enabled", Value: "false", Category: strPtr("trading")},
		{Key: "atr_annualization_days", Value: "365", Category: strPtr("trading")},
		{Key: "pyramiding_enabled", Value: "false", Category: strPtr("trading")},
		{Key: "max_pyramid_layers", Value: "3", Category: strPtr("trading")},
		{Key: "position_scale_percent", Value: "50.0", Category: strPtr("trading")},
		{Key: "auto_trade_enabled", Value: "false", Category: strPtr("trading")},
		{Key: "trading_engine_mode", Value: "legacy", Category: strPtr("migration")},
		{Key: "trading_engine_fallback", Value: "disabled", Category: strPtr("migration")},
		{Key: "exchange_venue_id", Value: "binance", Category: strPtr("trading")},
		{Key: "backtest_venue_id", Value: "binance", Category: strPtr("backtest")},
		{Key: "backtest_settlement_currency", Value: defaultCurrency, Category: strPtr("backtest")},
		{Key: "trending_coins_to_analyze", Value: "5", Category: strPtr("trading")},
		{Key: "regime_gate_enabled", Value: "true", Category: strPtr("trading")},
		{Key: "regime_timeframe", Value: "1h", Category: strPtr("trading")},
		{Key: "regime_ema_fast", Value: "50", Category: strPtr("trading")},
		{Key: "regime_ema_slow", Value: "200", Category: strPtr("trading")},
		{Key: "vol_atr_period", Value: "14", Category: strPtr("trading")},
		{Key: "vol_ratio_min", Value: "0.002", Category: strPtr("trading")},
		{Key: "vol_ratio_max", Value: "0.02", Category: strPtr("trading")},
		{Key: "vol_sizing_enabled", Value: "false", Category: strPtr("trading")},
		{Key: "risk_per_trade", Value: "0.5", Category: strPtr("trading")},
		{Key: "stop_mult", Value: "1.5", Category: strPtr("trading")},
		{Key: "tp_mult", Value: "3.0", Category: strPtr("trading")},
		{Key: "max_position_value", Value: "0", Category: strPtr("trading")},
		{Key: "time_stop_bars", Value: "0", Category: strPtr("trading")},
		{Key: "universe_mode", Value: "dynamic", Category: strPtr("universe")},
		{Key: "universe_rebalance_interval", Value: "1h", Category: strPtr("universe")},
		{Key: "universe_min_listing_days", Value: "45", Category: strPtr("universe")},
		{Key: "universe_min_daily_quote_volume", Value: "2000000", Category: strPtr("universe")},
		{Key: "universe_min_intraday_quote_volume", Value: "75000", Category: strPtr("universe")},
		{Key: "universe_max_gap_ratio", Value: "0.05", Category: strPtr("universe")},
		{Key: "universe_vol_ratio_min", Value: "0.004", Category: strPtr("universe")},
		{Key: "universe_vol_ratio_max", Value: "0.08", Category: strPtr("universe")},
		{Key: "universe_max_24h_move", Value: "25", Category: strPtr("universe")},
		{Key: "universe_top_k", Value: "20", Category: strPtr("universe")},
		{Key: "universe_analyze_top_n", Value: "8", Category: strPtr("universe")},
		{Key: "active_model_version", Value: "logistic_baseline_v1", Category: strPtr("model")},
		{Key: "model_rollout_state", Value: "shadow", Category: strPtr("model")},
		{Key: "model_fallback_mode", Value: "rule_based", Category: strPtr("model")},
		{Key: "model_rollback_target", Value: "", Category: strPtr("model")},
		{Key: "selection_policy_top_k", Value: "3", Category: strPtr("model")},
		{Key: "selection_policy_min_prob", Value: "0.53", Category: strPtr("model")},
		{Key: "selection_policy_min_ev", Value: "0.001", Category: strPtr("model")},
		{Key: "monitoring_window_days", Value: "30", Category: strPtr("model")},
		{Key: "monitoring_min_outcomes", Value: "10", Category: strPtr("model")},
		{Key: "backtest_fee_bps", Value: "10", Category: strPtr("backtest")},
		{Key: "backtest_slippage_bps", Value: "5", Category: strPtr("backtest")},
		{Key: "paper_fee_bps", Value: "10", Category: strPtr("trading")},
		{Key: "paper_slippage_bps", Value: "5", Category: strPtr("trading")},
		{Key: "backtest_start", Value: "", Category: strPtr("backtest")},
		{Key: "backtest_end", Value: "", Category: strPtr("backtest")},
		{Key: "backtest_symbols", Value: "", Category: strPtr("backtest")},
		{Key: "backtest_universe_mode", Value: "static", Category: strPtr("backtest")},
		{Key: "backtest_require_point_in_time", Value: "true", Category: strPtr("backtest")},
		{Key: "backtest_dataset_manifest_id", Value: "", Category: strPtr("backtest")},
		{Key: "validation_train_months", Value: "12", Category: strPtr("backtest")},
		{Key: "validation_test_months", Value: "3", Category: strPtr("backtest")},
		{Key: "validation_bootstrap_iterations", Value: "500", Category: strPtr("backtest")},
		// DEPRECATED: Manual probability model betas are superseded by the learned model artifact.
		// Retained for rollback compatibility. Do not use for new configurations.
		{Key: "prob_model_enabled", Value: "false", Category: strPtr("probabilistic")}, // DEPRECATED
		{Key: "prob_model_beta0", Value: "0.0", Category: strPtr("probabilistic")},     // DEPRECATED
		{Key: "prob_model_beta1", Value: "0.0", Category: strPtr("probabilistic")},     // DEPRECATED
		{Key: "prob_model_beta2", Value: "0.0", Category: strPtr("probabilistic")},     // DEPRECATED
		{Key: "prob_model_beta3", Value: "0.0", Category: strPtr("probabilistic")},     // DEPRECATED
		{Key: "prob_model_beta4", Value: "0.0", Category: strPtr("probabilistic")},     // DEPRECATED
		{Key: "prob_model_beta5", Value: "0.0", Category: strPtr("probabilistic")},     // DEPRECATED
		{Key: "prob_model_beta6", Value: "0.0", Category: strPtr("probabilistic")},     // DEPRECATED
		{Key: "prob_p_min", Value: "0.55", Category: strPtr("probabilistic")},          // DEPRECATED: use selection_policy_min_prob
		{Key: "prob_ev_min", Value: "0.0", Category: strPtr("probabilistic")},          // DEPRECATED: use selection_policy_min_ev
		{Key: "prob_avg_gain", Value: "0.02", Category: strPtr("probabilistic")},       // DEPRECATED
		{Key: "prob_avg_loss", Value: "0.01", Category: strPtr("probabilistic")},       // DEPRECATED
		// DEPRECATED: Indicator period settings are legacy controls. The learned model and
		// policy framework now govern entry decisions. Retained for rollback compatibility.
		{Key: "rsi_period", Value: "14", Category: strPtr("indicators")},        // DEPRECATED: legacy indicator tuning
		{Key: "rsi_oversold", Value: "30.0", Category: strPtr("indicators")},    // DEPRECATED: legacy indicator tuning
		{Key: "rsi_overbought", Value: "70.0", Category: strPtr("indicators")},  // DEPRECATED: legacy indicator tuning
		{Key: "macd_fast_period", Value: "12", Category: strPtr("indicators")},  // DEPRECATED: legacy indicator tuning
		{Key: "macd_slow_period", Value: "26", Category: strPtr("indicators")},  // DEPRECATED: legacy indicator tuning
		{Key: "macd_signal_period", Value: "9", Category: strPtr("indicators")}, // DEPRECATED: legacy indicator tuning
		{Key: "bb_period", Value: "20", Category: strPtr("indicators")},         // DEPRECATED: legacy indicator tuning
		{Key: "bb_std", Value: "2.0", Category: strPtr("indicators")},           // DEPRECATED: legacy indicator tuning
		{Key: "volume_ma_period", Value: "20", Category: strPtr("indicators")},  // DEPRECATED: legacy indicator tuning
		{Key: "momentum_period", Value: "10", Category: strPtr("indicators")},   // DEPRECATED: legacy indicator tuning
		{Key: "ai_analysis_interval", Value: "24", Category: strPtr("ai")},
		{Key: "ai_lookback_days", Value: "30", Category: strPtr("ai")},
		{Key: "ai_min_proposals", Value: "1", Category: strPtr("ai")},
		{Key: "ai_auto_apply_days", Value: "0", Category: strPtr("ai")},
		{Key: "ai_goal", Value: "", Category: strPtr("ai")},
		{Key: "ai_locked_keys", Value: "", Category: strPtr("ai")},
		{Key: "ai_change_budget_pct", Value: "10", Category: strPtr("ai")},
		{Key: "ai_max_proposals", Value: "5", Category: strPtr("ai")},
		{Key: "ai_max_keys_per_category", Value: "2", Category: strPtr("ai")},
		{Key: "ai_recent_decisions_limit", Value: "10", Category: strPtr("ai")},
		{Key: "ai_gate_metrics_limit", Value: "200", Category: strPtr("ai")},
	}
	weights := []IndicatorWeight{
		{Indicator: "rsi", Weight: 1.0},
		{Indicator: "macd", Weight: 1.0},
		{Indicator: "bollinger", Weight: 1.0},
		{Indicator: "volume", Weight: 0.5},
		{Indicator: "momentum", Weight: 1.0},
	}

	writer := LedgerWriter()
	if writer == nil {
		return fmt.Errorf("ledger writer database is not initialized")
	}
	if err := writer.Transaction(func(tx *gorm.DB) error {
		// Wallet/position lifecycle is database-guarded. Seeding is one atomic
		// ledger boundary, so authorize projection writes before the wallet insert;
		// seedLedgerBoundary appends the matching opening-capital event below.
		if err := tx.Exec("SET LOCAL ROLE trading_bot_ledger_writer").Error; err != nil {
			return err
		}
		wallet := Wallet{ID: 1, Balance: defaultBalance, Currency: defaultCurrency, AccountID: primaryLedgerAccount}
		walletResult := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&wallet)
		if err := walletResult.Error; err != nil {
			return err
		}
		if walletResult.RowsAffected == 0 {
			if err := tx.First(&wallet, 1).Error; err != nil {
				return err
			}
		}
		if err := seedLedgerBoundary(tx, wallet, walletResult.RowsAffected == 1); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	if DB == nil {
		return fmt.Errorf("runtime database is not initialized")
	}
	// Configuration seed data is deliberately written by the runtime principal;
	// the economic writer has no authority over settings, governance, or parity.
	return DB.Transaction(func(tx *gorm.DB) error {

		for _, s := range settings {
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&s)
			if result.Error != nil {
				return result.Error
			}
		}

		for _, w := range weights {
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&w)
			if result.Error != nil {
				return result.Error
			}
		}

		llmConfig := LLMConfig{
			ID:       1,
			Provider: "openrouter",
			BaseURL:  "https://openrouter.ai/api/v1",
			Model:    "google/gemini-2.0-flash-001",
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&llmConfig).Error; err != nil {
			return err
		}

		return nil
	})
}

func strPtr(s string) *string {
	return &s
}

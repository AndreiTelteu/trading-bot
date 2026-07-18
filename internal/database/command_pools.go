package database

import (
	"fmt"
	"strings"
	"trading-go/internal/config"
)

// CommandPoolRequirements describes the authority a CLI action needs. It is
// deliberately action-scoped: migrations are short lived, runtime is the only
// long-lived general pool, and writer pools are opened only before their
// protected writes (including economic seeding).
type CommandPoolRequirements struct {
	Migrate         bool
	ValidateRuntime bool
	LedgerWriter    bool
	ParityWriter    bool
	TrustedOperator bool
}

func (r CommandPoolRequirements) Validate(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("database configuration is required")
	}
	if r.TrustedOperator && r.ValidateRuntime {
		return fmt.Errorf("trusted operator flow cannot require runtime principal validation")
	}
	if !r.TrustedOperator && !r.ValidateRuntime {
		return fmt.Errorf("long-lived database connection requires runtime principal validation")
	}
	if r.Migrate && strings.TrimSpace(cfg.MigrationDatabaseURL) == "" {
		return fmt.Errorf("MIGRATION_DATABASE_URL is required for schema migrations")
	}
	if r.LedgerWriter && strings.TrimSpace(cfg.LedgerDatabaseURL) == "" {
		return fmt.Errorf("LEDGER_DATABASE_URL is required before economic writes or seeding")
	}
	if r.ParityWriter && strings.TrimSpace(cfg.ParityDatabaseURL) == "" {
		return fmt.Errorf("PARITY_DATABASE_URL is required before parity writes")
	}
	return nil
}

// OpenCommandPools opens only the pools declared by an action. Migrations are
// always performed via the short-lived migration connection; no writer DSN can
// substitute for another authority.
func OpenCommandPools(cfg *config.Config, r CommandPoolRequirements) error {
	if err := r.Validate(cfg); err != nil {
		return err
	}
	if r.Migrate {
		if err := Migrate(cfg); err != nil {
			return err
		}
	}
	if r.ValidateRuntime {
		if err := OpenRuntime(cfg); err != nil {
			return err
		}
	} else {
		if err := Open(cfg); err != nil {
			return err
		}
	}
	if r.LedgerWriter {
		if err := OpenLedgerWriter(cfg); err != nil {
			return err
		}
	}
	if r.ParityWriter {
		if err := OpenParityWriter(cfg); err != nil {
			return err
		}
	}
	return nil
}

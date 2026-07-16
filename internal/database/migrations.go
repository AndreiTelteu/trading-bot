package database

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

func schemaModels() []interface{} {
	return []interface{}{
		&Wallet{},
		&Position{},
		&Order{},
		&LedgerBatch{},
		&Fill{},
		&LedgerEvent{},
		&LedgerMigrationState{},
		&Setting{},
		&AIProposal{},
		&IndicatorWeight{},
		&LLMConfig{},
		&ActivityLog{},
		&BacktestJob{},
		&TrendAnalysisHistory{},
		&UniverseSymbol{},
		&UniverseSnapshot{},
		&UniverseMember{},
		&ModelArtifact{},
		&PolicyConfig{},
		&ExperimentRun{},
		&RolloutEvent{},
		&FeatureSnapshot{},
		&PredictionLog{},
		&TradeLabel{},
		&MonitoringSnapshot{},
		&PortfolioSnapshot{},
	}
}

func migrateSchema(db *gorm.DB) error {
	return db.AutoMigrate(schemaModels()...)
}

func RunMigrations(db *gorm.DB) error {
	m := gormigrate.New(db, &gormigrate.Options{
		TableName:      "schema_migrations",
		UseTransaction: true,
	}, []*gormigrate.Migration{
		{
			ID: "202603221700_initial_postgres_schema",
			Migrate: func(tx *gorm.DB) error {
				return migrateSchema(tx)
			},
			Rollback: func(tx *gorm.DB) error {
				return tx.Migrator().DropTable(schemaModels()...)
			},
		},
		{
			ID: "202603221830_backtest_job_summary_compact_json",
			Migrate: func(tx *gorm.DB) error {
				if tx.Migrator().HasColumn(&BacktestJob{}, "SummaryCompactJSON") {
					return nil
				}
				return tx.Migrator().AddColumn(&BacktestJob{}, "SummaryCompactJSON")
			},
			Rollback: func(tx *gorm.DB) error {
				if !tx.Migrator().HasColumn(&BacktestJob{}, "SummaryCompactJSON") {
					return nil
				}
				return tx.Migrator().DropColumn(&BacktestJob{}, "SummaryCompactJSON")
			},
		},
		{
			ID: "202603222100_execution_parity_fields",
			Migrate: func(tx *gorm.DB) error {
				for _, column := range []string{"ExecutionMode", "EntrySource", "ExitPending", "LastMarkPrice", "LastMarkAt", "ClientPositionID", "DecisionTimeframe"} {
					if !tx.Migrator().HasColumn(&Position{}, column) {
						if err := tx.Migrator().AddColumn(&Position{}, column); err != nil {
							return err
						}
					}
				}

				for _, column := range []string{"ExchangeOrderID", "ClientOrderID", "Status", "ExecutionMode", "TriggerReason", "RequestedPrice", "FillPrice", "ExecutedQty", "ExchangeFee", "SubmittedAt", "FilledAt"} {
					if !tx.Migrator().HasColumn(&Order{}, column) {
						if err := tx.Migrator().AddColumn(&Order{}, column); err != nil {
							return err
						}
					}
				}

				return migrateSchema(tx)
			},
			Rollback: func(tx *gorm.DB) error {
				for _, column := range []string{"FilledAt", "SubmittedAt", "ExchangeFee", "ExecutedQty", "FillPrice", "RequestedPrice", "TriggerReason", "ExecutionMode", "Status", "ClientOrderID", "ExchangeOrderID"} {
					if tx.Migrator().HasColumn(&Order{}, column) {
						if err := tx.Migrator().DropColumn(&Order{}, column); err != nil {
							return err
						}
					}
				}

				for _, column := range []string{"DecisionTimeframe", "ClientPositionID", "LastMarkAt", "LastMarkPrice", "ExitPending", "EntrySource", "ExecutionMode"} {
					if tx.Migrator().HasColumn(&Position{}, column) {
						if err := tx.Migrator().DropColumn(&Position{}, column); err != nil {
							return err
						}
					}
				}

				return nil
			},
		},
		{
			ID: "202603230100_universe_selection_tables",
			Migrate: func(tx *gorm.DB) error {
				return migrateSchema(tx)
			},
			Rollback: func(tx *gorm.DB) error {
				for _, model := range []interface{}{&UniverseMember{}, &UniverseSnapshot{}, &UniverseSymbol{}} {
					if tx.Migrator().HasTable(model) {
						if err := tx.Migrator().DropTable(model); err != nil {
							return err
						}
					}
				}
				return nil
			},
		},
		{
			ID: "202603230400_learned_model_entities",
			Migrate: func(tx *gorm.DB) error {
				return migrateSchema(tx)
			},
			Rollback: func(tx *gorm.DB) error {
				for _, model := range []interface{}{&TradeLabel{}, &PredictionLog{}, &FeatureSnapshot{}, &ModelArtifact{}} {
					if tx.Migrator().HasTable(model) {
						if err := tx.Migrator().DropTable(model); err != nil {
							return err
						}
					}
				}
				return nil
			},
		},
		{
			ID: "202603231200_governance_tracking_entities",
			Migrate: func(tx *gorm.DB) error {
				return migrateSchema(tx)
			},
			Rollback: func(tx *gorm.DB) error {
				for _, model := range []interface{}{&MonitoringSnapshot{}, &RolloutEvent{}, &ExperimentRun{}, &PolicyConfig{}} {
					if tx.Migrator().HasTable(model) {
						if err := tx.Migrator().DropTable(model); err != nil {
							return err
						}
					}
				}
				return nil
			},
		},
		{
			ID: "202607160100_immutable_ledger",
			Migrate: func(tx *gorm.DB) error {
				if err := migrateSchema(tx); err != nil {
					return err
				}
				// Database enforcement is deliberate: accidental GORM Save/Delete calls
				// cannot rewrite economic history.
				return tx.Exec(`
					ALTER TABLE ledger_events DROP CONSTRAINT IF EXISTS ledger_events_type_check;
					ALTER TABLE ledger_events ADD CONSTRAINT ledger_events_type_check CHECK (event_type IN ('capital_deposit','capital_withdrawal','buy_fill','sell_fill','trading_fee','exchange_fee','funding_interest','administrative_correction','reversal'));
					ALTER TABLE ledger_events DROP CONSTRAINT IF EXISTS ledger_events_sign_check;
					ALTER TABLE ledger_events ADD CONSTRAINT ledger_events_sign_check CHECK (
						(event_type = 'buy_fill' AND cash_delta < 0 AND asset_delta > 0) OR
						(event_type = 'sell_fill' AND cash_delta > 0 AND asset_delta < 0) OR
						(event_type IN ('trading_fee','exchange_fee') AND cash_delta <= 0 AND asset_delta = 0) OR
						(event_type = 'capital_deposit' AND cash_delta >= 0 AND asset_delta = 0) OR
						(event_type = 'capital_withdrawal' AND cash_delta <= 0 AND asset_delta = 0) OR
						event_type IN ('funding_interest','administrative_correction','reversal')
					);
					ALTER TABLE fills DROP CONSTRAINT IF EXISTS fills_economic_check;
					ALTER TABLE fills ADD CONSTRAINT fills_economic_check CHECK (side IN ('buy','sell') AND quantity > 0 AND requested_price > 0 AND fill_price > 0 AND gross_amount > 0 AND fee_amount >= 0);
					CREATE OR REPLACE FUNCTION reject_ledger_mutation() RETURNS trigger AS $$
					BEGIN RAISE EXCEPTION 'ledger rows are immutable'; END;
					$$ LANGUAGE plpgsql;
					DROP TRIGGER IF EXISTS ledger_events_immutable ON ledger_events;
					CREATE TRIGGER ledger_events_immutable BEFORE UPDATE OR DELETE ON ledger_events
					FOR EACH ROW EXECUTE FUNCTION reject_ledger_mutation();
					DROP TRIGGER IF EXISTS fills_immutable ON fills;
					CREATE TRIGGER fills_immutable BEFORE UPDATE OR DELETE ON fills
					FOR EACH ROW EXECUTE FUNCTION reject_ledger_mutation();
				`).Error
			},
			Rollback: func(tx *gorm.DB) error {
				if err := tx.Exec(`DROP TRIGGER IF EXISTS ledger_events_immutable ON ledger_events; DROP TRIGGER IF EXISTS fills_immutable ON fills;`).Error; err != nil {
					return err
				}
				if err := tx.Migrator().DropTable(&LedgerEvent{}, &Fill{}, &LedgerBatch{}, &LedgerMigrationState{}); err != nil {
					return err
				}
				return tx.Exec(`DROP FUNCTION IF EXISTS reject_ledger_mutation()`).Error
			},
		},
	})

	return m.Migrate()
}

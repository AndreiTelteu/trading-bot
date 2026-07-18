package database

import (
	"fmt"
	"sort"
	"strings"

	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

func schemaModels() []interface{} {
	return []interface{}{
		&Wallet{},
		&Position{},
		&Order{},
		&LedgerBatch{},
		&BrokerOutcomeIngestion{},
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
		&Asset{},
		&ExchangeSymbol{},
		&TradabilityInterval{},
		&SymbolConstraintVersion{},
		&HistoricalBar{},
		&DatasetManifest{},
		&IngestionCheckpoint{},
		&UniverseBuildCheckpoint{},
		&ModelArtifact{},
		&PolicyConfig{},
		&ExperimentRun{},
		&RolloutEvent{},
		&FeatureSnapshot{},
		&PredictionLog{},
		&TradeLabel{},
		&MonitoringSnapshot{},
		&ValidationExperiment{},
		&ValidationFoldEvidence{},
		&ValidationEvidence{},
		&ValidationMLEvidence{},
		&GovernanceApproval{},
		&GovernanceDeployment{},
		&GovernanceTransition{},
		&GovernanceMonitoringEvidence{},
		&Stage08FlagSnapshot{}, &ParityObservation{}, &ParityAggregate{}, &ParityAcceptancePolicy{},
		&OperationalIncident{}, &OperationalIncidentAudit{},
		&CutoverState{}, &CutoverTransition{}, &BackfillPlan{}, &BackupVerification{}, &ParityPopulation{}, &CutoverPrerequisiteEvidence{}, &ReconciliationEvidence{}, &BrokerConflictCounter{},
		&PortfolioSnapshot{},
	}
}

func migrateSchema(db *gorm.DB) error {
	return db.AutoMigrate(schemaModels()...)
}

func Stage04RollbackError() error {
	return fmt.Errorf("Stage 04 point-in-time market history is intentionally irreversible; export and manually remove immutable history/manifests, dependent foreign keys, exclusion constraints, and triggers before deleting migration history")
}

func requireProjectionGuardColumns(tx *gorm.DB, table string, columns []string) error {
	if !tx.Migrator().HasTable(table) {
		return nil
	}
	missing := make([]string, 0)
	for _, column := range columns {
		if !tx.Migrator().HasColumn(table, column) {
			missing = append(missing, column)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("cannot install %s economic guard: shaped schema is missing required columns [%s]", table, strings.Join(missing, ", "))
}

func validateProjectionGuardShape(tx *gorm.DB) error {
	if err := requireProjectionGuardColumns(tx, "positions", []string{"account_id", "amount", "amount_exact", "avg_price", "closed_at", "close_reason", "cost_basis_exact", "fees_exact", "opened_at", "realized_pn_l_exact", "status", "symbol"}); err != nil {
		return err
	}
	return requireProjectionGuardColumns(tx, "wallets", []string{"account_id", "balance", "balance_exact", "currency"})
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
		{
			ID: "202607160200_stage01_review_remediation",
			Migrate: func(tx *gorm.DB) error {
				if err := migrateSchema(tx); err != nil {
					return err
				}
				return tx.Exec(`
					DROP TRIGGER IF EXISTS ledger_events_immutable ON ledger_events;
					DROP TRIGGER IF EXISTS fills_immutable ON fills;
					UPDATE wallets SET account_id='primary' WHERE account_id IS NULL OR account_id='';
					UPDATE positions SET account_id='primary' WHERE account_id IS NULL OR account_id='';
					UPDATE orders SET account_id='primary' WHERE account_id IS NULL OR account_id='';
					UPDATE fills SET venue_id=CASE WHEN execution_mode='exchange' THEN 'binance' ELSE 'internal' END WHERE venue_id IS NULL OR venue_id='';
					UPDATE ledger_events SET venue_id=CASE WHEN execution_mode='exchange' THEN 'binance' ELSE 'internal' END WHERE venue_id IS NULL OR venue_id='';
					ALTER TABLE ledger_events DROP CONSTRAINT IF EXISTS ledger_events_time_check;
					ALTER TABLE ledger_events ADD CONSTRAINT ledger_events_time_check CHECK (occurred_at <= recorded_at);
					ALTER TABLE wallets DROP CONSTRAINT IF EXISTS wallets_primary_account_check;
					ALTER TABLE wallets ADD CONSTRAINT wallets_primary_account_check CHECK (account_id='primary');
					ALTER TABLE positions DROP CONSTRAINT IF EXISTS positions_primary_account_check;
					ALTER TABLE positions ADD CONSTRAINT positions_primary_account_check CHECK (account_id='primary');
					ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_primary_account_check;
					ALTER TABLE orders ADD CONSTRAINT orders_primary_account_check CHECK (account_id='primary');
					ALTER TABLE fills DROP CONSTRAINT IF EXISTS fills_primary_account_check;
					ALTER TABLE fills ADD CONSTRAINT fills_primary_account_check CHECK (account_id='primary' AND fee_currency <> '');
					ALTER TABLE fills DROP CONSTRAINT IF EXISTS fills_time_check;
					ALTER TABLE fills ADD CONSTRAINT fills_time_check CHECK (occurred_at <= created_at);
					ALTER TABLE fills DROP CONSTRAINT IF EXISTS fills_provider_identity_check;
					ALTER TABLE fills ADD CONSTRAINT fills_provider_identity_check CHECK (execution_mode <> 'exchange' OR (venue_id <> 'internal' AND provider_fill_id IS NOT NULL));
					ALTER TABLE ledger_events DROP CONSTRAINT IF EXISTS ledger_events_primary_account_check;
					ALTER TABLE ledger_events ADD CONSTRAINT ledger_events_primary_account_check CHECK (account_id='primary');
					ALTER TABLE ledger_batches DROP CONSTRAINT IF EXISTS ledger_batches_primary_account_check;
					ALTER TABLE ledger_batches ADD CONSTRAINT ledger_batches_primary_account_check CHECK (account_id='primary');
					DROP INDEX IF EXISTS idx_fills_provider_fill_id;
					CREATE UNIQUE INDEX IF NOT EXISTS idx_fills_provider_identity ON fills(account_id,venue_id,provider_fill_id) WHERE provider_fill_id IS NOT NULL;
					ALTER TABLE fills DROP CONSTRAINT IF EXISTS fk_fills_batch;
					ALTER TABLE fills ADD CONSTRAINT fk_fills_batch FOREIGN KEY (ledger_batch_id) REFERENCES ledger_batches(id) ON DELETE RESTRICT;
					ALTER TABLE fills DROP CONSTRAINT IF EXISTS fk_fills_order;
					ALTER TABLE fills ADD CONSTRAINT fk_fills_order FOREIGN KEY (order_id) REFERENCES orders(id) ON DELETE RESTRICT;
					ALTER TABLE fills DROP CONSTRAINT IF EXISTS fk_fills_position;
					ALTER TABLE fills ADD CONSTRAINT fk_fills_position FOREIGN KEY (position_id) REFERENCES positions(id) ON DELETE RESTRICT;
					ALTER TABLE ledger_events DROP CONSTRAINT IF EXISTS fk_events_batch;
					ALTER TABLE ledger_events ADD CONSTRAINT fk_events_batch FOREIGN KEY (ledger_batch_id) REFERENCES ledger_batches(id) ON DELETE RESTRICT;
					ALTER TABLE ledger_events DROP CONSTRAINT IF EXISTS fk_events_fill;
					ALTER TABLE ledger_events ADD CONSTRAINT fk_events_fill FOREIGN KEY (fill_id) REFERENCES fills(id) ON DELETE RESTRICT;
					ALTER TABLE ledger_events DROP CONSTRAINT IF EXISTS fk_events_order;
					ALTER TABLE ledger_events ADD CONSTRAINT fk_events_order FOREIGN KEY (order_id) REFERENCES orders(id) ON DELETE RESTRICT;
					ALTER TABLE ledger_events DROP CONSTRAINT IF EXISTS fk_events_position;
					ALTER TABLE ledger_events ADD CONSTRAINT fk_events_position FOREIGN KEY (position_id) REFERENCES positions(id) ON DELETE RESTRICT;
					ALTER TABLE ledger_events DROP CONSTRAINT IF EXISTS fk_events_reversal;
					ALTER TABLE ledger_events ADD CONSTRAINT fk_events_reversal FOREIGN KEY (reverses_event_id) REFERENCES ledger_events(id) ON DELETE RESTRICT;
					CREATE OR REPLACE FUNCTION validate_fill_links() RETURNS trigger AS $$ BEGIN IF NOT EXISTS(SELECT 1 FROM ledger_batches b WHERE b.id=NEW.ledger_batch_id AND b.account_id=NEW.account_id) OR NOT EXISTS(SELECT 1 FROM orders o WHERE o.id=NEW.order_id AND o.account_id=NEW.account_id AND o.symbol=NEW.symbol AND o.order_type=NEW.side AND (NEW.execution_mode <> 'exchange' OR o.exchange_order_id IS NOT NULL)) OR NOT EXISTS(SELECT 1 FROM positions p WHERE p.id=NEW.position_id AND p.account_id=NEW.account_id AND p.symbol=NEW.symbol) OR NOT EXISTS(SELECT 1 FROM wallets w WHERE w.account_id=NEW.account_id AND w.currency=NEW.fee_currency) THEN RAISE EXCEPTION 'fill account links are inconsistent'; END IF; RETURN NEW; END; $$ LANGUAGE plpgsql;
					DROP TRIGGER IF EXISTS fills_link_guard ON fills;
					CREATE TRIGGER fills_link_guard BEFORE INSERT ON fills FOR EACH ROW EXECUTE FUNCTION validate_fill_links();
					CREATE OR REPLACE FUNCTION validate_event_links() RETURNS trigger AS $$ BEGIN IF NOT EXISTS(SELECT 1 FROM ledger_batches b WHERE b.id=NEW.ledger_batch_id AND b.account_id=NEW.account_id) OR NOT EXISTS(SELECT 1 FROM wallets w WHERE w.account_id=NEW.account_id AND w.currency=NEW.currency) OR (NEW.order_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM orders o WHERE o.id=NEW.order_id AND o.account_id=NEW.account_id)) OR (NEW.position_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM positions p WHERE p.id=NEW.position_id AND p.account_id=NEW.account_id AND (NEW.symbol='' OR p.symbol=NEW.symbol))) OR (NEW.fill_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM fills f WHERE f.id=NEW.fill_id AND f.account_id=NEW.account_id AND f.venue_id=NEW.venue_id AND f.symbol=NEW.symbol AND (NEW.order_id IS NULL OR f.order_id=NEW.order_id) AND (NEW.position_id IS NULL OR f.position_id=NEW.position_id))) THEN RAISE EXCEPTION 'event account links are inconsistent'; END IF; RETURN NEW; END; $$ LANGUAGE plpgsql;
					DROP TRIGGER IF EXISTS ledger_events_link_guard ON ledger_events;
					CREATE TRIGGER ledger_events_link_guard BEFORE INSERT ON ledger_events FOR EACH ROW EXECUTE FUNCTION validate_event_links();
					CREATE OR REPLACE FUNCTION reject_ledger_mutation() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'ledger rows are immutable'; END; $$ LANGUAGE plpgsql;
					DROP TRIGGER IF EXISTS ledger_batches_immutable ON ledger_batches;
					CREATE TRIGGER ledger_batches_immutable BEFORE UPDATE OR DELETE ON ledger_batches FOR EACH ROW EXECUTE FUNCTION reject_ledger_mutation();
					CREATE TRIGGER ledger_events_immutable BEFORE UPDATE OR DELETE ON ledger_events FOR EACH ROW EXECUTE FUNCTION reject_ledger_mutation();
					CREATE TRIGGER fills_immutable BEFORE UPDATE OR DELETE ON fills FOR EACH ROW EXECUTE FUNCTION reject_ledger_mutation();
					CREATE OR REPLACE FUNCTION guard_position_economics() RETURNS trigger AS $$
					BEGIN
					 IF current_setting('trading_bot.ledger_write',true) IS DISTINCT FROM 'on' AND
					   (OLD.amount,OLD.amount_exact,OLD.cost_basis_exact,OLD.realized_pn_l_exact,OLD.fees_exact,OLD.avg_price,OLD.status,OLD.opened_at,OLD.closed_at,OLD.close_reason)
					   IS DISTINCT FROM
					   (NEW.amount,NEW.amount_exact,NEW.cost_basis_exact,NEW.realized_pn_l_exact,NEW.fees_exact,NEW.avg_price,NEW.status,NEW.opened_at,NEW.closed_at,NEW.close_reason)
					 THEN RAISE EXCEPTION 'position economic columns require ledger transaction'; END IF;
					 RETURN NEW;
					END; $$ LANGUAGE plpgsql;
					DROP TRIGGER IF EXISTS positions_economic_guard ON positions;
					CREATE TRIGGER positions_economic_guard BEFORE UPDATE ON positions FOR EACH ROW EXECUTE FUNCTION guard_position_economics();
					CREATE OR REPLACE FUNCTION guard_wallet_economics() RETURNS trigger AS $$
					BEGIN IF current_setting('trading_bot.ledger_write',true) IS DISTINCT FROM 'on' AND (OLD.balance,OLD.balance_exact,OLD.currency) IS DISTINCT FROM (NEW.balance,NEW.balance_exact,NEW.currency) THEN RAISE EXCEPTION 'wallet economic columns require ledger transaction'; END IF; RETURN NEW; END; $$ LANGUAGE plpgsql;
					DROP TRIGGER IF EXISTS wallets_economic_guard ON wallets;
					CREATE TRIGGER wallets_economic_guard BEFORE UPDATE ON wallets FOR EACH ROW EXECUTE FUNCTION guard_wallet_economics();
				`).Error
			},
			Rollback: func(tx *gorm.DB) error {
				return tx.Exec(`DROP TRIGGER IF EXISTS ledger_batches_immutable ON ledger_batches; DROP TRIGGER IF EXISTS positions_economic_guard ON positions; DROP TRIGGER IF EXISTS wallets_economic_guard ON wallets; DROP FUNCTION IF EXISTS guard_position_economics(); DROP FUNCTION IF EXISTS guard_wallet_economics();`).Error
			},
		},
		{
			ID: "202607170200_shared_broker_outcomes",
			Migrate: func(tx *gorm.DB) error {
				// Historical migrations must not pick up models introduced by later
				// stages or rebuild immutable economic tables. Freeze Stage 02 to
				// the table and columns it actually introduced.
				if err := tx.AutoMigrate(&BrokerOutcomeIngestion{}); err != nil {
					return err
				}
				for _, column := range []string{"RequestedQuantityExact", "ExecutedQuantityExact", "RemainingQuantityExact"} {
					if !tx.Migrator().HasColumn(&Order{}, column) {
						if err := tx.Migrator().AddColumn(&Order{}, column); err != nil {
							return err
						}
					}
				}
				if !tx.Migrator().HasColumn(&Fill{}, "CostModelVersion") {
					return tx.Migrator().AddColumn(&Fill{}, "CostModelVersion")
				}
				return nil
			},
			Rollback: func(tx *gorm.DB) error {
				if err := tx.Migrator().DropTable(&BrokerOutcomeIngestion{}); err != nil {
					return err
				}
				for _, column := range []string{"RequestedQuantityExact", "ExecutedQuantityExact", "RemainingQuantityExact"} {
					if tx.Migrator().HasColumn(&Order{}, column) {
						if err := tx.Migrator().DropColumn(&Order{}, column); err != nil {
							return err
						}
					}
				}
				if tx.Migrator().HasColumn(&Fill{}, "CostModelVersion") {
					return tx.Migrator().DropColumn(&Fill{}, "CostModelVersion")
				}
				return nil
			},
		},
		{
			ID: "202607170400_point_in_time_market_data",
			Migrate: func(tx *gorm.DB) error {
				// Scope this migration deliberately. Running the global AutoMigrate
				// here can rebuild Stage 01 economic tables after their immutable
				// triggers and partial unique indexes have been installed.
				if err := tx.AutoMigrate(
					&Asset{}, &ExchangeSymbol{}, &TradabilityInterval{},
					&SymbolConstraintVersion{}, &HistoricalBar{},
					&DatasetManifest{}, &IngestionCheckpoint{},
					&BacktestJob{}, &UniverseSymbol{}, &UniverseSnapshot{},
					&UniverseMember{}, &ExperimentRun{},
				); err != nil {
					return err
				}
				return tx.Exec(`
					ALTER TABLE exchange_symbols DROP CONSTRAINT IF EXISTS exchange_symbols_lifecycle_check;
					ALTER TABLE exchange_symbols ADD CONSTRAINT exchange_symbols_lifecycle_check CHECK (delisted_at IS NULL OR delisted_at > listed_at);
					ALTER TABLE tradability_intervals DROP CONSTRAINT IF EXISTS tradability_intervals_time_check;
					ALTER TABLE tradability_intervals ADD CONSTRAINT tradability_intervals_time_check CHECK (effective_to IS NULL OR effective_to > effective_from);
					ALTER TABLE symbol_constraint_versions DROP CONSTRAINT IF EXISTS symbol_constraint_versions_time_check;
					ALTER TABLE symbol_constraint_versions ADD CONSTRAINT symbol_constraint_versions_time_check CHECK (effective_to IS NULL OR effective_to > effective_from);
					ALTER TABLE historical_bars DROP CONSTRAINT IF EXISTS historical_bars_role_check;
					ALTER TABLE historical_bars ADD CONSTRAINT historical_bars_role_check CHECK (role IN ('decision','execution','benchmark'));
					ALTER TABLE historical_bars DROP CONSTRAINT IF EXISTS historical_bars_quality_check;
					ALTER TABLE historical_bars ADD CONSTRAINT historical_bars_quality_check CHECK (quality_status IN ('valid','warning','rejected','unresolved'));
					ALTER TABLE historical_bars DROP CONSTRAINT IF EXISTS historical_bars_values_check;
					ALTER TABLE historical_bars ADD CONSTRAINT historical_bars_values_check CHECK (open > 0 AND high >= open AND high >= close AND high >= low AND low <= open AND low <= close AND volume >= 0 AND quote_volume >= 0 AND dataset_version <> '' AND source <> '' AND length(content_hash)=64);
					ALTER TABLE symbol_constraint_versions DROP CONSTRAINT IF EXISTS symbol_constraint_values_check;
					ALTER TABLE symbol_constraint_versions ADD CONSTRAINT symbol_constraint_values_check CHECK (quantity_step > 0 AND price_tick > 0 AND min_quantity > 0 AND min_notional >= 0 AND source <> '');
					ALTER TABLE dataset_manifests DROP CONSTRAINT IF EXISTS dataset_manifests_interval_check;
					ALTER TABLE dataset_manifests ADD CONSTRAINT dataset_manifests_interval_check CHECK (requested_end >= requested_start AND effective_end >= effective_start AND dataset_version <> '' AND source <> '' AND id=content_hash AND length(content_hash)=64);
					ALTER TABLE exchange_symbols DROP CONSTRAINT IF EXISTS fk_exchange_symbols_asset;
					ALTER TABLE exchange_symbols ADD CONSTRAINT fk_exchange_symbols_asset FOREIGN KEY(asset_id) REFERENCES assets(id) ON DELETE RESTRICT;
					ALTER TABLE exchange_symbols ADD CONSTRAINT fk_exchange_symbols_base FOREIGN KEY(base_asset_id) REFERENCES assets(id) ON DELETE RESTRICT;
					ALTER TABLE exchange_symbols ADD CONSTRAINT fk_exchange_symbols_quote FOREIGN KEY(quote_asset_id) REFERENCES assets(id) ON DELETE RESTRICT;
					ALTER TABLE tradability_intervals ADD CONSTRAINT fk_tradability_symbol FOREIGN KEY(exchange_symbol_id) REFERENCES exchange_symbols(id) ON DELETE RESTRICT;
					ALTER TABLE symbol_constraint_versions ADD CONSTRAINT fk_constraints_symbol FOREIGN KEY(exchange_symbol_id) REFERENCES exchange_symbols(id) ON DELETE RESTRICT;
					ALTER TABLE historical_bars ADD CONSTRAINT fk_historical_bars_symbol FOREIGN KEY(exchange_symbol_id) REFERENCES exchange_symbols(id) ON DELETE RESTRICT;
					ALTER TABLE ingestion_checkpoints ADD CONSTRAINT fk_ingestion_checkpoint_symbol FOREIGN KEY(exchange_symbol_id) REFERENCES exchange_symbols(id) ON DELETE RESTRICT;
					ALTER TABLE universe_snapshots ADD CONSTRAINT fk_universe_snapshot_manifest FOREIGN KEY(dataset_manifest_id) REFERENCES dataset_manifests(id) ON DELETE RESTRICT;
					ALTER TABLE universe_members ADD CONSTRAINT fk_universe_member_asset FOREIGN KEY(asset_id) REFERENCES assets(id) ON DELETE RESTRICT;
					ALTER TABLE universe_members ADD CONSTRAINT fk_universe_member_symbol FOREIGN KEY(exchange_symbol_id) REFERENCES exchange_symbols(id) ON DELETE RESTRICT;
					ALTER TABLE backtest_jobs ADD CONSTRAINT fk_backtest_job_manifest FOREIGN KEY(dataset_manifest_id) REFERENCES dataset_manifests(id) ON DELETE RESTRICT;
					ALTER TABLE experiment_runs ADD CONSTRAINT fk_experiment_run_manifest FOREIGN KEY(dataset_manifest_id) REFERENCES dataset_manifests(id) ON DELETE RESTRICT;
					CREATE INDEX IF NOT EXISTS idx_exchange_symbol_asof ON exchange_symbols(venue_id,listed_at,delisted_at);
					CREATE INDEX IF NOT EXISTS idx_tradability_asof ON tradability_intervals(exchange_symbol_id,effective_from,effective_to);
					CREATE INDEX IF NOT EXISTS idx_constraints_asof ON symbol_constraint_versions(exchange_symbol_id,effective_from,effective_to);
					CREATE INDEX IF NOT EXISTS idx_historical_bars_lookup ON historical_bars(dataset_version,exchange_symbol_id,role,timeframe,open_time);
					CREATE OR REPLACE FUNCTION reject_market_history_mutation() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'point-in-time history is immutable'; END; $$ LANGUAGE plpgsql;
					DROP TRIGGER IF EXISTS historical_bars_immutable ON historical_bars;
					CREATE TRIGGER historical_bars_immutable BEFORE UPDATE OR DELETE ON historical_bars FOR EACH ROW EXECUTE FUNCTION reject_market_history_mutation();
					DROP TRIGGER IF EXISTS dataset_manifests_immutable ON dataset_manifests;
					CREATE TRIGGER dataset_manifests_immutable BEFORE UPDATE OR DELETE ON dataset_manifests FOR EACH ROW EXECUTE FUNCTION reject_market_history_mutation();
					DROP TRIGGER IF EXISTS assets_immutable ON assets;
					CREATE TRIGGER assets_immutable BEFORE UPDATE OR DELETE ON assets FOR EACH ROW EXECUTE FUNCTION reject_market_history_mutation();
					DROP TRIGGER IF EXISTS exchange_symbols_immutable ON exchange_symbols;
					CREATE TRIGGER exchange_symbols_immutable BEFORE UPDATE OR DELETE ON exchange_symbols FOR EACH ROW EXECUTE FUNCTION reject_market_history_mutation();
					DROP TRIGGER IF EXISTS tradability_intervals_immutable ON tradability_intervals;
					CREATE TRIGGER tradability_intervals_immutable BEFORE UPDATE OR DELETE ON tradability_intervals FOR EACH ROW EXECUTE FUNCTION reject_market_history_mutation();
					DROP TRIGGER IF EXISTS symbol_constraints_immutable ON symbol_constraint_versions;
					CREATE TRIGGER symbol_constraints_immutable BEFORE UPDATE OR DELETE ON symbol_constraint_versions FOR EACH ROW EXECUTE FUNCTION reject_market_history_mutation();
					CREATE OR REPLACE FUNCTION reject_overlapping_effective_interval() RETURNS trigger AS $$ BEGIN
					 IF TG_TABLE_NAME='tradability_intervals' AND EXISTS(SELECT 1 FROM tradability_intervals x WHERE x.exchange_symbol_id=NEW.exchange_symbol_id AND x.id<>COALESCE(NEW.id,0) AND tstzrange(x.effective_from,x.effective_to,'[)') && tstzrange(NEW.effective_from,NEW.effective_to,'[)')) THEN RAISE EXCEPTION 'overlapping tradability interval'; END IF;
					 IF TG_TABLE_NAME='symbol_constraint_versions' AND EXISTS(SELECT 1 FROM symbol_constraint_versions x WHERE x.exchange_symbol_id=NEW.exchange_symbol_id AND x.id<>COALESCE(NEW.id,0) AND tstzrange(x.effective_from,x.effective_to,'[)') && tstzrange(NEW.effective_from,NEW.effective_to,'[)')) THEN RAISE EXCEPTION 'overlapping constraint interval'; END IF;
					 RETURN NEW; END; $$ LANGUAGE plpgsql;
					DROP TRIGGER IF EXISTS tradability_no_overlap ON tradability_intervals;
					CREATE TRIGGER tradability_no_overlap BEFORE INSERT OR UPDATE ON tradability_intervals FOR EACH ROW EXECUTE FUNCTION reject_overlapping_effective_interval();
					DROP TRIGGER IF EXISTS constraints_no_overlap ON symbol_constraint_versions;
					CREATE TRIGGER constraints_no_overlap BEFORE INSERT OR UPDATE ON symbol_constraint_versions FOR EACH ROW EXECUTE FUNCTION reject_overlapping_effective_interval();
				`).Error
			},
			Rollback: func(tx *gorm.DB) error {
				return Stage04RollbackError()
			},
		},
		{
			ID: "202607170500_point_in_time_review_remediation",
			Migrate: func(tx *gorm.DB) error {
				if err := tx.AutoMigrate(&UniverseBuildCheckpoint{}); err != nil {
					return err
				}
				return tx.Exec(`
					CREATE EXTENSION IF NOT EXISTS btree_gist;
					ALTER TABLE assets ADD COLUMN IF NOT EXISTS available_at timestamptz;
					ALTER TABLE exchange_symbols ADD COLUMN IF NOT EXISTS available_at timestamptz;
					ALTER TABLE tradability_intervals ADD COLUMN IF NOT EXISTS available_at timestamptz;
					ALTER TABLE symbol_constraint_versions ADD COLUMN IF NOT EXISTS available_at timestamptz;
					ALTER TABLE historical_bars ADD COLUMN IF NOT EXISTS available_at timestamptz;
					ALTER TABLE dataset_manifests ADD COLUMN IF NOT EXISTS knowledge_cutoff timestamptz;
					UPDATE assets SET available_at=retrieved_at WHERE available_at IS NULL;
					UPDATE exchange_symbols SET available_at=listed_at WHERE available_at IS NULL;
					UPDATE tradability_intervals SET available_at=effective_from WHERE available_at IS NULL;
					UPDATE symbol_constraint_versions SET available_at=effective_from WHERE available_at IS NULL;
					UPDATE historical_bars SET available_at=open_time + CASE timeframe WHEN '1m' THEN interval '1 minute' WHEN '15m' THEN interval '15 minutes' WHEN '1h' THEN interval '1 hour' WHEN '1d' THEN interval '1 day' ELSE interval '1 millisecond' END - interval '1 millisecond' WHERE available_at IS NULL;
					UPDATE dataset_manifests SET knowledge_cutoff=created_at WHERE knowledge_cutoff IS NULL;
					ALTER TABLE assets ALTER COLUMN available_at SET NOT NULL;
					ALTER TABLE exchange_symbols ALTER COLUMN available_at SET NOT NULL;
					ALTER TABLE tradability_intervals ALTER COLUMN available_at SET NOT NULL;
					ALTER TABLE symbol_constraint_versions ALTER COLUMN available_at SET NOT NULL;
					ALTER TABLE historical_bars ALTER COLUMN available_at SET NOT NULL;
					ALTER TABLE dataset_manifests ALTER COLUMN knowledge_cutoff SET NOT NULL;
					ALTER TABLE dataset_manifests DROP CONSTRAINT IF EXISTS dataset_manifests_interval_check;
					ALTER TABLE dataset_manifests ADD CONSTRAINT dataset_manifests_interval_check CHECK (((schema_version='point-in-time-dataset-manifest-v1' AND requested_end>=requested_start) OR (schema_version<>'point-in-time-dataset-manifest-v1' AND requested_end>requested_start)) AND effective_end>=effective_start AND dataset_version<>'' AND source<>'' AND id=content_hash AND length(content_hash)=64);
					DROP TRIGGER IF EXISTS tradability_no_overlap ON tradability_intervals;
					DROP TRIGGER IF EXISTS constraints_no_overlap ON symbol_constraint_versions;
					DROP FUNCTION IF EXISTS reject_overlapping_effective_interval();
					DO $$ BEGIN
					 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='tradability_intervals_no_overlap') THEN
					  ALTER TABLE tradability_intervals ADD CONSTRAINT tradability_intervals_no_overlap EXCLUDE USING gist (exchange_symbol_id WITH =, tstzrange(effective_from,effective_to,'[)') WITH &&);
					 END IF;
					 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='symbol_constraint_versions_no_overlap') THEN
					  ALTER TABLE symbol_constraint_versions ADD CONSTRAINT symbol_constraint_versions_no_overlap EXCLUDE USING gist (exchange_symbol_id WITH =, tstzrange(effective_from,effective_to,'[)') WITH &&);
					 END IF;
					END $$;
					CREATE OR REPLACE FUNCTION market_timeframe_interval(value text) RETURNS interval AS $$
					 SELECT CASE
					  WHEN value='1d' THEN interval '1 day'
					  WHEN value ~ '^[1-9][0-9]*ms$' THEN substring(value from '^[0-9]+')::bigint * interval '1 millisecond'
					  WHEN value ~ '^[1-9][0-9]*s$' THEN substring(value from '^[0-9]+')::bigint * interval '1 second'
					  WHEN value ~ '^[1-9][0-9]*m$' THEN substring(value from '^[0-9]+')::bigint * interval '1 minute'
					  WHEN value ~ '^[1-9][0-9]*h$' THEN substring(value from '^[0-9]+')::bigint * interval '1 hour'
					 END;
					$$ LANGUAGE sql IMMUTABLE STRICT;
					ALTER TABLE historical_bars DROP CONSTRAINT IF EXISTS historical_bars_availability_check;
					ALTER TABLE historical_bars ADD CONSTRAINT historical_bars_availability_check CHECK (market_timeframe_interval(timeframe) IS NOT NULL AND available_at >= open_time + market_timeframe_interval(timeframe) - interval '1 millisecond');
					CREATE INDEX IF NOT EXISTS idx_historical_bars_manifest_lookup ON historical_bars(dataset_version,exchange_symbol_id,role,timeframe,open_time,available_at,retrieved_at);
				`).Error
			},
			Rollback: func(tx *gorm.DB) error {
				return Stage04RollbackError()
			},
		},
		{
			ID: "202607170600_stage05_governance_evidence",
			Migrate: func(tx *gorm.DB) error {
				for _, column := range []string{"JobType", "ArtifactDigest", "DiagnosticJSON"} {
					if !tx.Migrator().HasColumn(&BacktestJob{}, column) {
						if err := tx.Migrator().AddColumn(&BacktestJob{}, column); err != nil {
							return err
						}
					}
				}
				return nil
			},
			Rollback: func(tx *gorm.DB) error {
				return fmt.Errorf("Stage 05 canonical governance evidence is intentionally retained")
			},
		},
		{
			ID: "202607171900_stage07_validation_governance",
			Migrate: func(tx *gorm.DB) error {
				// Freeze this migration to the tables and additive model-artifact
				// columns introduced by Stage 07. A global AutoMigrate here would
				// revisit immutable Stage 01 economic tables during upgrades from a
				// Stage 03-shaped schema and could impose modern NOT NULL columns
				// before their historical backfills have run.
				if err := tx.AutoMigrate(
					&ValidationExperiment{}, &ValidationFoldEvidence{}, &ValidationEvidence{},
					&ValidationMLEvidence{},
					&GovernanceApproval{}, &GovernanceDeployment{}, &GovernanceTransition{},
					&GovernanceMonitoringEvidence{},
					&ModelArtifact{},
				); err != nil {
					return err
				}
				return tx.Exec(`
					CREATE OR REPLACE FUNCTION reject_stage07_immutable_mutation() RETURNS trigger AS $$
					BEGIN RAISE EXCEPTION 'stage07 immutable audit record cannot be changed'; END;
					$$ LANGUAGE plpgsql;
					DROP TRIGGER IF EXISTS validation_experiments_immutable ON validation_experiments;
					CREATE TRIGGER validation_experiments_immutable BEFORE UPDATE OR DELETE ON validation_experiments FOR EACH ROW EXECUTE FUNCTION reject_stage07_immutable_mutation();
					DROP TRIGGER IF EXISTS validation_fold_evidence_immutable ON validation_fold_evidences;
					CREATE TRIGGER validation_fold_evidence_immutable BEFORE UPDATE OR DELETE ON validation_fold_evidences FOR EACH ROW EXECUTE FUNCTION reject_stage07_immutable_mutation();
					DROP TRIGGER IF EXISTS validation_evidence_immutable ON validation_evidences;
					CREATE TRIGGER validation_evidence_immutable BEFORE UPDATE OR DELETE ON validation_evidences FOR EACH ROW EXECUTE FUNCTION reject_stage07_immutable_mutation();
					DROP TRIGGER IF EXISTS governance_approvals_immutable ON governance_approvals;
					CREATE TRIGGER governance_approvals_immutable BEFORE UPDATE OR DELETE ON governance_approvals FOR EACH ROW EXECUTE FUNCTION reject_stage07_immutable_mutation();
					DROP TRIGGER IF EXISTS governance_transitions_immutable ON governance_transitions;
					CREATE TRIGGER governance_transitions_immutable BEFORE UPDATE OR DELETE ON governance_transitions FOR EACH ROW EXECUTE FUNCTION reject_stage07_immutable_mutation();
					ALTER TABLE validation_experiments DROP CONSTRAINT IF EXISTS validation_experiment_digest_check;
					ALTER TABLE validation_experiments ADD CONSTRAINT validation_experiment_digest_check CHECK (length(id)=64 AND length(content_id)=64 AND length(content_digest)=64 AND content_id=content_digest);
					ALTER TABLE validation_evidences DROP CONSTRAINT IF EXISTS validation_evidence_state_check;
					ALTER TABLE validation_evidences ADD CONSTRAINT validation_evidence_state_check CHECK (status IN ('passed','failed') AND length(id)=64 AND length(evidence_digest)=64);
					ALTER TABLE validation_fold_evidences DROP CONSTRAINT IF EXISTS validation_fold_evidence_state_check;
					ALTER TABLE validation_fold_evidences ADD CONSTRAINT validation_fold_evidence_state_check CHECK (status IN ('passed','failed') AND fold_index>=0 AND length(frozen_digest)=64 AND length(evidence_digest)=64);
					ALTER TABLE model_artifacts DROP CONSTRAINT IF EXISTS model_artifact_class_check;
					ALTER TABLE model_artifacts ADD CONSTRAINT model_artifact_class_check CHECK (artifact_class IN ('bootstrap','contract_fixture','research','shadow_candidate','promotable_candidate'));
					ALTER TABLE governance_approvals DROP CONSTRAINT IF EXISTS governance_approval_state_check;
					ALTER TABLE governance_approvals ADD CONSTRAINT governance_approval_state_check CHECK (target_state IN ('research','shadow','paper','limited_live','full_live','rollback') AND length(id)=64 AND length(content_digest)=64);
					ALTER TABLE governance_deployments DROP CONSTRAINT IF EXISTS governance_deployment_state_check;
					ALTER TABLE governance_deployments ADD CONSTRAINT governance_deployment_state_check CHECK (state IN ('research','shadow','paper','limited_live','full_live','rollback'));
					ALTER TABLE governance_transitions DROP CONSTRAINT IF EXISTS governance_transition_state_check;
					ALTER TABLE governance_transitions ADD CONSTRAINT governance_transition_state_check CHECK (from_state IN ('','research','shadow','paper','limited_live','full_live') AND to_state IN ('research','shadow','paper','limited_live','full_live','rollback') AND length(id)=64 AND length(content_digest)=64);
				`).Error
			},
			Rollback: func(tx *gorm.DB) error {
				return fmt.Errorf("Stage 07 immutable validation and governance history is intentionally retained")
			},
		},
		{
			ID: "202607172300_stage07_feedback_integrity",
			Migrate: func(tx *gorm.DB) error {
				if err := tx.AutoMigrate(&BacktestJob{}, &ValidationExperiment{}, &ValidationMLEvidence{}, &GovernanceApproval{}, &GovernanceDeployment{}, &GovernanceTransition{}, &GovernanceMonitoringEvidence{}); err != nil {
					return err
				}
				return tx.Exec(`
					CREATE OR REPLACE FUNCTION guard_governance_deployment_write() RETURNS trigger AS $$
					DECLARE transition_setting text;
					BEGIN
						transition_setting := current_setting('trading_bot.governance_transition_id', true);
						IF transition_setting IS NULL OR transition_setting = '' OR transition_setting <> NEW.transition_id OR NOT EXISTS (SELECT 1 FROM governance_transitions t WHERE t.id=NEW.transition_id AND t.context_key=NEW.context_key AND t.to_state=NEW.state) THEN
							RAISE EXCEPTION 'governance deployment write requires matching immutable transition';
						END IF;
						RETURN NEW;
					END; $$ LANGUAGE plpgsql;
					DROP TRIGGER IF EXISTS governance_deployment_guard ON governance_deployments;
					CREATE TRIGGER governance_deployment_guard BEFORE INSERT OR UPDATE OR DELETE ON governance_deployments FOR EACH ROW EXECUTE FUNCTION guard_governance_deployment_write();
					DROP TRIGGER IF EXISTS governance_monitoring_evidence_immutable ON governance_monitoring_evidences;
					CREATE TRIGGER governance_monitoring_evidence_immutable BEFORE UPDATE OR DELETE ON governance_monitoring_evidences FOR EACH ROW EXECUTE FUNCTION reject_stage07_immutable_mutation();
					ALTER TABLE validation_fold_evidences DROP CONSTRAINT IF EXISTS fk_validation_fold_experiment;
					ALTER TABLE validation_fold_evidences ADD CONSTRAINT fk_validation_fold_experiment FOREIGN KEY (experiment_id) REFERENCES validation_experiments(id) ON DELETE RESTRICT;
					ALTER TABLE validation_evidences DROP CONSTRAINT IF EXISTS fk_validation_evidence_experiment;
					ALTER TABLE validation_evidences ADD CONSTRAINT fk_validation_evidence_experiment FOREIGN KEY (experiment_id) REFERENCES validation_experiments(id) ON DELETE RESTRICT;
					ALTER TABLE governance_approvals DROP CONSTRAINT IF EXISTS fk_governance_approval_experiment;
					ALTER TABLE governance_approvals ADD CONSTRAINT fk_governance_approval_experiment FOREIGN KEY (experiment_id) REFERENCES validation_experiments(id) ON DELETE RESTRICT;
					ALTER TABLE governance_approvals DROP CONSTRAINT IF EXISTS fk_governance_approval_evidence;
					ALTER TABLE governance_approvals ADD CONSTRAINT fk_governance_approval_evidence FOREIGN KEY (evidence_id) REFERENCES validation_evidences(id) ON DELETE RESTRICT;
					ALTER TABLE governance_transitions DROP CONSTRAINT IF EXISTS fk_governance_transition_experiment;
					ALTER TABLE governance_transitions ADD CONSTRAINT fk_governance_transition_experiment FOREIGN KEY (experiment_id) REFERENCES validation_experiments(id) ON DELETE RESTRICT;
					ALTER TABLE governance_transitions DROP CONSTRAINT IF EXISTS fk_governance_transition_evidence;
					ALTER TABLE governance_transitions ADD CONSTRAINT fk_governance_transition_evidence FOREIGN KEY (evidence_id) REFERENCES validation_evidences(id) ON DELETE RESTRICT;
					ALTER TABLE governance_transitions DROP CONSTRAINT IF EXISTS fk_governance_transition_approval;
					ALTER TABLE governance_transitions ADD CONSTRAINT fk_governance_transition_approval FOREIGN KEY (approval_id) REFERENCES governance_approvals(id) ON DELETE RESTRICT;
					ALTER TABLE governance_deployments DROP CONSTRAINT IF EXISTS fk_governance_deployment_experiment;
					ALTER TABLE governance_deployments ADD CONSTRAINT fk_governance_deployment_experiment FOREIGN KEY (experiment_id) REFERENCES validation_experiments(id) ON DELETE RESTRICT;
					ALTER TABLE governance_deployments DROP CONSTRAINT IF EXISTS fk_governance_deployment_evidence;
					ALTER TABLE governance_deployments ADD CONSTRAINT fk_governance_deployment_evidence FOREIGN KEY (evidence_id) REFERENCES validation_evidences(id) ON DELETE RESTRICT;
					ALTER TABLE governance_deployments DROP CONSTRAINT IF EXISTS fk_governance_deployment_transition;
					ALTER TABLE governance_deployments ADD CONSTRAINT fk_governance_deployment_transition FOREIGN KEY (transition_id) REFERENCES governance_transitions(id) ON DELETE RESTRICT NOT VALID;
					ALTER TABLE governance_monitoring_evidences DROP CONSTRAINT IF EXISTS fk_monitoring_experiment;
					ALTER TABLE governance_monitoring_evidences ADD CONSTRAINT fk_monitoring_experiment FOREIGN KEY (experiment_id) REFERENCES validation_experiments(id) ON DELETE RESTRICT;
					ALTER TABLE governance_monitoring_evidences DROP CONSTRAINT IF EXISTS fk_monitoring_transition;
					ALTER TABLE governance_monitoring_evidences ADD CONSTRAINT fk_monitoring_transition FOREIGN KEY (deployment_transition_id) REFERENCES governance_transitions(id) ON DELETE RESTRICT;
					ALTER TABLE validation_ml_evidences DROP CONSTRAINT IF EXISTS fk_validation_ml_experiment;
					ALTER TABLE validation_ml_evidences ADD CONSTRAINT fk_validation_ml_experiment FOREIGN KEY (experiment_id) REFERENCES validation_experiments(id) ON DELETE RESTRICT;
					DROP TRIGGER IF EXISTS validation_ml_evidence_immutable ON validation_ml_evidences;
					CREATE TRIGGER validation_ml_evidence_immutable BEFORE UPDATE OR DELETE ON validation_ml_evidences FOR EACH ROW EXECUTE FUNCTION reject_stage07_immutable_mutation();
					ALTER TABLE governance_monitoring_evidences DROP CONSTRAINT IF EXISTS governance_monitoring_coverage_check;
					ALTER TABLE governance_monitoring_evidences ADD CONSTRAINT governance_monitoring_coverage_check CHECK (expected_observations>0 AND observed_observations>=0 AND observed_observations<=expected_observations AND window_end>window_start AND length(content_digest)=64);
				`).Error
			},
			Rollback: func(tx *gorm.DB) error {
				return fmt.Errorf("Stage 07 feedback audit integrity is intentionally retained")
			},
		},
		{
			ID: "202607180100_stage08_migration_cutover_operations",
			Migrate: func(tx *gorm.DB) error {
				for _, item := range []struct {
					model  any
					column string
				}{
					{&Order{}, "Stage08ContextJSON"}, {&Fill{}, "Stage08ContextJSON"}, {&LedgerEvent{}, "Stage08ContextJSON"},
					{&BacktestJob{}, "Stage08ContextJSON"}, {&TrendAnalysisHistory{}, "Stage08ContextJSON"}, {&ValidationExperiment{}, "Stage08ContextJSON"},
				} {
					if !tx.Migrator().HasTable(item.model) {
						continue
					}
					if !tx.Migrator().HasColumn(item.model, item.column) {
						if err := tx.Migrator().AddColumn(item.model, item.column); err != nil {
							return err
						}
					}
				}
				if err := tx.AutoMigrate(
					&Stage08FlagSnapshot{}, &ParityObservation{}, &ParityAggregate{}, &ParityAcceptancePolicy{},
					&OperationalIncident{}, &OperationalIncidentAudit{},
					&CutoverState{}, &CutoverTransition{}, &BackfillPlan{}, &BackupVerification{},
				); err != nil {
					return err
				}
				return tx.Exec(`
					CREATE OR REPLACE FUNCTION reject_stage08_immutable_mutation() RETURNS trigger AS $$
					BEGIN RAISE EXCEPTION 'stage08 immutable audit record cannot be changed'; END;
					$$ LANGUAGE plpgsql;
					DROP TRIGGER IF EXISTS stage08_flag_snapshots_immutable ON stage08_flag_snapshots;
					CREATE TRIGGER stage08_flag_snapshots_immutable BEFORE UPDATE OR DELETE ON stage08_flag_snapshots FOR EACH ROW EXECUTE FUNCTION reject_stage08_immutable_mutation();
					DROP TRIGGER IF EXISTS parity_observations_immutable ON parity_observations;
					CREATE TRIGGER parity_observations_immutable BEFORE UPDATE OR DELETE ON parity_observations FOR EACH ROW EXECUTE FUNCTION reject_stage08_immutable_mutation();
					DROP TRIGGER IF EXISTS parity_acceptance_policies_immutable ON parity_acceptance_policies;
					CREATE TRIGGER parity_acceptance_policies_immutable BEFORE UPDATE OR DELETE ON parity_acceptance_policies FOR EACH ROW EXECUTE FUNCTION reject_stage08_immutable_mutation();
					DROP TRIGGER IF EXISTS operational_incident_audits_immutable ON operational_incident_audits;
					CREATE TRIGGER operational_incident_audits_immutable BEFORE UPDATE OR DELETE ON operational_incident_audits FOR EACH ROW EXECUTE FUNCTION reject_stage08_immutable_mutation();
					DROP TRIGGER IF EXISTS cutover_transitions_immutable ON cutover_transitions;
					CREATE TRIGGER cutover_transitions_immutable BEFORE UPDATE OR DELETE ON cutover_transitions FOR EACH ROW EXECUTE FUNCTION reject_stage08_immutable_mutation();
					DROP TRIGGER IF EXISTS backfill_plans_immutable_after_apply ON backfill_plans;
					CREATE OR REPLACE FUNCTION guard_applied_backfill_plan() RETURNS trigger AS $$ BEGIN
					 IF OLD.status='applied' THEN RAISE EXCEPTION 'applied backfill plan is immutable'; END IF;
					 IF OLD.status='approved' AND (TG_OP='DELETE' OR NEW.status<>'applied' OR NEW.id<>OLD.id OR NEW.account_id<>OLD.account_id OR NEW.report_digest<>OLD.report_digest OR NEW.report_json<>OLD.report_json OR NEW.approval_digest<>OLD.approval_digest OR NEW.approved_by<>OLD.approved_by OR NEW.approved_at<>OLD.approved_at) THEN RAISE EXCEPTION 'approved backfill evidence is immutable'; END IF;
					 RETURN NEW;
					END; $$ LANGUAGE plpgsql;
					CREATE TRIGGER backfill_plans_immutable_after_apply BEFORE UPDATE OR DELETE ON backfill_plans FOR EACH ROW EXECUTE FUNCTION guard_applied_backfill_plan();
					ALTER TABLE parity_observations DROP CONSTRAINT IF EXISTS parity_observation_class_check;
					ALTER TABLE parity_observations ADD CONSTRAINT parity_observation_class_check CHECK (classification IN ('match','expected','unexplained'));
					ALTER TABLE operational_incidents DROP CONSTRAINT IF EXISTS operational_incident_state_check;
					ALTER TABLE operational_incidents ADD CONSTRAINT operational_incident_state_check CHECK (state IN ('open','acknowledged','resolved'));
					CREATE OR REPLACE FUNCTION guard_operational_incident_write() RETURNS trigger AS $$ BEGIN IF current_setting('trading_bot.operational_incident_write',true)<>'on' THEN RAISE EXCEPTION 'operational incident writes require audited service contract'; END IF; RETURN NEW; END; $$ LANGUAGE plpgsql;
					DROP TRIGGER IF EXISTS operational_incident_write_guard ON operational_incidents;
					CREATE TRIGGER operational_incident_write_guard BEFORE UPDATE OR DELETE ON operational_incidents FOR EACH ROW EXECUTE FUNCTION guard_operational_incident_write();
					ALTER TABLE backfill_plans DROP CONSTRAINT IF EXISTS backfill_plan_state_check;
					ALTER TABLE backfill_plans ADD CONSTRAINT backfill_plan_state_check CHECK (status IN ('planned','approved','applied'));
				`).Error
			},
			Rollback: func(tx *gorm.DB) error {
				return fmt.Errorf("Stage 08 cutover and operational audit history is intentionally retained")
			},
		},
		{
			ID: "202607181200_stage08_feedback_integrity",
			Migrate: func(tx *gorm.DB) error {
				if err := tx.AutoMigrate(&Stage08FlagSnapshot{}, &ParityObservation{}, &CutoverState{}, &CutoverTransition{}, &BackupVerification{}, &ParityPopulation{}, &CutoverPrerequisiteEvidence{}, &ReconciliationEvidence{}, &BrokerConflictCounter{}); err != nil {
					return err
				}
				return tx.Exec(`
					DROP TRIGGER IF EXISTS parity_populations_immutable ON parity_populations;
					CREATE TRIGGER parity_populations_immutable BEFORE UPDATE OR DELETE ON parity_populations FOR EACH ROW EXECUTE FUNCTION reject_stage08_immutable_mutation();
					DROP TRIGGER IF EXISTS cutover_prerequisite_evidences_immutable ON cutover_prerequisite_evidences;
					CREATE TRIGGER cutover_prerequisite_evidences_immutable BEFORE UPDATE OR DELETE ON cutover_prerequisite_evidences FOR EACH ROW EXECUTE FUNCTION reject_stage08_immutable_mutation();
					DROP TRIGGER IF EXISTS reconciliation_evidences_immutable ON reconciliation_evidences;
					CREATE TRIGGER reconciliation_evidences_immutable BEFORE UPDATE OR DELETE ON reconciliation_evidences FOR EACH ROW EXECUTE FUNCTION reject_stage08_immutable_mutation();
					DROP TRIGGER IF EXISTS backup_verifications_immutable ON backup_verifications;
					CREATE TRIGGER backup_verifications_immutable BEFORE UPDATE OR DELETE ON backup_verifications FOR EACH ROW EXECUTE FUNCTION reject_stage08_immutable_mutation();
					ALTER TABLE parity_populations DROP CONSTRAINT IF EXISTS parity_population_positive_check;
					ALTER TABLE parity_populations ADD CONSTRAINT parity_population_positive_check CHECK (expected_contexts > 0 AND window_end > window_start AND length(content_digest)=64);
					ALTER TABLE cutover_prerequisite_evidences DROP CONSTRAINT IF EXISTS cutover_evidence_window_check;
					ALTER TABLE cutover_prerequisite_evidences ADD CONSTRAINT cutover_evidence_window_check CHECK (window_end > window_start AND length(content_digest)=64 AND content_digest=id);
				`).Error
			},
			Rollback: func(tx *gorm.DB) error {
				return fmt.Errorf("Stage 08 feedback integrity evidence is intentionally retained")
			},
		},
		{
			ID: "202607181900_final_audit_projection_lifecycle_guards",
			Migrate: func(tx *gorm.DB) error {
				if err := validateProjectionGuardShape(tx); err != nil {
					return err
				}
				if err := tx.Exec(`
					CREATE OR REPLACE FUNCTION guard_position_economics() RETURNS trigger AS $$
					BEGIN
					 IF current_setting('trading_bot.ledger_write',true) IS DISTINCT FROM 'on' THEN
					   IF TG_OP IN ('INSERT','DELETE') THEN
					     RAISE EXCEPTION 'position lifecycle requires ledger transaction';
					   END IF;
					   IF (OLD.amount,OLD.amount_exact,OLD.cost_basis_exact,OLD.realized_pn_l_exact,OLD.fees_exact,OLD.avg_price,OLD.status,OLD.opened_at,OLD.closed_at,OLD.close_reason)
					      IS DISTINCT FROM
					      (NEW.amount,NEW.amount_exact,NEW.cost_basis_exact,NEW.realized_pn_l_exact,NEW.fees_exact,NEW.avg_price,NEW.status,NEW.opened_at,NEW.closed_at,NEW.close_reason)
					   THEN RAISE EXCEPTION 'position economic columns require ledger transaction'; END IF;
					 END IF;
					 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
					 RETURN NEW;
					END; $$ LANGUAGE plpgsql;
					CREATE OR REPLACE FUNCTION guard_wallet_economics() RETURNS trigger AS $$
					BEGIN
					 IF current_setting('trading_bot.ledger_write',true) IS DISTINCT FROM 'on' THEN
					   IF TG_OP IN ('INSERT','DELETE') THEN
					     RAISE EXCEPTION 'wallet lifecycle requires ledger transaction';
					   END IF;
					   IF (OLD.balance,OLD.balance_exact,OLD.currency) IS DISTINCT FROM (NEW.balance,NEW.balance_exact,NEW.currency) THEN
					     RAISE EXCEPTION 'wallet economic columns require ledger transaction';
					   END IF;
					 END IF;
					 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
					 RETURN NEW;
					END; $$ LANGUAGE plpgsql;
				`).Error; err != nil {
					return err
				}
				if tx.Migrator().HasTable(&Position{}) {
					if err := tx.Exec(`DROP TRIGGER IF EXISTS positions_economic_guard ON positions; CREATE TRIGGER positions_economic_guard BEFORE INSERT OR UPDATE OR DELETE ON positions FOR EACH ROW EXECUTE FUNCTION guard_position_economics();`).Error; err != nil {
						return err
					}
				}
				if tx.Migrator().HasTable(&Wallet{}) {
					if err := tx.Exec(`DROP TRIGGER IF EXISTS wallets_economic_guard ON wallets; CREATE TRIGGER wallets_economic_guard BEFORE INSERT OR UPDATE OR DELETE ON wallets FOR EACH ROW EXECUTE FUNCTION guard_wallet_economics();`).Error; err != nil {
						return err
					}
				}
				return nil
			},
			Rollback: func(tx *gorm.DB) error {
				return fmt.Errorf("final audit economic projection guards are intentionally retained")
			},
		},
		{
			ID: "202607182300_final_audit_authority_and_evidence",
			Migrate: func(tx *gorm.DB) error {
				if !tx.Migrator().HasTable("wallets") || !tx.Migrator().HasTable("positions") {
					return fmt.Errorf("unsupported database shape: runtime requires both wallets and positions tables")
				}
				if err := validateProjectionGuardShape(tx); err != nil {
					return err
				}
				for table, columns := range map[string][]string{
					"fills":         {"strategy_version"},
					"ledger_events": {"strategy_version"},
					"orders":        {"model_version"},
					"positions":     {"model_version", "strategy_version"},
				} {
					if err := requireProjectionGuardColumns(tx, table, columns); err != nil {
						return err
					}
				}
				if err := tx.AutoMigrate(&ParityObservation{}); err != nil {
					return err
				}
				return tx.Exec(`
					DO $$ BEGIN
					 IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='trading_bot_ledger_owner') THEN CREATE ROLE trading_bot_ledger_owner NOLOGIN NOINHERIT; END IF;
					 IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='trading_bot_runtime') THEN CREATE ROLE trading_bot_runtime NOLOGIN NOINHERIT; END IF;
					 IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='trading_bot_ledger_writer') THEN CREATE ROLE trading_bot_ledger_writer NOLOGIN NOINHERIT; END IF;
					 IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='trading_bot_parity_writer') THEN CREATE ROLE trading_bot_parity_writer NOLOGIN NOINHERIT; END IF;
					END $$;
					ALTER ROLE trading_bot_ledger_writer NOINHERIT;
					REVOKE trading_bot_parity_writer FROM trading_bot_ledger_writer;
					ALTER TABLE fills ALTER COLUMN strategy_version TYPE text;
					ALTER TABLE ledger_events ALTER COLUMN strategy_version TYPE text;
					ALTER TABLE orders ALTER COLUMN model_version TYPE text;
					ALTER TABLE positions ALTER COLUMN model_version TYPE text;
					ALTER TABLE positions ALTER COLUMN strategy_version TYPE text;
					DROP INDEX IF EXISTS idx_parity_context_pair;
					CREATE UNIQUE INDEX IF NOT EXISTS idx_parity_population_context_pair ON parity_observations(population_id,context_id,pair_key);

					CREATE OR REPLACE FUNCTION guard_position_economics() RETURNS trigger AS $$
					BEGIN
					 IF current_user <> 'trading_bot_ledger_writer' THEN
					   IF TG_OP IN ('INSERT','DELETE') THEN RAISE EXCEPTION 'position lifecycle requires ledger writer role'; END IF;
					   IF (to_jsonb(OLD) - ARRAY['current_price','last_mark_price','last_mark_at','pnl','pnl_percent','updated_at','trailing_stop_price','stop_price','take_profit_price','exit_pending','last_atr_value','max_bars_held'])
					      IS DISTINCT FROM
					      (to_jsonb(NEW) - ARRAY['current_price','last_mark_price','last_mark_at','pnl','pnl_percent','updated_at','trailing_stop_price','stop_price','take_profit_price','exit_pending','last_atr_value','max_bars_held'])
					   THEN RAISE EXCEPTION 'position economic, provenance, identity, and lifecycle fields require ledger writer role'; END IF;
					 END IF;
					 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
					END; $$ LANGUAGE plpgsql;
					CREATE OR REPLACE FUNCTION guard_wallet_economics() RETURNS trigger AS $$
					BEGIN
					 IF current_user <> 'trading_bot_ledger_writer' THEN
					   IF TG_OP IN ('INSERT','DELETE') OR (to_jsonb(OLD)-ARRAY['updated_at']) IS DISTINCT FROM (to_jsonb(NEW)-ARRAY['updated_at'])
					   THEN RAISE EXCEPTION 'wallet economics and identity require ledger writer role'; END IF;
					 END IF;
					 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
					END; $$ LANGUAGE plpgsql;
					CREATE OR REPLACE FUNCTION require_current_ledger_projection() RETURNS trigger AS $$
					DECLARE current_xid xid := pg_current_xact_id()::text::xid;
					DECLARE event_delta numeric;
					BEGIN
					 IF TG_TABLE_NAME='wallets' THEN
					   IF TG_OP='UPDATE' AND (to_jsonb(OLD)-ARRAY['updated_at'])=(to_jsonb(NEW)-ARRAY['updated_at']) THEN RETURN NEW; END IF;
					   IF TG_OP='INSERT' AND NEW.balance_exact IS NULL THEN RETURN NEW; END IF;
					   SELECT COALESCE(sum(cash_delta),0) INTO event_delta FROM ledger_events
					    WHERE xmin=current_xid AND account_id=NEW.account_id AND currency=NEW.currency;
					   IF NOT EXISTS (SELECT 1 FROM ledger_events WHERE xmin=current_xid AND account_id=NEW.account_id AND currency=NEW.currency)
					      OR COALESCE(NEW.balance_exact,0)-COALESCE(CASE WHEN TG_OP='UPDATE' THEN OLD.balance_exact END,0) <> event_delta
					   THEN RAISE EXCEPTION 'wallet projection change must equal immutable ledger events written by the same transaction'; END IF;
					 ELSE
					   IF TG_OP='UPDATE' AND
					      (to_jsonb(OLD)-ARRAY['current_price','last_mark_price','last_mark_at','pnl','pnl_percent','updated_at','trailing_stop_price','stop_price','take_profit_price','exit_pending','last_atr_value','max_bars_held']) =
					      (to_jsonb(NEW)-ARRAY['current_price','last_mark_price','last_mark_at','pnl','pnl_percent','updated_at','trailing_stop_price','stop_price','take_profit_price','exit_pending','last_atr_value','max_bars_held']) THEN RETURN NEW; END IF;
					   IF TG_OP='UPDATE' AND OLD.amount_exact IS NULL AND COALESCE(NEW.amount_exact,0)=0 AND OLD.status<>'open' THEN RETURN NEW; END IF;
					   SELECT COALESCE(sum(asset_delta),0) INTO event_delta FROM ledger_events
					    WHERE xmin=current_xid AND account_id=NEW.account_id AND symbol=NEW.symbol AND position_id=NEW.id;
					   IF NOT EXISTS (SELECT 1 FROM ledger_events WHERE xmin=current_xid AND account_id=NEW.account_id AND symbol=NEW.symbol AND position_id=NEW.id)
					      OR COALESCE(NEW.amount_exact,0)-COALESCE(CASE WHEN TG_OP='UPDATE' THEN OLD.amount_exact END,0) <> event_delta
					   THEN RAISE EXCEPTION 'position projection change must equal immutable ledger events written by the same transaction'; END IF;
					 END IF;
					 RETURN NEW;
					END; $$ LANGUAGE plpgsql;
					CREATE OR REPLACE FUNCTION require_current_projection_for_ledger() RETURNS trigger AS $$
					DECLARE current_xid xid := pg_current_xact_id()::text::xid;
					BEGIN
					 IF TG_TABLE_NAME='ledger_batches' THEN
					   IF NOT EXISTS (SELECT 1 FROM ledger_events WHERE xmin=current_xid AND ledger_batch_id=NEW.id)
					   THEN RAISE EXCEPTION 'ledger batch must contain immutable events written by the same transaction'; END IF;
					 ELSIF TG_TABLE_NAME='fills' THEN
					   IF NOT EXISTS (SELECT 1 FROM ledger_events WHERE xmin=current_xid AND fill_id=NEW.id AND ledger_batch_id=NEW.ledger_batch_id)
					   THEN RAISE EXCEPTION 'fill must be coupled to immutable ledger events written by the same transaction'; END IF;
					 ELSE
					   IF NEW.cash_delta=0 AND NEW.asset_delta=0 AND NEW.fill_id IS NULL
					   THEN RAISE EXCEPTION 'ledger event must carry an economic delta or reference a coupled fill'; END IF;
					   IF NEW.cash_delta<>0 AND NOT EXISTS (SELECT 1 FROM wallets WHERE xmin=current_xid AND account_id=NEW.account_id AND currency=NEW.currency)
					   THEN RAISE EXCEPTION 'cash ledger event must be coupled to a wallet projection written by the same transaction'; END IF;
					   IF NEW.asset_delta<>0 AND NOT EXISTS (SELECT 1 FROM positions WHERE xmin=current_xid AND id=NEW.position_id AND account_id=NEW.account_id AND symbol=NEW.symbol)
					   THEN RAISE EXCEPTION 'asset ledger event must be coupled to a position projection written by the same transaction'; END IF;
					 END IF;
					 RETURN NEW;
					END; $$ LANGUAGE plpgsql;
					CREATE OR REPLACE FUNCTION require_current_operation_for_provenance() RETURNS trigger AS $$
					DECLARE current_xid xid := pg_current_xact_id()::text::xid;
					BEGIN
					 IF current_user<>'trading_bot_ledger_writer' THEN RETURN NEW; END IF;
					 IF TG_TABLE_NAME='orders' THEN
					   IF NOT EXISTS (SELECT 1 FROM fills WHERE xmin=current_xid AND order_id=NEW.id)
					      AND NOT EXISTS (SELECT 1 FROM broker_outcome_ingestions WHERE xmin=current_xid AND account_id=NEW.account_id)
					   THEN RAISE EXCEPTION 'ledger-writer order provenance must be coupled to a fill or broker outcome in the same transaction'; END IF;
					 ELSE
					   IF NOT EXISTS (SELECT 1 FROM ledger_events WHERE xmin=current_xid AND account_id=NEW.account_id)
					   THEN RAISE EXCEPTION 'ledger migration state must be coupled to immutable ledger evidence in the same transaction'; END IF;
					 END IF;
					 RETURN NEW;
					END; $$ LANGUAGE plpgsql;
					ALTER FUNCTION guard_position_economics() OWNER TO trading_bot_ledger_owner;
					ALTER FUNCTION guard_wallet_economics() OWNER TO trading_bot_ledger_owner;
					ALTER FUNCTION require_current_ledger_projection() OWNER TO trading_bot_ledger_owner;
					ALTER FUNCTION require_current_projection_for_ledger() OWNER TO trading_bot_ledger_owner;
					ALTER FUNCTION require_current_operation_for_provenance() OWNER TO trading_bot_ledger_owner;
					REVOKE ALL ON FUNCTION guard_position_economics() FROM PUBLIC;
					REVOKE ALL ON FUNCTION guard_wallet_economics() FROM PUBLIC;
					REVOKE ALL ON FUNCTION require_current_ledger_projection() FROM PUBLIC;
					REVOKE ALL ON FUNCTION require_current_projection_for_ledger() FROM PUBLIC;
					REVOKE ALL ON FUNCTION require_current_operation_for_provenance() FROM PUBLIC;
					DROP TRIGGER IF EXISTS positions_economic_guard ON positions;
					CREATE TRIGGER positions_economic_guard BEFORE INSERT OR UPDATE OR DELETE ON positions FOR EACH ROW EXECUTE FUNCTION guard_position_economics();
					DROP TRIGGER IF EXISTS wallets_economic_guard ON wallets;
					CREATE TRIGGER wallets_economic_guard BEFORE INSERT OR UPDATE OR DELETE ON wallets FOR EACH ROW EXECUTE FUNCTION guard_wallet_economics();
					DROP TRIGGER IF EXISTS wallets_ledger_coupling ON wallets;
					CREATE CONSTRAINT TRIGGER wallets_ledger_coupling AFTER INSERT OR UPDATE ON wallets DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION require_current_ledger_projection();
					DROP TRIGGER IF EXISTS positions_ledger_coupling ON positions;
					CREATE CONSTRAINT TRIGGER positions_ledger_coupling AFTER INSERT OR UPDATE ON positions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION require_current_ledger_projection();
					DROP TRIGGER IF EXISTS ledger_batches_projection_coupling ON ledger_batches;
					CREATE CONSTRAINT TRIGGER ledger_batches_projection_coupling AFTER INSERT ON ledger_batches DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION require_current_projection_for_ledger();
					DROP TRIGGER IF EXISTS fills_projection_coupling ON fills;
					CREATE CONSTRAINT TRIGGER fills_projection_coupling AFTER INSERT ON fills DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION require_current_projection_for_ledger();
					DROP TRIGGER IF EXISTS ledger_events_projection_coupling ON ledger_events;
					CREATE CONSTRAINT TRIGGER ledger_events_projection_coupling AFTER INSERT ON ledger_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION require_current_projection_for_ledger();
					DROP TRIGGER IF EXISTS orders_provenance_coupling ON orders;
					CREATE CONSTRAINT TRIGGER orders_provenance_coupling AFTER INSERT OR UPDATE ON orders DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION require_current_operation_for_provenance();
					DROP TRIGGER IF EXISTS ledger_migration_states_provenance_coupling ON ledger_migration_states;
					CREATE CONSTRAINT TRIGGER ledger_migration_states_provenance_coupling AFTER INSERT OR UPDATE ON ledger_migration_states DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION require_current_operation_for_provenance();

					REVOKE INSERT, UPDATE, DELETE ON wallets, positions, ledger_batches, fills, ledger_events, parity_observations FROM PUBLIC, trading_bot_runtime;
					GRANT SELECT ON wallets, positions, orders, ledger_batches, fills, ledger_events, parity_observations TO trading_bot_runtime;
					GRANT UPDATE (current_price,last_mark_price,last_mark_at,pnl,pnl_percent,exit_pending) ON positions TO trading_bot_runtime;
					REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM trading_bot_ledger_writer;
					REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM trading_bot_ledger_writer;
					REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public FROM trading_bot_ledger_writer;
					GRANT SELECT ON wallets,positions,orders,ledger_batches,fills,ledger_events,ledger_migration_states,broker_outcome_ingestions TO trading_bot_ledger_writer;
					GRANT INSERT,UPDATE ON wallets,positions,orders,ledger_migration_states TO trading_bot_ledger_writer;
					GRANT INSERT ON ledger_batches,fills,ledger_events,broker_outcome_ingestions TO trading_bot_ledger_writer;
					GRANT USAGE ON SCHEMA public TO trading_bot_runtime, trading_bot_ledger_writer;
					GRANT USAGE ON SCHEMA public TO trading_bot_parity_writer;
					GRANT SELECT ON parity_observations, parity_populations, parity_acceptance_policies, stage08_flag_snapshots, cutover_states TO trading_bot_parity_writer;
					GRANT INSERT, UPDATE ON parity_observations TO trading_bot_parity_writer;
					GRANT SELECT, INSERT, UPDATE ON parity_aggregates TO trading_bot_parity_writer;
					GRANT USAGE,SELECT ON SEQUENCE wallets_id_seq,positions_id_seq,orders_id_seq TO trading_bot_ledger_writer;
				`).Error
			},
			Rollback: func(tx *gorm.DB) error {
				return fmt.Errorf("final audit database authority boundary is intentionally retained")
			},
		},
		{
			ID: "202607182330_runtime_principal_grants",
			Migrate: func(tx *gorm.DB) error {
				if !tx.Migrator().HasTable("wallets") || !tx.Migrator().HasTable("positions") {
					return fmt.Errorf("unsupported database shape: runtime requires both wallets and positions tables")
				}
				return tx.Exec(`
					GRANT USAGE ON SCHEMA public TO trading_bot_runtime;
					GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO trading_bot_runtime;
					GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO trading_bot_runtime;

					REVOKE INSERT, UPDATE, DELETE ON schema_migrations, ledger_migration_states, wallets, positions, ledger_batches, fills, ledger_events, parity_observations, parity_aggregates, parity_acceptance_policies, parity_populations FROM trading_bot_runtime;
					GRANT SELECT ON wallets, positions, orders, ledger_batches, fills, ledger_events, parity_observations TO trading_bot_runtime;
					GRANT UPDATE (current_price,last_mark_price,last_mark_at,pnl,pnl_percent,exit_pending) ON positions TO trading_bot_runtime;
				`).Error
			},
			Rollback: func(tx *gorm.DB) error {
				return fmt.Errorf("runtime principal grants are intentionally retained")
			},
		},
		{
			ID: "202607190100_secure_ledger_operational_paths",
			Migrate: func(tx *gorm.DB) error {
				if !tx.Migrator().HasTable("wallets") || !tx.Migrator().HasTable("positions") || !tx.Migrator().HasTable("ledger_events") {
					return fmt.Errorf("unsupported database shape: secure ledger remediation requires ledger and projection tables")
				}
				// Forward-repair legacy shaped schemas before installing column-level grants
				// and trigger whitelists used by the runtime path.
				if err := tx.AutoMigrate(&Position{}, &Wallet{}, &LedgerMigrationState{}); err != nil {
					return err
				}
				return tx.Exec(`
					DO $$ BEGIN
					 IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='trading_bot_operations_owner') THEN CREATE ROLE trading_bot_operations_owner NOLOGIN NOINHERIT; END IF;
					 IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='trading_bot_migration_admin') THEN CREATE ROLE trading_bot_migration_admin NOLOGIN NOINHERIT; END IF;
					END $$;
					INSERT INTO ledger_migration_states(account_id,status,unresolved_json,created_at,updated_at)
					SELECT 'primary','pending_approval','["legacy wallet balance has no immutable capital provenance"]',now(),now()
					WHERE EXISTS(SELECT 1 FROM wallets)
					  AND NOT EXISTS(SELECT 1 FROM ledger_migration_states WHERE account_id='primary')
					  AND NOT EXISTS(SELECT 1 FROM ledger_events);
					ALTER ROLE trading_bot_ledger_writer NOINHERIT;
					DO $memberships$ DECLARE inherited record; BEGIN
					 FOR inherited IN
					   SELECT parent.rolname FROM pg_catalog.pg_auth_members membership
					   JOIN pg_catalog.pg_roles parent ON parent.oid=membership.roleid
					   WHERE membership.member=(SELECT oid FROM pg_catalog.pg_roles WHERE rolname='trading_bot_ledger_writer')
					 LOOP EXECUTE format('REVOKE %I FROM trading_bot_ledger_writer', inherited.rolname); END LOOP;
					END $memberships$;

					CREATE OR REPLACE FUNCTION guard_position_economics() RETURNS trigger AS $$
					BEGIN
					 IF current_user='trading_bot_migration_admin' THEN
					   IF TG_OP<>'INSERT' OR NEW.amount_exact IS NOT NULL OR NEW.cost_basis_exact IS NOT NULL
					      OR NOT EXISTS (SELECT 1 FROM ledger_migration_states WHERE account_id=NEW.account_id AND status IN ('pending_approval','pending_resolution'))
					   THEN RAISE EXCEPTION 'migration admin may only stage explicitly unresolved non-exact legacy positions'; END IF;
					   RETURN NEW;
					 END IF;
					 IF current_user <> 'trading_bot_ledger_writer' THEN
					   IF TG_OP IN ('INSERT','DELETE') THEN RAISE EXCEPTION 'position lifecycle requires ledger writer role'; END IF;
					   IF (to_jsonb(OLD) - ARRAY['current_price','last_mark_price','last_mark_at','pnl','pnl_percent','updated_at','trailing_stop_price','stop_price','take_profit_price','exit_pending','last_atr_value','max_bars_held'])
					      IS DISTINCT FROM
					      (to_jsonb(NEW) - ARRAY['current_price','last_mark_price','last_mark_at','pnl','pnl_percent','updated_at','trailing_stop_price','stop_price','take_profit_price','exit_pending','last_atr_value','max_bars_held'])
					   THEN RAISE EXCEPTION 'position economic, provenance, identity, and lifecycle fields require ledger writer role'; END IF;
					 END IF;
					 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
					END; $$ LANGUAGE plpgsql;
					CREATE OR REPLACE FUNCTION guard_wallet_economics() RETURNS trigger AS $$
					BEGIN
					 IF current_user='trading_bot_migration_admin' THEN
					   IF TG_OP<>'INSERT' OR NEW.balance_exact IS NOT NULL
					      OR NOT EXISTS (SELECT 1 FROM ledger_migration_states WHERE account_id=NEW.account_id AND status IN ('pending_approval','pending_resolution'))
					   THEN RAISE EXCEPTION 'migration admin may only stage explicitly unresolved non-exact legacy wallets'; END IF;
					   RETURN NEW;
					 END IF;
					 IF current_user <> 'trading_bot_ledger_writer' THEN
					   IF TG_OP IN ('INSERT','DELETE') OR (to_jsonb(OLD)-ARRAY['updated_at']) IS DISTINCT FROM (to_jsonb(NEW)-ARRAY['updated_at'])
					   THEN RAISE EXCEPTION 'wallet economics and identity require ledger writer role'; END IF;
					 END IF;
					 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
					END; $$ LANGUAGE plpgsql;
					CREATE OR REPLACE FUNCTION require_current_ledger_projection() RETURNS trigger AS $$
					DECLARE current_xid xid := pg_current_xact_id()::text::xid;
					DECLARE event_delta numeric;
					BEGIN
					 IF TG_TABLE_NAME='wallets' THEN
					   IF TG_OP='UPDATE' AND (to_jsonb(OLD)-ARRAY['updated_at'])=(to_jsonb(NEW)-ARRAY['updated_at']) THEN RETURN NEW; END IF;
					   IF TG_OP='INSERT' AND NEW.balance_exact IS NULL THEN RETURN NEW; END IF;
					   SELECT COALESCE(sum(cash_delta),0) INTO event_delta FROM ledger_events WHERE xmin=current_xid AND account_id=NEW.account_id AND currency=NEW.currency;
					   IF NOT EXISTS (SELECT 1 FROM ledger_events WHERE xmin=current_xid AND account_id=NEW.account_id AND currency=NEW.currency)
					      OR COALESCE(NEW.balance_exact,0)-COALESCE(CASE WHEN TG_OP='UPDATE' THEN OLD.balance_exact END,0) <> event_delta
					   THEN RAISE EXCEPTION 'wallet projection change must equal immutable ledger events written by the same transaction'; END IF;
					 ELSE
					   IF current_user='trading_bot_migration_admin' AND TG_OP='INSERT' AND NEW.amount_exact IS NULL AND NEW.cost_basis_exact IS NULL
					      AND EXISTS (SELECT 1 FROM ledger_migration_states WHERE xmin=current_xid AND account_id=NEW.account_id AND status IN ('pending_approval','pending_resolution'))
					   THEN RETURN NEW; END IF;
					   IF TG_OP='UPDATE' AND
					      (to_jsonb(OLD)-ARRAY['current_price','last_mark_price','last_mark_at','pnl','pnl_percent','updated_at','trailing_stop_price','stop_price','take_profit_price','exit_pending','last_atr_value','max_bars_held']) =
					      (to_jsonb(NEW)-ARRAY['current_price','last_mark_price','last_mark_at','pnl','pnl_percent','updated_at','trailing_stop_price','stop_price','take_profit_price','exit_pending','last_atr_value','max_bars_held']) THEN RETURN NEW; END IF;
					   IF TG_OP='UPDATE' AND OLD.amount_exact IS NULL AND COALESCE(NEW.amount_exact,0)=0 AND OLD.status<>'open' THEN RETURN NEW; END IF;
					   SELECT COALESCE(sum(asset_delta),0) INTO event_delta FROM ledger_events WHERE xmin=current_xid AND account_id=NEW.account_id AND symbol=NEW.symbol AND position_id=NEW.id;
					   IF NOT EXISTS (SELECT 1 FROM ledger_events WHERE xmin=current_xid AND account_id=NEW.account_id AND symbol=NEW.symbol AND position_id=NEW.id)
					      OR COALESCE(NEW.amount_exact,0)-COALESCE(CASE WHEN TG_OP='UPDATE' THEN OLD.amount_exact END,0) <> event_delta
					   THEN RAISE EXCEPTION 'position projection change must equal immutable ledger events written by the same transaction'; END IF;
					 END IF;
					 RETURN NEW;
					END; $$ LANGUAGE plpgsql;
					CREATE OR REPLACE FUNCTION require_current_operation_for_provenance() RETURNS trigger AS $$
					DECLARE current_xid xid := pg_current_xact_id()::text::xid;
					BEGIN
					 IF current_user='trading_bot_migration_admin' THEN
					   IF TG_TABLE_NAME='orders' OR NEW.status NOT IN ('pending_approval','pending_resolution') OR NEW.unresolved_json IS NULL OR NEW.unresolved_json = '[]'::jsonb
					   THEN RAISE EXCEPTION 'migration admin ledger state must remain explicitly unresolved'; END IF;
					   RETURN NEW;
					 END IF;
					 IF current_user<>'trading_bot_ledger_writer' THEN RETURN NEW; END IF;
					 IF TG_TABLE_NAME='orders' THEN
					   IF NOT EXISTS (SELECT 1 FROM fills WHERE xmin=current_xid AND order_id=NEW.id)
					      AND NOT EXISTS (SELECT 1 FROM broker_outcome_ingestions WHERE xmin=current_xid AND account_id=NEW.account_id)
					   THEN RAISE EXCEPTION 'ledger-writer order provenance must be coupled to a fill or broker outcome in the same transaction'; END IF;
					 ELSE
					   IF NOT EXISTS (SELECT 1 FROM ledger_events WHERE xmin=current_xid AND account_id=NEW.account_id)
					   THEN RAISE EXCEPTION 'ledger migration state must be coupled to immutable ledger evidence in the same transaction'; END IF;
					 END IF;
					 RETURN NEW;
					END; $$ LANGUAGE plpgsql;
					CREATE OR REPLACE FUNCTION finalize_applied_backfill_plan(plan_id text, approval text, applied timestamptz) RETURNS void AS $$
					DECLARE changed bigint;
					DECLARE current_xid xid := pg_current_xact_id()::text::xid;
					BEGIN
					 UPDATE backfill_plans plan SET status='applied',applied_at=applied
					 WHERE plan.id=plan_id AND plan.status='approved' AND plan.approval_digest=approval
					   AND EXISTS (SELECT 1 FROM ledger_migration_states state WHERE state.xmin=current_xid AND state.account_id=plan.account_id)
					   AND EXISTS (SELECT 1 FROM ledger_events event WHERE event.xmin=current_xid AND event.account_id=plan.account_id);
					 GET DIAGNOSTICS changed = ROW_COUNT;
					 IF changed<>1 THEN RAISE EXCEPTION 'valid approved backfill plan required'; END IF;
					END; $$ LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public;
					ALTER FUNCTION guard_position_economics() OWNER TO trading_bot_ledger_owner;
					ALTER FUNCTION guard_wallet_economics() OWNER TO trading_bot_ledger_owner;
					ALTER FUNCTION require_current_ledger_projection() OWNER TO trading_bot_ledger_owner;
					ALTER FUNCTION require_current_operation_for_provenance() OWNER TO trading_bot_ledger_owner;
					ALTER FUNCTION finalize_applied_backfill_plan(text,text,timestamptz) OWNER TO trading_bot_operations_owner;
					REVOKE ALL ON FUNCTION finalize_applied_backfill_plan(text,text,timestamptz) FROM PUBLIC;
					GRANT SELECT ON backfill_plans TO trading_bot_operations_owner;
					GRANT SELECT ON ledger_migration_states,ledger_events TO trading_bot_operations_owner;
					GRANT UPDATE(status,applied_at) ON backfill_plans TO trading_bot_operations_owner;
					GRANT USAGE ON SCHEMA public TO trading_bot_operations_owner;
					GRANT EXECUTE ON FUNCTION finalize_applied_backfill_plan(text,text,timestamptz) TO trading_bot_ledger_writer;
					GRANT SELECT ON backfill_plans TO trading_bot_ledger_writer;
					GRANT UPDATE (current_price,last_mark_price,last_mark_at,pnl,pnl_percent,trailing_stop_price,stop_price,take_profit_price,exit_pending,last_atr_value,max_bars_held) ON positions TO trading_bot_runtime;
					GRANT USAGE ON SCHEMA public TO trading_bot_migration_admin;
					GRANT SELECT ON ledger_migration_states TO trading_bot_migration_admin;
					GRANT SELECT ON positions,wallets TO trading_bot_migration_admin;
					GRANT INSERT,UPDATE(status,unresolved_json,updated_at) ON ledger_migration_states TO trading_bot_migration_admin;
					GRANT INSERT ON positions,wallets TO trading_bot_migration_admin;
					GRANT USAGE,SELECT ON SEQUENCE wallets_id_seq,positions_id_seq TO trading_bot_migration_admin;
				`).Error
			},
			Rollback: func(tx *gorm.DB) error {
				return fmt.Errorf("secure ledger operational paths are intentionally retained")
			},
		},
		{
			ID: "202607190101_fix_jsonb_provenance_guard",
			Migrate: func(tx *gorm.DB) error {
				return tx.Exec(`
					CREATE OR REPLACE FUNCTION require_current_operation_for_provenance() RETURNS trigger AS $$
					DECLARE current_xid xid := pg_current_xact_id()::text::xid;
					BEGIN
					 IF current_user='trading_bot_migration_admin' THEN
					   IF TG_TABLE_NAME='orders' OR NEW.status NOT IN ('pending_approval','pending_resolution') OR NEW.unresolved_json IS NULL OR NEW.unresolved_json = '[]'::jsonb
					   THEN RAISE EXCEPTION 'migration admin ledger state must remain explicitly unresolved'; END IF;
					   RETURN NEW;
					 END IF;
					 IF current_user<>'trading_bot_ledger_writer' THEN RETURN NEW; END IF;
					 IF TG_TABLE_NAME='orders' THEN
					   IF NOT EXISTS (SELECT 1 FROM fills WHERE xmin=current_xid AND order_id=NEW.id) AND NOT EXISTS (SELECT 1 FROM broker_outcome_ingestions WHERE xmin=current_xid AND account_id=NEW.account_id)
					   THEN RAISE EXCEPTION 'ledger-writer order provenance must be coupled to a fill or broker outcome in the same transaction'; END IF;
					 ELSE
					   IF NOT EXISTS (SELECT 1 FROM ledger_events WHERE xmin=current_xid AND account_id=NEW.account_id)
					   THEN RAISE EXCEPTION 'ledger migration state must be coupled to immutable ledger evidence in the same transaction'; END IF;
					 END IF;
					 RETURN NEW;
					END; $$ LANGUAGE plpgsql;
				`).Error
			},
			Rollback: func(tx *gorm.DB) error { return fmt.Errorf("jsonb provenance guard fix is retained") },
		},
		{
			ID: "202607190102_fix_subtransaction_coupling_and_parity_grants",
			Migrate: func(tx *gorm.DB) error {
				return tx.Exec(`
					CREATE OR REPLACE FUNCTION require_current_ledger_projection() RETURNS trigger AS $$
					DECLARE event_delta numeric;
					DECLARE projected_value numeric;
					DECLARE event_total numeric;
					BEGIN
					 IF TG_TABLE_NAME='wallets' THEN
					   IF TG_OP='UPDATE' AND (to_jsonb(OLD)-ARRAY['updated_at'])=(to_jsonb(NEW)-ARRAY['updated_at']) THEN RETURN NEW; END IF;
					   IF TG_OP='INSERT' AND NEW.balance_exact IS NULL THEN RETURN NEW; END IF;
					   SELECT balance_exact INTO projected_value FROM wallets WHERE id=NEW.id;
					   SELECT COALESCE(sum(cash_delta),0) INTO event_total FROM ledger_events
					    WHERE account_id=NEW.account_id AND currency=NEW.currency;
					   IF NOT EXISTS (SELECT 1 FROM ledger_events WHERE pg_xact_status(xmin::text::xid8)='in progress' AND account_id=NEW.account_id AND currency=NEW.currency)
					      OR COALESCE(projected_value,0) <> event_total
					   THEN RAISE EXCEPTION 'wallet projection change must equal immutable ledger events written by the same transaction'; END IF;
					 ELSE
					   IF current_user='trading_bot_migration_admin' AND TG_OP='INSERT' AND NEW.amount_exact IS NULL AND NEW.cost_basis_exact IS NULL
					      AND EXISTS (SELECT 1 FROM ledger_migration_states WHERE pg_xact_status(xmin::text::xid8)='in progress' AND account_id=NEW.account_id AND status IN ('pending_approval','pending_resolution'))
					   THEN RETURN NEW; END IF;
					   IF TG_OP='UPDATE' AND
					      (to_jsonb(OLD)-ARRAY['current_price','last_mark_price','last_mark_at','pnl','pnl_percent','updated_at','trailing_stop_price','stop_price','take_profit_price','exit_pending','last_atr_value','max_bars_held']) =
					      (to_jsonb(NEW)-ARRAY['current_price','last_mark_price','last_mark_at','pnl','pnl_percent','updated_at','trailing_stop_price','stop_price','take_profit_price','exit_pending','last_atr_value','max_bars_held']) THEN RETURN NEW; END IF;
					   IF TG_OP='UPDATE' AND OLD.amount_exact IS NULL AND COALESCE(NEW.amount_exact,0)=0 AND OLD.status<>'open' THEN RETURN NEW; END IF;
					   SELECT amount_exact INTO projected_value FROM positions WHERE id=NEW.id;
					   SELECT COALESCE(sum(asset_delta),0) INTO event_total FROM ledger_events
					    WHERE account_id=NEW.account_id AND symbol=NEW.symbol AND position_id=NEW.id;
					   IF NOT EXISTS (SELECT 1 FROM ledger_events WHERE pg_xact_status(xmin::text::xid8)='in progress' AND account_id=NEW.account_id AND symbol=NEW.symbol AND position_id=NEW.id)
					      OR COALESCE(projected_value,0) <> event_total
					   THEN RAISE EXCEPTION 'position projection change must equal immutable ledger events written by the same transaction'; END IF;
					 END IF;
					 RETURN NEW;
					END; $$ LANGUAGE plpgsql;

					CREATE OR REPLACE FUNCTION require_current_projection_for_ledger() RETURNS trigger AS $$
					BEGIN
					 IF TG_TABLE_NAME='ledger_batches' THEN
					   IF NOT EXISTS (SELECT 1 FROM ledger_events WHERE pg_xact_status(xmin::text::xid8)='in progress' AND ledger_batch_id=NEW.id)
					   THEN RAISE EXCEPTION 'ledger batch must contain immutable events written by the same transaction'; END IF;
					 ELSIF TG_TABLE_NAME='fills' THEN
					   IF NOT EXISTS (SELECT 1 FROM ledger_events WHERE pg_xact_status(xmin::text::xid8)='in progress' AND fill_id=NEW.id AND ledger_batch_id=NEW.ledger_batch_id)
					   THEN RAISE EXCEPTION 'fill must be coupled to immutable ledger events written by the same transaction'; END IF;
					 ELSE
					   IF NEW.cash_delta=0 AND NEW.asset_delta=0 AND NEW.fill_id IS NULL
					   THEN RAISE EXCEPTION 'ledger event must carry an economic delta or reference a coupled fill'; END IF;
					   IF NEW.cash_delta<>0 AND NOT EXISTS (SELECT 1 FROM wallets WHERE pg_xact_status(xmin::text::xid8)='in progress' AND account_id=NEW.account_id AND currency=NEW.currency)
					   THEN RAISE EXCEPTION 'cash ledger event must be coupled to a wallet projection written by the same transaction'; END IF;
					   IF NEW.asset_delta<>0 AND NOT EXISTS (SELECT 1 FROM positions WHERE pg_xact_status(xmin::text::xid8)='in progress' AND id=NEW.position_id AND account_id=NEW.account_id AND symbol=NEW.symbol)
					   THEN RAISE EXCEPTION 'asset ledger event must be coupled to a position projection written by the same transaction'; END IF;
					 END IF;
					 RETURN NEW;
					END; $$ LANGUAGE plpgsql;

					CREATE OR REPLACE FUNCTION require_current_operation_for_provenance() RETURNS trigger AS $$
					BEGIN
					 IF current_user='trading_bot_migration_admin' THEN
					   IF TG_TABLE_NAME='orders' OR NEW.status NOT IN ('pending_approval','pending_resolution') OR NEW.unresolved_json IS NULL OR NEW.unresolved_json = '[]'::jsonb
					   THEN RAISE EXCEPTION 'migration admin ledger state must remain explicitly unresolved'; END IF;
					   RETURN NEW;
					 END IF;
					 IF current_user<>'trading_bot_ledger_writer' THEN RETURN NEW; END IF;
					 IF TG_TABLE_NAME='orders' THEN
					   IF NOT EXISTS (SELECT 1 FROM fills WHERE pg_xact_status(xmin::text::xid8)='in progress' AND order_id=NEW.id)
					      AND NOT EXISTS (SELECT 1 FROM broker_outcome_ingestions WHERE pg_xact_status(xmin::text::xid8)='in progress' AND account_id=NEW.account_id)
					   THEN RAISE EXCEPTION 'ledger-writer order provenance must be coupled to a fill or broker outcome in the same transaction'; END IF;
					 ELSE
					   IF NOT EXISTS (SELECT 1 FROM ledger_events WHERE pg_xact_status(xmin::text::xid8)='in progress' AND account_id=NEW.account_id)
					   THEN RAISE EXCEPTION 'ledger migration state must be coupled to immutable ledger evidence in the same transaction'; END IF;
					 END IF;
					 RETURN NEW;
					END; $$ LANGUAGE plpgsql;

					CREATE OR REPLACE FUNCTION finalize_applied_backfill_plan(plan_id text, approval text, applied timestamptz) RETURNS void AS $$
					DECLARE changed bigint;
					BEGIN
					 UPDATE backfill_plans plan SET status='applied',applied_at=applied
					 WHERE plan.id=plan_id AND plan.status='approved' AND plan.approval_digest=approval
					   AND EXISTS (
					     SELECT 1 FROM ledger_migration_states state
					     JOIN ledger_events event ON event.id=state.opening_event_id AND event.account_id=state.account_id
					     JOIN ledger_batches batch ON batch.id=event.ledger_batch_id AND batch.account_id=event.account_id
					     WHERE state.account_id=plan.account_id
					       AND pg_xact_status(state.xmin::text::xid8)='in progress'
					       AND pg_xact_status(event.xmin::text::xid8)='in progress'
					       AND pg_xact_status(batch.xmin::text::xid8)='in progress'
					       AND event.event_type='capital_deposit'
					       AND event.actor=plan.approved_by
					       AND event.metadata_json->>'backfill_plan_id'=plan.id
					       AND event.metadata_json->>'report_digest'=plan.report_digest
					       AND event.metadata_json->>'approval_digest'=plan.approval_digest
					   );
					 GET DIAGNOSTICS changed = ROW_COUNT;
					 IF changed<>1 THEN RAISE EXCEPTION 'valid approved backfill plan required'; END IF;
					END; $$ LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public;

					ALTER FUNCTION require_current_ledger_projection() OWNER TO trading_bot_ledger_owner;
					ALTER FUNCTION require_current_projection_for_ledger() OWNER TO trading_bot_ledger_owner;
					ALTER FUNCTION require_current_operation_for_provenance() OWNER TO trading_bot_ledger_owner;
					ALTER FUNCTION finalize_applied_backfill_plan(text,text,timestamptz) OWNER TO trading_bot_operations_owner;
					REVOKE ALL ON FUNCTION finalize_applied_backfill_plan(text,text,timestamptz) FROM PUBLIC;

					REVOKE ALL PRIVILEGES ON wallets,positions,orders,ledger_batches,fills,ledger_events,ledger_migration_states,backfill_plans,broker_outcome_ingestions FROM trading_bot_parity_writer;
					GRANT USAGE ON SCHEMA public TO trading_bot_parity_writer;
					GRANT SELECT ON parity_observations,parity_populations,parity_acceptance_policies,stage08_flag_snapshots,cutover_states TO trading_bot_parity_writer;
					GRANT INSERT,UPDATE ON parity_observations TO trading_bot_parity_writer;
					GRANT SELECT,INSERT,UPDATE ON parity_aggregates TO trading_bot_parity_writer;
				`).Error
			},
			Rollback: func(tx *gorm.DB) error {
				return fmt.Errorf("subtransaction-safe ledger coupling and least-privilege parity grants are intentionally retained")
			},
		},
		{
			ID: "202607190103_fix_nested_transaction_coupling_and_backfill_owner_grant",
			Migrate: func(tx *gorm.DB) error {
				return tx.Exec(`
					CREATE OR REPLACE FUNCTION require_current_ledger_projection() RETURNS trigger AS $$
					DECLARE projected_value numeric;
					DECLARE event_total numeric;
					BEGIN
					 IF TG_TABLE_NAME='wallets' THEN
					   IF TG_OP='UPDATE' AND (to_jsonb(OLD)-ARRAY['updated_at'])=(to_jsonb(NEW)-ARRAY['updated_at']) THEN RETURN NEW; END IF;
					   IF TG_OP='INSERT' AND NEW.balance_exact IS NULL THEN RETURN NEW; END IF;
					   SELECT balance_exact INTO projected_value FROM wallets WHERE id=NEW.id;
					   SELECT COALESCE(sum(cash_delta),0) INTO event_total FROM ledger_events
					    WHERE account_id=NEW.account_id AND currency=NEW.currency;
					   IF NOT EXISTS (SELECT 1 FROM ledger_events WHERE xmin=pg_current_xact_id()::text::xid AND account_id=NEW.account_id AND currency=NEW.currency)
					      OR COALESCE(projected_value,0) <> event_total
					   THEN RAISE EXCEPTION 'wallet projection change must equal immutable ledger events written by the same transaction'; END IF;
					 ELSE
					   IF current_user='trading_bot_migration_admin' AND TG_OP='INSERT' AND NEW.amount_exact IS NULL AND NEW.cost_basis_exact IS NULL
					      AND EXISTS (SELECT 1 FROM ledger_migration_states WHERE xmin=pg_current_xact_id()::text::xid AND account_id=NEW.account_id AND status IN ('pending_approval','pending_resolution'))
					   THEN RETURN NEW; END IF;
					   IF TG_OP='UPDATE' AND
					      (to_jsonb(OLD)-ARRAY['current_price','last_mark_price','last_mark_at','pnl','pnl_percent','updated_at','trailing_stop_price','stop_price','take_profit_price','exit_pending','last_atr_value','max_bars_held']) =
					      (to_jsonb(NEW)-ARRAY['current_price','last_mark_price','last_mark_at','pnl','pnl_percent','updated_at','trailing_stop_price','stop_price','take_profit_price','exit_pending','last_atr_value','max_bars_held']) THEN RETURN NEW; END IF;
					   IF TG_OP='UPDATE' AND OLD.amount_exact IS NULL AND COALESCE(NEW.amount_exact,0)=0 AND OLD.status<>'open' THEN RETURN NEW; END IF;
					   SELECT amount_exact INTO projected_value FROM positions WHERE id=NEW.id;
					   SELECT COALESCE(sum(asset_delta),0) INTO event_total FROM ledger_events
					    WHERE account_id=NEW.account_id AND symbol=NEW.symbol AND position_id=NEW.id;
					   IF NOT EXISTS (SELECT 1 FROM ledger_events WHERE xmin=pg_current_xact_id()::text::xid AND account_id=NEW.account_id AND symbol=NEW.symbol AND position_id=NEW.id)
					      OR COALESCE(projected_value,0) <> event_total
					   THEN RAISE EXCEPTION 'position projection change must equal immutable ledger events written by the same transaction'; END IF;
					 END IF;
					 RETURN NEW;
					END; $$ LANGUAGE plpgsql;

					CREATE OR REPLACE FUNCTION require_current_projection_for_ledger() RETURNS trigger AS $$
					BEGIN
					 IF TG_TABLE_NAME='ledger_batches' THEN
					   IF NOT EXISTS (SELECT 1 FROM ledger_events WHERE xmin=pg_current_xact_id()::text::xid AND ledger_batch_id=NEW.id)
					   THEN RAISE EXCEPTION 'ledger batch must contain immutable events written by the same transaction'; END IF;
					 ELSIF TG_TABLE_NAME='fills' THEN
					   IF NOT EXISTS (SELECT 1 FROM ledger_events WHERE xmin=pg_current_xact_id()::text::xid AND fill_id=NEW.id AND ledger_batch_id=NEW.ledger_batch_id)
					   THEN RAISE EXCEPTION 'fill must be coupled to immutable ledger events written by the same transaction'; END IF;
					 ELSE
					   IF NEW.cash_delta=0 AND NEW.asset_delta=0 AND NEW.fill_id IS NULL
					   THEN RAISE EXCEPTION 'ledger event must carry an economic delta or reference a coupled fill'; END IF;
					   IF NEW.cash_delta<>0 AND NOT EXISTS (SELECT 1 FROM wallets WHERE xmin=pg_current_xact_id()::text::xid AND account_id=NEW.account_id AND currency=NEW.currency)
					   THEN RAISE EXCEPTION 'cash ledger event must be coupled to a wallet projection written by the same transaction'; END IF;
					   IF NEW.asset_delta<>0 AND NOT EXISTS (SELECT 1 FROM positions WHERE xmin=pg_current_xact_id()::text::xid AND id=NEW.position_id AND account_id=NEW.account_id AND symbol=NEW.symbol)
					   THEN RAISE EXCEPTION 'asset ledger event must be coupled to a position projection written by the same transaction'; END IF;
					 END IF;
					 RETURN NEW;
					END; $$ LANGUAGE plpgsql;

					CREATE OR REPLACE FUNCTION require_current_operation_for_provenance() RETURNS trigger AS $$
					BEGIN
					 IF current_user='trading_bot_migration_admin' THEN
					   IF TG_TABLE_NAME='orders' OR NEW.status NOT IN ('pending_approval','pending_resolution') OR NEW.unresolved_json IS NULL OR NEW.unresolved_json = '[]'::jsonb
					   THEN RAISE EXCEPTION 'migration admin ledger state must remain explicitly unresolved'; END IF;
					   RETURN NEW;
					 END IF;
					 IF current_user<>'trading_bot_ledger_writer' THEN RETURN NEW; END IF;
					 IF TG_TABLE_NAME='orders' THEN
					   IF NOT EXISTS (SELECT 1 FROM fills WHERE xmin=pg_current_xact_id()::text::xid AND order_id=NEW.id)
					      AND NOT EXISTS (SELECT 1 FROM broker_outcome_ingestions WHERE xmin=pg_current_xact_id()::text::xid AND account_id=NEW.account_id)
					   THEN RAISE EXCEPTION 'ledger-writer order provenance must be coupled to a fill or broker outcome in the same transaction'; END IF;
					 ELSE
					   IF NOT EXISTS (SELECT 1 FROM ledger_events WHERE xmin=pg_current_xact_id()::text::xid AND account_id=NEW.account_id)
					   THEN RAISE EXCEPTION 'ledger migration state must be coupled to immutable ledger evidence in the same transaction'; END IF;
					 END IF;
					 RETURN NEW;
					END; $$ LANGUAGE plpgsql;

					CREATE OR REPLACE FUNCTION finalize_applied_backfill_plan(plan_id text, approval text, applied timestamptz) RETURNS void AS $$
					DECLARE changed bigint;
					BEGIN
					 UPDATE backfill_plans plan SET status='applied',applied_at=applied
					 WHERE plan.id=plan_id AND plan.status='approved' AND plan.approval_digest=approval
					   AND EXISTS (
					     SELECT 1 FROM ledger_migration_states state
					     JOIN ledger_events event ON event.id=state.opening_event_id AND event.account_id=state.account_id
					     JOIN ledger_batches batch ON batch.id=event.ledger_batch_id AND batch.account_id=event.account_id
					     WHERE state.account_id=plan.account_id
					       AND state.xmin=pg_current_xact_id()::text::xid
					       AND event.xmin=pg_current_xact_id()::text::xid
					       AND batch.xmin=pg_current_xact_id()::text::xid
					       AND event.event_type='capital_deposit'
					       AND event.actor=plan.approved_by
					       AND event.metadata_json->>'backfill_plan_id'=plan.id
					       AND event.metadata_json->>'report_digest'=plan.report_digest
					       AND event.metadata_json->>'approval_digest'=plan.approval_digest
					   );
					 GET DIAGNOSTICS changed = ROW_COUNT;
					 IF changed<>1 THEN RAISE EXCEPTION 'valid approved backfill plan required'; END IF;
					END; $$ LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public;

					ALTER FUNCTION require_current_ledger_projection() OWNER TO trading_bot_ledger_owner;
					ALTER FUNCTION require_current_projection_for_ledger() OWNER TO trading_bot_ledger_owner;
					ALTER FUNCTION require_current_operation_for_provenance() OWNER TO trading_bot_ledger_owner;
					ALTER FUNCTION finalize_applied_backfill_plan(text,text,timestamptz) OWNER TO trading_bot_operations_owner;
					REVOKE ALL ON FUNCTION finalize_applied_backfill_plan(text,text,timestamptz) FROM PUBLIC;
					GRANT SELECT ON ledger_batches TO trading_bot_operations_owner;
					GRANT EXECUTE ON FUNCTION finalize_applied_backfill_plan(text,text,timestamptz) TO trading_bot_ledger_writer;
				`).Error
			},
			Rollback: func(tx *gorm.DB) error {
				return fmt.Errorf("nested transaction ledger coupling and backfill owner grants are intentionally retained")
			},
		},
		{
			ID: "202607190104_final_review_parity_and_position_coupling",
			Migrate: func(tx *gorm.DB) error {
				return tx.Exec(`
					CREATE OR REPLACE FUNCTION require_current_ledger_projection() RETURNS trigger AS $$
					DECLARE projected_amount numeric;
					DECLARE projected_basis numeric;
					DECLARE projected_fees numeric;
					DECLARE projected_realized numeric;
					DECLARE event_amount numeric;
					DECLARE event_basis numeric;
					DECLARE event_fees numeric;
					DECLARE event_realized numeric;
					BEGIN
					 IF TG_TABLE_NAME='wallets' THEN
					   IF TG_OP='UPDATE' AND (to_jsonb(OLD)-ARRAY['updated_at'])=(to_jsonb(NEW)-ARRAY['updated_at']) THEN RETURN NEW; END IF;
					   IF TG_OP='INSERT' AND NEW.balance_exact IS NULL THEN RETURN NEW; END IF;
					   SELECT balance_exact INTO projected_amount FROM wallets WHERE id=NEW.id;
					   SELECT COALESCE(sum(cash_delta),0) INTO event_amount FROM ledger_events
					    WHERE account_id=NEW.account_id AND currency=NEW.currency;
					   IF NOT EXISTS (SELECT 1 FROM ledger_events WHERE xmin=pg_current_xact_id()::text::xid AND account_id=NEW.account_id AND currency=NEW.currency)
					      OR COALESCE(projected_amount,0) <> event_amount
					   THEN RAISE EXCEPTION 'wallet projection change must equal immutable ledger events written by the same transaction'; END IF;
					 ELSE
					   IF current_user='trading_bot_migration_admin' AND TG_OP='INSERT' AND NEW.amount_exact IS NULL AND NEW.cost_basis_exact IS NULL
					      AND EXISTS (SELECT 1 FROM ledger_migration_states WHERE xmin=pg_current_xact_id()::text::xid AND account_id=NEW.account_id AND status IN ('pending_approval','pending_resolution'))
					   THEN RETURN NEW; END IF;
					   IF TG_OP='UPDATE' AND
					      (to_jsonb(OLD)-ARRAY['current_price','last_mark_price','last_mark_at','pnl','pnl_percent','updated_at','trailing_stop_price','stop_price','take_profit_price','exit_pending','last_atr_value','max_bars_held']) =
					      (to_jsonb(NEW)-ARRAY['current_price','last_mark_price','last_mark_at','pnl','pnl_percent','updated_at','trailing_stop_price','stop_price','take_profit_price','exit_pending','last_atr_value','max_bars_held']) THEN RETURN NEW; END IF;
					   IF TG_OP='UPDATE' AND OLD.amount_exact IS NULL AND COALESCE(NEW.amount_exact,0)=0 AND OLD.status<>'open' THEN RETURN NEW; END IF;
					   SELECT amount_exact,cost_basis_exact,fees_exact,realized_pn_l_exact
					     INTO projected_amount,projected_basis,projected_fees,projected_realized
					     FROM positions WHERE id=NEW.id;
					   SELECT COALESCE(sum(asset_delta),0),COALESCE(sum(cost_basis_delta),0),COALESCE(sum(fee_delta),0),COALESCE(sum(realized_pn_l),0)
					     INTO event_amount,event_basis,event_fees,event_realized
					     FROM ledger_events WHERE account_id=NEW.account_id AND symbol=NEW.symbol AND position_id=NEW.id;
					   IF NOT EXISTS (SELECT 1 FROM ledger_events WHERE xmin=pg_current_xact_id()::text::xid AND account_id=NEW.account_id AND symbol=NEW.symbol AND position_id=NEW.id)
					      OR COALESCE(projected_amount,0) <> event_amount
					      OR COALESCE(projected_basis,0) <> event_basis
					      OR COALESCE(projected_fees,0) <> event_fees
					      OR COALESCE(projected_realized,0) <> event_realized
					   THEN RAISE EXCEPTION 'position economic projection must equal immutable ledger events written by the same transaction'; END IF;
					 END IF;
					 RETURN NEW;
					END; $$ LANGUAGE plpgsql;
					ALTER FUNCTION require_current_ledger_projection() OWNER TO trading_bot_ledger_owner;

					REVOKE UPDATE ON positions FROM trading_bot_ledger_writer;
					GRANT UPDATE (
					 symbol,account_id,amount,amount_exact,cost_basis_exact,realized_pn_l_exact,fees_exact,avg_price,entry_price,current_price,
					 execution_mode,entry_source,exit_pending,last_mark_price,last_mark_at,client_position_id,decision_timeframe,model_version,
					 strategy_version,policy_version,universe_mode,rollout_state,experiment_id,prediction_log_id,decision_context_json,stop_price,
					 take_profit_price,trailing_stop_price,last_atr_value,max_bars_held,pnl,pnl_percent,status,opened_at,closed_at,close_reason
					) ON positions TO trading_bot_ledger_writer;

					REVOKE UPDATE ON parity_observations FROM trading_bot_parity_writer;
					REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM trading_bot_parity_writer;
					GRANT INSERT ON parity_populations TO trading_bot_parity_writer;
				`).Error
			},
			Rollback: func(tx *gorm.DB) error {
				return fmt.Errorf("final reviewer parity authority and complete economic projection coupling are intentionally retained")
			},
		},
		{
			ID: "202607190105_runtime_backup_verification_evidence_grant",
			Migrate: func(tx *gorm.DB) error {
				return tx.Exec(`
					REVOKE ALL PRIVILEGES ON backup_verifications FROM trading_bot_runtime;
					GRANT INSERT ON backup_verifications TO trading_bot_runtime;
					GRANT SELECT ON cutover_states TO trading_bot_runtime;
				`).Error
			},
			Rollback: func(tx *gorm.DB) error {
				return fmt.Errorf("runtime backup verification evidence grant is intentionally retained")
			},
		},
		{
			ID: "202607190106_runtime_backup_persisted_authority_read_grant",
			Migrate: func(tx *gorm.DB) error {
				// The constrained evidence writer verifies the restored authority
				// envelope before inserting evidence. These are immutable metadata
				// reads only; it retains no state or transition write privileges.
				return tx.Exec(`
					GRANT SELECT ON stage08_flag_snapshots, cutover_transitions TO trading_bot_runtime;
				`).Error
			},
			Rollback: func(tx *gorm.DB) error {
				return fmt.Errorf("runtime persisted authority read grant is intentionally retained")
			},
		},
		{
			ID: "202607190107_secure_backup_verification_evidence_boundary",
			Migrate: func(tx *gorm.DB) error {
				if !tx.Migrator().HasTable("backup_verifications") || !tx.Migrator().HasTable("stage08_flag_snapshots") || !tx.Migrator().HasTable("cutover_transitions") || !tx.Migrator().HasTable("cutover_states") {
					return fmt.Errorf("unsupported database shape: backup evidence boundary requires stage08 authority tables")
				}
				return tx.Exec(`
					CREATE EXTENSION IF NOT EXISTS pgcrypto;
					DO $shape$ DECLARE bad_flags bigint; bad_transitions bigint; BEGIN
					 SELECT count(*) INTO bad_flags FROM backup_verifications b LEFT JOIN stage08_flag_snapshots s ON s.id=b.flag_snapshot_id WHERE s.id IS NULL;
					 IF bad_flags > 0 THEN RAISE EXCEPTION 'cannot install backup_verifications_flag_snapshot_fk: % preexisting rows reference missing stage08_flag_snapshots', bad_flags; END IF;
					 SELECT count(*) INTO bad_transitions FROM backup_verifications b LEFT JOIN cutover_transitions c ON c.id=b.cutover_transition_id WHERE c.id IS NULL;
					 IF bad_transitions > 0 THEN RAISE EXCEPTION 'cannot install backup_verifications_cutover_transition_fk: % preexisting rows reference missing cutover_transitions', bad_transitions; END IF;
					 IF EXISTS (SELECT 1 FROM backup_verifications WHERE status <> 'verified' OR source_fingerprint !~ '^[a-f0-9]{64}$' OR target_fingerprint <> source_fingerprint OR canonical_digest <> source_fingerprint OR dump_checksum !~ '^[a-f0-9]{64}$' OR manifest_checksum !~ '^[a-f0-9]{64}$') THEN
					   RAISE EXCEPTION 'cannot install backup evidence boundary: preexisting rows do not satisfy verified canonical digest shape';
					 END IF;
				END $shape$;
				DO $fk$ BEGIN
					IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='backup_verifications_flag_snapshot_fk') THEN ALTER TABLE backup_verifications ADD CONSTRAINT backup_verifications_flag_snapshot_fk FOREIGN KEY(flag_snapshot_id) REFERENCES stage08_flag_snapshots(id) ON UPDATE RESTRICT ON DELETE RESTRICT; END IF;
					IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='backup_verifications_cutover_transition_fk') THEN ALTER TABLE backup_verifications ADD CONSTRAINT backup_verifications_cutover_transition_fk FOREIGN KEY(cutover_transition_id) REFERENCES cutover_transitions(id) ON UPDATE RESTRICT ON DELETE RESTRICT; END IF;
				END $fk$;
				CREATE OR REPLACE FUNCTION public.record_verified_backup_evidence(
					p_source_before text, p_source_after text, p_target_fingerprint text, p_dump_checksum text,
					p_manifest_checksum text, p_target_identity_token text, p_tool_versions jsonb, p_verified_at timestamptz,
					p_principal text, p_flag_snapshot_id text, p_cutover_transition_id text
				) RETURNS TABLE(id text, source_fingerprint text, dump_checksum text, fixture_metadata_json jsonb,
					target_fingerprint text, canonical_digest text, status text, verified_at timestamptz, manifest_checksum text,
					tool_versions_json jsonb, flag_snapshot_id text, cutover_transition_id text)
				LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $fn$
				DECLARE v_state public.cutover_states%ROWTYPE; v_snapshot public.stage08_flag_snapshots%ROWTYPE; v_transition public.cutover_transitions%ROWTYPE; v_id text; v_fixture jsonb;
				BEGIN
					IF p_source_before !~ '^[a-f0-9]{64}$' OR p_source_after !~ '^[a-f0-9]{64}$' OR p_target_fingerprint !~ '^[a-f0-9]{64}$' OR p_dump_checksum !~ '^[a-f0-9]{64}$' OR p_manifest_checksum !~ '^[a-f0-9]{64}$' THEN RAISE EXCEPTION 'backup evidence digests must be lowercase 64-character hexadecimal'; END IF;
					IF p_target_identity_token !~ '^[a-f0-9]{32,128}$' THEN RAISE EXCEPTION 'backup evidence target identity token violates manifest contract'; END IF;
					IF p_principal IS NULL OR length(p_principal)=0 OR length(p_principal)>200 OR p_verified_at IS NULL OR date_trunc('second',p_verified_at)<>p_verified_at THEN RAISE EXCEPTION 'backup evidence principal or timestamp is malformed'; END IF;
					IF jsonb_typeof(p_tool_versions)<>'object' OR (SELECT count(*) FROM jsonb_object_keys(p_tool_versions))>50 OR EXISTS (SELECT 1 FROM jsonb_each_text(p_tool_versions) v(k,val) WHERE k !~ '^[A-Za-z0-9._/-]{1,80}$' OR length(val)=0 OR length(val)>200) THEN RAISE EXCEPTION 'backup evidence tool metadata is malformed'; END IF;
					IF p_source_before<>p_source_after OR p_source_before<>p_target_fingerprint THEN RAISE EXCEPTION 'backup evidence fingerprints must be identical'; END IF;
					IF p_manifest_checksum<>encode(digest(convert_to(p_source_before||'|'||p_dump_checksum||'|'||p_target_identity_token||'|'||to_char(p_verified_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),'UTF8'),'sha256'),'hex') THEN RAISE EXCEPTION 'backup verification manifest checksum mismatch'; END IF;
					SELECT * INTO v_state FROM public.cutover_states AS cs WHERE cs.id=1; IF NOT FOUND THEN RAISE EXCEPTION 'persisted cutover state required'; END IF;
					IF p_flag_snapshot_id<>v_state.flag_snapshot_id OR p_cutover_transition_id<>v_state.transition_id THEN RAISE EXCEPTION 'backup evidence authority bindings do not equal current cutover state'; END IF;
					SELECT * INTO v_snapshot FROM public.stage08_flag_snapshots AS ss WHERE ss.id=p_flag_snapshot_id; IF NOT FOUND OR v_snapshot.id<>v_snapshot.content_digest OR v_snapshot.content_digest<>v_state.flag_digest OR v_snapshot.content_json IS NULL OR v_snapshot.schema_version<>'stage08-flags-v1' THEN RAISE EXCEPTION 'current cutover flag snapshot integrity is invalid'; END IF;
					IF v_state.authority_json IS NULL OR v_state.authority_json='{}'::jsonb OR v_state.authority_digest !~ '^[a-f0-9]{64}$' OR v_state.authority_json->>'FlagID'<>v_snapshot.id OR v_state.authority_json->>'FlagDigest'<>v_snapshot.content_digest OR v_state.authority_json->>'Stage'<>v_state.stage OR v_state.authority_json->>'Authority'<>v_state.authority THEN RAISE EXCEPTION 'current cutover authority envelope is invalid'; END IF;
					IF p_cutover_transition_id=repeat('0',64) THEN
						IF v_state.stage<>'schema_legacy' OR v_state.authority<>'legacy' OR v_snapshot.id<>encode(digest(convert_to('{"Schema":"stage08-flags-v1","LedgerAuthority":"legacy","SharedEngine":"off","NewBacktest":"off","PointInTimeUniverse":"off","CandidateStrategy":"off","DualRun":"off","Stage07Context":""}','UTF8'),'sha256'),'hex') OR v_state.version<1 THEN RAISE EXCEPTION 'persisted legacy cutover authority is ambiguous or tampered'; END IF;
					ELSE
						SELECT * INTO v_transition FROM public.cutover_transitions AS ct WHERE ct.id=p_cutover_transition_id; IF NOT FOUND OR v_transition.content_digest<>v_transition.id OR v_transition.flag_snapshot_id<>v_snapshot.id OR v_transition.flag_snapshot_digest<>v_snapshot.content_digest OR v_transition.to_stage<>v_state.stage OR v_transition.to_authority<>v_state.authority OR v_transition.target_envelope_digest<>v_state.authority_digest OR v_transition.target_envelope_json IS DISTINCT FROM v_state.authority_json THEN RAISE EXCEPTION 'current cutover transition is not bound to current authority envelope'; END IF;
					END IF;
					v_fixture := jsonb_build_object('recorded_by',p_principal,'target_identity_token',p_target_identity_token);
					v_id := encode(digest(convert_to(p_source_before||'|'||p_dump_checksum||'|'||p_target_identity_token||'|'||to_char(p_verified_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')||'|'||p_principal||'|'||v_snapshot.id||'|'||v_transition.id,'UTF8'),'sha256'),'hex');
					RETURN QUERY INSERT INTO public.backup_verifications AS inserted(id,source_fingerprint,dump_checksum,fixture_metadata_json,target_fingerprint,canonical_digest,status,verified_at,manifest_checksum,tool_versions_json,flag_snapshot_id,cutover_transition_id)
					VALUES(v_id,p_source_before,p_dump_checksum,v_fixture,p_target_fingerprint,p_source_before,'verified',p_verified_at,p_manifest_checksum,p_tool_versions,v_snapshot.id,v_transition.id)
					ON CONFLICT DO NOTHING RETURNING inserted.id,inserted.source_fingerprint,inserted.dump_checksum,inserted.fixture_metadata_json,inserted.target_fingerprint,inserted.canonical_digest,inserted.status,inserted.verified_at,inserted.manifest_checksum,inserted.tool_versions_json,inserted.flag_snapshot_id,inserted.cutover_transition_id;
					IF NOT FOUND THEN RETURN QUERY SELECT b.id,b.source_fingerprint,b.dump_checksum,b.fixture_metadata_json,b.target_fingerprint,b.canonical_digest,b.status,b.verified_at,b.manifest_checksum,b.tool_versions_json,b.flag_snapshot_id,b.cutover_transition_id FROM public.backup_verifications b WHERE b.id=v_id; END IF;
				END $fn$;
				ALTER FUNCTION public.record_verified_backup_evidence(text,text,text,text,text,text,jsonb,timestamptz,text,text,text) OWNER TO trading_bot_operations_owner;
				REVOKE ALL ON FUNCTION public.record_verified_backup_evidence(text,text,text,text,text,text,jsonb,timestamptz,text,text,text) FROM PUBLIC;
				GRANT EXECUTE ON FUNCTION public.record_verified_backup_evidence(text,text,text,text,text,text,jsonb,timestamptz,text,text,text) TO trading_bot_runtime;
				GRANT USAGE ON SCHEMA public TO trading_bot_operations_owner;
				GRANT SELECT ON cutover_states,stage08_flag_snapshots,cutover_transitions TO trading_bot_operations_owner;
				GRANT INSERT,SELECT ON backup_verifications TO trading_bot_operations_owner;
				REVOKE ALL PRIVILEGES ON backup_verifications FROM PUBLIC, trading_bot_runtime, trading_bot_ledger_writer, trading_bot_parity_writer;
			`).Error
			},
			Rollback: func(tx *gorm.DB) error {
				return fmt.Errorf("secure backup verification evidence boundary is intentionally retained")
			},
		},
		{
			// Corrects the function body on databases which recorded the original
			// boundary migration before its output-column qualification fix.
			ID: "202607190108_qualify_backup_evidence_function_columns",
			Migrate: func(tx *gorm.DB) error {
				return tx.Exec(`
				CREATE OR REPLACE FUNCTION public.record_verified_backup_evidence(
					p_source_before text, p_source_after text, p_target_fingerprint text, p_dump_checksum text,
					p_manifest_checksum text, p_target_identity_token text, p_tool_versions jsonb, p_verified_at timestamptz,
					p_principal text, p_flag_snapshot_id text, p_cutover_transition_id text
				) RETURNS TABLE(id text, source_fingerprint text, dump_checksum text, fixture_metadata_json jsonb,
					target_fingerprint text, canonical_digest text, status text, verified_at timestamptz, manifest_checksum text,
					tool_versions_json jsonb, flag_snapshot_id text, cutover_transition_id text)
				LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $fn$
				DECLARE v_state public.cutover_states%ROWTYPE; v_snapshot public.stage08_flag_snapshots%ROWTYPE; v_transition public.cutover_transitions%ROWTYPE; v_id text; v_fixture jsonb;
				BEGIN
					IF p_source_before !~ '^[a-f0-9]{64}$' OR p_source_after !~ '^[a-f0-9]{64}$' OR p_target_fingerprint !~ '^[a-f0-9]{64}$' OR p_dump_checksum !~ '^[a-f0-9]{64}$' OR p_manifest_checksum !~ '^[a-f0-9]{64}$' THEN RAISE EXCEPTION 'backup evidence digests must be lowercase 64-character hexadecimal'; END IF;
					IF p_target_identity_token !~ '^[a-f0-9]{32,128}$' THEN RAISE EXCEPTION 'backup evidence target identity token violates manifest contract'; END IF;
					IF p_principal IS NULL OR length(p_principal)=0 OR length(p_principal)>200 OR p_verified_at IS NULL OR date_trunc('second',p_verified_at)<>p_verified_at THEN RAISE EXCEPTION 'backup evidence principal or timestamp is malformed'; END IF;
					IF jsonb_typeof(p_tool_versions)<>'object' OR (SELECT count(*) FROM jsonb_object_keys(p_tool_versions))>50 OR EXISTS (SELECT 1 FROM jsonb_each_text(p_tool_versions) v(k,val) WHERE k !~ '^[A-Za-z0-9._/-]{1,80}$' OR length(val)=0 OR length(val)>200) THEN RAISE EXCEPTION 'backup evidence tool metadata is malformed'; END IF;
					IF p_source_before<>p_source_after OR p_source_before<>p_target_fingerprint THEN RAISE EXCEPTION 'backup evidence fingerprints must be identical'; END IF;
					IF p_manifest_checksum<>encode(digest(convert_to(p_source_before||'|'||p_dump_checksum||'|'||p_target_identity_token||'|'||to_char(p_verified_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),'UTF8'),'sha256'),'hex') THEN RAISE EXCEPTION 'backup verification manifest checksum mismatch'; END IF;
					SELECT * INTO v_state FROM public.cutover_states AS cs WHERE cs.id=1; IF NOT FOUND THEN RAISE EXCEPTION 'persisted cutover state required'; END IF;
					IF p_flag_snapshot_id<>v_state.flag_snapshot_id OR p_cutover_transition_id<>v_state.transition_id THEN RAISE EXCEPTION 'backup evidence authority bindings do not equal current cutover state'; END IF;
					SELECT * INTO v_snapshot FROM public.stage08_flag_snapshots AS ss WHERE ss.id=p_flag_snapshot_id; IF NOT FOUND OR v_snapshot.id<>v_snapshot.content_digest OR v_snapshot.content_digest<>v_state.flag_digest OR v_snapshot.content_json IS NULL OR v_snapshot.schema_version<>'stage08-flags-v1' THEN RAISE EXCEPTION 'current cutover flag snapshot integrity is invalid'; END IF;
					IF v_state.authority_json IS NULL OR v_state.authority_json='{}'::jsonb OR v_state.authority_digest !~ '^[a-f0-9]{64}$' OR v_state.authority_json->>'FlagID'<>v_snapshot.id OR v_state.authority_json->>'FlagDigest'<>v_snapshot.content_digest OR v_state.authority_json->>'Stage'<>v_state.stage OR v_state.authority_json->>'Authority'<>v_state.authority THEN RAISE EXCEPTION 'current cutover authority envelope is invalid'; END IF;
					SELECT * INTO v_transition FROM public.cutover_transitions AS ct WHERE ct.id=p_cutover_transition_id; IF NOT FOUND OR v_transition.content_digest<>v_transition.id OR v_transition.flag_snapshot_id<>v_snapshot.id OR v_transition.flag_snapshot_digest<>v_snapshot.content_digest OR v_transition.to_stage<>v_state.stage OR v_transition.to_authority<>v_state.authority OR v_transition.target_envelope_digest<>v_state.authority_digest OR v_transition.target_envelope_json IS DISTINCT FROM v_state.authority_json THEN RAISE EXCEPTION 'current cutover transition is not bound to current authority envelope'; END IF;
					v_fixture := jsonb_build_object('recorded_by',p_principal,'target_identity_token',p_target_identity_token);
					v_id := encode(digest(convert_to(p_source_before||'|'||p_dump_checksum||'|'||p_target_identity_token||'|'||to_char(p_verified_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')||'|'||p_principal||'|'||v_snapshot.id||'|'||v_transition.id,'UTF8'),'sha256'),'hex');
					RETURN QUERY INSERT INTO public.backup_verifications AS inserted(id,source_fingerprint,dump_checksum,fixture_metadata_json,target_fingerprint,canonical_digest,status,verified_at,manifest_checksum,tool_versions_json,flag_snapshot_id,cutover_transition_id)
					VALUES(v_id,p_source_before,p_dump_checksum,v_fixture,p_target_fingerprint,p_source_before,'verified',p_verified_at,p_manifest_checksum,p_tool_versions,v_snapshot.id,v_transition.id)
					ON CONFLICT DO NOTHING RETURNING inserted.id::text,inserted.source_fingerprint::text,inserted.dump_checksum::text,inserted.fixture_metadata_json,inserted.target_fingerprint::text,inserted.canonical_digest::text,inserted.status::text,inserted.verified_at,inserted.manifest_checksum::text,inserted.tool_versions_json,inserted.flag_snapshot_id::text,inserted.cutover_transition_id::text;
					IF NOT FOUND THEN RETURN QUERY SELECT b.id::text,b.source_fingerprint::text,b.dump_checksum::text,b.fixture_metadata_json,b.target_fingerprint::text,b.canonical_digest::text,b.status::text,b.verified_at,b.manifest_checksum::text,b.tool_versions_json,b.flag_snapshot_id::text,b.cutover_transition_id::text FROM public.backup_verifications AS b WHERE b.id=v_id; END IF;
				END $fn$;
				ALTER FUNCTION public.record_verified_backup_evidence(text,text,text,text,text,text,jsonb,timestamptz,text,text,text) OWNER TO trading_bot_operations_owner;
				`).Error
			},
			Rollback: func(tx *gorm.DB) error {
				return fmt.Errorf("qualified backup evidence function is intentionally retained")
			},
		},
		{
			ID: "202607190109_backup_evidence_result_types_and_legacy_transition",
			Migrate: func(tx *gorm.DB) error {
				return tx.Exec(`
				CREATE OR REPLACE FUNCTION public.record_verified_backup_evidence(p_source_before text,p_source_after text,p_target_fingerprint text,p_dump_checksum text,p_manifest_checksum text,p_target_identity_token text,p_tool_versions jsonb,p_verified_at timestamptz,p_principal text,p_flag_snapshot_id text,p_cutover_transition_id text)
				RETURNS TABLE(id text,source_fingerprint text,dump_checksum text,fixture_metadata_json jsonb,target_fingerprint text,canonical_digest text,status text,verified_at timestamptz,manifest_checksum text,tool_versions_json jsonb,flag_snapshot_id text,cutover_transition_id text)
				LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $fn$
				DECLARE v_state public.cutover_states%ROWTYPE; v_snapshot public.stage08_flag_snapshots%ROWTYPE; v_transition public.cutover_transitions%ROWTYPE; v_id text; v_fixture jsonb;
				BEGIN
					IF p_source_before !~ '^[a-f0-9]{64}$' OR p_source_after !~ '^[a-f0-9]{64}$' OR p_target_fingerprint !~ '^[a-f0-9]{64}$' OR p_dump_checksum !~ '^[a-f0-9]{64}$' OR p_manifest_checksum !~ '^[a-f0-9]{64}$' THEN RAISE EXCEPTION 'backup evidence digests must be lowercase 64-character hexadecimal'; END IF;
					IF p_target_identity_token !~ '^[a-f0-9]{32,128}$' THEN RAISE EXCEPTION 'backup evidence target identity token violates manifest contract'; END IF;
					IF p_principal IS NULL OR length(p_principal)=0 OR length(p_principal)>200 OR p_verified_at IS NULL OR date_trunc('second',p_verified_at)<>p_verified_at THEN RAISE EXCEPTION 'backup evidence principal or timestamp is malformed'; END IF;
					IF jsonb_typeof(p_tool_versions)<>'object' OR (SELECT count(*) FROM jsonb_object_keys(p_tool_versions))>50 OR EXISTS (SELECT 1 FROM jsonb_each_text(p_tool_versions) v(k,val) WHERE k !~ '^[A-Za-z0-9._/-]{1,80}$' OR length(val)=0 OR length(val)>200) THEN RAISE EXCEPTION 'backup evidence tool metadata is malformed'; END IF;
					IF p_source_before<>p_source_after OR p_source_before<>p_target_fingerprint THEN RAISE EXCEPTION 'backup evidence fingerprints must be identical'; END IF;
					IF p_manifest_checksum<>encode(digest(convert_to(p_source_before||'|'||p_dump_checksum||'|'||p_target_identity_token||'|'||to_char(p_verified_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),'UTF8'),'sha256'),'hex') THEN RAISE EXCEPTION 'backup verification manifest checksum mismatch'; END IF;
					SELECT * INTO v_state FROM public.cutover_states AS cs WHERE cs.id=1; IF NOT FOUND THEN RAISE EXCEPTION 'persisted cutover state required'; END IF;
					IF p_flag_snapshot_id<>v_state.flag_snapshot_id OR p_cutover_transition_id<>v_state.transition_id THEN RAISE EXCEPTION 'backup evidence authority bindings do not equal current cutover state'; END IF;
					SELECT * INTO v_snapshot FROM public.stage08_flag_snapshots AS ss WHERE ss.id=p_flag_snapshot_id; IF NOT FOUND OR v_snapshot.id<>v_snapshot.content_digest OR v_snapshot.content_digest<>v_state.flag_digest OR v_snapshot.content_json IS NULL OR v_snapshot.schema_version<>'stage08-flags-v1' THEN RAISE EXCEPTION 'current cutover flag snapshot integrity is invalid'; END IF;
					IF v_state.authority_json IS NULL OR v_state.authority_json='{}'::jsonb OR v_state.authority_digest !~ '^[a-f0-9]{64}$' OR v_state.authority_json->>'FlagID'<>v_snapshot.id OR v_state.authority_json->>'FlagDigest'<>v_snapshot.content_digest OR v_state.authority_json->>'Stage'<>v_state.stage OR v_state.authority_json->>'Authority'<>v_state.authority THEN RAISE EXCEPTION 'current cutover authority envelope is invalid'; END IF;
					IF p_cutover_transition_id=repeat('0',64) THEN
						IF v_state.stage<>'schema_legacy' OR v_state.authority<>'legacy' OR v_snapshot.id<>encode(digest(convert_to('{"schema_version":"stage08-flags-v1","ledger_authority":"legacy","shared_engine":"off","new_backtest":"off","point_in_time_universe":"off","candidate_strategy":"off","dual_run":"off"}','UTF8'),'sha256'),'hex') OR v_state.version<1 THEN RAISE EXCEPTION 'persisted legacy cutover authority is ambiguous or tampered'; END IF;
					ELSE
						SELECT * INTO v_transition FROM public.cutover_transitions AS ct WHERE ct.id=p_cutover_transition_id; IF NOT FOUND OR v_transition.content_digest<>v_transition.id OR v_transition.flag_snapshot_id<>v_snapshot.id OR v_transition.flag_snapshot_digest<>v_snapshot.content_digest OR v_transition.to_stage<>v_state.stage OR v_transition.to_authority<>v_state.authority OR v_transition.target_envelope_digest<>v_state.authority_digest OR v_transition.target_envelope_json IS DISTINCT FROM v_state.authority_json THEN RAISE EXCEPTION 'current cutover transition is not bound to current authority envelope'; END IF;
					END IF;
					v_fixture:=jsonb_build_object('recorded_by',p_principal,'target_identity_token',p_target_identity_token);
					v_id:=encode(digest(convert_to(p_source_before||'|'||p_dump_checksum||'|'||p_target_identity_token||'|'||to_char(p_verified_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')||'|'||p_principal||'|'||v_snapshot.id||'|'||p_cutover_transition_id,'UTF8'),'sha256'),'hex');
					RETURN QUERY INSERT INTO public.backup_verifications AS inserted(id,source_fingerprint,dump_checksum,fixture_metadata_json,target_fingerprint,canonical_digest,status,verified_at,manifest_checksum,tool_versions_json,flag_snapshot_id,cutover_transition_id) VALUES(v_id,p_source_before,p_dump_checksum,v_fixture,p_target_fingerprint,p_source_before,'verified',p_verified_at,p_manifest_checksum,p_tool_versions,v_snapshot.id,p_cutover_transition_id) ON CONFLICT DO NOTHING RETURNING inserted.id::text,inserted.source_fingerprint::text,inserted.dump_checksum::text,inserted.fixture_metadata_json,inserted.target_fingerprint::text,inserted.canonical_digest::text,inserted.status::text,inserted.verified_at,inserted.manifest_checksum::text,inserted.tool_versions_json,inserted.flag_snapshot_id::text,inserted.cutover_transition_id::text;
					IF NOT FOUND THEN RETURN QUERY SELECT b.id::text,b.source_fingerprint::text,b.dump_checksum::text,b.fixture_metadata_json,b.target_fingerprint::text,b.canonical_digest::text,b.status::text,b.verified_at,b.manifest_checksum::text,b.tool_versions_json,b.flag_snapshot_id::text,b.cutover_transition_id::text FROM public.backup_verifications AS b WHERE b.id=v_id; END IF;
				END $fn$;
				ALTER FUNCTION public.record_verified_backup_evidence(text,text,text,text,text,text,jsonb,timestamptz,text,text,text) OWNER TO trading_bot_operations_owner;
				REVOKE ALL ON FUNCTION public.record_verified_backup_evidence(text,text,text,text,text,text,jsonb,timestamptz,text,text,text) FROM PUBLIC;
				GRANT EXECUTE ON FUNCTION public.record_verified_backup_evidence(text,text,text,text,text,text,jsonb,timestamptz,text,text,text) TO trading_bot_runtime;
				`).Error
			},
			Rollback: func(tx *gorm.DB) error { return fmt.Errorf("typed backup evidence function is intentionally retained") },
		},
		{
			ID: "202607190110_bootstrap_transition_sentinel",
			Migrate: func(tx *gorm.DB) error {
				// Current-development corrective path: a pre-sentinel deterministic
				// bootstrap is upgraded only when every bound authority field is exact.
				return tx.Exec(`
				DO $sentinel$ DECLARE s record; f record; e jsonb; d text; BEGIN
				 SELECT * INTO s FROM public.cutover_states WHERE id=1;
				 IF FOUND AND s.transition_id=repeat('0',64) THEN
				   SELECT * INTO f FROM public.stage08_flag_snapshots WHERE id=s.flag_snapshot_id;
				   e:=jsonb_build_object('Schema','stage08-authority-envelope-v1','Stage','schema_legacy','Authority','legacy','FlagID',s.flag_snapshot_id,'FlagDigest',s.flag_digest,'Stage07Context','','Stage07Deployment','');
				   IF s.stage<>'schema_legacy' OR s.authority<>'legacy' OR s.flag_snapshot_id<>s.flag_digest OR f.id IS NULL OR f.id<>f.content_digest OR s.authority_json IS DISTINCT FROM e OR s.version<1 THEN RAISE EXCEPTION 'cannot install bootstrap transition sentinel: legacy state is not canonical'; END IF;
				   INSERT INTO public.cutover_transitions(id,idempotency_key,from_stage,to_stage,from_authority,to_authority,flag_snapshot_id,flag_snapshot_digest,source_state_version,source_envelope_json,source_envelope_digest,target_envelope_json,target_envelope_digest,request_digest,principal,reason,prerequisites_json,content_digest,created_at)
				   VALUES(repeat('0',64),'stage08-bootstrap-sentinel-v1','schema_legacy','schema_legacy','legacy','legacy',s.flag_snapshot_id,s.flag_digest,0,s.authority_json,s.authority_digest,s.authority_json,s.authority_digest,repeat('0',64),'system:stage08-bootstrap','deterministic initial legacy authority bootstrap','[]',repeat('0',64),s.updated_at)
				   ON CONFLICT (id) DO NOTHING;
				 END IF;
				END $sentinel$;
				CREATE OR REPLACE FUNCTION public.record_verified_backup_evidence(p_source_before text,p_source_after text,p_target_fingerprint text,p_dump_checksum text,p_manifest_checksum text,p_target_identity_token text,p_tool_versions jsonb,p_verified_at timestamptz,p_principal text,p_flag_snapshot_id text,p_cutover_transition_id text)
				RETURNS TABLE(id text,source_fingerprint text,dump_checksum text,fixture_metadata_json jsonb,target_fingerprint text,canonical_digest text,status text,verified_at timestamptz,manifest_checksum text,tool_versions_json jsonb,flag_snapshot_id text,cutover_transition_id text) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $fn$
				DECLARE s public.cutover_states%ROWTYPE; f public.stage08_flag_snapshots%ROWTYPE; tr public.cutover_transitions%ROWTYPE; v_id text; fixture jsonb;
				BEGIN
				 IF p_source_before !~ '^[a-f0-9]{64}$' OR p_source_after<>p_source_before OR p_target_fingerprint<>p_source_before OR p_dump_checksum !~ '^[a-f0-9]{64}$' OR p_manifest_checksum !~ '^[a-f0-9]{64}$' OR p_target_identity_token !~ '^[a-f0-9]{32,128}$' OR p_principal IS NULL OR length(p_principal)=0 OR length(p_principal)>200 OR p_verified_at IS NULL OR date_trunc('second',p_verified_at)<>p_verified_at THEN RAISE EXCEPTION 'backup evidence manifest is malformed'; END IF;
				 IF jsonb_typeof(p_tool_versions)<>'object' OR (SELECT count(*) FROM jsonb_object_keys(p_tool_versions))>50 OR EXISTS(SELECT 1 FROM jsonb_each_text(p_tool_versions) v(k,val) WHERE k !~ '^[A-Za-z0-9._/-]{1,80}$' OR length(val)=0 OR length(val)>200) THEN RAISE EXCEPTION 'backup evidence tool metadata is malformed'; END IF;
				 IF p_manifest_checksum<>encode(digest(convert_to(p_source_before||'|'||p_dump_checksum||'|'||p_target_identity_token||'|'||to_char(p_verified_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),'UTF8'),'sha256'),'hex') THEN RAISE EXCEPTION 'backup verification manifest checksum mismatch'; END IF;
				 SELECT * INTO s FROM public.cutover_states AS cs WHERE cs.id=1; IF NOT FOUND OR p_flag_snapshot_id<>s.flag_snapshot_id OR p_cutover_transition_id<>s.transition_id THEN RAISE EXCEPTION 'backup evidence authority bindings do not equal current cutover state'; END IF;
				 SELECT * INTO f FROM public.stage08_flag_snapshots AS ss WHERE ss.id=p_flag_snapshot_id; IF NOT FOUND OR f.id<>f.content_digest OR f.content_digest<>s.flag_digest OR f.schema_version<>'stage08-flags-v1' OR s.authority_json->>'FlagID'<>f.id OR s.authority_json->>'FlagDigest'<>f.content_digest OR s.authority_json->>'Stage'<>s.stage OR s.authority_json->>'Authority'<>s.authority THEN RAISE EXCEPTION 'current cutover authority envelope is invalid'; END IF;
				 SELECT * INTO tr FROM public.cutover_transitions AS ct WHERE ct.id=p_cutover_transition_id; IF NOT FOUND OR tr.content_digest<>tr.id OR tr.flag_snapshot_id<>f.id OR tr.flag_snapshot_digest<>f.content_digest OR tr.to_stage<>s.stage OR tr.to_authority<>s.authority OR tr.target_envelope_digest<>s.authority_digest OR tr.target_envelope_json IS DISTINCT FROM s.authority_json THEN RAISE EXCEPTION 'current cutover transition is not bound to current authority envelope'; END IF;
				 IF p_cutover_transition_id=repeat('0',64) AND (s.stage<>'schema_legacy' OR s.authority<>'legacy' OR f.id<>encode(digest(convert_to('{"schema_version":"stage08-flags-v1","ledger_authority":"legacy","shared_engine":"off","new_backtest":"off","point_in_time_universe":"off","candidate_strategy":"off","dual_run":"off"}','UTF8'),'sha256'),'hex') OR s.version<1 OR tr.idempotency_key<>'stage08-bootstrap-sentinel-v1' OR tr.from_stage<>'schema_legacy' OR tr.from_authority<>'legacy' OR tr.source_state_version<>0 OR tr.source_envelope_json IS DISTINCT FROM s.authority_json OR tr.source_envelope_digest<>s.authority_digest OR tr.request_digest<>repeat('0',64) OR tr.principal<>'system:stage08-bootstrap' OR tr.reason<>'deterministic initial legacy authority bootstrap' OR tr.prerequisites_json<>'[]'::jsonb OR tr.rollback_of IS NOT NULL) THEN RAISE EXCEPTION 'persisted bootstrap transition sentinel is invalid'; END IF;
				 fixture:=jsonb_build_object('recorded_by',p_principal,'target_identity_token',p_target_identity_token); v_id:=encode(digest(convert_to(p_source_before||'|'||p_dump_checksum||'|'||p_target_identity_token||'|'||to_char(p_verified_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')||'|'||p_principal||'|'||f.id||'|'||tr.id,'UTF8'),'sha256'),'hex');
				 RETURN QUERY INSERT INTO public.backup_verifications AS b(id,source_fingerprint,dump_checksum,fixture_metadata_json,target_fingerprint,canonical_digest,status,verified_at,manifest_checksum,tool_versions_json,flag_snapshot_id,cutover_transition_id) VALUES(v_id,p_source_before,p_dump_checksum,fixture,p_target_fingerprint,p_source_before,'verified',p_verified_at,p_manifest_checksum,p_tool_versions,f.id,tr.id) ON CONFLICT DO NOTHING RETURNING b.id::text,b.source_fingerprint::text,b.dump_checksum::text,b.fixture_metadata_json,b.target_fingerprint::text,b.canonical_digest::text,b.status::text,b.verified_at,b.manifest_checksum::text,b.tool_versions_json,b.flag_snapshot_id::text,b.cutover_transition_id::text;
				 IF NOT FOUND THEN RETURN QUERY SELECT b.id::text,b.source_fingerprint::text,b.dump_checksum::text,b.fixture_metadata_json,b.target_fingerprint::text,b.canonical_digest::text,b.status::text,b.verified_at,b.manifest_checksum::text,b.tool_versions_json,b.flag_snapshot_id::text,b.cutover_transition_id::text FROM public.backup_verifications b WHERE b.id=v_id; END IF;
				END $fn$;
				ALTER FUNCTION public.record_verified_backup_evidence(text,text,text,text,text,text,jsonb,timestamptz,text,text,text) OWNER TO trading_bot_operations_owner;
				REVOKE ALL ON FUNCTION public.record_verified_backup_evidence(text,text,text,text,text,text,jsonb,timestamptz,text,text,text) FROM PUBLIC; GRANT EXECUTE ON FUNCTION public.record_verified_backup_evidence(text,text,text,text,text,text,jsonb,timestamptz,text,text,text) TO trading_bot_runtime;
				`).Error
			},
			Rollback: func(tx *gorm.DB) error { return fmt.Errorf("bootstrap transition sentinel is intentionally retained") },
		},
	})

	return m.Migrate()
}

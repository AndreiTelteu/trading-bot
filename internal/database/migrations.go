package database

import (
	"fmt"
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
	})

	return m.Migrate()
}

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
			Rollback: func(tx *gorm.DB) error { return nil },
		},
	})

	return m.Migrate()
}

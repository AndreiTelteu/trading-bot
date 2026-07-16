package ledger_test

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"sync"
	"testing"
	"time"
	"trading-go/internal/accounting"
	"trading-go/internal/database"
	ledgerpkg "trading-go/internal/ledger"
	"trading-go/internal/testutil"
	"trading-go/internal/tradingcore"
)

func readyService(t *testing.T) *ledgerpkg.Service {
	t.Helper()
	db := testutil.SetupPostgresDB(t)
	if err := database.SeedData(); err != nil {
		t.Fatalf("seed fresh ledger: %v", err)
	}
	return ledgerpkg.New(db)
}

func fill(key, side, symbol, quantity, price, fee string) ledgerpkg.FillCommand {
	return ledgerpkg.FillCommand{IdempotencyKey: key, Symbol: symbol, Side: side, Quantity: accounting.MustParse(quantity), RequestedPrice: accounting.MustParse(price), FillPrice: accounting.MustParse(price), Fee: accounting.MustParse(fee), FeeType: ledgerpkg.EventTradingFee, Currency: "USDT", ExecutionMode: "paper", Actor: "test", Reason: "invariant fixture", OccurredAt: time.Now().UTC()}
}

func TestRoundTripFeesIdempotencyAndReconciliation(t *testing.T) {
	service := readyService(t)
	ctx := context.Background()
	buy := fill("roundtrip-buy", "buy", "BTC", "1", "100", "1")
	first, err := service.ApplyFill(ctx, buy)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.ApplyFill(ctx, buy)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.AlreadyApplied {
		t.Fatal("expected idempotent replay")
	}
	if replayed.Wallet.Balance != first.Wallet.Balance {
		t.Fatal("replay changed cash")
	}
	sold, err := service.ApplyFill(ctx, fill("roundtrip-sell", "sell", "BTC", "1", "110", "1"))
	if err != nil {
		t.Fatal(err)
	}
	if sold.Position.Pnl != 8 {
		t.Fatalf("realized pnl=%v want 8", sold.Position.Pnl)
	}
	report, err := service.Reconcile(ctx, "", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Balanced {
		t.Fatalf("report not balanced:\n%s", report.String())
	}
	if report.CashByCurrency["USDT"] != "408" {
		t.Fatalf("cash=%s want 408", report.CashByCurrency["USDT"])
	}
	if report.FeesByCurrency["USDT"] != "2" {
		t.Fatalf("fees=%s want 2", report.FeesByCurrency["USDT"])
	}
	if report.RealizedPnLByCurrency["USDT"] != "8" {
		t.Fatalf("pnl=%s want 8", report.RealizedPnLByCurrency["USDT"])
	}
}

func TestPartialFillsReleaseAllCostBasisExactlyOnce(t *testing.T) {
	service := readyService(t)
	ctx := context.Background()
	if _, err := service.ApplyFill(ctx, fill("partial-buy", "buy", "ADA", "3", "10", "0")); err != nil {
		t.Fatal(err)
	}
	var result ledgerpkg.FillResult
	for _, key := range []string{"partial-sell-1", "partial-sell-2", "partial-sell-3"} {
		var err error
		result, err = service.ApplyFill(ctx, fill(key, "sell", "ADA", "1", "11", "0"))
		if err != nil {
			t.Fatal(err)
		}
	}
	if result.Position.CostBasisExact == nil || result.Position.CostBasisExact.String() != "0" || result.Position.Pnl != 3 {
		t.Fatalf("position=%+v basis=%v", result.Position, result.Position.CostBasisExact)
	}
	report, err := service.Reconcile(ctx, "", time.Time{})
	if err != nil || !report.Balanced {
		t.Fatalf("err=%v report=%s", err, report.String())
	}
}

func TestMultiplePartialFillsAccumulateOneOrderProjection(t *testing.T) {
	service := readyService(t)
	ctx := context.Background()
	if _, err := service.ApplyFill(ctx, fill("multi-open", "buy", "XRP", "2", "5", "0")); err != nil {
		t.Fatal(err)
	}
	first := fill("multi-close-1", "sell", "XRP", "1", "6", "0")
	first.OrderStatus = "partially_filled"
	firstResult, err := service.ApplyFill(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	second := fill("multi-close-2", "sell", "XRP", "1", "6", "0")
	second.ExistingOrderID = firstResult.Order.ID
	second.OrderStatus = "filled"
	secondResult, err := service.ApplyFill(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if secondResult.Order.AmountCryptoExact == nil || secondResult.Order.AmountCryptoExact.String() != "2" || secondResult.Order.AmountUsdtExact.String() != "12" {
		t.Fatalf("order=%+v", secondResult.Order)
	}
	if secondResult.Order.AmountCrypto != 2 || secondResult.Order.AmountUsdt != 12 || secondResult.Order.FilledAt == nil {
		t.Fatalf("compatibility aggregates=%+v", secondResult.Order)
	}
	if firstResult.Order.FilledAt != nil {
		t.Fatal("partially filled order must not have filled_at")
	}
	var count int64
	if err := database.DB.Model(&database.Fill{}).Where("order_id = ?", secondResult.Order.ID).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("fills=%d err=%v", count, err)
	}
	report, err := service.Reconcile(ctx, "", time.Time{})
	if err != nil || !report.Balanced {
		t.Fatalf("err=%v report=%s", err, report.String())
	}
}

func TestFailureAfterEachEconomicWriteRollsBackEverything(t *testing.T) {
	for _, stage := range []string{"projection", "order", "fill", "ledger"} {
		t.Run(stage, func(t *testing.T) {
			service := readyService(t)
			service.AfterWrite = func(got string) error {
				if got == stage {
					return errors.New("injected")
				}
				return nil
			}
			_, err := service.ApplyFill(context.Background(), fill("rollback-"+stage, "buy", "ETH", "1", "10", "0"))
			if err == nil {
				t.Fatal("expected failure")
			}
			for name, model := range map[string]interface{}{"orders": &database.Order{}, "fills": &database.Fill{}} {
				var count int64
				if err := database.DB.Model(model).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("%s count=%d err=%v", name, count, err)
				}
			}
			var eventCount int64
			database.DB.Model(&database.LedgerEvent{}).Where("event_type = ?", ledgerpkg.EventBuyFill).Count(&eventCount)
			if eventCount != 0 {
				t.Fatalf("fill events=%d", eventCount)
			}
			var wallet database.Wallet
			database.DB.First(&wallet)
			if wallet.BalanceExact == nil || wallet.BalanceExact.String() != "400" {
				t.Fatalf("wallet changed: %+v", wallet.BalanceExact)
			}
		})
	}
}

func TestConcurrentBuysAndClosesCannotOverspendOrOversell(t *testing.T) {
	service := readyService(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, key := range []string{"cash-a", "cash-b"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			_, err := service.ApplyFill(ctx, fill(key, "buy", key, "1", "300", "0"))
			errs <- err
		}(key)
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		} else if !errors.Is(err, ledgerpkg.ErrInsufficientCash) {
			t.Fatalf("unexpected buy error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful buys=%d want 1", successes)
	}

	service = readyService(t)
	if _, err := service.ApplyFill(ctx, fill("close-open", "buy", "SOL", "1", "10", "0")); err != nil {
		t.Fatal(err)
	}
	errs = make(chan error, 2)
	wg = sync.WaitGroup{}
	for _, key := range []string{"close-a", "close-b"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			_, err := service.ApplyFill(ctx, fill(key, "sell", "SOL", "1", "11", "0"))
			errs <- err
		}(key)
	}
	wg.Wait()
	close(errs)
	successes = 0
	for err := range errs {
		if err == nil {
			successes++
		} else if !errors.Is(err, ledgerpkg.ErrProjectionUnavailable) && !errors.Is(err, ledgerpkg.ErrInsufficientAsset) {
			t.Fatalf("unexpected close error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful closes=%d want 1", successes)
	}
}

func TestCashReversalBalancesOriginalWithoutMutation(t *testing.T) {
	service := readyService(t)
	ctx := context.Background()
	adjusted, err := service.ApplyAdjustment(ctx, ledgerpkg.AdjustmentCommand{IdempotencyKey: "deposit", Type: ledgerpkg.EventCapitalDeposit, Amount: accounting.MustParse("25"), Currency: "USDT", Actor: "operator", Reason: "test deposit"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReverseCashEvent(ctx, ledgerpkg.ReversalCommand{IdempotencyKey: "reverse-deposit", OriginalEventID: adjusted.Event.ID, Actor: "operator", Reason: "deposit entered twice"}); err != nil {
		t.Fatal(err)
	}
	var original database.LedgerEvent
	if err := database.DB.First(&original, "id = ?", adjusted.Event.ID).Error; err != nil {
		t.Fatal(err)
	}
	if original.CashDelta.String() != "25" {
		t.Fatalf("original mutated: %s", original.CashDelta.String())
	}
	original.Reason = "attempted rewrite"
	if err := database.DB.Save(&original).Error; err == nil {
		t.Fatal("database allowed immutable ledger event update")
	}
	if err := database.DB.Delete(&original).Error; err == nil {
		t.Fatal("database allowed immutable ledger event delete")
	}
	report, err := service.Reconcile(ctx, "", time.Time{})
	if err != nil || !report.Balanced {
		t.Fatalf("reconcile err=%v report=%s", err, report.String())
	}
}

func TestBackfillDefaultsToDryRunAndLeavesLegacyHistoryUnresolved(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	database.DB = db
	if err := db.Create(&database.Wallet{ID: 1, Balance: 123, Currency: "USDT"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.Position{Symbol: "LEGACY", Amount: 2, AvgPrice: 10, Status: "open", OpenedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	type snapshot struct{ Wallets, Positions, Orders, Batches, Fills, Events, States int64 }
	take := func() snapshot {
		var s snapshot
		db.Model(&database.Wallet{}).Count(&s.Wallets)
		db.Model(&database.Position{}).Count(&s.Positions)
		db.Model(&database.Order{}).Count(&s.Orders)
		db.Model(&database.LedgerBatch{}).Count(&s.Batches)
		db.Model(&database.Fill{}).Count(&s.Fills)
		db.Model(&database.LedgerEvent{}).Count(&s.Events)
		db.Model(&database.LedgerMigrationState{}).Count(&s.States)
		return s
	}
	before := take()
	report, err := ledgerpkg.New(db).Backfill(context.Background(), ledgerpkg.BackfillOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.DryRun || report.Applied || len(report.Unresolved) == 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	var count int64
	db.Model(&database.LedgerEvent{}).Count(&count)
	if count != 0 {
		t.Fatalf("dry run wrote %d events", count)
	}
	if after := take(); after != before {
		t.Fatalf("dry-run mutated tables before=%+v after=%+v", before, after)
	}
}

func TestBackfillRequiresApprovalAndAppliesOnlyOpeningCash(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	database.DB = db
	if err := db.Create(&database.Wallet{ID: 1, Balance: 321, Currency: "USDT"}).Error; err != nil {
		t.Fatal(err)
	}
	service := ledgerpkg.New(db)
	if _, err := service.Backfill(context.Background(), ledgerpkg.BackfillOptions{Apply: true, ApprovedBy: "operator"}); err == nil {
		t.Fatal("expected approval phrase rejection")
	}
	report, err := service.Backfill(context.Background(), ledgerpkg.BackfillOptions{Apply: true, Approval: ledgerpkg.BackfillApproval, ApprovedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Applied {
		t.Fatalf("report=%+v", report)
	}
	var events []database.LedgerEvent
	if err := db.Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EventType != ledgerpkg.EventCapitalDeposit || events[0].CashDelta.String() != "321" {
		t.Fatalf("events=%+v", events)
	}
}

func TestReconciliationReportsKnownProjectionAndOrphanFixture(t *testing.T) {
	service := readyService(t)
	opened, err := service.ApplyFill(context.Background(), fill("reconcile-open", "buy", "REC", "2", "10", "1"))
	if err != nil {
		t.Fatal(err)
	}
	var wallet database.Wallet
	if err := database.DB.First(&wallet).Error; err != nil {
		t.Fatal(err)
	}
	wrong := accounting.MustParse("399")
	wallet.BalanceExact = &wrong
	wallet.Balance = 399
	if err := database.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL trading_bot.ledger_write='on'").Error; err != nil {
			return err
		}
		return tx.Save(&wallet).Error
	}); err != nil {
		t.Fatal(err)
	}
	wrongBasis := accounting.MustParse("99")
	wrongFee := accounting.MustParse("9")
	wrongPnL := accounting.MustParse("7")
	if err := database.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL trading_bot.ledger_write='on'").Error; err != nil {
			return err
		}
		return tx.Model(&database.Position{}).Where("id = ?", opened.Position.ID).Updates(map[string]interface{}{"cost_basis_exact": wrongBasis, "fees_exact": wrongFee, "realized_pn_l_exact": wrongPnL}).Error
	}); err != nil {
		t.Fatal(err)
	}
	orphan := database.Order{OrderType: "buy", Symbol: "ORPHAN", AmountCrypto: 1, AmountUsdt: 10, Price: 10, Status: "filled", ExecutionMode: "paper", ExecutedAt: time.Now()}
	if err := database.DB.Create(&orphan).Error; err != nil {
		t.Fatal(err)
	}
	report, err := service.Reconcile(context.Background(), "", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	dimensions := map[string]bool{}
	for _, difference := range report.Differences {
		dimensions[difference.Dimension] = true
	}
	if report.Balanced || !dimensions["cash"] || !dimensions["cost_basis"] || !dimensions["fees"] || !dimensions["realized_pnl"] {
		t.Fatalf("cash differences=%+v", report.Differences)
	}
	if len(report.OrphanOrderIDs) != 1 || report.OrphanOrderIDs[0] != orphan.ID {
		t.Fatalf("orphan orders=%v", report.OrphanOrderIDs)
	}
}

func TestTradingCoreContractAdapterAppendsAndReadsCashEvent(t *testing.T) {
	service := readyService(t)
	adapter := ledgerpkg.NewContractAdapter(service.DB)
	eventID, _ := tradingcore.NewEventID("core-deposit-event")
	key, _ := tradingcore.NewIdempotencyKey("core-deposit")
	account, _ := tradingcore.NewAccountID("primary")
	asset, _ := tradingcore.NewAssetID("USDT")
	venue, _ := tradingcore.NewVenueID("internal")
	amount, _ := tradingcore.NewSignedAmount(mustCore(t, "10"))
	event, err := tradingcore.NewLedgerEvent(tradingcore.LedgerEvent{ID: eventID, IdempotencyKey: key, Type: tradingcore.LedgerCapitalDeposit, AccountID: account, VenueID: venue, OccurredAt: time.Now(), RecordedAt: time.Now(), Provenance: tradingcore.Provenance{Actor: "test", Reason: "adapter fixture"}}, []tradingcore.LedgerPosting{{Dimension: tradingcore.PostingCash, AssetID: asset, Amount: amount}})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := tradingcore.NewLedgerBatch(key, []tradingcore.LedgerEvent{event})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := adapter.AppendAtomic(context.Background(), batch)
	if err != nil || outcome.Status() != tradingcore.LedgerAppended {
		t.Fatalf("outcome=%v err=%v", outcome.Status(), err)
	}
	events, err := adapter.Events(context.Background(), account, time.Time{})
	if err != nil || len(events) != 2 {
		t.Fatalf("events=%d err=%v", len(events), err)
	}
}

func mustCore(t *testing.T, value string) tradingcore.Decimal {
	t.Helper()
	result, err := tradingcore.ParseDecimal(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestFutureEventAccountCurrencyAndProviderIdentityValidation(t *testing.T) {
	service := readyService(t)
	future := fill("future", "buy", "FUT", "1", "1", "0")
	future.OccurredAt = time.Now().Add(time.Hour)
	if _, err := service.ApplyFill(context.Background(), future); err == nil {
		t.Fatal("future event accepted")
	}
	unsupported := fill("account", "buy", "ACC", "1", "1", "0")
	unsupported.AccountID = "other"
	if _, err := service.ApplyFill(context.Background(), unsupported); err == nil {
		t.Fatal("unsupported account accepted")
	}
	feeAsset := fill("fee-asset", "buy", "FEE", "1", "1", "0.1")
	feeAsset.FeeCurrency = "BNB"
	if _, err := service.ApplyFill(context.Background(), feeAsset); err == nil {
		t.Fatal("third-asset fee accepted")
	}
	provider := fill("provider-1", "buy", "PROV", "1", "2", "0")
	provider.ExecutionMode = "exchange"
	provider.VenueID = "binance"
	provider.ProviderOrderID = "order-1"
	provider.ProviderFillID = "trade-77"
	if _, err := service.ApplyFill(context.Background(), provider); err != nil {
		t.Fatal(err)
	}
	duplicate := provider
	duplicate.IdempotencyKey = "provider-2"
	if _, err := service.ApplyFill(context.Background(), duplicate); err == nil {
		t.Fatal("duplicate provider fill accepted")
	} else {
		kind, code := ledgerpkg.ErrorDetails(err)
		if kind != ledgerpkg.KindConflict || code != "duplicate_identity" {
			t.Fatalf("kind=%s code=%s err=%v", kind, code, err)
		}
	}
}

func TestAssetCorrectionAndCompensatingReversalResolveLegacyExposure(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	database.DB = db
	if err := db.Create(&database.Wallet{ID: 1, AccountID: "primary", Balance: 100, Currency: "USDT"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.Position{AccountID: "primary", Symbol: "LEG", Amount: 2, AvgPrice: 10, Status: "open", OpenedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	service := ledgerpkg.New(db)
	if _, err := service.Backfill(context.Background(), ledgerpkg.BackfillOptions{Apply: true, Approval: ledgerpkg.BackfillApproval, ApprovedBy: "operator"}); err != nil {
		t.Fatal(err)
	}
	if err := service.CheckReady(context.Background(), ""); err == nil {
		t.Fatal("unresolved exposure marked ready")
	}
	event, err := service.ApplyAssetCorrection(context.Background(), ledgerpkg.AssetCorrectionCommand{IdempotencyKey: "legacy-asset", Symbol: "LEG", Quantity: accounting.MustParse("2"), CostBasis: accounting.MustParse("20"), Currency: "USDT", Actor: "operator", Reason: "verified statement", Evidence: map[string]interface{}{"statement": "sha256:test"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.CheckReady(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	report, err := service.Reconcile(context.Background(), "", time.Time{})
	if err != nil || !report.Balanced {
		t.Fatalf("report=%s err=%v", report.String(), err)
	}
	if _, err := service.ReverseCashEvent(context.Background(), ledgerpkg.ReversalCommand{IdempotencyKey: "reverse-asset", OriginalEventID: event.ID, Actor: "operator", Reason: "statement corrected"}); err != nil {
		t.Fatal(err)
	}
	report, err = service.Reconcile(context.Background(), "", time.Time{})
	if err != nil || !report.Balanced {
		t.Fatalf("post reversal report=%s err=%v", report.String(), err)
	}
}

func TestBatchImmutabilityForeignKeysAndTimeConstraint(t *testing.T) {
	service := readyService(t)
	result, err := service.ApplyFill(context.Background(), fill("immutable-batch", "buy", "IMM", "1", "1", "0"))
	if err != nil {
		t.Fatal(err)
	}
	var batch database.LedgerBatch
	if err := database.DB.First(&batch, "id = ?", "immutable-batch").Error; err != nil {
		t.Fatal(err)
	}
	batch.PayloadHash = "rewrite"
	if err := database.DB.Save(&batch).Error; err == nil {
		t.Fatal("mutable ledger batch")
	}
	if err := database.DB.Delete(&batch).Error; err == nil {
		t.Fatal("deletable ledger batch")
	}
	if err := database.DB.Delete(&result.Order).Error; err == nil {
		t.Fatal("order with fill was deletable")
	}
	now := time.Now().UTC()
	if err := database.DB.Create(&database.LedgerBatch{ID: "bad-links", AccountID: "primary", PayloadHash: "fixture", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	badFill := database.Fill{ID: "bad-fill", LedgerBatchID: "bad-links", AccountID: "primary", OrderID: result.Order.ID, VenueID: "internal", PositionID: result.Position.ID, Symbol: "OTHER", Side: "buy", Quantity: accounting.MustParse("1"), RequestedPrice: accounting.MustParse("1"), FillPrice: accounting.MustParse("1"), GrossAmount: accounting.MustParse("1"), FeeAmount: accounting.Zero(), FeeType: ledgerpkg.EventTradingFee, FeeCurrency: "USDT", ExecutionMode: "paper", OccurredAt: now, CreatedAt: now}
	if err := database.DB.Create(&badFill).Error; err == nil {
		t.Fatal("database accepted inconsistent fill/order/position links")
	}
	future := database.LedgerEvent{ID: "future-direct", LedgerBatchID: "immutable-batch", Sequence: 99, IdempotencyKey: "future-direct", EventType: ledgerpkg.EventAdminCorrection, AccountID: "primary", VenueID: "internal", Currency: "USDT", CashDelta: accounting.Zero(), AssetDelta: accounting.Zero(), ExecutionMode: "administrative", Actor: "test", Reason: "future", RealizedPnL: accounting.Zero(), CostBasisDelta: accounting.Zero(), FeeDelta: accounting.Zero(), MetadataJSON: "{}", OccurredAt: time.Now().Add(time.Hour), RecordedAt: time.Now()}
	if err := database.DB.Create(&future).Error; err == nil {
		t.Fatal("database accepted occurred_at after recorded_at")
	}
}

package operations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"trading-go/internal/cutover"
	"trading-go/internal/database"
	stage07 "trading-go/internal/governance"
	"trading-go/internal/ledger"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const StatusSchemaVersion = "stage08-operational-status-v1"

type AlertDispatcher interface {
	Dispatch(context.Context, database.OperationalIncident) error
}
type Service struct {
	DB     *gorm.DB
	Flags  cutover.Flags
	Alerts AlertDispatcher
	Now    func() time.Time
}

func New(db *gorm.DB, flags cutover.Flags) Service {
	return Service{DB: db, Flags: flags, Now: func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }}
}
func (s Service) now() time.Time {
	if s.Now == nil {
		return time.Now().UTC().Truncate(time.Microsecond)
	}
	return s.Now().UTC().Truncate(time.Microsecond)
}
func hash(v any) (string, []byte, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return "", nil, e
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), b, nil
}

func (s Service) Initialize(ctx context.Context) (database.Stage08FlagSnapshot, error) {
	if err := s.Flags.Validate(); err != nil {
		return database.Stage08FlagSnapshot{}, err
	}
	content, digest, err := s.Flags.Canonical()
	if err != nil {
		return database.Stage08FlagSnapshot{}, err
	}
	row := database.Stage08FlagSnapshot{ID: digest, SchemaVersion: cutover.FlagSchemaVersion, ContentJSON: string(content), ContentDigest: digest, CreatedAt: s.now()}
	if err := s.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return row, err
	}
	var engineSetting database.Setting
	if err := s.DB.First(&engineSetting, "key=?", "trading_engine_mode").Error; err != nil {
		return row, fmt.Errorf("trading_engine_mode setting missing: %w", err)
	}
	if engineSetting.Value != "legacy" && engineSetting.Value != "shared" && engineSetting.Value != "shadow_compare" {
		return row, fmt.Errorf("unknown persisted trading_engine_mode %q", engineSetting.Value)
	}
	var fallbackSetting database.Setting
	if err := s.DB.First(&fallbackSetting, "key=?", "trading_engine_fallback").Error; err != nil {
		return row, fmt.Errorf("trading_engine_fallback setting missing: %w", err)
	}
	if fallbackSetting.Value != "disabled" && fallbackSetting.Value != "legacy" {
		return row, fmt.Errorf("unknown persisted trading_engine_fallback %q", fallbackSetting.Value)
	}
	if s.Flags.LedgerAuthority == "authoritative" {
		report, err := ledger.New(s.DB).Reconcile(ctx, ledger.DefaultAccountID, time.Time{})
		if err != nil {
			return row, fmt.Errorf("ledger authority reconciliation: %w", err)
		}
		if !report.Balanced {
			return row, fmt.Errorf("ledger authority blocked: %s", strings.Join(report.ActionableIssues, "; "))
		}
	}
	if s.Flags.IsLive() {
		var deployment database.GovernanceDeployment
		if err := s.DB.First(&deployment, "context_key=?", s.Flags.Stage07Context).Error; err != nil {
			return row, fmt.Errorf("Stage 07 exact deployment missing: %w", err)
		}
		if err := stage07.VerifyDeployment(s.DB, deployment); err != nil {
			return row, fmt.Errorf("Stage 07 deployment invalid: %w", err)
		}
		if deployment.State != s.Flags.SharedEngine {
			return row, fmt.Errorf("Stage 07 deployment state %q does not match live mode %q", deployment.State, s.Flags.SharedEngine)
		}
	}
	return row, s.reconcileCutoverStartup(row.ID)
}

func (s Service) reconcileCutoverStartup(flagID string) error {
	var state database.CutoverState
	err := s.DB.First(&state, 1).Error
	if err == gorm.ErrRecordNotFound {
		state = database.CutoverState{ID: 1, Stage: "schema_legacy", Authority: "legacy", FlagSnapshotID: flagID, TransitionID: strings.Repeat("0", 64), Version: 1, UpdatedAt: s.now()}
		return s.DB.Create(&state).Error
	}
	if err != nil {
		return err
	}
	var transition database.CutoverTransition
	if state.TransitionID != strings.Repeat("0", 64) {
		if err := s.DB.First(&transition, "id=?", state.TransitionID).Error; err != nil || transition.ToStage != state.Stage || transition.ToAuthority != state.Authority {
			return fmt.Errorf("cutover state does not match immutable transition")
		}
	}
	return nil
}

type IncidentInput struct {
	DedupeKey, Type, Severity, Summary string
	Details                            map[string]any
	Cooldown                           time.Duration
}

func (s Service) RaiseIncident(ctx context.Context, input IncidentInput) (database.OperationalIncident, error) {
	if input.DedupeKey == "" || input.Type == "" || input.Summary == "" {
		return database.OperationalIncident{}, fmt.Errorf("typed incident identity is required")
	}
	if input.Cooldown <= 0 {
		input.Cooldown = 15 * time.Minute
	}
	details, _ := json.Marshal(input.Details)
	if len(details) > 16<<10 {
		return database.OperationalIncident{}, fmt.Errorf("incident details exceed 16KiB")
	}
	now := s.now()
	id, _, _ := hash(struct{ Key, Type string }{input.DedupeKey, input.Type})
	var row database.OperationalIncident
	shouldDispatch := false
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL trading_bot.operational_incident_write = 'on'").Error; err != nil {
			return err
		}
		q := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "dedupe_key=?", input.DedupeKey)
		if q.Error == gorm.ErrRecordNotFound {
			shouldDispatch = true
			row = database.OperationalIncident{ID: id, DedupeKey: input.DedupeKey, Type: input.Type, Severity: input.Severity, State: "open", Summary: input.Summary, DetailsJSON: string(details), Occurrences: 1, FirstSeenAt: now, LastSeenAt: now, CooldownUntil: now.Add(input.Cooldown), LastDeliveryState: "not_configured", UpdatedAt: now}
			return tx.Create(&row).Error
		}
		if q.Error != nil {
			return q.Error
		}
		shouldDispatch = !now.Before(row.CooldownUntil)
		row.Occurrences++
		row.LastSeenAt = now
		row.Summary = input.Summary
		row.DetailsJSON = string(details)
		if row.State == "resolved" {
			row.State = "open"
			row.ResolvedAt = nil
			row.ResolvedBy = nil
		}
		row.UpdatedAt = now
		return tx.Save(&row).Error
	})
	if err != nil {
		return row, err
	}
	if s.Alerts != nil && shouldDispatch {
		attempt := now
		deliveryErr := s.Alerts.Dispatch(ctx, row)
		updates := map[string]any{"last_delivery_attempt": attempt, "last_delivery_state": "delivered", "last_delivery_error": ""}
		if deliveryErr != nil {
			updates["last_delivery_state"] = "failed"
			updates["last_delivery_error"] = deliveryErr.Error()
		}
		_ = s.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SET LOCAL trading_bot.operational_incident_write = 'on'").Error; err != nil {
				return err
			}
			return tx.Model(&database.OperationalIncident{}).Where("id=?", row.ID).Updates(updates).Error
		})
	}
	return row, nil
}
func (s Service) TransitionIncident(ctx context.Context, id, to, actor, reason string) (database.OperationalIncident, error) {
	if actor == "" || reason == "" {
		return database.OperationalIncident{}, fmt.Errorf("trusted actor and reason required")
	}
	var row database.OperationalIncident
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL trading_bot.operational_incident_write = 'on'").Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id=?", id).Error; err != nil {
			return err
		}
		from := row.State
		if (from == "open" && to != "acknowledged" && to != "resolved") || (from == "acknowledged" && to != "resolved") || from == "resolved" {
			return fmt.Errorf("illegal incident transition %s -> %s", from, to)
		}
		now := s.now()
		if to == "acknowledged" {
			row.AcknowledgedAt = &now
			row.AcknowledgedBy = &actor
		} else {
			row.ResolvedAt = &now
			row.ResolvedBy = &actor
		}
		row.State = to
		row.UpdatedAt = now
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		digest, _, _ := hash(struct {
			ID, From, To, Actor, Reason string
			At                          time.Time
		}{id, from, to, actor, reason, now})
		audit := database.OperationalIncidentAudit{ID: digest, IncidentID: id, FromState: from, ToState: to, Actor: actor, Reason: reason, CreatedAt: now, Digest: digest}
		return tx.Create(&audit).Error
	})
	return row, err
}

type ParityAggregate struct {
	Total       int64  `json:"total"`
	Matches     int64  `json:"matches"`
	Expected    int64  `json:"expected"`
	Unexplained int64  `json:"unexplained"`
	Action      int64  `json:"unexplained_action,omitempty"`
	Quantity    int64  `json:"unexplained_quantity,omitempty"`
	Reason      int64  `json:"unexplained_reason,omitempty"`
	Version     int64  `json:"unexplained_version,omitempty"`
	CoverageBPS int64  `json:"coverage_bps"`
	Accepted    bool   `json:"accepted"`
	Failure     string `json:"failure,omitempty"`
}

type DeclareParityPolicyRequest struct {
	Name                 string                   `json:"name"`
	MinimumSamples       int64                    `json:"minimum_samples"`
	MinimumCoverageBPS   int64                    `json:"minimum_coverage_bps"`
	MaxActionRateBPS     int64                    `json:"max_action_rate_bps"`
	MaxQuantityRateBPS   int64                    `json:"max_quantity_rate_bps"`
	MaxReasonRateBPS     int64                    `json:"max_reason_rate_bps"`
	MaxVersionRateBPS    int64                    `json:"max_version_rate_bps"`
	QuantityToleranceBPS int64                    `json:"quantity_tolerance_bps"`
	NotionalToleranceBPS int64                    `json:"notional_tolerance_bps"`
	Expected             []cutover.ExpectedReason `json:"expected_reasons"`
}

func (s Service) DeclareParityPolicy(ctx context.Context, request DeclareParityPolicyRequest, principal string) (database.ParityAcceptancePolicy, error) {
	if principal == "" || request.Name == "" || request.MinimumSamples <= 0 || request.MinimumCoverageBPS <= 0 || request.MinimumCoverageBPS > 10000 {
		return database.ParityAcceptancePolicy{}, fmt.Errorf("trusted principal, name, positive samples, and coverage in (0,10000] required")
	}
	for _, value := range []int64{request.MaxActionRateBPS, request.MaxQuantityRateBPS, request.MaxReasonRateBPS, request.MaxVersionRateBPS, request.QuantityToleranceBPS, request.NotionalToleranceBPS} {
		if value < 0 || value > 10000 {
			return database.ParityAcceptancePolicy{}, fmt.Errorf("parity rates/tolerances must be in [0,10000]")
		}
	}
	expectedJSON, _ := json.Marshal(request.Expected)
	now := s.now()
	canonical := struct {
		Schema, Name                                                                                                                                              string
		MinimumSamples, MinimumCoverageBPS, MaxActionRateBPS, MaxQuantityRateBPS, MaxReasonRateBPS, MaxVersionRateBPS, QuantityToleranceBPS, NotionalToleranceBPS int64
		Expected                                                                                                                                                  json.RawMessage
		Principal                                                                                                                                                 string
		At                                                                                                                                                        time.Time
	}{cutover.ParitySchemaVersion, request.Name, request.MinimumSamples, request.MinimumCoverageBPS, request.MaxActionRateBPS, request.MaxQuantityRateBPS, request.MaxReasonRateBPS, request.MaxVersionRateBPS, request.QuantityToleranceBPS, request.NotionalToleranceBPS, expectedJSON, principal, now}
	id, _, _ := hash(canonical)
	row := database.ParityAcceptancePolicy{ID: id, SchemaVersion: cutover.ParitySchemaVersion, Name: request.Name, MinimumSamples: request.MinimumSamples, MinimumCoverageBPS: request.MinimumCoverageBPS, MaxActionRateBPS: request.MaxActionRateBPS, MaxQuantityRateBPS: request.MaxQuantityRateBPS, MaxReasonRateBPS: request.MaxReasonRateBPS, MaxVersionRateBPS: request.MaxVersionRateBPS, QuantityToleranceBPS: request.QuantityToleranceBPS, NotionalToleranceBPS: request.NotionalToleranceBPS, ExpectedReasonsJSON: string(expectedJSON), ContentDigest: id, DeclaredBy: principal, DeclaredAt: now}
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&database.ParityObservation{}).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("parity policy must be declared before observations")
		}
		return tx.Create(&row).Error
	})
	return row, err
}

func (s Service) PersistParity(ctx context.Context, pairKey, flagID string, c cutover.Comparison, at time.Time) (database.ParityObservation, error) {
	codes, _ := json.Marshal(c.DivergenceCodes)
	reasons, _ := json.Marshal(c.ExpectedReasons)
	sample, _ := json.Marshal(struct{ Legacy, Candidate cutover.DecisionOutcome }{c.Legacy, c.Candidate})
	row := database.ParityObservation{ID: c.ContentDigest, ContextID: c.ContextID, PairKey: pairKey, SchemaVersion: cutover.ParitySchemaVersion, FlagSnapshotID: flagID, LegacyDigest: c.LegacyDigest, CandidateDigest: c.CandidateDigest, Classification: c.Classification, DivergenceCodesJSON: string(codes), ExpectedPolicyReasons: string(reasons), CompactSampleJSON: string(sample), ContentDigest: c.ContentDigest, ObservedAt: at.UTC()}
	if len(row.CompactSampleJSON) > 32<<10 {
		return row, fmt.Errorf("parity compact sample exceeds 32KiB")
	}
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing database.ParityObservation
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&existing, "context_id=? AND pair_key=?", row.ContextID, row.PairKey)
		if query.Error == nil {
			if existing.ContentDigest != row.ContentDigest {
				return fmt.Errorf("parity idempotency payload conflict")
			}
			row = existing
			return nil
		}
		if query.Error != gorm.ErrRecordNotFound {
			return query.Error
		}
		var count int64
		if err := tx.Model(&database.ParityObservation{}).Where("pair_key=?", pairKey).Count(&count).Error; err != nil {
			return err
		}
		if count >= 10000 {
			return fmt.Errorf("parity retention cap reached for pair %s", pairKey)
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		increments := map[string]any{"total": gorm.Expr("parity_aggregates.total + 1"), "updated_at": at.UTC()}
		switch c.Classification {
		case "match":
			increments["matches"] = gorm.Expr("parity_aggregates.matches + 1")
		case "expected":
			increments["expected"] = gorm.Expr("parity_aggregates.expected + 1")
		case "unexplained":
			increments["unexplained"] = gorm.Expr("parity_aggregates.unexplained + 1")
		}
		codes := strings.Join(c.DivergenceCodes, ",")
		if strings.Contains(codes, "action") {
			increments["action_divergences"] = gorm.Expr("parity_aggregates.action_divergences + 1")
		}
		if strings.Contains(codes, "quantity") || strings.Contains(codes, "notional") {
			increments["quantity_divergences"] = gorm.Expr("parity_aggregates.quantity_divergences + 1")
		}
		if strings.Contains(codes, "reason") {
			increments["reason_divergences"] = gorm.Expr("parity_aggregates.reason_divergences + 1")
		}
		if strings.Contains(codes, "version") {
			increments["version_divergences"] = gorm.Expr("parity_aggregates.version_divergences + 1")
		}
		aggregate := database.ParityAggregate{PairKey: pairKey, Total: 1, UpdatedAt: at.UTC()}
		switch c.Classification {
		case "match":
			aggregate.Matches = 1
		case "expected":
			aggregate.Expected = 1
		case "unexplained":
			aggregate.Unexplained = 1
		}
		if strings.Contains(codes, "action") {
			aggregate.ActionDivergences = 1
		}
		if strings.Contains(codes, "quantity") || strings.Contains(codes, "notional") {
			aggregate.QuantityDivergences = 1
		}
		if strings.Contains(codes, "reason") {
			aggregate.ReasonDivergences = 1
		}
		if strings.Contains(codes, "version") {
			aggregate.VersionDivergences = 1
		}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "pair_key"}}, DoUpdates: clause.Assignments(increments)}).Create(&aggregate).Error
	})
	return row, err
}
func (s Service) EvaluateParity(policyID string, expectedContexts int64) (ParityAggregate, error) {
	var p database.ParityAcceptancePolicy
	if err := s.DB.First(&p, "id=?", policyID).Error; err != nil {
		return ParityAggregate{}, err
	}
	var rows []database.ParityObservation
	if err := s.DB.Order("observed_at desc").Limit(10000).Find(&rows).Error; err != nil {
		return ParityAggregate{}, err
	}
	a := ParityAggregate{Total: int64(len(rows))}
	for _, r := range rows {
		switch r.Classification {
		case "match":
			a.Matches++
		case "expected":
			a.Expected++
		case "unexplained":
			a.Unexplained++
		}
		if r.Classification == "unexplained" {
			if strings.Contains(r.DivergenceCodesJSON, "action") {
				a.Action++
			}
			if strings.Contains(r.DivergenceCodesJSON, "quantity") || strings.Contains(r.DivergenceCodesJSON, "notional") {
				a.Quantity++
			}
			if strings.Contains(r.DivergenceCodesJSON, "reason") {
				a.Reason++
			}
			if strings.Contains(r.DivergenceCodesJSON, "version") {
				a.Version++
			}
		}
	}
	if expectedContexts > 0 {
		a.CoverageBPS = a.Total * 10000 / expectedContexts
		if a.CoverageBPS > 10000 {
			a.CoverageBPS = 10000
		}
	}
	rate := func(v int64) int64 {
		if a.Total == 0 {
			return 10000
		}
		return v * 10000 / a.Total
	}
	if a.Total < p.MinimumSamples {
		a.Failure = "insufficient_samples"
	} else if a.CoverageBPS < p.MinimumCoverageBPS {
		a.Failure = "insufficient_coverage"
	} else if rate(a.Action) > p.MaxActionRateBPS || rate(a.Quantity) > p.MaxQuantityRateBPS || rate(a.Reason) > p.MaxReasonRateBPS || rate(a.Version) > p.MaxVersionRateBPS {
		a.Failure = "unexplained_rate_exceeded"
	} else {
		a.Accepted = true
	}
	return a, nil
}

var stages = []string{"schema_legacy", "ledger_compare", "shared_shadow", "parity_accepted", "new_paper", "paper_observation", "research_validation", "limited_live", "legacy_removal_eligible"}

type TransitionRequest struct {
	IdempotencyKey         string          `json:"idempotency_key"`
	ToStage                string          `json:"to_stage"`
	Principal              string          `json:"-"`
	Reason                 string          `json:"reason"`
	FlagSnapshotID         string          `json:"flag_snapshot_id"`
	Stage07ContextKey      string          `json:"stage07_context_key"`
	ParityPolicyID         string          `json:"parity_policy_id"`
	ExpectedParityContexts int64           `json:"expected_parity_contexts"`
	Prerequisites          map[string]bool `json:"prerequisites,omitempty"`
	Rollback               bool            `json:"rollback"`
}

func (s Service) TransitionCutover(ctx context.Context, r TransitionRequest) (database.CutoverTransition, error) {
	if r.IdempotencyKey == "" || r.Principal == "" || r.Reason == "" {
		return database.CutoverTransition{}, fmt.Errorf("idempotency key, trusted principal, and reason required")
	}
	var result database.CutoverTransition
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing database.CutoverTransition
		if e := tx.First(&existing, "idempotency_key=?", r.IdempotencyKey).Error; e == nil {
			if existing.ToStage != r.ToStage || existing.Principal != r.Principal || existing.Reason != r.Reason {
				return fmt.Errorf("idempotency payload conflict")
			}
			result = existing
			return nil
		}
		var state database.CutoverState
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&state, 1).Error; err != nil {
			return err
		}
		fromIndex, toIndex := stageIndex(state.Stage), stageIndex(r.ToStage)
		if toIndex < 0 {
			return fmt.Errorf("unknown cutover stage %q", r.ToStage)
		}
		if !r.Rollback && toIndex != fromIndex+1 {
			return fmt.Errorf("illegal cutover transition %s -> %s", state.Stage, r.ToStage)
		}
		if r.Rollback && (toIndex < 0 || toIndex >= fromIndex) {
			return fmt.Errorf("rollback target must be an earlier reversible stage")
		}
		if r.ToStage == "legacy_removal_eligible" {
			return fmt.Errorf("legacy removal requires separate future irreversible approval and is not implemented")
		}
		required := requiredPrerequisites(r.ToStage)
		for _, key := range required {
			if err := s.verifyPrerequisite(ctx, tx, key, r); err != nil {
				return fmt.Errorf("prerequisite %s failed: %w", key, err)
			}
		}
		authority := "legacy"
		if toIndex >= stageIndex("new_paper") {
			authority = "new_paper"
		}
		if r.ToStage == "limited_live" {
			authority = "limited_live"
			var dep database.GovernanceDeployment
			if err := tx.First(&dep, "context_key=?", r.Stage07ContextKey).Error; err != nil {
				return fmt.Errorf("Stage 07 target deployment missing: %w", err)
			}
			if dep.State != "limited_live" || stage07.VerifyDeployment(tx, dep) != nil {
				return fmt.Errorf("Stage 07 exact limited-live deployment invalid")
			}
		}
		prereq, _ := json.Marshal(r.Prerequisites)
		now := s.now()
		digest, _, _ := hash(struct {
			Key, From, To, Principal, Reason, Flag string
			Prereq                                 json.RawMessage
			Rollback                               bool
		}{r.IdempotencyKey, state.Stage, r.ToStage, r.Principal, r.Reason, r.FlagSnapshotID, prereq, r.Rollback})
		result = database.CutoverTransition{ID: digest, IdempotencyKey: r.IdempotencyKey, FromStage: state.Stage, ToStage: r.ToStage, FromAuthority: state.Authority, ToAuthority: authority, FlagSnapshotID: r.FlagSnapshotID, Principal: r.Principal, Reason: r.Reason, PrerequisitesJSON: string(prereq), Stage07ContextKey: r.Stage07ContextKey, ContentDigest: digest, CreatedAt: now}
		if r.Rollback {
			previous := state.TransitionID
			result.RollbackOf = &previous
		}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		updates := map[string]any{"stage": r.ToStage, "authority": authority, "flag_snapshot_id": r.FlagSnapshotID, "transition_id": digest, "version": state.Version + 1, "updated_at": now}
		updated := tx.Model(&database.CutoverState{}).Where("id=1 AND version=?", state.Version).Updates(updates)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("concurrent cutover transition")
		}
		return nil
	})
	return result, err
}

func (s Service) verifyPrerequisite(ctx context.Context, tx *gorm.DB, key string, r TransitionRequest) error {
	switch key {
	case "schema_deployed":
		if !tx.Migrator().HasTable(&database.CutoverState{}) {
			return fmt.Errorf("Stage 08 schema is absent")
		}
	case "ledger_compare_clean", "ledger_reconciled":
		report, err := ledger.New(tx).Reconcile(ctx, ledger.DefaultAccountID, time.Time{})
		if err != nil {
			return err
		}
		if !report.Balanced {
			return fmt.Errorf("ledger is unreconciled")
		}
	case "parity_threshold_passed":
		check := s
		check.DB = tx
		aggregate, err := check.EvaluateParity(r.ParityPolicyID, r.ExpectedParityContexts)
		if err != nil {
			return err
		}
		if !aggregate.Accepted {
			return fmt.Errorf("%s", aggregate.Failure)
		}
	case "stage07_paper", "stage07_exact", "human_approved":
		var deployment database.GovernanceDeployment
		if err := tx.First(&deployment, "context_key=?", r.Stage07ContextKey).Error; err != nil {
			return err
		}
		if err := stage07.VerifyDeployment(tx, deployment); err != nil {
			return err
		}
		if key == "stage07_paper" && deployment.State != "paper" {
			return fmt.Errorf("deployment is %s", deployment.State)
		}
		if key == "stage07_exact" && deployment.State != "limited_live" {
			return fmt.Errorf("deployment is %s", deployment.State)
		}
		if key == "human_approved" {
			var transition database.GovernanceTransition
			if err := tx.First(&transition, "id=?", deployment.TransitionID).Error; err != nil {
				return err
			}
			if transition.ApprovalID == nil {
				return fmt.Errorf("Stage 07 approval is absent")
			}
		}
	case "paper_round_trip":
		var buys, sells int64
		tx.Model(&database.Fill{}).Where("execution_mode=? AND side=?", "paper", "buy").Count(&buys)
		tx.Model(&database.Fill{}).Where("execution_mode=? AND side=?", "paper", "sell").Count(&sells)
		if buys == 0 || sells == 0 {
			return fmt.Errorf("no reconciled paper buy/sell round trip")
		}
	case "restart_idempotency":
		var duplicates int64
		tx.Raw(`SELECT count(*) FROM (SELECT ledger_batch_id,count(*) c FROM fills GROUP BY ledger_batch_id HAVING count(*)>1) x`).Scan(&duplicates)
		if duplicates > 0 {
			return fmt.Errorf("duplicate fill batches found")
		}
	case "dataset_coverage":
		var manifest database.DatasetManifest
		if err := tx.Order("created_at desc").First(&manifest).Error; err != nil {
			return err
		}
		var incomplete int64
		tx.Model(&database.UniverseSnapshot{}).Where("dataset_manifest_id=? AND coverage_state<>?", manifest.ID, "complete").Count(&incomplete)
		if incomplete > 0 {
			return fmt.Errorf("incomplete universe snapshots")
		}
	case "backtest_reproduced":
		var count int64
		tx.Model(&database.BacktestJob{}).Where("status=? AND job_type IN ?", "completed", []string{"stage05_comparison", "stage07_validation"}).Count(&count)
		if count == 0 {
			return fmt.Errorf("canonical completed research job missing")
		}
	case "validation_passed":
		var count int64
		tx.Model(&database.ValidationEvidence{}).Where("status=?", "passed").Count(&count)
		if count == 0 {
			return fmt.Errorf("passed validation evidence missing")
		}
	case "observation_elapsed":
		var count int64
		tx.Model(&database.GovernanceMonitoringEvidence{}).Where("window_end<=?", s.now()).Count(&count)
		if count == 0 {
			return fmt.Errorf("elapsed monitoring evidence missing")
		}
	default:
		return fmt.Errorf("unknown prerequisite")
	}
	return nil
}
func stageIndex(stage string) int {
	for i, v := range stages {
		if v == stage {
			return i
		}
	}
	return -1
}
func requiredPrerequisites(stage string) []string {
	m := map[string][]string{"ledger_compare": {"schema_deployed"}, "shared_shadow": {"ledger_compare_clean"}, "parity_accepted": {"parity_threshold_passed"}, "new_paper": {"ledger_reconciled", "stage07_paper"}, "paper_observation": {"paper_round_trip", "restart_idempotency"}, "research_validation": {"dataset_coverage", "backtest_reproduced"}, "limited_live": {"observation_elapsed", "validation_passed", "human_approved", "stage07_exact"}}
	return m[stage]
}

type Status struct {
	SchemaVersion  string                         `json:"schema_version"`
	Status         string                         `json:"status"`
	CheckedAt      time.Time                      `json:"checked_at"`
	Flags          cutover.Flags                  `json:"flags"`
	FlagSnapshotID string                         `json:"flag_snapshot_id,omitempty"`
	Cutover        any                            `json:"cutover"`
	Ledger         any                            `json:"ledger"`
	Backtest       any                            `json:"backtest"`
	Parity         any                            `json:"parity"`
	Governance     any                            `json:"governance"`
	Data           any                            `json:"data"`
	Backup         any                            `json:"backup"`
	Incidents      []database.OperationalIncident `json:"incidents"`
	Diagnostics    []string                       `json:"diagnostics"`
}

func (s Service) Status(ctx context.Context) Status {
	now := s.now()
	out := Status{SchemaVersion: StatusSchemaVersion, Status: "degraded", CheckedAt: now, Flags: s.Flags, Diagnostics: []string{}, Incidents: []database.OperationalIncident{}}
	_, flagID, _ := s.Flags.Canonical()
	out.FlagSnapshotID = flagID
	var state database.CutoverState
	if err := s.DB.First(&state, 1).Error; err != nil {
		out.Diagnostics = append(out.Diagnostics, "cutover_state_missing")
	} else {
		out.Cutover = state
	}
	report, err := ledger.New(s.DB).Reconcile(ctx, ledger.DefaultAccountID, time.Time{})
	if err != nil {
		out.Ledger = map[string]any{"balanced": false, "error": err.Error()}
		out.Diagnostics = append(out.Diagnostics, "ledger_reconciliation_unavailable")
	} else {
		out.Ledger = map[string]any{"balanced": report.Balanced, "last_successful_check": report.AsOf, "actionable_issues": report.ActionableIssues}
		if !report.Balanced {
			out.Diagnostics = append(out.Diagnostics, "ledger_unreconciled")
		}
	}
	var job database.BacktestJob
	if err := s.DB.Order("created_at desc").First(&job).Error; err != nil {
		out.Backtest = map[string]any{"evidence": "missing"}
		out.Diagnostics = append(out.Diagnostics, "backtest_evidence_missing")
	} else {
		classification := "unknown"
		if job.DiagnosticJSON != nil {
			var d map[string]any
			_ = json.Unmarshal([]byte(*job.DiagnosticJSON), &d)
			if v, ok := d["classification"].(string); ok {
				classification = v
			}
		}
		out.Backtest = map[string]any{"job_id": job.ID, "status": job.Status, "classification": classification, "coverage_failed": classification == "coverage_failed", "zero_trades": classification == "strategy_zero_trades" || classification == "gating_zero_trades", "dataset_manifest_id": job.DatasetManifestID}
	}
	var parityRows []database.ParityObservation
	s.DB.Order("observed_at desc").Limit(20).Find(&parityRows)
	var parityTotal, parityUnexplained int64
	s.DB.Model(&database.ParityObservation{}).Count(&parityTotal)
	s.DB.Model(&database.ParityObservation{}).Where("classification=?", "unexplained").Count(&parityUnexplained)
	unexplainedSamples := []database.ParityObservation{}
	for _, row := range parityRows {
		if row.Classification == "unexplained" && len(unexplainedSamples) < 10 {
			unexplainedSamples = append(unexplainedSamples, row)
		}
	}
	out.Parity = map[string]any{"total": parityTotal, "unexplained": parityUnexplained, "bounded_unexplained_samples": unexplainedSamples}
	if s.Flags.DualRun == "observe" && parityTotal == 0 {
		out.Diagnostics = append(out.Diagnostics, "parity_evidence_missing")
	}
	var manifest database.DatasetManifest
	if err := s.DB.Order("created_at desc").First(&manifest).Error; err != nil {
		out.Data = map[string]any{"dataset": "missing", "universe": "missing"}
		out.Diagnostics = append(out.Diagnostics, "dataset_manifest_missing")
	} else {
		var universe database.UniverseSnapshot
		uerr := s.DB.Where("dataset_manifest_id=?", manifest.ID).Order("snapshot_time desc").First(&universe).Error
		out.Data = map[string]any{"dataset_manifest_id": manifest.ID, "dataset_version": manifest.DatasetVersion, "universe_policy": universe.PolicyVersion, "universe_coverage": universe.CoverageState, "universe_observed_at": universe.SnapshotTime}
		if uerr != nil || universe.CoverageState != "complete" {
			out.Diagnostics = append(out.Diagnostics, "universe_evidence_missing_or_incomplete")
		} else if s.Flags.PointInTime == "authoritative" && now.Sub(universe.SnapshotTime) > 24*time.Hour {
			out.Diagnostics = append(out.Diagnostics, "universe_evidence_stale")
		}
	}
	var deployment database.GovernanceDeployment
	if err := s.DB.Order("updated_at desc").First(&deployment).Error; err != nil {
		out.Governance = map[string]any{"state": "missing", "approval_state": "missing"}
		if s.Flags.CapitalEnabled() {
			out.Diagnostics = append(out.Diagnostics, "governance_evidence_missing")
		}
	} else {
		verify := stage07.VerifyDeployment(s.DB, deployment)
		out.Governance = map[string]any{"context_key": deployment.ContextKey, "state": deployment.State, "strategy_or_model_version": deployment.ArtifactVersion, "policy_version": deployment.PolicyVersion, "failed_gates": func() []string {
			if verify != nil {
				return []string{verify.Error()}
			}
			return []string{}
		}()}
	}
	var backup database.BackupVerification
	if err := s.DB.Order("verified_at desc").First(&backup).Error; err != nil {
		out.Backup = map[string]any{"status": "unverified"}
	} else {
		out.Backup = backup
	}
	s.DB.Where("state <> ?", "resolved").Order("last_seen_at desc").Limit(100).Find(&out.Incidents)
	if len(out.Diagnostics) == 0 {
		out.Status = "ok"
	}
	sort.Strings(out.Diagnostics)
	return out
}

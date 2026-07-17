package operations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
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
type StructuredLogDispatcher struct{}

func (StructuredLogDispatcher) Dispatch(_ context.Context, incident database.OperationalIncident) error {
	log.Printf("operational_alert type=%q severity=%q incident_id=%q occurrences=%d", incident.Type, incident.Severity, incident.ID, incident.Occurrences)
	return nil
}

type Service struct {
	DB       *gorm.DB
	Flags    cutover.Flags
	Alerts   AlertDispatcher
	Now      func() time.Time
	ReadOnly bool
}

func New(db *gorm.DB, flags cutover.Flags) Service {
	return Service{DB: db, Flags: flags, Alerts: StructuredLogDispatcher{}, Now: func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }}
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
	var state database.CutoverState
	bootstrapped := false
	stateErr := s.DB.WithContext(ctx).First(&state, 1).Error
	if stateErr == gorm.ErrRecordNotFound {
		if s.ReadOnly {
			return row, fmt.Errorf("read-only verification requires an existing cutover state")
		}
		bootstrapped = true
		if digest != mustFlagDigest(cutover.SafeFlags()) {
			return row, fmt.Errorf("first install requires the explicit legacy-safe Stage 08 flag envelope")
		}
		if err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			envelopeJSON, envelopeDigest := authorityEnvelope("schema_legacy", "legacy", row.ID, row.ContentDigest, "", "")
			state = database.CutoverState{ID: 1, Stage: "schema_legacy", Authority: "legacy", FlagSnapshotID: row.ID, FlagDigest: row.ContentDigest, AuthorityJSON: envelopeJSON, AuthorityDigest: envelopeDigest, TransitionID: strings.Repeat("0", 64), Version: 1, UpdatedAt: s.now()}
			return tx.Create(&state).Error
		}); err != nil {
			return row, err
		}
	} else if stateErr != nil {
		return row, stateErr
	} else {
		if state.FlagSnapshotID != digest {
			return row, fmt.Errorf("configured Stage 08 flags %s do not match locked cutover snapshot %s", digest, state.FlagSnapshotID)
		}
		var persisted database.Stage08FlagSnapshot
		if err := s.DB.WithContext(ctx).First(&persisted, "id=?", state.FlagSnapshotID).Error; err != nil {
			return row, fmt.Errorf("locked flag snapshot missing: %w", err)
		}
		verified, err := verifyFlagSnapshot(persisted)
		if err != nil || verified != s.Flags {
			return row, fmt.Errorf("locked flag snapshot corrupt or does not match runtime: %w", err)
		}
		row = persisted
		if state.FlagDigest == "" || state.AuthorityDigest == "" || state.AuthorityJSON == "{}" {
			if s.ReadOnly {
				return row, fmt.Errorf("read-only verification refuses incomplete cutover integrity fields")
			}
			if state.Stage != "schema_legacy" || state.Authority != "legacy" || state.TransitionID != strings.Repeat("0", 64) || persisted.ID != mustFlagDigest(cutover.SafeFlags()) {
				return row, fmt.Errorf("pre-integrity cutover state is not the deterministic legacy bootstrap and requires explicit recovery")
			}
			envelope, envelopeDigest := authorityEnvelope(state.Stage, state.Authority, persisted.ID, persisted.ContentDigest, "", "")
			if err := s.DB.WithContext(ctx).Model(&database.CutoverState{}).Where("id=1 AND version=?", state.Version).Updates(map[string]any{"flag_digest": persisted.ContentDigest, "authority_json": envelope, "authority_digest": envelopeDigest}).Error; err != nil {
				return row, err
			}
			state.FlagDigest, state.AuthorityJSON, state.AuthorityDigest = persisted.ContentDigest, envelope, envelopeDigest
		}
		if state.FlagDigest != persisted.ContentDigest {
			return row, fmt.Errorf("cutover flag digest mismatch")
		}
		expectedJSON, expectedDigest := authorityEnvelope(state.Stage, state.Authority, persisted.ID, persisted.ContentDigest, stageContextFor(state.Stage, verified), "")
		if state.AuthorityDigest != expectedDigest || canonicalJSONEqual(state.AuthorityJSON, expectedJSON) == false {
			return row, fmt.Errorf("cutover authority envelope integrity mismatch")
		}
	}
	var engineSetting database.Setting
	if err := s.DB.First(&engineSetting, "key=?", "trading_engine_mode").Error; err != nil {
		if bootstrapped && err == gorm.ErrRecordNotFound {
			if err := cutover.ActivateVerified(s.Flags, row.ID, state.Authority); err != nil {
				return row, err
			}
			return row, nil
		}
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
	expectedEngine := "legacy"
	if state.Stage == "shared_shadow" || state.Stage == "parity_accepted" {
		expectedEngine = "shadow_compare"
	}
	if stageIndex(state.Stage) >= stageIndex("new_paper") {
		expectedEngine = "shared"
	}
	if engineSetting.Value != expectedEngine {
		return row, fmt.Errorf("persisted trading_engine_mode %q conflicts with cutover stage %q (required %q)", engineSetting.Value, state.Stage, expectedEngine)
	}
	if expectedEngine != "legacy" && fallbackSetting.Value != "disabled" {
		return row, fmt.Errorf("new authority requires disabled legacy fallback; rollback is an explicit cutover transition")
	}
	if state.Stage == "schema_legacy" && s.Flags.CapitalEnabled() {
		return row, fmt.Errorf("schema_legacy cannot activate paper or live authority")
	}
	if state.Authority != authorityForStage(state.Stage) {
		return row, fmt.Errorf("cutover stage/authority mismatch")
	}
	if state.Authority != "legacy" || s.Flags.LedgerAuthority == "authoritative" {
		report, err := ledger.New(s.DB).Reconcile(ctx, ledger.DefaultAccountID, time.Time{})
		if err != nil {
			return row, fmt.Errorf("ledger authority reconciliation: %w", err)
		}
		if !report.Balanced {
			return row, fmt.Errorf("ledger authority blocked: %s", strings.Join(report.ActionableIssues, "; "))
		}
		if !s.ReadOnly {
			if err := s.persistReconciliation(ctx, state, row.ID, report); err != nil {
				return row, err
			}
		}
	}
	if state.Stage == "new_paper" || state.Stage == "paper_observation" || s.Flags.IsLive() {
		var deployment database.GovernanceDeployment
		if err := s.DB.First(&deployment, "context_key=?", s.Flags.Stage07Context).Error; err != nil {
			return row, fmt.Errorf("Stage 07 exact deployment missing: %w", err)
		}
		if err := stage07.VerifyDeployment(s.DB, deployment); err != nil {
			return row, fmt.Errorf("Stage 07 deployment invalid: %w", err)
		}
		requiredState := s.Flags.SharedEngine
		if state.Stage == "new_paper" || state.Stage == "paper_observation" {
			requiredState = "paper"
		}
		if deployment.State != requiredState {
			return row, fmt.Errorf("Stage 07 deployment state %q does not match live mode %q", deployment.State, s.Flags.SharedEngine)
		}
	}
	if err := s.reconcileCutoverStartup(row.ID); err != nil {
		return row, err
	}
	if err := cutover.ActivateVerified(s.Flags, row.ID, state.Authority); err != nil {
		return row, err
	}
	return row, nil
}
func (s Service) DeclareFlagSnapshot(ctx context.Context, flags cutover.Flags, principal string) (database.Stage08FlagSnapshot, error) {
	var row database.Stage08FlagSnapshot
	if principal == "" {
		return row, fmt.Errorf("trusted operations principal required")
	}
	if err := flags.Validate(); err != nil {
		return row, err
	}
	content, digest, err := flags.Canonical()
	if err != nil {
		return row, err
	}
	row = database.Stage08FlagSnapshot{ID: digest, SchemaVersion: cutover.FlagSchemaVersion, ContentJSON: string(content), ContentDigest: digest, CreatedAt: s.now()}
	err = s.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
	return row, err
}

func (s Service) reconcileCutoverStartup(flagID string) error {
	var state database.CutoverState
	err := s.DB.First(&state, 1).Error
	if err == gorm.ErrRecordNotFound {
		return fmt.Errorf("cutover bootstrap state missing")
	}
	if err != nil {
		return err
	}
	var transition database.CutoverTransition
	if state.TransitionID != strings.Repeat("0", 64) {
		if err := s.DB.First(&transition, "id=?", state.TransitionID).Error; err != nil || transition.ToStage != state.Stage || transition.ToAuthority != state.Authority || transition.FlagSnapshotID != state.FlagSnapshotID || transition.TargetEnvelopeDigest != state.AuthorityDigest {
			return fmt.Errorf("cutover state does not match immutable transition")
		}
	}
	return nil
}

func mustFlagDigest(f cutover.Flags) string { _, d, _ := f.Canonical(); return d }
func authorityForStage(stage string) string {
	if stage == "limited_live" {
		return "limited_live"
	}
	if stageIndex(stage) >= stageIndex("new_paper") {
		return "new_paper"
	}
	return "legacy"
}
func stageContextFor(stage string, f cutover.Flags) string {
	if stageIndex(stage) >= stageIndex("new_paper") {
		return f.Stage07Context
	}
	return ""
}
func authorityEnvelope(stage, authority, flagID, flagDigest, context, deployment string) (string, string) {
	v := struct{ Schema, Stage, Authority, FlagID, FlagDigest, Stage07Context, Stage07Deployment string }{"stage08-authority-envelope-v1", stage, authority, flagID, flagDigest, context, deployment}
	d, b, _ := hash(v)
	return string(b), d
}
func canonicalJSONEqual(a, b string) bool {
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return false
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return string(xb) == string(yb)
}
func verifyFlagSnapshot(row database.Stage08FlagSnapshot) (cutover.Flags, error) {
	var f cutover.Flags
	if row.ID == "" || row.ID != row.ContentDigest || json.Unmarshal([]byte(row.ContentJSON), &f) != nil {
		return f, fmt.Errorf("invalid flag snapshot encoding")
	}
	if err := f.Validate(); err != nil {
		return f, err
	}
	canonical, digest, err := f.Canonical()
	if err != nil || digest != row.ID || !canonicalJSONEqual(string(canonical), row.ContentJSON) {
		return f, fmt.Errorf("flag snapshot digest mismatch")
	}
	return f, nil
}
func (s Service) persistReconciliation(ctx context.Context, state database.CutoverState, flagID string, report ledger.ReconciliationReport) error {
	digest, payload, err := hash(report)
	if err != nil {
		return err
	}
	id, _, _ := hash(struct {
		Flag, Transition, Digest string
		At                       time.Time
	}{flagID, state.TransitionID, digest, report.AsOf})
	row := database.ReconciliationEvidence{ID: id, FlagSnapshotID: flagID, CutoverTransitionID: state.TransitionID, Balanced: report.Balanced, CanonicalDigest: digest, ReportJSON: string(payload), CheckedAt: report.AsOf, ContentDigest: id}
	return s.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
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
		if shouldDispatch {
			row.CooldownUntil = now.Add(input.Cooldown)
		}
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
		updateErr := s.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SET LOCAL trading_bot.operational_incident_write = 'on'").Error; err != nil {
				return err
			}
			return tx.Model(&database.OperationalIncident{}).Where("id=?", row.ID).Updates(updates).Error
		})
		if updateErr != nil {
			return row, fmt.Errorf("incident persisted but alert delivery state persistence failed: %w", updateErr)
		}
		row.LastDeliveryAttempt = &attempt
		if deliveryErr != nil {
			row.LastDeliveryState = "failed"
			row.LastDeliveryError = deliveryErr.Error()
			return row, fmt.Errorf("incident persisted; alert dispatch failed: %w", deliveryErr)
		}
		row.LastDeliveryState = "delivered"
		row.LastDeliveryError = ""
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

func verifyParityPolicy(row database.ParityAcceptancePolicy) error {
	var expected []cutover.ExpectedReason
	if err := json.Unmarshal([]byte(row.ExpectedReasonsJSON), &expected); err != nil {
		return fmt.Errorf("parity policy expected reasons corrupt: %w", err)
	}
	expectedJSON, _ := json.Marshal(expected)
	canonical := struct {
		Schema, Name                                                                                                                                              string
		MinimumSamples, MinimumCoverageBPS, MaxActionRateBPS, MaxQuantityRateBPS, MaxReasonRateBPS, MaxVersionRateBPS, QuantityToleranceBPS, NotionalToleranceBPS int64
		Expected                                                                                                                                                  json.RawMessage
		Principal                                                                                                                                                 string
		At                                                                                                                                                        time.Time
	}{row.SchemaVersion, row.Name, row.MinimumSamples, row.MinimumCoverageBPS, row.MaxActionRateBPS, row.MaxQuantityRateBPS, row.MaxReasonRateBPS, row.MaxVersionRateBPS, row.QuantityToleranceBPS, row.NotionalToleranceBPS, expectedJSON, row.DeclaredBy, row.DeclaredAt.UTC()}
	digest, _, err := hash(canonical)
	if err != nil || row.ID != digest || row.ContentDigest != digest {
		return fmt.Errorf("parity policy content digest mismatch")
	}
	return nil
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
	return database.ParityObservation{}, fmt.Errorf("unbound parity persistence is forbidden; declare an immutable server-derived population")
}

type ParityBinding struct{ PopulationID string }

// PostgreSQL timestamptz stores microsecond precision. Content identities that
// survive a write/read cycle must be computed from that same representation.
func postgresTime(value time.Time) time.Time {
	return time.UnixMicro(value.UTC().UnixMicro()).UTC()
}

func verifyParityPopulation(db *gorm.DB, row database.ParityPopulation) error {
	var ids []string
	if err := json.Unmarshal([]byte(row.ContextDigestsJSON), &ids); err != nil || len(ids) == 0 || int64(len(ids)) != row.ExpectedContexts {
		return fmt.Errorf("parity population context set is invalid")
	}
	canonicalIDs := append([]string(nil), ids...)
	sort.Strings(canonicalIDs)
	for i, id := range canonicalIDs {
		if len(id) != 64 || (i > 0 && id == canonicalIDs[i-1]) {
			return fmt.Errorf("parity population context set is invalid")
		}
	}
	canonicalJSON, _ := json.Marshal(canonicalIDs)
	var policy database.ParityAcceptancePolicy
	if err := db.First(&policy, "id=?", row.PolicyID).Error; err != nil || verifyParityPolicy(policy) != nil || policy.ContentDigest != row.PolicyDigest {
		return fmt.Errorf("parity population policy binding mismatch")
	}
	var snapshot database.Stage08FlagSnapshot
	if err := db.First(&snapshot, "id=?", row.FlagSnapshotID).Error; err != nil || snapshot.ContentDigest != row.FlagSnapshotDigest {
		return fmt.Errorf("parity population flag binding mismatch")
	}
	if _, err := verifyFlagSnapshot(snapshot); err != nil {
		return err
	}
	canonical := struct {
		Schema, Pair, Policy, PolicyDigest, Flag, FlagDigest, Attempt, Dataset, Universe string
		Start, End                                                                       time.Time
		IDs                                                                              json.RawMessage
	}{"stage08-parity-population-v1", row.PairKey, row.PolicyID, row.PolicyDigest, row.FlagSnapshotID, row.FlagSnapshotDigest, row.CutoverAttemptID, row.DatasetVersion, row.UniverseVersion, postgresTime(row.WindowStart), postgresTime(row.WindowEnd), canonicalJSON}
	digest, _, err := hash(canonical)
	if err != nil || row.ID != digest || row.ContentDigest != digest {
		return fmt.Errorf("parity population content digest mismatch")
	}
	return nil
}

func (s Service) BeginParityPopulation(ctx context.Context, pairKey, policyID, flagID string, contextIDs []string, windowStart, windowEnd time.Time, datasetVersion, universeVersion string) (database.ParityPopulation, cutover.ComparisonPolicy, error) {
	var out database.ParityPopulation
	if pairKey == "" || policyID == "" || flagID == "" || len(contextIDs) == 0 || len(contextIDs) > 10000 || !windowEnd.After(windowStart) {
		return out, cutover.ComparisonPolicy{}, fmt.Errorf("bounded non-empty parity population and window required")
	}
	ids := append([]string(nil), contextIDs...)
	sort.Strings(ids)
	for i, id := range ids {
		if len(id) != 64 || (i > 0 && id == ids[i-1]) {
			return out, cutover.ComparisonPolicy{}, fmt.Errorf("invalid or duplicate server-derived context identity")
		}
	}
	var p database.ParityAcceptancePolicy
	if err := s.DB.WithContext(ctx).First(&p, "id=?", policyID).Error; err != nil {
		return out, cutover.ComparisonPolicy{}, err
	}
	if err := verifyParityPolicy(p); err != nil {
		return out, cutover.ComparisonPolicy{}, err
	}
	var snapshot database.Stage08FlagSnapshot
	if err := s.DB.WithContext(ctx).First(&snapshot, "id=?", flagID).Error; err != nil {
		return out, cutover.ComparisonPolicy{}, err
	}
	if _, err := verifyFlagSnapshot(snapshot); err != nil {
		return out, cutover.ComparisonPolicy{}, err
	}
	var state database.CutoverState
	if err := s.DB.WithContext(ctx).First(&state, 1).Error; err != nil {
		return out, cutover.ComparisonPolicy{}, err
	}
	if state.FlagSnapshotID != flagID {
		return out, cutover.ComparisonPolicy{}, fmt.Errorf("parity flag snapshot is not active")
	}
	idsJSON, _ := json.Marshal(ids)
	canonical := struct {
		Schema, Pair, Policy, PolicyDigest, Flag, FlagDigest, Attempt, Dataset, Universe string
		Start, End                                                                       time.Time
		IDs                                                                              json.RawMessage
	}{"stage08-parity-population-v1", pairKey, p.ID, p.ContentDigest, flagID, snapshot.ContentDigest, state.TransitionID, datasetVersion, universeVersion, postgresTime(windowStart), postgresTime(windowEnd), idsJSON}
	id, _, _ := hash(canonical)
	out = database.ParityPopulation{ID: id, PairKey: pairKey, PolicyID: p.ID, PolicyDigest: p.ContentDigest, FlagSnapshotID: flagID, FlagSnapshotDigest: snapshot.ContentDigest, CutoverAttemptID: state.TransitionID, WindowStart: postgresTime(windowStart), WindowEnd: postgresTime(windowEnd), ExpectedContexts: int64(len(ids)), ContextDigestsJSON: string(idsJSON), DatasetVersion: datasetVersion, UniverseVersion: universeVersion, ContentDigest: id, CreatedAt: s.now()}
	if err := s.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&out).Error; err != nil {
		return out, cutover.ComparisonPolicy{}, err
	}
	var expected []cutover.ExpectedReason
	if err := json.Unmarshal([]byte(p.ExpectedReasonsJSON), &expected); err != nil {
		return out, cutover.ComparisonPolicy{}, fmt.Errorf("parity policy expected reasons corrupt: %w", err)
	}
	return out, cutover.ComparisonPolicy{QuantityToleranceBPS: p.QuantityToleranceBPS, NotionalToleranceBPS: p.NotionalToleranceBPS, Expected: expected}, nil
}

func (s Service) PersistParityBound(ctx context.Context, binding ParityBinding, c cutover.Comparison, at time.Time) (database.ParityObservation, error) {
	var population database.ParityPopulation
	if err := s.DB.WithContext(ctx).First(&population, "id=?", binding.PopulationID).Error; err != nil {
		return database.ParityObservation{}, err
	}
	if err := verifyParityPopulation(s.DB.WithContext(ctx), population); err != nil {
		return database.ParityObservation{}, err
	}
	var acceptance database.ParityAcceptancePolicy
	if err := s.DB.WithContext(ctx).First(&acceptance, "id=?", population.PolicyID).Error; err != nil {
		return database.ParityObservation{}, err
	}
	var expected []cutover.ExpectedReason
	if err := json.Unmarshal([]byte(acceptance.ExpectedReasonsJSON), &expected); err != nil {
		return database.ParityObservation{}, fmt.Errorf("parity policy expected reasons corrupt: %w", err)
	}
	boundPolicy := cutover.ComparisonPolicy{QuantityToleranceBPS: acceptance.QuantityToleranceBPS, NotionalToleranceBPS: acceptance.NotionalToleranceBPS, Expected: expected}
	if err := cutover.VerifyComparisonWithPolicy(c, boundPolicy); err != nil {
		return database.ParityObservation{}, err
	}
	var allowed []string
	if err := json.Unmarshal([]byte(population.ContextDigestsJSON), &allowed); err != nil {
		return database.ParityObservation{}, err
	}
	i := sort.SearchStrings(allowed, c.ContextID)
	if i >= len(allowed) || allowed[i] != c.ContextID {
		return database.ParityObservation{}, fmt.Errorf("observation context is outside immutable population")
	}
	pairKey, flagID := population.PairKey, population.FlagSnapshotID
	codes, _ := json.Marshal(c.DivergenceCodes)
	reasons, _ := json.Marshal(c.ExpectedReasons)
	sample, _ := json.Marshal(struct{ Legacy, Candidate cutover.DecisionOutcome }{c.Legacy, c.Candidate})
	boundID, _, _ := hash(struct{ Population, Comparison string }{population.ID, c.ContentDigest})
	row := database.ParityObservation{ID: boundID, ContextID: c.ContextID, PairKey: pairKey, SchemaVersion: cutover.ParitySchemaVersion, FlagSnapshotID: flagID, FlagSnapshotDigest: population.FlagSnapshotDigest, PolicyID: population.PolicyID, PolicyDigest: population.PolicyDigest, PopulationID: population.ID, CutoverAttemptID: population.CutoverAttemptID, LegacyDigest: c.LegacyDigest, CandidateDigest: c.CandidateDigest, Classification: c.Classification, DivergenceCodesJSON: string(codes), ExpectedPolicyReasons: string(reasons), CompactSampleJSON: string(sample), ContentDigest: boundID, ObservedAt: at.UTC()}
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
func (s Service) EvaluateParity(populationID string) (ParityAggregate, error) {
	var population database.ParityPopulation
	if err := s.DB.First(&population, "id=?", populationID).Error; err != nil {
		return ParityAggregate{}, err
	}
	if err := verifyParityPopulation(s.DB, population); err != nil {
		return ParityAggregate{}, err
	}
	var p database.ParityAcceptancePolicy
	if err := s.DB.First(&p, "id=?", population.PolicyID).Error; err != nil {
		return ParityAggregate{}, err
	}
	if err := verifyParityPolicy(p); err != nil {
		return ParityAggregate{}, err
	}
	var rows []database.ParityObservation
	if err := s.DB.Where("population_id=? AND policy_id=? AND flag_snapshot_id=? AND cutover_attempt_id=?", population.ID, population.PolicyID, population.FlagSnapshotID, population.CutoverAttemptID).Order("context_id").Limit(10000).Find(&rows).Error; err != nil {
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
	if population.ExpectedContexts > 0 {
		a.CoverageBPS = a.Total * 10000 / population.ExpectedContexts
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
	ParityPopulationID     string          `json:"parity_population_id"`
	EvidenceIDs            []string        `json:"evidence_ids,omitempty"`
	ExpectedParityContexts int64           `json:"expected_parity_contexts,omitempty"` // rejected legacy caller denominator
	Prerequisites          map[string]bool `json:"prerequisites,omitempty"`
	Rollback               bool            `json:"rollback"`
}

type PrerequisiteEvidenceRequest struct {
	EvidenceType        string         `json:"evidence_type"`
	TargetStage         string         `json:"target_stage"`
	ContextKey          string         `json:"context_key"`
	FlagSnapshotID      string         `json:"flag_snapshot_id"`
	ParityPolicyID      string         `json:"parity_policy_id"`
	DatasetVersion      string         `json:"dataset_version"`
	UniverseVersion     string         `json:"universe_version"`
	Stage07DeploymentID string         `json:"stage07_deployment_id"`
	WindowStart         time.Time      `json:"window_start"`
	WindowEnd           time.Time      `json:"window_end"`
	Payload             map[string]any `json:"payload"`
}

func (s Service) DeclarePrerequisiteEvidence(ctx context.Context, r PrerequisiteEvidenceRequest, principal string) (database.CutoverPrerequisiteEvidence, error) {
	var out database.CutoverPrerequisiteEvidence
	if principal == "" || r.EvidenceType == "" || stageIndex(r.TargetStage) < 0 || r.FlagSnapshotID == "" || !r.WindowEnd.After(r.WindowStart) || r.WindowEnd.After(s.now()) {
		return out, fmt.Errorf("trusted principal, known target, snapshot, and completed evidence window required")
	}
	payload, err := json.Marshal(r.Payload)
	if err != nil || len(payload) > 32<<10 {
		return out, fmt.Errorf("evidence payload invalid or exceeds 32KiB")
	}
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var state database.CutoverState
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).First(&state, 1).Error; err != nil {
			return err
		}
		var snap database.Stage08FlagSnapshot
		if err := tx.First(&snap, "id=?", r.FlagSnapshotID).Error; err != nil {
			return err
		}
		if _, err := verifyFlagSnapshot(snap); err != nil {
			return err
		}
		canonical := struct {
			Schema, Type, Source, Target, Context, Flag, FlagDigest, Policy, Dataset, Universe, Deployment, Principal string
			Start, End                                                                                                time.Time
			Payload                                                                                                   json.RawMessage
		}{"stage08-prerequisite-evidence-v1", r.EvidenceType, state.Stage, r.TargetStage, r.ContextKey, r.FlagSnapshotID, snap.ContentDigest, r.ParityPolicyID, r.DatasetVersion, r.UniverseVersion, r.Stage07DeploymentID, principal, r.WindowStart.UTC(), r.WindowEnd.UTC(), payload}
		id, _, _ := hash(canonical)
		out = database.CutoverPrerequisiteEvidence{ID: id, EvidenceType: r.EvidenceType, SourceStage: state.Stage, TargetStage: r.TargetStage, ContextKey: r.ContextKey, FlagSnapshotID: r.FlagSnapshotID, ParityPolicyID: r.ParityPolicyID, DatasetVersion: r.DatasetVersion, UniverseVersion: r.UniverseVersion, Stage07DeploymentID: r.Stage07DeploymentID, WindowStart: r.WindowStart.UTC(), WindowEnd: r.WindowEnd.UTC(), PayloadJSON: string(payload), ContentDigest: id, CreatedBy: principal, CreatedAt: s.now()}
		return tx.Create(&out).Error
	})
	return out, err
}
func verifyPrerequisiteEvidenceIntegrity(db *gorm.DB, row database.CutoverPrerequisiteEvidence) error {
	var payload any
	if json.Unmarshal([]byte(row.PayloadJSON), &payload) != nil {
		return fmt.Errorf("evidence payload corrupt")
	}
	canonicalPayload, _ := json.Marshal(payload)
	canonical := struct {
		Schema, Type, Source, Target, Context, Flag, FlagDigest, Policy, Dataset, Universe, Deployment, Principal string
		Start, End                                                                                                time.Time
		Payload                                                                                                   json.RawMessage
	}{"stage08-prerequisite-evidence-v1", row.EvidenceType, row.SourceStage, row.TargetStage, row.ContextKey, row.FlagSnapshotID, "", row.ParityPolicyID, row.DatasetVersion, row.UniverseVersion, row.Stage07DeploymentID, row.CreatedBy, row.WindowStart.UTC(), row.WindowEnd.UTC(), canonicalPayload}
	var snapshot database.Stage08FlagSnapshot
	if db == nil {
		return fmt.Errorf("database unavailable")
	}
	if err := db.First(&snapshot, "id=?", row.FlagSnapshotID).Error; err != nil {
		return err
	}
	canonical.FlagDigest = snapshot.ContentDigest
	digest, _, _ := hash(canonical)
	if digest != row.ID || row.ContentDigest != row.ID {
		return fmt.Errorf("evidence content digest mismatch")
	}
	return nil
}

func (s Service) TransitionCutover(ctx context.Context, r TransitionRequest) (database.CutoverTransition, error) {
	if r.IdempotencyKey == "" || r.Principal == "" || r.Reason == "" {
		return database.CutoverTransition{}, fmt.Errorf("idempotency key, trusted principal, and reason required")
	}
	var result database.CutoverTransition
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var state database.CutoverState
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&state, 1).Error; err != nil {
			return err
		}
		if r.ExpectedParityContexts != 0 {
			return fmt.Errorf("caller-provided parity denominator is forbidden")
		}
		if len(r.Prerequisites) > 0 {
			return fmt.Errorf("caller prerequisite summaries are forbidden; use immutable evidence IDs")
		}
		var existing database.CutoverTransition
		if e := tx.First(&existing, "idempotency_key=?", r.IdempotencyKey).Error; e == nil {
			var envelope struct{ FlagID, FlagDigest string }
			if json.Unmarshal([]byte(existing.SourceEnvelopeJSON), &envelope) != nil {
				return fmt.Errorf("stored transition source envelope corrupt")
			}
			source := database.CutoverState{Stage: existing.FromStage, Authority: existing.FromAuthority, FlagSnapshotID: envelope.FlagID, FlagDigest: envelope.FlagDigest, AuthorityDigest: existing.SourceEnvelopeDigest, Version: existing.SourceStateVersion}
			replayDigest, _, err := s.transitionRequestDigest(tx, source, r)
			if err != nil {
				return err
			}
			if existing.RequestDigest != replayDigest {
				return fmt.Errorf("idempotency payload integrity conflict")
			}
			result = existing
			return nil
		} else if e != gorm.ErrRecordNotFound {
			return e
		}
		requestDigest, _, err := s.transitionRequestDigest(tx, state, r)
		if err != nil {
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
		if r.Rollback && r.ToStage != "schema_legacy" {
			var prior database.CutoverTransition
			if err := tx.Where("to_stage=?", r.ToStage).Order("created_at desc").First(&prior).Error; err != nil {
				return fmt.Errorf("rollback target has no immutable prior authority envelope: %w", err)
			}
			if prior.FlagSnapshotID != r.FlagSnapshotID {
				return fmt.Errorf("rollback must restore exact prior flag snapshot %s", prior.FlagSnapshotID)
			}
		}
		if r.ToStage == "legacy_removal_eligible" {
			return fmt.Errorf("legacy removal requires separate future irreversible approval and is not implemented")
		}
		if (r.ToStage == "shared_shadow" || r.ToStage == "parity_accepted") && r.ParityPolicyID == "" {
			return fmt.Errorf("shadow/parity stages require an exact immutable parity policy")
		}
		var targetSnapshot database.Stage08FlagSnapshot
		if err := tx.First(&targetSnapshot, "id=?", r.FlagSnapshotID).Error; err != nil {
			return fmt.Errorf("target flag snapshot missing: %w", err)
		}
		targetFlags, err := verifyFlagSnapshot(targetSnapshot)
		if err != nil {
			return err
		}
		if err := validateFlagsForStage(targetFlags, r.ToStage); err != nil {
			return err
		}
		if stageIndex(r.ToStage) >= stageIndex("new_paper") && targetFlags.Stage07Context != r.Stage07ContextKey {
			return fmt.Errorf("target flag snapshot Stage 07 context does not match transition target")
		}
		required := requiredPrerequisites(r.ToStage)
		for _, key := range required {
			if err := s.verifyPrerequisite(ctx, tx, key, r); err != nil {
				return fmt.Errorf("prerequisite %s failed: %w", key, err)
			}
		}
		authority := authorityForStage(r.ToStage)
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
		prereq, _ := json.Marshal(r.EvidenceIDs)
		now := s.now()
		sourceJSON, sourceDigest := state.AuthorityJSON, state.AuthorityDigest
		if sourceDigest == "" {
			sourceJSON, sourceDigest = authorityEnvelope(state.Stage, state.Authority, state.FlagSnapshotID, state.FlagDigest, "", "")
		}
		targetJSON, targetDigest := authorityEnvelope(r.ToStage, authority, targetSnapshot.ID, targetSnapshot.ContentDigest, targetFlags.Stage07Context, "")
		evidenceDigest, _, _ := hash(r.EvidenceIDs)
		digest := requestDigest
		result = database.CutoverTransition{ID: digest, IdempotencyKey: r.IdempotencyKey, FromStage: state.Stage, ToStage: r.ToStage, FromAuthority: state.Authority, ToAuthority: authority, FlagSnapshotID: r.FlagSnapshotID, FlagSnapshotDigest: targetSnapshot.ContentDigest, SourceStateVersion: state.Version, SourceEnvelopeJSON: sourceJSON, SourceEnvelopeDigest: sourceDigest, TargetEnvelopeJSON: targetJSON, TargetEnvelopeDigest: targetDigest, RequestDigest: requestDigest, ParityPolicyID: r.ParityPolicyID, EvidenceDigest: evidenceDigest, Principal: r.Principal, Reason: r.Reason, PrerequisitesJSON: string(prereq), Stage07ContextKey: r.Stage07ContextKey, ContentDigest: digest, CreatedAt: now}
		if r.Rollback {
			previous := state.TransitionID
			result.RollbackOf = &previous
		}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		updates := map[string]any{"stage": r.ToStage, "authority": authority, "flag_snapshot_id": r.FlagSnapshotID, "flag_digest": targetSnapshot.ContentDigest, "authority_json": targetJSON, "authority_digest": targetDigest, "transition_id": digest, "version": state.Version + 1, "updated_at": now}
		updated := tx.Model(&database.CutoverState{}).Where("id=1 AND version=?", state.Version).Updates(updates)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("concurrent cutover transition")
		}
		targetEngine := "legacy"
		if r.ToStage == "shared_shadow" || r.ToStage == "parity_accepted" {
			targetEngine = "shadow_compare"
		}
		if stageIndex(r.ToStage) >= stageIndex("new_paper") {
			targetEngine = "shared"
		}
		if result := tx.Model(&database.Setting{}).Where("key=?", "trading_engine_mode").Update("value", targetEngine); result.Error != nil || result.RowsAffected != 1 {
			if result.Error != nil {
				return result.Error
			}
			return fmt.Errorf("trading_engine_mode setting missing during atomic authority transition")
		}
		if result := tx.Model(&database.Setting{}).Where("key=?", "trading_engine_fallback").Update("value", "disabled"); result.Error != nil || result.RowsAffected != 1 {
			if result.Error != nil {
				return result.Error
			}
			return fmt.Errorf("trading_engine_fallback setting missing during atomic authority transition")
		}
		return nil
	})
	if err == nil {
		var snap database.Stage08FlagSnapshot
		if s.DB.First(&snap, "id=?", result.FlagSnapshotID).Error == nil {
			if f, e := verifyFlagSnapshot(snap); e == nil {
				_ = cutover.ActivateVerified(f, snap.ID, result.ToAuthority)
			}
		}
	}
	return result, err
}

func validateFlagsForStage(f cutover.Flags, stage string) error {
	if stage == "schema_legacy" || stage == "ledger_compare" {
		if f != cutover.SafeFlags() {
			return fmt.Errorf("legacy cutover stages require exact safe flag snapshot")
		}
		return nil
	}
	if stage == "shared_shadow" || stage == "parity_accepted" {
		if f.SharedEngine != "shadow" || f.DualRun != "observe" || f.LedgerAuthority == "authoritative" {
			return fmt.Errorf("shadow stages require observational shared engine/dual-run without capital authority")
		}
		return nil
	}
	if stage == "new_paper" || stage == "paper_observation" {
		if f.SharedEngine != "paper" || f.CandidateStrategy != "paper" || f.PointInTime != "authoritative" || f.LedgerAuthority != "authoritative" || f.Stage07Context == "" {
			return fmt.Errorf("new paper requires ledger authority, shared/candidate paper, PIT authority, and exact Stage 07 context")
		}
		return nil
	}
	if stage == "limited_live" && !f.IsLive() {
		return fmt.Errorf("limited live target requires exact live flag envelope")
	}
	return nil
}
func (s Service) transitionRequestDigest(tx *gorm.DB, state database.CutoverState, r TransitionRequest) (string, []byte, error) {
	ids := append([]string(nil), r.EvidenceIDs...)
	sort.Strings(ids)
	var evidence []struct{ ID, Digest string }
	for _, id := range ids {
		var row database.CutoverPrerequisiteEvidence
		if err := tx.First(&row, "id=?", id).Error; err != nil {
			return "", nil, err
		}
		if err := verifyPrerequisiteEvidenceIntegrity(tx, row); err != nil {
			return "", nil, err
		}
		evidence = append(evidence, struct{ ID, Digest string }{row.ID, row.ContentDigest})
	}
	var requested database.Stage08FlagSnapshot
	if err := tx.First(&requested, "id=?", r.FlagSnapshotID).Error; err != nil {
		return "", nil, err
	}
	if _, err := verifyFlagSnapshot(requested); err != nil {
		return "", nil, err
	}
	policyDigest, populationDigest, deploymentDigest := "", "", ""
	if r.ParityPolicyID != "" {
		var row database.ParityAcceptancePolicy
		if err := tx.First(&row, "id=?", r.ParityPolicyID).Error; err != nil {
			return "", nil, err
		}
		policyDigest = row.ContentDigest
	}
	if r.ParityPopulationID != "" {
		var row database.ParityPopulation
		if err := tx.First(&row, "id=?", r.ParityPopulationID).Error; err != nil {
			return "", nil, err
		}
		if err := verifyParityPopulation(tx, row); err != nil {
			return "", nil, err
		}
		populationDigest = row.ContentDigest
	}
	if r.Stage07ContextKey != "" {
		var row database.GovernanceDeployment
		if err := tx.First(&row, "context_key=?", r.Stage07ContextKey).Error; err != nil {
			return "", nil, err
		}
		deploymentDigest, _, _ = hash(row)
	}
	return hash(struct {
		Schema, Key, Target, Principal, Reason, ActiveFlag, ActiveDigest, ActiveEnvelopeDigest, RequestedFlag, RequestedFlagDigest, Context, DeploymentDigest, Policy, PolicyDigest, Population, PopulationDigest string
		SourceVersion                                                                                                                                                                                             int64
		Rollback                                                                                                                                                                                                  bool
		Evidence                                                                                                                                                                                                  any
	}{"stage08-cutover-request-v2", r.IdempotencyKey, r.ToStage, r.Principal, r.Reason, state.FlagSnapshotID, state.FlagDigest, state.AuthorityDigest, r.FlagSnapshotID, requested.ContentDigest, r.Stage07ContextKey, deploymentDigest, r.ParityPolicyID, policyDigest, r.ParityPopulationID, populationDigest, state.Version, r.Rollback, evidence})
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
		state := database.CutoverState{}
		if err := tx.First(&state, 1).Error; err != nil {
			return err
		}
		check := s
		check.DB = tx
		if err := check.persistReconciliation(ctx, state, state.FlagSnapshotID, report); err != nil {
			return err
		}
	case "parity_threshold_passed":
		check := s
		check.DB = tx
		if r.ParityPopulationID == "" {
			return fmt.Errorf("immutable parity population is required")
		}
		var population database.ParityPopulation
		if err := tx.First(&population, "id=?", r.ParityPopulationID).Error; err != nil {
			return err
		}
		if population.PolicyID != r.ParityPolicyID || population.FlagSnapshotID != r.FlagSnapshotID {
			return fmt.Errorf("parity population binding mismatch")
		}
		aggregate, err := check.EvaluateParity(r.ParityPopulationID)
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
		return s.verifyBoundEvidence(tx, key, r, func(payload map[string]any) error {
			positionID, buyID, sellID := fmt.Sprint(payload["position_id"]), fmt.Sprint(payload["buy_fill_id"]), fmt.Sprint(payload["sell_fill_id"])
			if positionID == "" || buyID == "" || sellID == "" || payload["closed"] != true || payload["reconciled"] != true {
				return fmt.Errorf("paper round trip must identify one chronological closed reconciled economic chain")
			}
			var buy, sell database.Fill
			if err := tx.First(&buy, "id=?", buyID).Error; err != nil {
				return err
			}
			if err := tx.First(&sell, "id=?", sellID).Error; err != nil {
				return err
			}
			if fmt.Sprint(buy.PositionID) != positionID || buy.PositionID != sell.PositionID || buy.Side != "buy" || sell.Side != "sell" || buy.ExecutionMode != "paper" || sell.ExecutionMode != "paper" || !sell.OccurredAt.After(buy.OccurredAt) || buy.FeeAmount.Sign() < 0 || sell.FeeAmount.Sign() < 0 {
				return fmt.Errorf("paper evidence does not match one chronological costed fill chain")
			}
			var position database.Position
			if err := tx.First(&position, buy.PositionID).Error; err != nil {
				return err
			}
			if position.Status != "closed" {
				return fmt.Errorf("paper evidence position is not closed")
			}
			return nil
		})
	case "restart_idempotency":
		return s.verifyBoundEvidence(tx, key, r, func(payload map[string]any) error {
			before, _ := payload["before_digest"].(string)
			after, _ := payload["after_digest"].(string)
			cycles, _ := payload["restart_cycles"].(float64)
			if len(before) != 64 || before != after || cycles < 1 {
				return fmt.Errorf("explicit restart cycle and equal before/after economic digests required")
			}
			current, err := FingerprintDatabase(ctx, tx)
			if err != nil {
				return err
			}
			if current.Digest != after {
				return fmt.Errorf("restart after-digest does not match current canonical economics")
			}
			return nil
		})
	case "dataset_coverage":
		return s.verifyBoundEvidence(tx, key, r, func(payload map[string]any) error {
			expected, _ := payload["expected_snapshots"].(float64)
			complete, _ := payload["complete_snapshots"].(float64)
			benchmark, _ := payload["benchmark_complete"].(bool)
			universe, _ := payload["universe_complete"].(bool)
			if expected <= 0 || complete != expected || !benchmark || !universe {
				return fmt.Errorf("nonzero complete benchmark/universe snapshot population required")
			}
			return nil
		})
	case "backtest_reproduced":
		return s.verifyBoundEvidence(tx, key, r, nil)
	case "validation_passed":
		return s.verifyBoundEvidence(tx, key, r, nil)
	case "observation_elapsed":
		return s.verifyBoundEvidence(tx, key, r, func(payload map[string]any) error {
			observed, _ := payload["observed"].(float64)
			expected, _ := payload["expected"].(float64)
			if expected <= 0 || observed < expected {
				return fmt.Errorf("complete nonzero observation window required")
			}
			return nil
		})
	default:
		return fmt.Errorf("unknown prerequisite")
	}
	return nil
}
func (s Service) verifyBoundEvidence(tx *gorm.DB, kind string, r TransitionRequest, validate func(map[string]any) error) error {
	for _, id := range r.EvidenceIDs {
		var row database.CutoverPrerequisiteEvidence
		if err := tx.First(&row, "id=?", id).Error; err != nil {
			return err
		}
		if err := verifyPrerequisiteEvidenceIntegrity(tx, row); err != nil {
			return err
		}
		if row.EvidenceType != kind || row.TargetStage != r.ToStage || row.FlagSnapshotID != r.FlagSnapshotID || row.ContextKey != r.Stage07ContextKey || (r.ParityPolicyID != "" && row.ParityPolicyID != r.ParityPolicyID) || row.WindowEnd.After(s.now()) {
			continue
		}
		var payload map[string]any
		if json.Unmarshal([]byte(row.PayloadJSON), &payload) != nil {
			return fmt.Errorf("evidence payload corrupt")
		}
		if validate != nil {
			if err := validate(payload); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("exact content-addressed %s evidence missing", kind)
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
	m := map[string][]string{"ledger_compare": {"schema_deployed"}, "shared_shadow": {"ledger_compare_clean"}, "parity_accepted": {"parity_threshold_passed"}, "new_paper": {"ledger_reconciled", "dataset_coverage", "stage07_paper"}, "paper_observation": {"paper_round_trip", "restart_idempotency"}, "research_validation": {"dataset_coverage", "backtest_reproduced"}, "limited_live": {"observation_elapsed", "validation_passed", "human_approved", "stage07_exact"}}
	result := append([]string(nil), m[stage]...)
	if stageIndex(stage) > stageIndex("new_paper") {
		result = append([]string{"ledger_reconciled"}, result...)
	}
	return result
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
		if state.FlagSnapshotID != flagID {
			out.Diagnostics = append(out.Diagnostics, "runtime_flag_snapshot_mismatch")
		}
		var snap database.Stage08FlagSnapshot
		if err := s.DB.First(&snap, "id=?", state.FlagSnapshotID).Error; err != nil {
			out.Diagnostics = append(out.Diagnostics, "cutover_flag_snapshot_missing")
		} else if _, err := verifyFlagSnapshot(snap); err != nil {
			out.Diagnostics = append(out.Diagnostics, "cutover_flag_snapshot_corrupt")
		}
		if state.Authority != authorityForStage(state.Stage) {
			out.Diagnostics = append(out.Diagnostics, "cutover_authority_mismatch")
		}
	}
	report, err := ledger.New(s.DB).Reconcile(ctx, ledger.DefaultAccountID, time.Time{})
	if err != nil {
		out.Ledger = map[string]any{"balanced": false, "error": err.Error()}
		out.Diagnostics = append(out.Diagnostics, "ledger_reconciliation_unavailable")
	} else {
		var evidence database.ReconciliationEvidence
		evidenceErr := s.DB.Where("flag_snapshot_id=? AND cutover_transition_id=? AND balanced=true", state.FlagSnapshotID, state.TransitionID).Order("checked_at desc").First(&evidence).Error
		out.Ledger = map[string]any{"balanced": report.Balanced, "last_successful_check": evidence.CheckedAt, "evidence_id": evidence.ID, "actionable_issues": report.ActionableIssues}
		if !report.Balanced {
			out.Diagnostics = append(out.Diagnostics, "ledger_unreconciled")
		}
		if state.Authority != "legacy" && evidenceErr != nil {
			out.Diagnostics = append(out.Diagnostics, "reconciliation_evidence_missing")
		}
	}
	var job database.BacktestJob
	if err := s.DB.Where("stage08_context_json->>'flag_snapshot_id' = ?", state.FlagSnapshotID).Order("created_at desc").First(&job).Error; err != nil {
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
	if err := s.DB.Where("flag_snapshot_id=? AND cutover_attempt_id=?", state.FlagSnapshotID, state.TransitionID).Order("observed_at desc").Limit(20).Find(&parityRows).Error; err != nil {
		out.Diagnostics = append(out.Diagnostics, "parity_query_failed")
	}
	var parityTotal, parityUnexplained int64
	if err := s.DB.Model(&database.ParityObservation{}).Where("flag_snapshot_id=? AND cutover_attempt_id=?", state.FlagSnapshotID, state.TransitionID).Count(&parityTotal).Error; err != nil {
		out.Diagnostics = append(out.Diagnostics, "parity_count_failed")
	}
	if err := s.DB.Model(&database.ParityObservation{}).Where("flag_snapshot_id=? AND cutover_attempt_id=? AND classification=?", state.FlagSnapshotID, state.TransitionID, "unexplained").Count(&parityUnexplained).Error; err != nil {
		out.Diagnostics = append(out.Diagnostics, "parity_unexplained_count_failed")
	}
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
	dataQuery := s.DB.Order("created_at desc")
	boundDataset, boundUniverse := "", ""
	if state.TransitionID != strings.Repeat("0", 64) {
		var transition database.CutoverTransition
		if err := s.DB.First(&transition, "id=?", state.TransitionID).Error; err != nil {
			out.Diagnostics = append(out.Diagnostics, "cutover_transition_missing")
		} else {
			var ids []string
			if json.Unmarshal([]byte(transition.PrerequisitesJSON), &ids) != nil {
				out.Diagnostics = append(out.Diagnostics, "cutover_evidence_binding_corrupt")
			}
			for _, id := range ids {
				var evidence database.CutoverPrerequisiteEvidence
				if err := s.DB.First(&evidence, "id=?", id).Error; err != nil || verifyPrerequisiteEvidenceIntegrity(s.DB, evidence) != nil || evidence.FlagSnapshotID != state.FlagSnapshotID || evidence.TargetStage != state.Stage {
					out.Diagnostics = append(out.Diagnostics, "cutover_evidence_missing_or_corrupt")
					continue
				}
				if evidence.DatasetVersion != "" {
					boundDataset = evidence.DatasetVersion
				}
				if evidence.UniverseVersion != "" {
					boundUniverse = evidence.UniverseVersion
				}
			}
		}
	}
	if boundDataset != "" {
		dataQuery = dataQuery.Where("dataset_version=?", boundDataset)
	} else if stageIndex(state.Stage) >= stageIndex("new_paper") {
		out.Diagnostics = append(out.Diagnostics, "bound_dataset_evidence_missing")
	}
	if err := dataQuery.First(&manifest).Error; err != nil {
		out.Data = map[string]any{"dataset": "missing", "universe": "missing"}
		out.Diagnostics = append(out.Diagnostics, "dataset_manifest_missing")
	} else {
		var universe database.UniverseSnapshot
		uerr := s.DB.Where("dataset_manifest_id=?", manifest.ID).Order("snapshot_time desc").First(&universe).Error
		out.Data = map[string]any{"dataset_manifest_id": manifest.ID, "dataset_version": manifest.DatasetVersion, "universe_policy": universe.PolicyVersion, "universe_coverage": universe.CoverageState, "universe_observed_at": universe.SnapshotTime, "bound_dataset_version": boundDataset, "bound_universe_version": boundUniverse}
		if uerr != nil || universe.CoverageState != "complete" {
			out.Diagnostics = append(out.Diagnostics, "universe_evidence_missing_or_incomplete")
		} else if s.Flags.PointInTime == "authoritative" && now.Sub(universe.SnapshotTime) > 24*time.Hour {
			out.Diagnostics = append(out.Diagnostics, "universe_evidence_stale")
		}
		if boundUniverse != "" && universe.PolicyVersion != boundUniverse {
			out.Diagnostics = append(out.Diagnostics, "bound_universe_version_mismatch")
		}
	}
	var deployment database.GovernanceDeployment
	if err := s.DB.First(&deployment, "context_key=?", s.Flags.Stage07Context).Error; err != nil {
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
	if err := s.DB.Where("flag_snapshot_id=? AND cutover_transition_id=? AND status=?", state.FlagSnapshotID, state.TransitionID, "verified").Order("verified_at desc").First(&backup).Error; err != nil {
		out.Backup = map[string]any{"status": "unverified"}
		if stageIndex(state.Stage) >= stageIndex("new_paper") {
			out.Diagnostics = append(out.Diagnostics, "backup_verification_missing")
		}
	} else {
		out.Backup = backup
	}
	if err := s.DB.Where("state <> ?", "resolved").Order("last_seen_at desc").Limit(100).Find(&out.Incidents).Error; err != nil {
		out.Diagnostics = append(out.Diagnostics, "incident_query_failed")
	}
	for _, incident := range out.Incidents {
		if incident.Severity == "critical" {
			out.Diagnostics = append(out.Diagnostics, "open_critical_incident:"+incident.Type)
		}
		if incident.LastDeliveryState == "failed" {
			out.Diagnostics = append(out.Diagnostics, "alert_delivery_failed:"+incident.Type)
		}
	}
	if len(out.Diagnostics) == 0 {
		out.Status = "ok"
	}
	sort.Strings(out.Diagnostics)
	return out
}

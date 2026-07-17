package governance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/validation"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type State string

const (
	StateResearch    State = "research"
	StateShadow      State = "shadow"
	StatePaper       State = "paper"
	StateLimitedLive State = "limited_live"
	StateFullLive    State = "full_live"
	StateRollback    State = "rollback"
)

type Code string

const (
	CodeIllegalTransition   Code = "illegal_transition"
	CodeApprovalRequired    Code = "human_approval_required"
	CodeApprovalMismatch    Code = "approval_mismatch_or_stale"
	CodeEvidenceFailed      Code = "validation_evidence_failed"
	CodeVersionMismatch     Code = "experiment_version_mismatch"
	CodeArtifactQuarantined Code = "artifact_class_quarantined"
	CodeElapsedEvidence     Code = "elapsed_evidence_insufficient"
	CodeRollbackGate        Code = "rollback_threshold_not_met"
	CodeFallbackRequired    Code = "fallback_required"
	CodeIntegrity           Code = "governance_integrity_failure"
	CodeUnauthorized        Code = "governance_unauthorized"
)

type Error struct {
	Code    Code   `json:"code"`
	Details string `json:"details,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Details) }

type Clock interface{ Now() time.Time }
type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

type Service struct {
	DB         *gorm.DB
	Validation validation.Repository
	Clock      Clock
}

type Capability string

const (
	CapabilityResearch   Capability = "research"
	CapabilityApprove    Capability = "approve"
	CapabilityTransition Capability = "transition"
	CapabilityRollback   Capability = "rollback"
)

// Principal can only be constructed through the configured-authentication
// seam; service calls with its zero value fail closed.
type Principal struct {
	id           string
	capabilities map[Capability]struct{}
	trusted      bool
}

func NewTrustedPrincipal(id string, capabilities ...Capability) Principal {
	p := Principal{id: strings.TrimSpace(id), capabilities: map[Capability]struct{}{}, trusted: true}
	for _, capability := range capabilities {
		p.capabilities[capability] = struct{}{}
	}
	return p
}
func (p Principal) ID() string { return p.id }
func (p Principal) Has(capability Capability) bool {
	_, ok := p.capabilities[capability]
	return p.trusted && ok
}
func (p Principal) require(capability Capability) error {
	if !p.trusted || p.id == "" {
		return &Error{Code: CodeUnauthorized, Details: "trusted principal is required"}
	}
	if _, ok := p.capabilities[capability]; !ok {
		return &Error{Code: CodeUnauthorized, Details: "missing capability: " + string(capability)}
	}
	return nil
}

func NewService(db *gorm.DB) Service {
	return Service{DB: db, Validation: validation.Repository{DB: db}, Clock: realClock{}}
}
func (s Service) now() time.Time {
	if s.Clock == nil {
		return time.Now().UTC().Truncate(time.Microsecond)
	}
	return s.Clock.Now().UTC().Truncate(time.Microsecond)
}

type ApprovalRequest struct {
	IdempotencyKey  string    `json:"idempotency_key"`
	ExperimentID    string    `json:"experiment_id"`
	EvidenceID      string    `json:"evidence_id"`
	TargetState     State     `json:"target_state"`
	ArtifactVersion string    `json:"artifact_version"`
	PolicyVersion   string    `json:"policy_version"`
	Approver        string    `json:"approver,omitempty"`
	Reason          string    `json:"reason"`
	ExpiresAt       time.Time `json:"expires_at"`
}

func (s Service) Approve(principal Principal, request ApprovalRequest) (database.GovernanceApproval, error) {
	if err := principal.require(CapabilityApprove); err != nil {
		return database.GovernanceApproval{}, err
	}
	if s.DB == nil {
		return database.GovernanceApproval{}, fmt.Errorf("governance database is required")
	}
	if request.IdempotencyKey == "" || request.ExperimentID == "" || request.EvidenceID == "" || request.ArtifactVersion == "" || request.PolicyVersion == "" || strings.TrimSpace(request.Reason) == "" || !knownState(request.TargetState) {
		return database.GovernanceApproval{}, &Error{Code: CodeApprovalMismatch, Details: "complete approval identity, target, versions, actor, and reason are required"}
	}
	manifest, evidence, err := s.boundEvidence(request.ExperimentID, request.EvidenceID)
	if err != nil {
		return database.GovernanceApproval{}, err
	}
	if evidence.Status != "passed" || evidence.Result == nil || !evidence.Result.Aggregate.Passed {
		return database.GovernanceApproval{}, &Error{Code: CodeEvidenceFailed}
	}
	if err := s.verifyMLEvidence(manifest, evidence); err != nil {
		return database.GovernanceApproval{}, err
	}
	if manifest.Spec.Exploratory || manifest.Spec.StudyType != "confirmatory" {
		return database.GovernanceApproval{}, &Error{Code: CodeEvidenceFailed, Details: "exploratory evidence cannot promote"}
	}
	if err := matchVersions(manifest, request.ArtifactVersion, request.PolicyVersion); err != nil {
		return database.GovernanceApproval{}, err
	}
	now := s.now()
	if request.ExpiresAt.IsZero() || !request.ExpiresAt.After(now) || request.ExpiresAt.After(now.Add(7*24*time.Hour)) {
		return database.GovernanceApproval{}, &Error{Code: CodeApprovalMismatch, Details: "bounded future approval expiry is required"}
	}
	var source database.GovernanceDeployment
	if err := s.DB.Where("context_key=?", authorityContext(manifest)).First(&source).Error; err != nil {
		return database.GovernanceApproval{}, &Error{Code: CodeApprovalMismatch, Details: "source deployment does not exist"}
	}
	if now.Before(source.ActivatedAt) {
		return database.GovernanceApproval{}, &Error{Code: CodeApprovalMismatch, Details: "approval predates source activation"}
	}
	evidenceDigest, err := s.evidenceDigest(request.EvidenceID)
	if err != nil {
		return database.GovernanceApproval{}, err
	}
	targetPolicy, err := manifest.Spec.AuthorityPolicy.WithRolloutState(string(request.TargetState))
	if err != nil {
		return database.GovernanceApproval{}, err
	}
	row := database.GovernanceApproval{IdempotencyKey: request.IdempotencyKey, ExperimentID: request.ExperimentID, EvidenceID: request.EvidenceID, TargetState: string(request.TargetState), ArtifactVersion: request.ArtifactVersion, PolicyVersion: request.PolicyVersion, Approver: principal.ID(), Reason: strings.TrimSpace(request.Reason), ApprovedAt: now, SourceState: source.State, SourceActivatedAt: source.ActivatedAt.UTC(), AuthorityPolicyDigest: targetPolicy.Digest, EvidenceDigest: evidenceDigest, ExpiresAt: request.ExpiresAt.UTC()}
	row.ContentDigest = approvalDigest(row)
	row.ID = hash([]byte(row.ContentDigest))
	res := s.DB.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "idempotency_key"}}, DoNothing: true}).Create(&row)
	if res.Error != nil {
		return database.GovernanceApproval{}, res.Error
	}
	if res.RowsAffected == 0 {
		var existing database.GovernanceApproval
		if err := s.DB.Where("idempotency_key=?", request.IdempotencyKey).First(&existing).Error; err != nil {
			return database.GovernanceApproval{}, err
		}
		if existing.ExperimentID != request.ExperimentID || existing.EvidenceID != request.EvidenceID || existing.TargetState != string(request.TargetState) || existing.ArtifactVersion != request.ArtifactVersion || existing.PolicyVersion != request.PolicyVersion || existing.Approver != principal.ID() || existing.Reason != strings.TrimSpace(request.Reason) || !existing.ExpiresAt.Equal(request.ExpiresAt.UTC()) || existing.SourceState != source.State || !existing.SourceActivatedAt.Equal(source.ActivatedAt) || existing.EvidenceDigest != evidenceDigest || existing.AuthorityPolicyDigest != targetPolicy.Digest {
			return database.GovernanceApproval{}, &Error{Code: CodeIntegrity, Details: "idempotency key replayed with different approval"}
		}
		if existing.ContentDigest != approvalDigest(existing) || existing.ID != hash([]byte(existing.ContentDigest)) {
			return database.GovernanceApproval{}, &Error{Code: CodeIntegrity, Details: "stored approval integrity check failed"}
		}
		return existing, nil
	}
	return row, nil
}

type TransitionRequest struct {
	IdempotencyKey    string `json:"idempotency_key"`
	ContextKey        string `json:"context_key"`
	ExperimentID      string `json:"experiment_id"`
	EvidenceID        string `json:"evidence_id"`
	ApprovalID        string `json:"approval_id"`
	TargetState       State  `json:"target_state"`
	ArtifactVersion   string `json:"artifact_version"`
	PolicyVersion     string `json:"policy_version"`
	FallbackVersion   string `json:"fallback_version,omitempty"`
	Reason            string `json:"reason"`
	ElapsedEvidenceID string `json:"elapsed_evidence_id,omitempty"`
}

func (s Service) Transition(principal Principal, request TransitionRequest) (database.GovernanceTransition, error) {
	if err := principal.require(CapabilityTransition); err != nil {
		return database.GovernanceTransition{}, err
	}
	if request.TargetState == StateRollback {
		return database.GovernanceTransition{}, &Error{Code: CodeIllegalTransition, Details: "use the rollback operation"}
	}
	if request.IdempotencyKey == "" || request.ContextKey == "" || request.ExperimentID == "" || request.EvidenceID == "" || request.ArtifactVersion == "" || request.PolicyVersion == "" || request.Reason == "" || !knownState(request.TargetState) {
		return database.GovernanceTransition{}, &Error{Code: CodeIllegalTransition, Details: "complete immutable transition context is required"}
	}
	manifest, evidence, err := s.boundEvidence(request.ExperimentID, request.EvidenceID)
	if err != nil {
		return database.GovernanceTransition{}, err
	}
	if request.TargetState != StateResearch && (evidence.Status != "passed" || evidence.Result == nil || !evidence.Result.Aggregate.Passed) {
		return database.GovernanceTransition{}, &Error{Code: CodeEvidenceFailed}
	}
	if request.TargetState != StateResearch {
		if err := s.verifyMLEvidence(manifest, evidence); err != nil {
			return database.GovernanceTransition{}, err
		}
	}
	if err := matchVersions(manifest, request.ArtifactVersion, request.PolicyVersion); err != nil {
		return database.GovernanceTransition{}, err
	}
	if request.ContextKey != authorityContext(manifest) {
		return database.GovernanceTransition{}, &Error{Code: CodeVersionMismatch, Details: "context key does not match immutable experiment authority"}
	}
	now := s.now()
	var result database.GovernanceTransition
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if e := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", request.ContextKey).Error; e != nil {
			return e
		}
		var replay database.GovernanceTransition
		if e := tx.Where("idempotency_key=?", request.IdempotencyKey).First(&replay).Error; e == nil {
			if replay.ContentDigest != transitionDigest(replay) || replay.ID != hash([]byte(replay.ContentDigest)) || replay.ContextKey != request.ContextKey || replay.ExperimentID != request.ExperimentID || replay.EvidenceID != request.EvidenceID || replay.ToState != string(request.TargetState) || replay.ArtifactVersion != request.ArtifactVersion || replay.FallbackVersion != request.FallbackVersion || replay.Reason != request.Reason || stringValue(replay.ApprovalID) != request.ApprovalID || stringValue(replay.MonitoringEvidenceID) != request.ElapsedEvidenceID {
				return &Error{Code: CodeIntegrity, Details: "idempotency key replayed with different transition"}
			}
			result = replay
			return nil
		} else if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		var current database.GovernanceDeployment
		e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("context_key=?", request.ContextKey).First(&current).Error
		from := State("")
		if e == nil {
			from = State(current.State)
		} else if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		if !legal(from, request.TargetState) {
			return &Error{Code: CodeIllegalTransition, Details: fmt.Sprintf("%s -> %s", from, request.TargetState)}
		}
		var approval database.GovernanceApproval
		if from != "" || request.TargetState != StateResearch {
			if request.ApprovalID == "" {
				return &Error{Code: CodeApprovalRequired}
			}
			if e := tx.Where("id=?", request.ApprovalID).First(&approval).Error; e != nil {
				return &Error{Code: CodeApprovalMismatch, Details: "approval not found"}
			}
			evidenceDigest, digestErr := s.evidenceDigestTx(tx, request.EvidenceID)
			if digestErr != nil {
				return digestErr
			}
			targetPolicy, policyErr := manifest.Spec.AuthorityPolicy.WithRolloutState(string(request.TargetState))
			if policyErr != nil {
				return policyErr
			}
			if approval.ExperimentID != request.ExperimentID || approval.EvidenceID != request.EvidenceID || approval.TargetState != string(request.TargetState) || approval.ArtifactVersion != request.ArtifactVersion || approval.PolicyVersion != request.PolicyVersion || approval.ApprovedAt.Before(evidence.CreatedAt) || !approval.ExpiresAt.After(now) || approval.SourceState != string(from) || !approval.SourceActivatedAt.Equal(current.ActivatedAt) || approval.EvidenceDigest != evidenceDigest || approval.AuthorityPolicyDigest != targetPolicy.Digest {
				return &Error{Code: CodeApprovalMismatch, Details: "approval is stale or bound to different evidence/target/versions"}
			}
			if approval.ContentDigest != approvalDigest(approval) || approval.ID != hash([]byte(approval.ContentDigest)) {
				return &Error{Code: CodeApprovalMismatch, Details: "approval integrity check failed"}
			}
		}
		if (request.TargetState == StatePaper || request.TargetState == StateLimitedLive || request.TargetState == StateFullLive) && manifest.Spec.Model != nil && !manifest.Spec.Model.CanHoldAuthority() {
			return &Error{Code: CodeArtifactQuarantined, Details: string(manifest.Spec.Model.Class)}
		}
		var elapsedEvidenceID *string
		elapsedEvidenceDigest := ""
		if elapsed := manifest.Spec.RequiredElapsed[string(request.TargetState)]; elapsed > 0 {
			if request.ElapsedEvidenceID == "" {
				return &Error{Code: CodeElapsedEvidence, Details: "immutable elapsed monitoring evidence is required"}
			}
			monitor, monitorErr := loadMonitoringEvidence(tx, request.ElapsedEvidenceID)
			if monitorErr != nil {
				return monitorErr
			}
			if monitor.ContextKey != request.ContextKey || monitor.ExperimentID != request.ExperimentID || monitor.DeploymentTransitionID != current.TransitionID || monitor.AuthorityPolicyDigest != current.AuthorityPolicyDigest || monitor.WindowStart.Before(current.ActivatedAt) || monitor.WindowEnd.After(now) || monitor.WindowEnd.Sub(monitor.WindowStart) < elapsed || monitor.ObservedObservations < monitor.ExpectedObservations {
				return &Error{Code: CodeElapsedEvidence, Details: "elapsed evidence is stale, incomplete, or bound to another source deployment"}
			}
			id := monitor.ID
			elapsedEvidenceID = &id
			elapsedEvidenceDigest = monitor.ContentDigest
		}
		approvalID := request.ApprovalID
		created := now
		payload := struct {
			TransitionRequest
			From      State     `json:"from_state"`
			CreatedAt time.Time `json:"created_at"`
		}{request, from, created}
		encoded, _ := json.Marshal(payload)
		_ = encoded
		evidenceDigest, e := s.evidenceDigestTx(tx, request.EvidenceID)
		if e != nil {
			return e
		}
		targetPolicy, e := manifest.Spec.AuthorityPolicy.WithRolloutState(string(request.TargetState))
		if e != nil {
			return e
		}
		approvalDigestValue := ""
		if approvalID != "" {
			approvalDigestValue = approval.ContentDigest
		}
		row := database.GovernanceTransition{IdempotencyKey: request.IdempotencyKey, ContextKey: request.ContextKey, ExperimentID: request.ExperimentID, EvidenceID: request.EvidenceID, FromState: string(from), ToState: string(request.TargetState), ArtifactVersion: request.ArtifactVersion, FallbackVersion: request.FallbackVersion, Reason: request.Reason, CreatedAt: created, PolicyVersion: request.PolicyVersion, AuthorityPolicyDigest: targetPolicy.Digest, ApprovalDigest: approvalDigestValue, EvidenceDigest: evidenceDigest, MonitoringEvidenceID: elapsedEvidenceID, MonitoringEvidenceDigest: elapsedEvidenceDigest, Actor: principal.ID()}
		if approvalID != "" {
			row.ApprovalID = &approvalID
		}
		row.ContentDigest = transitionDigest(row)
		row.ID = hash([]byte(row.ContentDigest))
		if e := tx.Create(&row).Error; e != nil {
			return e
		}
		if e := tx.Exec("SELECT set_config('trading_bot.governance_transition_id', ?, true)", row.ID).Error; e != nil {
			return e
		}
		policyBytes, _ := json.Marshal(targetPolicy)
		deployment := database.GovernanceDeployment{ContextKey: request.ContextKey, ExperimentID: request.ExperimentID, EvidenceID: request.EvidenceID, State: string(request.TargetState), ArtifactVersion: request.ArtifactVersion, PolicyVersion: request.PolicyVersion, FallbackVersion: request.FallbackVersion, ActivatedAt: now, UpdatedAt: now, AuthorityPolicyJSON: string(policyBytes), AuthorityPolicyDigest: targetPolicy.Digest, TransitionID: row.ID}
		if e := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "context_key"}}, DoUpdates: clause.AssignmentColumns([]string{"experiment_id", "evidence_id", "state", "artifact_version", "policy_version", "fallback_version", "activated_at", "updated_at", "authority_policy_json", "authority_policy_digest", "transition_id"})}).Create(&deployment).Error; e != nil {
			return e
		}
		if manifest.Spec.Model != nil && request.TargetState != StateResearch {
			if e := writeModelAuthorityProjection(tx, request.ArtifactVersion, request.TargetState); e != nil {
				return e
			}
		}
		result = row
		return nil
	})
	return result, err
}

type RollbackRequest struct {
	IdempotencyKey       string `json:"idempotency_key"`
	ContextKey           string `json:"context_key"`
	ExperimentID         string `json:"experiment_id"`
	EvidenceID           string `json:"evidence_id"`
	FallbackVersion      string `json:"fallback_version"`
	Reason               string `json:"reason"`
	MonitoringEvidenceID string `json:"monitoring_evidence_id"`
}

func (s Service) Rollback(principal Principal, request RollbackRequest) (database.GovernanceTransition, error) {
	if err := principal.require(CapabilityRollback); err != nil {
		return database.GovernanceTransition{}, err
	}
	if request.IdempotencyKey == "" || request.ContextKey == "" || request.ExperimentID == "" || request.EvidenceID == "" || request.FallbackVersion == "" || request.Reason == "" || request.MonitoringEvidenceID == "" {
		return database.GovernanceTransition{}, &Error{Code: CodeFallbackRequired}
	}
	manifest, _, err := s.boundEvidence(request.ExperimentID, request.EvidenceID)
	if err != nil {
		return database.GovernanceTransition{}, err
	}
	now := s.now()
	var result database.GovernanceTransition
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		var replay database.GovernanceTransition
		if e := tx.Where("idempotency_key=?", request.IdempotencyKey).First(&replay).Error; e == nil {
			if replay.ContentDigest != transitionDigest(replay) || replay.ID != hash([]byte(replay.ContentDigest)) || replay.ContextKey != request.ContextKey || replay.ExperimentID != request.ExperimentID || replay.EvidenceID != request.EvidenceID || replay.ToState != string(StateRollback) || replay.FallbackVersion != request.FallbackVersion || replay.Reason != request.Reason || stringValue(replay.MonitoringEvidenceID) != request.MonitoringEvidenceID {
				return &Error{Code: CodeIntegrity}
			}
			result = replay
			return nil
		}
		var current database.GovernanceDeployment
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("context_key=?", request.ContextKey).First(&current).Error; e != nil {
			return e
		}
		if current.ExperimentID != request.ExperimentID || current.EvidenceID != request.EvidenceID {
			return &Error{Code: CodeVersionMismatch}
		}
		if request.FallbackVersion != current.FallbackVersion {
			return &Error{Code: CodeFallbackRequired, Details: "requested fallback differs from preconfigured fallback"}
		}
		monitor, monitorErr := loadMonitoringEvidence(tx, request.MonitoringEvidenceID)
		if monitorErr != nil {
			return monitorErr
		}
		if monitor.ContextKey != request.ContextKey || monitor.ExperimentID != request.ExperimentID || monitor.DeploymentTransitionID != current.TransitionID || monitor.AuthorityPolicyDigest != current.AuthorityPolicyDigest || monitor.ArtifactVersion != current.ArtifactVersion || monitor.WindowStart.Before(current.ActivatedAt) || monitor.WindowEnd.After(now) || now.Sub(monitor.WindowEnd) > 24*time.Hour || monitor.ExpectedObservations <= 0 || monitor.ObservedObservations < monitor.ExpectedObservations {
			return &Error{Code: CodeRollbackGate, Details: "monitoring evidence is stale, incomplete, or bound to another deployment"}
		}
		metrics := map[string]float64{}
		if json.Unmarshal([]byte(monitor.MetricsJSON), &metrics) != nil {
			return &Error{Code: CodeIntegrity, Details: "monitoring metrics malformed"}
		}
		triggered := false
		for _, threshold := range manifest.Spec.RollbackThresholds {
			value, ok := metrics[threshold.Metric]
			if ok && finite(value) && compare(value, threshold.Op, threshold.Value) {
				triggered = true
				break
			}
		}
		if !triggered {
			return &Error{Code: CodeRollbackGate}
		}
		payload, _ := json.Marshal(request)
		_ = payload
		monitorID := monitor.ID
		evidenceDigest, e := s.evidenceDigestTx(tx, request.EvidenceID)
		if e != nil {
			return e
		}
		rollbackPolicy, e := manifest.Spec.AuthorityPolicy.WithRolloutState(string(StateRollback))
		if e != nil {
			return e
		}
		row := database.GovernanceTransition{IdempotencyKey: request.IdempotencyKey, ContextKey: request.ContextKey, ExperimentID: request.ExperimentID, EvidenceID: request.EvidenceID, FromState: current.State, ToState: string(StateRollback), ArtifactVersion: current.ArtifactVersion, FallbackVersion: request.FallbackVersion, Reason: request.Reason, CreatedAt: now, PolicyVersion: current.PolicyVersion, AuthorityPolicyDigest: rollbackPolicy.Digest, EvidenceDigest: evidenceDigest, MonitoringEvidenceID: &monitorID, MonitoringEvidenceDigest: monitor.ContentDigest, Actor: principal.ID()}
		row.ContentDigest = transitionDigest(row)
		row.ID = hash([]byte(row.ContentDigest))
		if e := tx.Create(&row).Error; e != nil {
			return e
		}
		if e := tx.Exec("SELECT set_config('trading_bot.governance_transition_id', ?, true)", row.ID).Error; e != nil {
			return e
		}
		rollbackBytes, _ := json.Marshal(rollbackPolicy)
		if e := tx.Model(&database.GovernanceDeployment{}).Where("context_key=?", request.ContextKey).Updates(map[string]any{"state": string(StateRollback), "artifact_version": request.FallbackVersion, "activated_at": now, "updated_at": now, "authority_policy_json": string(rollbackBytes), "authority_policy_digest": rollbackPolicy.Digest, "transition_id": row.ID}).Error; e != nil {
			return e
		}
		if manifest.Spec.Model != nil {
			if e := writeModelAuthorityProjection(tx, request.FallbackVersion, StateRollback); e != nil {
				return e
			}
		}
		result = row
		return nil
	})
	return result, err
}

func (s Service) boundEvidence(manifestID, evidenceID string) (validation.ExperimentManifest, validation.PersistedEvidence, error) {
	m, e := s.Validation.LoadManifest(manifestID)
	if e != nil {
		return m, validation.PersistedEvidence{}, e
	}
	v, e := s.Validation.LoadEvidence(evidenceID)
	if e != nil {
		return m, v, e
	}
	if v.ExperimentID != m.ID {
		return m, v, &Error{Code: CodeVersionMismatch, Details: "evidence is not bound to experiment"}
	}
	return m, v, nil
}

func (s Service) verifyMLEvidence(m validation.ExperimentManifest, v validation.PersistedEvidence) error {
	if m.Spec.Model == nil {
		return nil
	}
	ml, err := s.Validation.LoadMLEvidenceForExperiment(m.ID)
	if err != nil {
		return &Error{Code: CodeEvidenceFailed, Details: "immutable passing ML evidence is required"}
	}
	if v.Result == nil {
		return &Error{Code: CodeEvidenceFailed, Details: "walk-forward result missing"}
	}
	foldDigest, digestErr := validation.FoldResultsDigest(v.Result.Folds)
	if digestErr != nil || foldDigest != ml.FoldEvidenceDigest || ml.Provenance.ArtifactDigest != m.Spec.Model.ModelDigest || ml.Provenance.TrainingManifestDigest != m.Spec.Model.TrainingManifest || ml.Provenance.BaselineStrategy != m.Spec.Baseline || ml.Provenance.BaselinePolicyDigest != m.Spec.Policies.Composite || ml.Provenance.DatasetManifestDigest != m.Spec.DatasetManifestHash || !ml.Evaluation.Passed {
		return &Error{Code: CodeEvidenceFailed, Details: "ML evidence binding or gates failed"}
	}
	return nil
}
func matchVersions(m validation.ExperimentManifest, artifact, policy string) error {
	expected := m.Spec.Candidate.Version
	if m.Spec.Model != nil {
		expected = m.Spec.Model.Version
	}
	if artifact != expected || policy != m.Spec.Policies.Composite {
		return &Error{Code: CodeVersionMismatch, Details: "artifact or policy differs from immutable experiment"}
	}
	return nil
}
func authorityContext(m validation.ExperimentManifest) string {
	if m.Spec.Model != nil {
		return "model:" + m.Spec.Model.Version
	}
	return "strategy:" + m.Spec.Candidate.ID + "@" + m.Spec.Candidate.Version
}
func legal(from, to State) bool {
	if from == "" {
		return to == StateResearch
	}
	return map[State]State{StateResearch: StateShadow, StateShadow: StatePaper, StatePaper: StateLimitedLive, StateLimitedLive: StateFullLive}[from] == to
}
func knownState(s State) bool {
	return s == StateResearch || s == StateShadow || s == StatePaper || s == StateLimitedLive || s == StateFullLive || s == StateRollback
}
func hash(v []byte) string { x := sha256.Sum256(v); return hex.EncodeToString(x[:]) }
func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func approvalDigest(row database.GovernanceApproval) string {
	payload := struct {
		IdempotencyKey        string    `json:"idempotency_key"`
		ExperimentID          string    `json:"experiment_id"`
		EvidenceID            string    `json:"evidence_id"`
		TargetState           string    `json:"target_state"`
		ArtifactVersion       string    `json:"artifact_version"`
		PolicyVersion         string    `json:"policy_version"`
		Approver              string    `json:"approver"`
		Reason                string    `json:"reason"`
		ApprovedAt            time.Time `json:"approved_at"`
		SourceState           string    `json:"source_state"`
		SourceActivatedAt     time.Time `json:"source_activated_at"`
		AuthorityPolicyDigest string    `json:"authority_policy_digest"`
		EvidenceDigest        string    `json:"evidence_digest"`
		ExpiresAt             time.Time `json:"expires_at"`
	}{row.IdempotencyKey, row.ExperimentID, row.EvidenceID, row.TargetState, row.ArtifactVersion, row.PolicyVersion, row.Approver, row.Reason, row.ApprovedAt.UTC(), row.SourceState, row.SourceActivatedAt.UTC(), row.AuthorityPolicyDigest, row.EvidenceDigest, row.ExpiresAt.UTC()}
	encoded, _ := json.Marshal(payload)
	return hash(encoded)
}

func transitionDigest(row database.GovernanceTransition) string {
	payload := struct {
		IdempotencyKey, ContextKey, ExperimentID, EvidenceID, FromState, ToState, ArtifactVersion, PolicyVersion, FallbackVersion, Reason, AuthorityPolicyDigest, ApprovalDigest, EvidenceDigest, MonitoringEvidenceDigest, Actor string
		ApprovalID, MonitoringEvidenceID                                                                                                                                                                                          *string
		CreatedAt                                                                                                                                                                                                                 time.Time
	}{row.IdempotencyKey, row.ContextKey, row.ExperimentID, row.EvidenceID, row.FromState, row.ToState, row.ArtifactVersion, row.PolicyVersion, row.FallbackVersion, row.Reason, row.AuthorityPolicyDigest, row.ApprovalDigest, row.EvidenceDigest, row.MonitoringEvidenceDigest, row.Actor, row.ApprovalID, row.MonitoringEvidenceID, row.CreatedAt.UTC()}
	encoded, _ := json.Marshal(payload)
	return hash(encoded)
}

func VerifyApproval(row database.GovernanceApproval) error {
	if row.ContentDigest != approvalDigest(row) || row.ID != hash([]byte(row.ContentDigest)) {
		return &Error{Code: CodeIntegrity, Details: "approval digest mismatch"}
	}
	return nil
}
func VerifyTransition(row database.GovernanceTransition) error {
	if row.ContentDigest != transitionDigest(row) || row.ID != hash([]byte(row.ContentDigest)) {
		return &Error{Code: CodeIntegrity, Details: "transition digest mismatch"}
	}
	return nil
}
func VerifyDeployment(db *gorm.DB, row database.GovernanceDeployment) error {
	var policy validation.AuthorityPolicyEnvelope
	if json.Unmarshal([]byte(row.AuthorityPolicyJSON), &policy) != nil || policy.Verify() != nil || policy.Digest != row.AuthorityPolicyDigest {
		return &Error{Code: CodeIntegrity, Details: "deployment policy digest mismatch"}
	}
	var transition database.GovernanceTransition
	if db.Where("id=?", row.TransitionID).First(&transition).Error != nil || VerifyTransition(transition) != nil || transition.ContextKey != row.ContextKey || transition.ToState != row.State || transition.AuthorityPolicyDigest != row.AuthorityPolicyDigest {
		return &Error{Code: CodeIntegrity, Details: "deployment transition mismatch"}
	}
	return nil
}

func (s Service) evidenceDigest(id string) (string, error) { return s.evidenceDigestTx(s.DB, id) }
func (s Service) evidenceDigestTx(tx *gorm.DB, id string) (string, error) {
	var row database.ValidationEvidence
	if err := tx.Where("id=?", id).First(&row).Error; err != nil {
		return "", err
	}
	return row.EvidenceDigest, nil
}

type MonitoringEvidenceRequest struct {
	ContextKey                                 string
	ExperimentID                               string
	WindowStart, WindowEnd                     time.Time
	ExpectedObservations, ObservedObservations int
	Metrics                                    map[string]float64
}

func (s Service) RecordMonitoringEvidence(principal Principal, request MonitoringEvidenceRequest) (database.GovernanceMonitoringEvidence, error) {
	if err := principal.require(CapabilityRollback); err != nil {
		return database.GovernanceMonitoringEvidence{}, err
	}
	now := s.now()
	var deployment database.GovernanceDeployment
	if err := s.DB.Where("context_key=?", request.ContextKey).First(&deployment).Error; err != nil {
		return database.GovernanceMonitoringEvidence{}, err
	}
	if deployment.ExperimentID != request.ExperimentID || request.WindowStart.Before(deployment.ActivatedAt) || !request.WindowEnd.After(request.WindowStart) || request.WindowEnd.After(now) || request.ExpectedObservations <= 0 || request.ObservedObservations < 0 || request.ObservedObservations > request.ExpectedObservations {
		return database.GovernanceMonitoringEvidence{}, &Error{Code: CodeIntegrity, Details: "invalid monitoring evidence envelope"}
	}
	for key, value := range request.Metrics {
		if key == "" || !finite(value) {
			return database.GovernanceMonitoringEvidence{}, &Error{Code: CodeIntegrity, Details: "non-finite monitoring metric"}
		}
	}
	metrics, _ := json.Marshal(request.Metrics)
	row := database.GovernanceMonitoringEvidence{ContextKey: request.ContextKey, ExperimentID: request.ExperimentID, DeploymentTransitionID: deployment.TransitionID, AuthorityPolicyDigest: deployment.AuthorityPolicyDigest, ArtifactVersion: deployment.ArtifactVersion, WindowStart: request.WindowStart.UTC(), WindowEnd: request.WindowEnd.UTC(), ExpectedObservations: request.ExpectedObservations, ObservedObservations: request.ObservedObservations, MetricsJSON: string(metrics), CreatedAt: now}
	row.ContentDigest = monitoringDigest(row)
	row.ID = hash([]byte(row.ContentDigest))
	if err := s.DB.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).Create(&row).Error; err != nil {
		return database.GovernanceMonitoringEvidence{}, err
	}
	return loadMonitoringEvidence(s.DB, row.ID)
}

func monitoringDigest(row database.GovernanceMonitoringEvidence) string {
	metricsJSON := row.MetricsJSON
	var metrics any
	if json.Unmarshal([]byte(row.MetricsJSON), &metrics) == nil {
		if canonical, err := json.Marshal(metrics); err == nil {
			metricsJSON = string(canonical)
		}
	}
	payload := struct {
		ContextKey, ExperimentID, DeploymentTransitionID, AuthorityPolicyDigest, ArtifactVersion, MetricsJSON string
		WindowStart, WindowEnd                                                                                time.Time
		ExpectedObservations, ObservedObservations                                                            int
	}{row.ContextKey, row.ExperimentID, row.DeploymentTransitionID, row.AuthorityPolicyDigest, row.ArtifactVersion, metricsJSON, row.WindowStart.UTC(), row.WindowEnd.UTC(), row.ExpectedObservations, row.ObservedObservations}
	encoded, _ := json.Marshal(payload)
	return hash(encoded)
}
func loadMonitoringEvidence(tx *gorm.DB, id string) (database.GovernanceMonitoringEvidence, error) {
	var row database.GovernanceMonitoringEvidence
	if err := tx.Where("id=?", id).First(&row).Error; err != nil {
		return row, &Error{Code: CodeRollbackGate, Details: "monitoring evidence not found"}
	}
	if row.ContentDigest != monitoringDigest(row) || row.ID != hash([]byte(row.ContentDigest)) {
		return row, &Error{Code: CodeIntegrity, Details: "monitoring evidence integrity failed"}
	}
	return row, nil
}
func writeModelAuthorityProjection(tx *gorm.DB, version string, state State) error {
	for key, value := range map[string]string{"active_model_version": version, "model_rollout_state": string(state)} {
		setting := database.Setting{Key: key, Value: value}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"})}).Create(&setting).Error; err != nil {
			return err
		}
	}
	return nil
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func compare(v float64, op string, t float64) bool {
	switch op {
	case ">":
		return v > t
	case ">=":
		return v >= t
	case "<":
		return v < t
	case "<=":
		return v <= t
	}
	return false
}

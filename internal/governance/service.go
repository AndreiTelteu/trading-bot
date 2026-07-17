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
	IdempotencyKey  string `json:"idempotency_key"`
	ExperimentID    string `json:"experiment_id"`
	EvidenceID      string `json:"evidence_id"`
	TargetState     State  `json:"target_state"`
	ArtifactVersion string `json:"artifact_version"`
	PolicyVersion   string `json:"policy_version"`
	Approver        string `json:"approver"`
	Reason          string `json:"reason"`
}

func (s Service) Approve(request ApprovalRequest) (database.GovernanceApproval, error) {
	if s.DB == nil {
		return database.GovernanceApproval{}, fmt.Errorf("governance database is required")
	}
	if request.IdempotencyKey == "" || request.ExperimentID == "" || request.EvidenceID == "" || request.ArtifactVersion == "" || request.PolicyVersion == "" || strings.TrimSpace(request.Approver) == "" || strings.TrimSpace(request.Reason) == "" || !knownState(request.TargetState) {
		return database.GovernanceApproval{}, &Error{Code: CodeApprovalMismatch, Details: "complete approval identity, target, versions, actor, and reason are required"}
	}
	manifest, evidence, err := s.boundEvidence(request.ExperimentID, request.EvidenceID)
	if err != nil {
		return database.GovernanceApproval{}, err
	}
	if evidence.Status != "passed" || evidence.Result == nil || !evidence.Result.Aggregate.Passed {
		return database.GovernanceApproval{}, &Error{Code: CodeEvidenceFailed}
	}
	if err := matchVersions(manifest, request.ArtifactVersion, request.PolicyVersion); err != nil {
		return database.GovernanceApproval{}, err
	}
	now := s.now()
	row := database.GovernanceApproval{IdempotencyKey: request.IdempotencyKey, ExperimentID: request.ExperimentID, EvidenceID: request.EvidenceID, TargetState: string(request.TargetState), ArtifactVersion: request.ArtifactVersion, PolicyVersion: request.PolicyVersion, Approver: strings.TrimSpace(request.Approver), Reason: strings.TrimSpace(request.Reason), ApprovedAt: now}
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
		if existing.ExperimentID != request.ExperimentID || existing.EvidenceID != request.EvidenceID || existing.TargetState != string(request.TargetState) || existing.ArtifactVersion != request.ArtifactVersion || existing.PolicyVersion != request.PolicyVersion || existing.Approver != strings.TrimSpace(request.Approver) || existing.Reason != strings.TrimSpace(request.Reason) {
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
	IdempotencyKey  string `json:"idempotency_key"`
	ContextKey      string `json:"context_key"`
	ExperimentID    string `json:"experiment_id"`
	EvidenceID      string `json:"evidence_id"`
	ApprovalID      string `json:"approval_id"`
	TargetState     State  `json:"target_state"`
	ArtifactVersion string `json:"artifact_version"`
	PolicyVersion   string `json:"policy_version"`
	FallbackVersion string `json:"fallback_version,omitempty"`
	Reason          string `json:"reason"`
}

func (s Service) Transition(request TransitionRequest) (database.GovernanceTransition, error) {
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
	if err := matchVersions(manifest, request.ArtifactVersion, request.PolicyVersion); err != nil {
		return database.GovernanceTransition{}, err
	}
	if request.ContextKey != authorityContext(manifest) {
		return database.GovernanceTransition{}, &Error{Code: CodeVersionMismatch, Details: "context key does not match immutable experiment authority"}
	}
	now := s.now()
	var result database.GovernanceTransition
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		var replay database.GovernanceTransition
		if e := tx.Where("idempotency_key=?", request.IdempotencyKey).First(&replay).Error; e == nil {
			if replay.ContextKey != request.ContextKey || replay.ExperimentID != request.ExperimentID || replay.EvidenceID != request.EvidenceID || replay.ToState != string(request.TargetState) || replay.ArtifactVersion != request.ArtifactVersion || replay.FallbackVersion != request.FallbackVersion || replay.Reason != request.Reason {
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
			if approval.ExperimentID != request.ExperimentID || approval.EvidenceID != request.EvidenceID || approval.TargetState != string(request.TargetState) || approval.ArtifactVersion != request.ArtifactVersion || approval.PolicyVersion != request.PolicyVersion || approval.ApprovedAt.Before(evidence.CreatedAt) {
				return &Error{Code: CodeApprovalMismatch, Details: "approval is stale or bound to different evidence/target/versions"}
			}
			if approval.ContentDigest != approvalDigest(approval) || approval.ID != hash([]byte(approval.ContentDigest)) {
				return &Error{Code: CodeApprovalMismatch, Details: "approval integrity check failed"}
			}
		}
		if (request.TargetState == StatePaper || request.TargetState == StateLimitedLive || request.TargetState == StateFullLive) && manifest.Spec.Model != nil && !manifest.Spec.Model.CanHoldAuthority() {
			return &Error{Code: CodeArtifactQuarantined, Details: string(manifest.Spec.Model.Class)}
		}
		if elapsed := manifest.Spec.RequiredElapsed[string(request.TargetState)]; elapsed > 0 {
			if from == "" || now.Sub(current.ActivatedAt) < elapsed {
				return &Error{Code: CodeElapsedEvidence, Details: fmt.Sprintf("required=%s observed=%s", elapsed, now.Sub(current.ActivatedAt))}
			}
		}
		approvalID := request.ApprovalID
		created := now
		payload := struct {
			TransitionRequest
			From      State     `json:"from_state"`
			CreatedAt time.Time `json:"created_at"`
		}{request, from, created}
		encoded, _ := json.Marshal(payload)
		id := hash(encoded)
		row := database.GovernanceTransition{ID: id, IdempotencyKey: request.IdempotencyKey, ContextKey: request.ContextKey, ExperimentID: request.ExperimentID, EvidenceID: request.EvidenceID, FromState: string(from), ToState: string(request.TargetState), ArtifactVersion: request.ArtifactVersion, FallbackVersion: request.FallbackVersion, Reason: request.Reason, ContentDigest: hash(encoded), CreatedAt: created}
		if approvalID != "" {
			row.ApprovalID = &approvalID
		}
		if e := tx.Create(&row).Error; e != nil {
			return e
		}
		deployment := database.GovernanceDeployment{ContextKey: request.ContextKey, ExperimentID: request.ExperimentID, EvidenceID: request.EvidenceID, State: string(request.TargetState), ArtifactVersion: request.ArtifactVersion, PolicyVersion: request.PolicyVersion, FallbackVersion: request.FallbackVersion, ActivatedAt: now, UpdatedAt: now}
		if e := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "context_key"}}, DoUpdates: clause.AssignmentColumns([]string{"experiment_id", "evidence_id", "state", "artifact_version", "policy_version", "fallback_version", "activated_at", "updated_at"})}).Create(&deployment).Error; e != nil {
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
	IdempotencyKey  string             `json:"idempotency_key"`
	ContextKey      string             `json:"context_key"`
	ExperimentID    string             `json:"experiment_id"`
	EvidenceID      string             `json:"evidence_id"`
	FallbackVersion string             `json:"fallback_version"`
	Reason          string             `json:"reason"`
	Observed        map[string]float64 `json:"observed"`
}

func (s Service) Rollback(request RollbackRequest) (database.GovernanceTransition, error) {
	if request.IdempotencyKey == "" || request.ContextKey == "" || request.ExperimentID == "" || request.EvidenceID == "" || request.FallbackVersion == "" || request.Reason == "" {
		return database.GovernanceTransition{}, &Error{Code: CodeFallbackRequired}
	}
	manifest, _, err := s.boundEvidence(request.ExperimentID, request.EvidenceID)
	if err != nil {
		return database.GovernanceTransition{}, err
	}
	triggered := false
	for _, threshold := range manifest.Spec.RollbackThresholds {
		value, ok := request.Observed[threshold.Metric]
		if ok && finite(value) && compare(value, threshold.Op, threshold.Value) {
			triggered = true
			break
		}
	}
	if !triggered {
		return database.GovernanceTransition{}, &Error{Code: CodeRollbackGate}
	}
	now := s.now()
	var result database.GovernanceTransition
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		var replay database.GovernanceTransition
		if e := tx.Where("idempotency_key=?", request.IdempotencyKey).First(&replay).Error; e == nil {
			if replay.ContextKey != request.ContextKey || replay.ExperimentID != request.ExperimentID || replay.EvidenceID != request.EvidenceID || replay.ToState != string(StateRollback) || replay.FallbackVersion != request.FallbackVersion || replay.Reason != request.Reason {
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
		payload, _ := json.Marshal(request)
		row := database.GovernanceTransition{ID: hash(append(payload, []byte(now.Format(time.RFC3339Nano))...)), IdempotencyKey: request.IdempotencyKey, ContextKey: request.ContextKey, ExperimentID: request.ExperimentID, EvidenceID: request.EvidenceID, FromState: current.State, ToState: string(StateRollback), ArtifactVersion: current.ArtifactVersion, FallbackVersion: request.FallbackVersion, Reason: request.Reason, ContentDigest: hash(payload), CreatedAt: now}
		if e := tx.Create(&row).Error; e != nil {
			return e
		}
		if e := tx.Model(&database.GovernanceDeployment{}).Where("context_key=?", request.ContextKey).Updates(map[string]any{"state": string(StateRollback), "artifact_version": request.FallbackVersion, "activated_at": now, "updated_at": now}).Error; e != nil {
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
func approvalDigest(row database.GovernanceApproval) string {
	payload := struct {
		IdempotencyKey  string    `json:"idempotency_key"`
		ExperimentID    string    `json:"experiment_id"`
		EvidenceID      string    `json:"evidence_id"`
		TargetState     string    `json:"target_state"`
		ArtifactVersion string    `json:"artifact_version"`
		PolicyVersion   string    `json:"policy_version"`
		Approver        string    `json:"approver"`
		Reason          string    `json:"reason"`
		ApprovedAt      time.Time `json:"approved_at"`
	}{row.IdempotencyKey, row.ExperimentID, row.EvidenceID, row.TargetState, row.ArtifactVersion, row.PolicyVersion, row.Approver, row.Reason, row.ApprovedAt.UTC()}
	encoded, _ := json.Marshal(payload)
	return hash(encoded)
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

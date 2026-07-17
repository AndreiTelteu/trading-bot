package handlers

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"trading-go/internal/backtest"
	"trading-go/internal/cutover"
	"trading-go/internal/database"
	stage07governance "trading-go/internal/governance"
	"trading-go/internal/middleware"
	"trading-go/internal/validation"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func CreateValidationExperiment(c *fiber.Ctx) error {
	if flags, active := cutover.Active(); active && flags.NewBacktest != "research" {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Stage 08 research backtest/validation mode is disabled"})
	}
	var request struct {
		Spec validation.ManifestSpec `json:"spec"`
	}
	if err := c.BodyParser(&request); err != nil {
		return stage07Error(c, err)
	}
	principal, ok := authenticatedGovernancePrincipal(c)
	if !ok || !principal.Has(stage07governance.CapabilityResearch) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "research capability required"})
	}
	key := c.Get("Idempotency-Key")
	if len(key) < 8 || len(key) > 120 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "bounded Idempotency-Key is required"})
	}
	references := make([]backtest.Stage07ComparisonReference, 0, len(request.Spec.FoldSourceJobIDs))
	for _, jobID := range request.Spec.FoldSourceJobIDs {
		reference, loadErr := backtest.LoadStage07ComparisonReference(databaseDB(), jobID)
		if loadErr != nil {
			return stage07Error(c, loadErr)
		}
		if reference.DatasetID != request.Spec.DatasetManifestID || reference.Candidate != request.Spec.Candidate.ID+"@"+request.Spec.Candidate.Version || reference.StrategyDigests[request.Spec.Candidate.ID] != request.Spec.Candidate.Digest || reference.StrategyDigests[request.Spec.Baseline.ID] != request.Spec.Baseline.Digest {
			return stage07Error(c, &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Details: "server-derived Stage 05/06 provenance mismatch"})
		}
		references = append(references, reference)
	}
	referenceBytes, _ := json.Marshal(references)
	sum := sha256.Sum256(referenceBytes)
	comparisonDigest := fmt.Sprintf("%x", sum)
	var firstJob *uint
	if len(request.Spec.FoldSourceJobIDs) > 0 {
		id := request.Spec.FoldSourceJobIDs[0]
		firstJob = &id
	}
	manifest, err := validation.NewManifest(request.Spec, time.Now().UTC())
	if err != nil {
		return stage07Error(c, err)
	}
	created, err := (validation.Repository{DB: databaseDB()}).CreateManifestAuthenticated(manifest, firstJob, &comparisonDigest, principal.ID(), key)
	if err != nil {
		return stage07Error(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(created)
}

func RunValidationExperiment(c *fiber.Ctx) error {
	if flags, active := cutover.Active(); active && flags.NewBacktest != "research" {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Stage 08 research backtest/validation mode is disabled"})
	}
	principal, ok := authenticatedGovernancePrincipal(c)
	if !ok || !principal.Has(stage07governance.CapabilityResearch) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "research capability required"})
	}
	key := c.Get("Idempotency-Key")
	if len(key) < 8 || len(key) > 120 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "bounded Idempotency-Key is required"})
	}
	manifest, err := (validation.Repository{DB: databaseDB()}).LoadManifest(c.Params("id"))
	if err != nil {
		return stage07Error(c, err)
	}
	var job database.BacktestJob
	err = databaseDB().Transaction(func(tx *gorm.DB) error {
		if e := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", key).Error; e != nil {
			return e
		}
		if e := tx.Where("request_key=?", key).First(&job).Error; e == nil {
			if job.SemanticID != manifest.ID {
				return &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Field: "idempotency_key", Details: "key reused for another experiment"}
			}
			return nil
		} else if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		now := time.Now().UTC()
		stage08Context := "{}"
		if flags, active := cutover.Active(); active {
			stage08Context = flags.ObservationContext("stage07_validation", map[string]string{"strategy": manifest.Spec.Candidate.ID + "@" + manifest.Spec.Candidate.Version, "model": manifest.Spec.Model.Version, "policy": manifest.Spec.Policies.Composite, "dataset": manifest.Spec.DatasetManifestID, "universe": manifest.Spec.UniversePolicy})
		}
		job = database.BacktestJob{Status: "queued", JobType: "stage07_validation", Progress: 0, RequestKey: &key, SemanticID: manifest.ID, DatasetManifestID: &manifest.Spec.DatasetManifestID, Stage08ContextJSON: stage08Context, CreatedAt: now, UpdatedAt: now}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_key"}}, DoNothing: true}).Create(&job).Error
	})
	if err != nil {
		return stage07Error(c, err)
	}
	if job.Status == "queued" {
		go executeValidationJob(job.ID, manifest.ID)
	}
	return c.Status(fiber.StatusAccepted).JSON(job)
}

func executeValidationJob(jobID uint, manifestID string) {
	now := time.Now().UTC()
	claimed := databaseDB().Model(&database.BacktestJob{}).Where("id=? AND status=?", jobID, "queued").Updates(map[string]any{"status": "running", "started_at": now, "progress": .1, "updated_at": now})
	if claimed.Error != nil || claimed.RowsAffected != 1 {
		return
	}
	service := validation.JobService{Repository: validation.Repository{DB: databaseDB()}, Source: backtest.Stage07ExperimentSource{DB: databaseDB()}}
	evidence, err := service.Run(manifestID)
	finished := time.Now().UTC()
	updates := map[string]any{"status": "completed", "progress": 1.0, "finished_at": finished, "updated_at": finished}
	if encoded, e := json.Marshal(evidence); e == nil {
		value := string(encoded)
		updates["summary_json"] = value
		updates["summary_compact_json"] = value
		sum := sha256.Sum256(encoded)
		updates["artifact_digest"] = fmt.Sprintf("%x", sum)
	}
	if err != nil {
		updates["status"] = "failed"
		updates["error"] = err.Error()
		if diagnostic, e := json.Marshal(err); e == nil {
			updates["diagnostic_json"] = string(diagnostic)
		}
	}
	databaseDB().Model(&database.BacktestJob{}).Where("id=?", jobID).Updates(updates)
}

func GetValidationExperiment(c *fiber.Ctx) error {
	repo := validation.Repository{DB: databaseDB()}
	manifest, err := repo.LoadManifest(c.Params("id"))
	if err != nil {
		return stage07Error(c, err)
	}
	response := fiber.Map{"manifest": manifest, "ci_available": false, "ci_reason": "validation evidence not yet persisted", "fold_coverage": 0, "governance_state": stage07governance.StateResearch, "approvals": []interface{}{}, "rollback_history": []interface{}{}}
	var row database.ValidationEvidence
	if err := databaseDB().Where("experiment_id=?", manifest.ID).First(&row).Error; err == nil {
		evidence, loadErr := repo.LoadEvidence(row.ID)
		if loadErr != nil {
			return stage07Error(c, loadErr)
		}
		response["evidence"] = evidence
		if evidence.Result != nil {
			response["ci_available"] = evidence.Result.Aggregate.Metrics.AfterCostReturn.Available
			response["ci_reason"] = evidence.Result.Aggregate.Metrics.AfterCostReturn.Reason
			response["fold_coverage"] = len(evidence.Result.Folds)
		}
	}
	var deployment database.GovernanceDeployment
	if err := databaseDB().Where("experiment_id=?", manifest.ID).First(&deployment).Error; err == nil {
		if err := stage07governance.VerifyDeployment(databaseDB(), deployment); err != nil {
			return stage07Error(c, err)
		}
		response["governance_state"] = deployment.State
		response["deployment"] = deployment
	}
	var approvals []database.GovernanceApproval
	databaseDB().Where("experiment_id=?", manifest.ID).Order("approved_at DESC").Limit(20).Find(&approvals)
	for _, approval := range approvals {
		if err := stage07governance.VerifyApproval(approval); err != nil {
			return stage07Error(c, err)
		}
	}
	response["approvals"] = approvals
	var history []database.GovernanceTransition
	databaseDB().Where("experiment_id=? AND to_state=?", manifest.ID, string(stage07governance.StateRollback)).Order("created_at DESC").Limit(20).Find(&history)
	for _, transition := range history {
		if err := stage07governance.VerifyTransition(transition); err != nil {
			return stage07Error(c, err)
		}
	}
	response["rollback_history"] = history
	return c.JSON(response)
}

func ApproveGovernanceTransition(c *fiber.Ctx) error {
	var request stage07governance.ApprovalRequest
	if err := c.BodyParser(&request); err != nil {
		return stage07Error(c, err)
	}
	principal, ok := authenticatedGovernancePrincipal(c)
	if !ok || !principal.Has(stage07governance.CapabilityApprove) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authenticated human approval identity is required"})
	}
	row, err := stage07governance.NewService(databaseDB()).Approve(principal, request)
	if err != nil {
		return stage07Error(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(row)
}
func ApplyGovernanceTransition(c *fiber.Ctx) error {
	var request stage07governance.TransitionRequest
	if err := c.BodyParser(&request); err != nil {
		return stage07Error(c, err)
	}
	principal, ok := authenticatedGovernancePrincipal(c)
	if !ok || !principal.Has(stage07governance.CapabilityTransition) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "governance capability required"})
	}
	row, err := stage07governance.NewService(databaseDB()).Transition(principal, request)
	if err != nil {
		return stage07Error(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(row)
}
func ApplyGovernanceRollback(c *fiber.Ctx) error {
	var request stage07governance.RollbackRequest
	if err := c.BodyParser(&request); err != nil {
		return stage07Error(c, err)
	}
	principal, ok := authenticatedGovernancePrincipal(c)
	if !ok || !principal.Has(stage07governance.CapabilityRollback) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "governance capability required"})
	}
	row, err := stage07governance.NewService(databaseDB()).Rollback(principal, request)
	if err != nil {
		return stage07Error(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(row)
}

func databaseDB() *gorm.DB { return database.DB }
func authenticatedGovernancePrincipal(c *fiber.Ctx) (stage07governance.Principal, bool) {
	actor, ok := middleware.AuthenticatedActor(c)
	if !ok {
		return stage07governance.Principal{}, false
	}
	capabilities := []stage07governance.Capability{}
	for _, capability := range middleware.AuthenticatedCapabilities(c) {
		capabilities = append(capabilities, stage07governance.Capability(capability))
	}
	if len(capabilities) == 0 {
		return stage07governance.Principal{}, false
	}
	return stage07governance.NewTrustedPrincipal(actor, capabilities...), true
}
func stage07Error(c *fiber.Ctx, err error) error {
	status := fiber.StatusUnprocessableEntity
	if errors.Is(err, gorm.ErrRecordNotFound) {
		status = fiber.StatusNotFound
	}
	return c.Status(status).JSON(fiber.Map{"error": err.Error()})
}

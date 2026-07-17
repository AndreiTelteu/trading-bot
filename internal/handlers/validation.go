package handlers

import (
	"errors"
	"time"
	"trading-go/internal/database"
	stage07governance "trading-go/internal/governance"
	"trading-go/internal/middleware"
	"trading-go/internal/validation"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

func CreateValidationExperiment(c *fiber.Ctx) error {
	var request struct {
		Spec             validation.ManifestSpec `json:"spec"`
		BacktestJobID    *uint                   `json:"backtest_job_id,omitempty"`
		ComparisonDigest *string                 `json:"comparison_digest,omitempty"`
	}
	if err := c.BodyParser(&request); err != nil {
		return stage07Error(c, err)
	}
	manifest, err := validation.NewManifest(request.Spec, time.Now().UTC())
	if err != nil {
		return stage07Error(c, err)
	}
	created, err := (validation.Repository{DB: databaseDB()}).CreateManifest(manifest, request.BacktestJobID, request.ComparisonDigest)
	if err != nil {
		return stage07Error(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(created)
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
		response["governance_state"] = deployment.State
		response["deployment"] = deployment
	}
	var approvals []database.GovernanceApproval
	databaseDB().Where("experiment_id=?", manifest.ID).Order("approved_at DESC").Limit(20).Find(&approvals)
	response["approvals"] = approvals
	var history []database.GovernanceTransition
	databaseDB().Where("experiment_id=? AND to_state=?", manifest.ID, string(stage07governance.StateRollback)).Order("created_at DESC").Limit(20).Find(&history)
	response["rollback_history"] = history
	return c.JSON(response)
}

func ApproveGovernanceTransition(c *fiber.Ctx) error {
	var request stage07governance.ApprovalRequest
	if err := c.BodyParser(&request); err != nil {
		return stage07Error(c, err)
	}
	actor, ok := middleware.AuthenticatedActor(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authenticated human approval identity is required"})
	}
	request.Approver = actor
	row, err := stage07governance.NewService(databaseDB()).Approve(request)
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
	row, err := stage07governance.NewService(databaseDB()).Transition(request)
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
	row, err := stage07governance.NewService(databaseDB()).Rollback(request)
	if err != nil {
		return stage07Error(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(row)
}

func databaseDB() *gorm.DB { return database.DB }
func stage07Error(c *fiber.Ctx, err error) error {
	status := fiber.StatusUnprocessableEntity
	if errors.Is(err, gorm.ErrRecordNotFound) {
		status = fiber.StatusNotFound
	}
	return c.Status(status).JSON(fiber.Map{"error": err.Error()})
}

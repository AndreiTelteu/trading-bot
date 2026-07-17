package handlers

import (
	"trading-go/internal/cutover"
	"trading-go/internal/database"
	"trading-go/internal/operations"

	"github.com/gofiber/fiber/v2"
)

func operationsService() (operations.Service, error) {
	flags, ok := cutover.Active()
	if !ok {
		return operations.Service{}, fiber.NewError(fiber.StatusServiceUnavailable, "Stage 08 authority is not initialized")
	}
	return operations.New(database.DB, flags), nil
}
func GetOperationalStatus(c *fiber.Ctx) error {
	s, err := operationsService()
	if err != nil {
		return err
	}
	status := s.Status(c.UserContext())
	code := fiber.StatusOK
	if status.Status != "ok" {
		code = fiber.StatusServiceUnavailable
	}
	return c.Status(code).JSON(status)
}
func TransitionOperationalIncident(c *fiber.Ctx) error {
	var request struct{ State, Reason string }
	if c.BodyParser(&request) != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	actor, ok := c.Locals("authenticated_actor").(string)
	if !ok || actor == "" {
		return c.Status(401).JSON(fiber.Map{"error": "trusted actor required"})
	}
	s, err := operationsService()
	if err != nil {
		return err
	}
	row, err := s.TransitionIncident(c.UserContext(), c.Params("id"), request.State, actor, request.Reason)
	if err != nil {
		return c.Status(409).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(row)
}
func TransitionCutover(c *fiber.Ctx) error {
	var request operations.TransitionRequest
	if c.BodyParser(&request) != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	actor, ok := c.Locals("authenticated_actor").(string)
	if !ok || actor == "" {
		return c.Status(401).JSON(fiber.Map{"error": "trusted actor required"})
	}
	caps, _ := c.Locals("governance_capabilities").([]string)
	authorized := false
	for _, capability := range caps {
		if capability == "transition" || capability == "rollback" {
			authorized = true
		}
	}
	if !authorized {
		return c.Status(403).JSON(fiber.Map{"error": "governance transition capability required"})
	}
	request.Principal = actor
	s, err := operationsService()
	if err != nil {
		return err
	}
	row, err := s.TransitionCutover(c.UserContext(), request)
	if err != nil {
		return c.Status(409).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(row)
}
func PlanLedgerBackfill(c *fiber.Ctx) error {
	s, err := operationsService()
	if err != nil {
		return err
	}
	row, err := s.PlanBackfill(c.UserContext(), "primary")
	if err != nil {
		return c.Status(409).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(row)
}
func ApproveLedgerBackfill(c *fiber.Ctx) error {
	var request struct {
		ReportDigest string `json:"report_digest"`
	}
	if c.BodyParser(&request) != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	actor, ok := c.Locals("authenticated_actor").(string)
	if !ok || actor == "" {
		return c.Status(401).JSON(fiber.Map{"error": "trusted actor required"})
	}
	caps, _ := c.Locals("governance_capabilities").([]string)
	approved := false
	for _, v := range caps {
		approved = approved || v == "approve"
	}
	if !approved {
		return c.Status(403).JSON(fiber.Map{"error": "approval capability required"})
	}
	s, err := operationsService()
	if err != nil {
		return err
	}
	row, err := s.ApproveBackfill(c.UserContext(), c.Params("id"), request.ReportDigest, actor)
	if err != nil {
		return c.Status(409).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(row)
}
func ApplyLedgerBackfill(c *fiber.Ctx) error {
	var request struct {
		ApprovalDigest string `json:"approval_digest"`
	}
	if c.BodyParser(&request) != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	caps, _ := c.Locals("governance_capabilities").([]string)
	authorized := false
	for _, v := range caps {
		authorized = authorized || v == "transition"
	}
	if !authorized {
		return c.Status(403).JSON(fiber.Map{"error": "transition capability required"})
	}
	s, err := operationsService()
	if err != nil {
		return err
	}
	row, err := s.ApplyBackfill(c.UserContext(), c.Params("id"), request.ApprovalDigest)
	if err != nil {
		return c.Status(409).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(row)
}

func DeclareParityPolicy(c *fiber.Ctx) error {
	var request operations.DeclareParityPolicyRequest
	if c.BodyParser(&request) != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	actor, ok := c.Locals("authenticated_actor").(string)
	if !ok || actor == "" {
		return c.Status(401).JSON(fiber.Map{"error": "trusted actor required"})
	}
	caps, _ := c.Locals("governance_capabilities").([]string)
	authorized := false
	for _, v := range caps {
		authorized = authorized || v == "approve"
	}
	if !authorized {
		return c.Status(403).JSON(fiber.Map{"error": "approval capability required"})
	}
	s, err := operationsService()
	if err != nil {
		return err
	}
	row, err := s.DeclareParityPolicy(c.UserContext(), request, actor)
	if err != nil {
		return c.Status(409).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(row)
}

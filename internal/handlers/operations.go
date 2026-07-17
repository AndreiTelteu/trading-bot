package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"trading-go/internal/cutover"
	"trading-go/internal/database"
	"trading-go/internal/operations"

	"github.com/gofiber/fiber/v2"
)

const maxOperationsBody = 64 << 10

func decodeOperationsJSON(c *fiber.Ctx, dst any) error {
	body := c.Body()
	if len(body) == 0 || len(body) > maxOperationsBody {
		return fmt.Errorf("request body must be between 1 byte and 64KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("trailing JSON data is forbidden")
	}
	return nil
}
func requireOperationsCapability(c *fiber.Ctx, required string) error {
	caps, _ := c.Locals("governance_capabilities").([]string)
	for _, v := range caps {
		if v == required {
			return nil
		}
	}
	return fiber.NewError(fiber.StatusForbidden, "trusted operations capability required")
}

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
	if err := requireOperationsCapability(c, "operations:mutate"); err != nil {
		return err
	}
	var request struct{ State, Reason string }
	if decodeOperationsJSON(c, &request) != nil {
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
	if decodeOperationsJSON(c, &request) != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	if err := requireOperationsCapability(c, "operations:mutate"); err != nil {
		return err
	}
	if len(request.IdempotencyKey) > 160 || len(request.Reason) > 1000 || len(request.EvidenceIDs) > 32 {
		return c.Status(400).JSON(fiber.Map{"error": "cutover request bounds exceeded"})
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
	if err := requireOperationsCapability(c, "operations:mutate"); err != nil {
		return err
	}
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
	if decodeOperationsJSON(c, &request) != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	if err := requireOperationsCapability(c, "operations:mutate"); err != nil {
		return err
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
	if decodeOperationsJSON(c, &request) != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	if err := requireOperationsCapability(c, "operations:mutate"); err != nil {
		return err
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
	if decodeOperationsJSON(c, &request) != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	if err := requireOperationsCapability(c, "operations:mutate"); err != nil {
		return err
	}
	if len(request.Name) > 120 || len(request.Expected) > 64 {
		return c.Status(400).JSON(fiber.Map{"error": "parity policy bounds exceeded"})
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

func DeclareCutoverEvidence(c *fiber.Ctx) error {
	if err := requireOperationsCapability(c, "operations:mutate"); err != nil {
		return err
	}
	var request operations.PrerequisiteEvidenceRequest
	if err := decodeOperationsJSON(c, &request); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	actor, ok := c.Locals("authenticated_actor").(string)
	if !ok || actor == "" {
		return c.Status(401).JSON(fiber.Map{"error": "trusted actor required"})
	}
	service, err := operationsService()
	if err != nil {
		return err
	}
	row, err := service.DeclarePrerequisiteEvidence(c.UserContext(), request, actor)
	if err != nil {
		return c.Status(409).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(row)
}
func DeclareStage08FlagSnapshot(c *fiber.Ctx) error {
	if err := requireOperationsCapability(c, "operations:mutate"); err != nil {
		return err
	}
	var flags cutover.Flags
	if err := decodeOperationsJSON(c, &flags); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	actor, ok := c.Locals("authenticated_actor").(string)
	if !ok || actor == "" {
		return c.SendStatus(401)
	}
	service, err := operationsService()
	if err != nil {
		return err
	}
	row, err := service.DeclareFlagSnapshot(c.UserContext(), flags, actor)
	if err != nil {
		return c.Status(409).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(row)
}

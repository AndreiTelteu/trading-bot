package handlers

import (
	"strconv"
	"trading-go/internal/services"

	"github.com/gofiber/fiber/v2"
)

func GetAIProposals(c *fiber.Ctx) error {
	proposals, err := services.GetAllProposals()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch proposals: " + err.Error()})
	}
	return c.JSON(proposals)
}

func ApproveProposal(c *fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid proposal ID"})
	}

	result, err := services.ApproveProposal(uint(id))
	if err != nil {
		if fiberErr, ok := err.(*fiber.Error); ok {
			return c.Status(fiberErr.Code).JSON(fiber.Map{"error": fiberErr.Message})
		}
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(result)
}

func DenyProposal(c *fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid proposal ID"})
	}

	result, err := services.DenyProposal(uint(id))
	if err != nil {
		if fiberErr, ok := err.(*fiber.Error); ok {
			return c.Status(fiberErr.Code).JSON(fiber.Map{"error": fiberErr.Message})
		}
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(result)
}

func GenerateProposals(c *fiber.Ctx) error {
	result, err := services.GenerateProposals()
	if err != nil {
		if fiberErr, ok := err.(*fiber.Error); ok {
			return c.Status(fiberErr.Code).JSON(fiber.Map{"error": fiberErr.Message})
		}
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(result)
}

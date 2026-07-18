package handlers

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
)

type timeIDCursor struct {
	Time     time.Time `json:"time"`
	ID       uint      `json:"id"`
	Boundary time.Time `json:"boundary,omitempty"`
}

type stringIDCursor struct {
	Value string `json:"value"`
	ID    uint   `json:"id"`
}

func pageLimit(c *fiber.Ctx, fallback, maximum int) (int, error) {
	value := fallback
	if raw := c.Query("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maximum {
			return 0, fmt.Errorf("limit must be an integer from 1 through %d", maximum)
		}
		value = parsed
	}
	return value, nil
}

func encodeCursor(value any) string {
	payload, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeCursor(raw string, target any) error {
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || json.Unmarshal(payload, target) != nil {
		return fmt.Errorf("invalid cursor")
	}
	return nil
}

func advertiseNext(c *fiber.Ctx, next string) {
	c.Set("X-Next-Cursor", next)
	c.Set("X-Result-Truncated", strconv.FormatBool(next != ""))
}

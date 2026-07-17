package middleware

import (
	"crypto/subtle"
	"path/filepath"
	"strings"
	"time"
	"trading-go/internal/config"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/session"
)

const (
	authenticatedSessionKey = "authenticated"
	usernameSessionKey      = "username"
)

type AuthManager struct {
	store            *session.Store
	username         string
	password         string
	governanceAdmins map[string]struct{}
}

func NewAuthManager(cfg *config.Config) *AuthManager {
	admins := map[string]struct{}{}
	for _, user := range strings.Split(cfg.GovernanceAdminUsers, ",") {
		if user = strings.TrimSpace(user); user != "" {
			admins[user] = struct{}{}
		}
	}
	return &AuthManager{
		store: session.New(session.Config{
			CookieHTTPOnly: true,
			CookiePath:     "/",
			CookieSameSite: "Lax",
			Expiration:     24 * time.Hour,
			KeyLookup:      "cookie:" + cfg.SessionCookie,
		}),
		username:         cfg.AuthUsername,
		password:         cfg.AuthPassword,
		governanceAdmins: admins,
	}
}

func (a *AuthManager) FrontendRouteGuard(c *fiber.Ctx) error {
	if c.Method() != fiber.MethodGet {
		return c.Next()
	}

	path := c.Path()
	if strings.HasPrefix(path, "/api") || strings.HasPrefix(path, "/ws") || strings.HasPrefix(path, "/assets") || path == "/favicon.ico" || filepath.Ext(path) != "" {
		return c.Next()
	}

	authenticated, _, err := a.sessionState(c)
	if err != nil {
		return err
	}

	if path == "/login" {
		if authenticated {
			return c.Redirect("/", fiber.StatusFound)
		}
		return c.Next()
	}

	if !authenticated {
		return c.Redirect("/login", fiber.StatusFound)
	}

	return c.Next()
}

func (a *AuthManager) RequireAuth(c *fiber.Ctx) error {
	authenticated, username, err := a.sessionState(c)
	if err != nil {
		return err
	}
	if !authenticated {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "authentication required",
		})
	}
	c.Locals("authenticated_actor", username)
	if _, ok := a.governanceAdmins[username]; ok {
		c.Locals("governance_capabilities", []string{"research", "approve", "transition", "rollback"})
	} else {
		c.Locals("governance_capabilities", []string{})
	}
	return c.Next()
}

func AuthenticatedCapabilities(c *fiber.Ctx) []string {
	values, _ := c.Locals("governance_capabilities").([]string)
	return append([]string(nil), values...)
}

func AuthenticatedActor(c *fiber.Ctx) (string, bool) {
	value, ok := c.Locals("authenticated_actor").(string)
	return value, ok && strings.TrimSpace(value) != ""
}

func (a *AuthManager) HandleLogin(c *fiber.Ctx) error {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := c.BodyParser(&body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid login payload")
	}

	if subtle.ConstantTimeCompare([]byte(body.Username), []byte(a.username)) != 1 || subtle.ConstantTimeCompare([]byte(body.Password), []byte(a.password)) != 1 {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "invalid credentials",
		})
	}

	sess, err := a.store.Get(c)
	if err != nil {
		return err
	}

	sess.Set(authenticatedSessionKey, true)
	sess.Set(usernameSessionKey, body.Username)
	if err := sess.Save(); err != nil {
		return err
	}

	return c.JSON(fiber.Map{
		"success":  true,
		"username": body.Username,
	})
}

func (a *AuthManager) HandleLogout(c *fiber.Ctx) error {
	sess, err := a.store.Get(c)
	if err != nil {
		return err
	}

	if err := sess.Destroy(); err != nil {
		return err
	}

	return c.JSON(fiber.Map{
		"success": true,
	})
}

func (a *AuthManager) HandleSession(c *fiber.Ctx) error {
	authenticated, username, err := a.sessionState(c)
	if err != nil {
		return err
	}

	if !authenticated {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"authenticated": false,
			"error":         "authentication required",
		})
	}

	return c.JSON(fiber.Map{
		"authenticated": true,
		"username":      username,
	})
}

func (a *AuthManager) sessionState(c *fiber.Ctx) (bool, string, error) {
	sess, err := a.store.Get(c)
	if err != nil {
		return false, "", err
	}

	authenticated, _ := sess.Get(authenticatedSessionKey).(bool)
	username, _ := sess.Get(usernameSessionKey).(string)
	return authenticated, username, nil
}

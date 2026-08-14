package api

import (
	"log/slog"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
)

// What broke in the browser, written into the server's log.
//
// A screen that fails says its message to whoever is looking at it, and that
// person is not the one who can fix it. This is the shortest path from "I get
// an error" to the sentence the error actually said.
//
// It is deliberately small: signed in only, a few hundred characters, nothing
// stored in the database. The log is where a fault belongs; a table would only
// grow.
func (s *Server) mountClientErrors(r fiber.Router) {
	r.Post("/client-errors", func(c *fiber.Ctx) error {
		actor := auth.From(c)
		if !actor.IsUser() {
			return httpx.Unauthorized("Sign in first.")
		}
		var in struct {
			Message string `json:"message"`
			Where   string `json:"where"`
			Stack   string `json:"stack"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The report could not be read.")
		}
		clip := func(v string, n int) string {
			v = strings.TrimSpace(v)
			if len(v) > n {
				return v[:n] + "…"
			}
			return v
		}
		slog.Error("the browser could not draw a screen",
			"user", actor.User.Username,
			"where", clip(in.Where, 200),
			"message", clip(in.Message, 500),
			"stack", clip(in.Stack, 1500),
			"agent", clip(c.Get("User-Agent"), 120),
		)
		return httpx.OK(c)
	})
}

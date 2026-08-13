package capability

import (
	"github.com/gofiber/fiber/v2"
	"github.com/offlinebot/home-projects/backend/internal/access"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
)

// The core resolves the project and its group before it hands a request to a
// capability, and puts both here. A capability therefore never looks a project
// up itself, and never has to repeat the permission check.

const (
	projectKey = "capability.project"
	groupKey   = "capability.group"
)

func SetProject(c *fiber.Ctx, p *model.Project, g *model.Group) {
	c.Locals(projectKey, p)
	if g != nil {
		c.Locals(groupKey, g)
	}
}

// Project returns the project this request is about.
func Project(c *fiber.Ctx) *model.Project {
	if p, ok := c.Locals(projectKey).(*model.Project); ok {
		return p
	}
	return nil
}

func Group(c *fiber.Ctx) *model.Group {
	if g, ok := c.Locals(groupKey).(*model.Group); ok {
		return g
	}
	return nil
}

func Actor(c *fiber.Ctx) *auth.Actor { return auth.From(c) }

// RequireWrite is the guard a capability puts in front of anything that
// changes a file. Read-only, archived and visitor rules are all folded in.
func RequireWrite(c *fiber.Ctx) error {
	p := Project(c)
	if p == nil {
		return httpx.NotFound("No project at this address.")
	}
	return access.RequireWriteProject(auth.From(c), p, Group(c))
}

// RequireRead mirrors it for reads.
func RequireRead(c *fiber.Ctx) error {
	p := Project(c)
	if p == nil {
		return httpx.NotFound("No project at this address.")
	}
	return access.RequireReadProject(auth.From(c), p)
}

// AuthorOf names the commit author for a change made through the API.
func AuthorOf(c *fiber.Ctx) (name, email string) {
	a := auth.From(c)
	if a.IsUser() {
		name = a.User.DisplayName
		if name == "" {
			name = a.User.Username
		}
		return name, a.User.Username + "@home-projects"
	}
	return "a visitor", "anonymous@home-projects"
}

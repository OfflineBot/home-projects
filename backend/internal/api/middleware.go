package api

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/access"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/store"
)

// resolveProject turns the :project parameter into a project, checks that the
// actor may read it, and puts it where the handlers and the capabilities find
// it.
//
// The parameter may be the id or the slug. A slug is only accepted when it is
// unambiguous — two projects in different groups may share one, and guessing
// which was meant is exactly the kind of doubling this server does not do.
func (s *Server) resolveProject(extra ...fiber.Handler) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ref := c.Params("project")
		project, err := s.lookupProject(c, ref)
		if err != nil {
			return err
		}
		var group *model.Group
		if project.GroupID != nil {
			group, err = s.Store.GroupByID(c.UserContext(), *project.GroupID)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return httpx.Internal("the group could not be read").WithCause(err)
			}
		}
		// The door to a locked project is the one thing that has to be
		// reachable while it is locked: refusing the unlock because it is
		// locked leaves nobody a way in.
		unlocking := c.Method() == fiber.MethodPost && strings.HasSuffix(c.Path(), "/unlock")
		if !unlocking {
			if err := access.RequireReadProject(auth.From(c), project); err != nil {
				return err
			}
		}
		capability.SetProject(c, project, group)
		for _, h := range extra {
			if err := h(c); err != nil {
				return err
			}
		}
		return c.Next()
	}
}

func (s *Server) lookupProject(c *fiber.Ctx, ref string) (*model.Project, error) {
	ctx := c.UserContext()
	if id, err := uuid.Parse(ref); err == nil {
		p, err := s.Store.ProjectByID(ctx, id)
		if err != nil {
			return nil, httpx.NotFound("No project at this address.")
		}
		return p, nil
	}
	matches, err := s.Store.ProjectsBySlug(ctx, ref)
	if err != nil {
		return nil, httpx.Internal("the project could not be read").WithCause(err)
	}
	switch len(matches) {
	case 0:
		return nil, httpx.NotFound("No project at this address.")
	case 1:
		return &matches[0], nil
	}
	groups := make([]string, 0, len(matches))
	for _, m := range matches {
		g := m.GroupSlug
		if g == "" {
			g = "ungrouped"
		}
		groups = append(groups, g)
	}
	return nil, httpx.Conflict("Several projects are called %q — use the address with its group, or the id.", ref).
		WithDetail(fiber.Map{"groups": groups})
}

// project returns what resolveProject put in place.
func project(c *fiber.Ctx) *model.Project { return capability.Project(c) }
func group(c *fiber.Ctx) *model.Group     { return capability.Group(c) }

// resolveGroup does the same for the :group parameter.
func (s *Server) resolveGroup(c *fiber.Ctx) error {
	ref := c.Params("group")
	ctx := c.UserContext()

	var g *model.Group
	var err error
	if id, uerr := uuid.Parse(ref); uerr == nil {
		g, err = s.Store.GroupByID(ctx, id)
	} else {
		g, err = s.Store.GroupBySlug(ctx, ref)
	}
	if err != nil {
		return httpx.NotFound("No group at this address.")
	}
	if err := access.RequireReadGroup(auth.From(c), g); err != nil {
		return err
	}
	c.Locals("group", g)
	return c.Next()
}

func groupOf(c *fiber.Ctx) *model.Group {
	if g, ok := c.Locals("group").(*model.Group); ok {
		return g
	}
	return nil
}

// requireOwner guards everything that is not for visitors.
func requireOwner(c *fiber.Ctx) error {
	if !auth.From(c).IsUser() {
		return httpx.Unauthorized("Please sign in.")
	}
	return c.Next()
}

// stepUp is the guard in front of the sensitive steps: deleting, changing
// visibility, touching credentials, creating a token.
func (s *Server) stepUp(c *fiber.Ctx, action string) error {
	return s.Auth.RequireStepUp(auth.From(c), action)
}

// authorOf names the git author for changes made through the API.
func authorOf(c *fiber.Ctx) (string, string) { return capability.AuthorOf(c) }

// writable folds the read-only and visitor rules into one call.
func writable(c *fiber.Ctx) error {
	return access.RequireWriteProject(auth.From(c), project(c), group(c))
}

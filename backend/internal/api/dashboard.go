package api

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/access"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/variables"
)

// What every group collects, and what somebody has put away.
//
// The arranged part of this page moved out to boards, where it belongs: this
// answers the other half — everything the projects report, which is what the
// "add a card" dialog picks from, and the list of things taken off the board.
func (s *Server) mountDashboard(r fiber.Router) {
	g := r.Group("/dashboard")

	g.Get("/", func(c *fiber.Ctx) error {
		ctx := c.UserContext()
		actor := auth.From(c)

		groups, err := s.Store.ListGroups(ctx, false)
		if err != nil {
			return httpx.Internal("the groups could not be read").WithCause(err)
		}
		groups = access.FilterGroups(actor, groups)

		visible := s.visibilityFilter(c)
		type groupBlock struct {
			Group     model.Group         `json:"group"`
			Variables []model.Variable    `json:"variables"`
			Derived   []variables.Derived `json:"derived"`
		}
		blocks := []groupBlock{}
		for _, grp := range groups {
			view, err := variables.ForGroup(ctx, s.Store, grp.ID, visible)
			if err != nil {
				return httpx.Internal("the variables could not be read").WithCause(err)
			}
			blocks = append(blocks, groupBlock{Group: grp, Variables: view.Variables, Derived: view.Derived})
		}

		return c.JSON(fiber.Map{"groups": blocks})
	})

	// The visual map: which projects hang in which groups, and which links run
	// between them.
	r.Get("/structure", func(c *fiber.Ctx) error {
		ctx := c.UserContext()
		actor := auth.From(c)

		groups, err := s.Store.ListGroups(ctx, true)
		if err != nil {
			return httpx.Internal("the groups could not be read").WithCause(err)
		}
		groups = access.FilterGroups(actor, groups)

		projects, err := s.Store.ListAllProjects(ctx, true)
		if err != nil {
			return httpx.Internal("the projects could not be read").WithCause(err)
		}
		projects = access.FilterProjects(actor, projects)

		links := []model.Link{}
		if actor.IsUser() {
			links, err = s.Store.AllLinks(ctx)
			if err != nil {
				return httpx.Internal("the links could not be read").WithCause(err)
			}
		}
		return c.JSON(fiber.Map{"groups": groups, "projects": projects, "links": links})
	})
}

// visibilityFilter answers "may this actor see that project's variables?".
// Visibility applies on the dashboard too: nothing from a project someone may
// not open shows up here.
func (s *Server) visibilityFilter(c *fiber.Ctx) func(uuid.UUID) bool {
	actor := auth.From(c)
	ctx := c.UserContext()
	cache := map[uuid.UUID]bool{}
	return func(id uuid.UUID) bool {
		if allowed, ok := cache[id]; ok {
			return allowed
		}
		p, err := s.Store.ProjectByID(ctx, id)
		allowed := err == nil && access.CanReadProject(actor, p)
		cache[id] = allowed
		return allowed
	}
}

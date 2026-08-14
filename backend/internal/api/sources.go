package api

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/access"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
)

// What a project gathers.
//
// A calendar that shows five calendars is not a special kind of calendar. It is
// an ordinary project that has been told which other projects to gather, and a
// view that knows what to do with that. The same list serves any capability
// that can merge: nothing here knows what a calendar is.
//
// Visibility is not widened by gathering: a source that the person looking may
// not read is not in the answer. Otherwise a public "main calendar" would be a
// way to read private ones.
func (s *Server) mountSources(one fiber.Router) {
	one.Get("/sources", func(c *fiber.Ctx) error {
		p := project(c)
		list, err := s.Store.SourcesOf(c.UserContext(), p.ID)
		if err != nil {
			return httpx.Internal("what this project gathers could not be read").WithCause(err)
		}
		return c.JSON(fiber.Map{"sources": access.FilterProjects(auth.From(c), list)})
	})

	one.Put("/sources", func(c *fiber.Ctx) error {
		if err := writable(c); err != nil {
			return err
		}
		var in struct {
			Sources []string `json:"sources"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The list could not be read.")
		}
		p := project(c)
		ids := make([]uuid.UUID, 0, len(in.Sources))
		for _, raw := range in.Sources {
			id, err := uuid.Parse(raw)
			if err != nil {
				return httpx.BadRequest("%q is not a project id.", raw)
			}
			source, err := s.Store.ProjectByID(c.UserContext(), id)
			if err != nil || !access.CanReadProject(auth.From(c), source) {
				return httpx.BadRequest("There is no such project.")
			}
			if id == p.ID {
				return httpx.BadRequest("A project cannot gather itself.")
			}
			ids = append(ids, id)
		}
		if err := s.Store.SetSources(c.UserContext(), p.ID, ids); err != nil {
			return httpx.Internal("the list could not be saved").WithCause(err)
		}
		list, err := s.Store.SourcesOf(c.UserContext(), p.ID)
		if err != nil {
			return httpx.Internal("the list could not be read back").WithCause(err)
		}
		return c.JSON(fiber.Map{"sources": list})
	})
}

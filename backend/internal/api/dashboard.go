package api

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/access"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/store"
	"github.com/offlinebot/home-projects/backend/internal/variables"
)

// The dashboard is made of added groups and the variables they collect. There
// is no second route into the data — a tile always names a group and a
// variable, never a project's insides.
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

		tiles := []model.DashboardTile{}
		hidden := []store.Hidden{}
		if !actor.IsUser() {
			// A visitor sees the tiles their owner made public — and only as
			// far as the thing behind them is public too. A tile is a window,
			// and a window cannot show more than the room behind it.
			owner, oerr := s.Store.OwnerID(ctx)
			if oerr == nil {
				all, terr := s.Store.ListTiles(ctx, owner)
				if terr != nil {
					return httpx.Internal("the tiles could not be read").WithCause(terr)
				}
				for _, t := range all {
					if s.tileIsVisible(c, t, visible) {
						tiles = append(tiles, t)
					}
				}
			}
		}
		if actor.IsUser() {
			tiles, err = s.Store.ListTiles(ctx, actor.User.ID)
			if err != nil {
				return httpx.Internal("the tiles could not be read").WithCause(err)
			}
			hidden, err = s.Store.ListHidden(ctx, actor.User.ID)
			if err != nil {
				return httpx.Internal("what was put away could not be read").WithCause(err)
			}
		}
		return c.JSON(fiber.Map{"groups": blocks, "tiles": tiles, "hidden": hidden})
	})

	// Putting one thing away, and bringing it back. Not a deletion: the project
	// keeps reporting, this page just stops showing it.
	g.Post("/hidden", requireOwner, func(c *fiber.Ctx) error {
		var in store.Hidden
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The request could not be read.")
		}
		if in.Kind != "project" && in.Kind != "variable" {
			return httpx.BadRequest("Only a project or a variable can be put away.")
		}
		if strings.TrimSpace(in.Ref) == "" {
			return httpx.BadRequest("Nothing was named.")
		}
		if err := s.Store.Hide(c.UserContext(), auth.From(c).User.ID, in.Kind, in.Ref); err != nil {
			return httpx.Internal("it could not be put away").WithCause(err)
		}
		return httpx.OK(c)
	})

	g.Delete("/hidden", requireOwner, func(c *fiber.Ctx) error {
		kind, ref := c.Query("kind"), c.Query("ref")
		if kind == "" || ref == "" {
			return httpx.BadRequest("Nothing was named.")
		}
		if err := s.Store.Unhide(c.UserContext(), auth.From(c).User.ID, kind, ref); err != nil {
			return httpx.Internal("it could not be brought back").WithCause(err)
		}
		return httpx.OK(c)
	})

	g.Post("/tiles", requireOwner, func(c *fiber.Ctx) error {
		var in model.DashboardTile
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The tile could not be read.")
		}
		// Two kinds of tile: a number a group collected, or a project to go
		// straight into.
		if in.Kind == "project" {
			if in.ProjectID == nil {
				return httpx.BadRequest("A project tile has to name a project.")
			}
			p, err := s.Store.ProjectByID(c.UserContext(), *in.ProjectID)
			if err != nil || !access.CanReadProject(auth.From(c), p) {
				return httpx.BadRequest("There is no such project.")
			}
			in.GroupID = uuid.Nil
		} else {
			if in.GroupID == uuid.Nil {
				return httpx.BadRequest("A tile belongs to a group.")
			}
			if _, err := s.Store.GroupByID(c.UserContext(), in.GroupID); err != nil {
				return httpx.BadRequest("There is no such group.")
			}
		}
		created, err := s.Store.CreateTile(c.UserContext(), auth.From(c).User.ID, in)
		if err != nil {
			return httpx.Internal("the tile could not be created").WithCause(err)
		}
		return c.Status(fiber.StatusCreated).JSON(created)
	})

	g.Patch("/tiles/:id", requireOwner, func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return httpx.BadRequest("That is not a tile id.")
		}
		var in struct {
			Variable   *string          `json:"variable"`
			Title      *string          `json:"title"`
			Kind       *string          `json:"kind"`
			Options    *json.RawMessage `json:"options"`
			Section    *string          `json:"section"`
			Visibility *string          `json:"visibility"`
			X          *int             `json:"x"`
			Y          *int             `json:"y"`
			W          *int             `json:"w"`
			H          *int             `json:"h"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The change could not be read.")
		}
		if in.Visibility != nil {
			switch *in.Visibility {
			case "private", "public", "password":
			default:
				return httpx.BadRequest("A tile is private, public or password.")
			}
		}
		patch := store.TilePatch{
			Variable: in.Variable, Title: in.Title, Kind: in.Kind, Options: in.Options,
			Section: in.Section, Visibility: in.Visibility,
			X: in.X, Y: in.Y, W: in.W, H: in.H,
		}
		if err := s.Store.UpdateTile(c.UserContext(), auth.From(c).User.ID, id, patch); err != nil {
			return httpx.NotFound("There is no such tile.")
		}
		return httpx.OK(c)
	})

	g.Delete("/tiles/:id", requireOwner, func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return httpx.BadRequest("That is not a tile id.")
		}
		if err := s.Store.DeleteTile(c.UserContext(), auth.From(c).User.ID, id); err != nil {
			return httpx.NotFound("There is no such tile.")
		}
		return httpx.OK(c)
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

// tileIsVisible answers whether somebody without an account may see this tile.
// Two things have to agree: what the tile says about itself, and what the
// project behind it allows. The stricter of the two wins, always — otherwise a
// public tile would be a way to read a private project.
func (s *Server) tileIsVisible(c *fiber.Ctx, t model.DashboardTile, readable func(uuid.UUID) bool) bool {
	if t.Visibility == "private" || t.Visibility == "" {
		return false
	}
	ctx := c.UserContext()
	actor := auth.From(c)
	if t.ProjectID != nil {
		p, err := s.Store.ProjectByID(ctx, *t.ProjectID)
		if err != nil {
			return false
		}
		if t.Visibility == "password" {
			// Behind a password means: once that project has been unlocked in
			// this session, and not before.
			return access.CanReadProject(actor, p) || actor.HasUnlocked(p.ID)
		}
		return access.CanReadProject(actor, p)
	}
	// A tile that shows a number shows one project's number. Which project that
	// is comes from the variable's own name, and the same filter the variables
	// went through answers for it.
	project, ok := s.projectOfVariable(ctx, t)
	if !ok {
		return false
	}
	if t.Visibility == "password" && actor.HasUnlocked(project.ID) {
		return true
	}
	return readable(project.ID)
}

// projectOfVariable finds the project a tile's variable comes from. The name is
// written project-slug.variable inside a group.
func (s *Server) projectOfVariable(ctx context.Context, t model.DashboardTile) (*model.Project, bool) {
	slug, _, found := strings.Cut(t.Variable, ".")
	if !found || t.GroupID == uuid.Nil {
		// A number the group itself worked out: it is as open as the group.
		return nil, false
	}
	p, err := s.Store.ProjectBySlug(ctx, &t.GroupID, slug)
	if err != nil {
		return nil, false
	}
	return p, true
}

package api

import (
	"encoding/json"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/filter"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/slug"
	"github.com/offlinebot/home-projects/backend/internal/store"
)

// Filters are a menu of their own, next to accounts and schedulers: named rules
// that answer "where does this belong?". A scheduler points at one; a project
// can be run through one; nothing owns one.
func (s *Server) mountFilters(r fiber.Router) {
	g := r.Group("/filters", requireOwner)

	g.Get("/", func(c *fiber.Ctx) error {
		list, err := s.Store.ListFilters(c.UserContext())
		if err != nil {
			return httpx.Internal("the filters could not be read").WithCause(err)
		}
		return c.JSON(fiber.Map{"filters": list})
	})

	g.Post("/", func(c *fiber.Ctx) error {
		var in filterInput
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The filter could not be read.")
		}
		if strings.TrimSpace(in.Title) == "" {
			return httpx.BadRequest("A filter needs a name.")
		}
		rules, bad := in.rules()
		if len(bad) > 0 {
			return httpx.BadRequest("These lines are not rules: %s. Write them as \"match -> project\".",
				strings.Join(bad, "; "))
		}
		encoded, _ := json.Marshal(rules)
		created, err := s.Store.CreateFilter(c.UserContext(), store.NewFilter{
			OwnerID:     auth.From(c).User.ID,
			Slug:        slug.Make(firstNonEmpty(in.Slug, in.Title)),
			Title:       in.Title,
			Description: in.Description,
			Rules:       encoded,
		})
		if err != nil {
			return httpx.Conflict("A filter with that name already exists.")
		}
		return c.Status(fiber.StatusCreated).JSON(created)
	})

	g.Patch("/:id", func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return httpx.BadRequest("That is not a filter id.")
		}
		var in filterInput
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The change could not be read.")
		}
		patch := store.FilterPatch{}
		if in.Title != "" {
			patch.Title = &in.Title
		}
		if in.Description != "" {
			patch.Description = &in.Description
		}
		if in.Text != nil || in.Rules != nil {
			rules, bad := in.rules()
			if len(bad) > 0 {
				return httpx.BadRequest("These lines are not rules: %s.", strings.Join(bad, "; "))
			}
			encoded, _ := json.Marshal(rules)
			raw := json.RawMessage(encoded)
			patch.Rules = &raw
		}
		updated, err := s.Store.UpdateFilter(c.UserContext(), id, patch)
		if err != nil {
			return httpx.NotFound("There is no such filter.")
		}
		return c.JSON(updated)
	})

	g.Delete("/:id", func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return httpx.BadRequest("That is not a filter id.")
		}
		if err := s.Store.DeleteFilter(c.UserContext(), id); err != nil {
			return httpx.NotFound("There is no such filter.")
		}
		return httpx.OK(c)
	})

	// What would these rules do? Names in, destinations out — no writing, so it
	// can be asked while typing.
	g.Post("/try", func(c *fiber.Ctx) error {
		var in struct {
			filterInput
			Names []string `json:"names"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The request could not be read.")
		}
		rules, bad := in.rules()
		out := make([]fiber.Map, 0, len(in.Names))
		for _, name := range in.Names {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			d, matched := filter.Apply(rules, filter.Item{Name: name, Path: name})
			out = append(out, fiber.Map{
				"name": name, "matched": matched, "project": d.Project,
				"folder": d.Folder, "skip": d.Skip, "rule": d.Rule,
			})
		}
		return c.JSON(fiber.Map{"results": out, "unusable": bad})
	})
}

type filterInput struct {
	Slug        string        `json:"slug"`
	Title       string        `json:"title"`
	Description string        `json:"description"`
	Rules       []filter.Rule `json:"rules"`
	// Text is the same thing as a person types it. One of the two is enough.
	Text *string `json:"text"`
}

func (in filterInput) rules() ([]filter.Rule, []string) {
	if in.Text != nil {
		return filter.ParseText(*in.Text)
	}
	return in.Rules, nil
}

// mountProjectFilter is the other half: running a project's own files through a
// filter. It answers with what it would do unless told to do it, because moving
// files is not something to discover afterwards.
func (s *Server) mountProjectFilter(r fiber.Router) {
	r.Post("/filter", func(c *fiber.Ctx) error {
		p := project(c)
		var in struct {
			Filter string `json:"filter"`
			Path   string `json:"path"`
			Apply  bool   `json:"apply"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The request could not be read.")
		}
		if in.Apply {
			if err := writable(c); err != nil {
				return err
			}
		}
		rules, err := s.rulesOf(c, in.Filter)
		if err != nil {
			return err
		}

		// One level: the folders and files where you are standing. That is what
		// "all folders in ./ starting with Grundlagen" means, and it is what can
		// be looked at before it happens.
		fs := s.WS.Open(p.ID)
		here := strings.Trim(in.Path, "/")
		entries, err := fs.List(here)
		if err != nil {
			return httpx.NotFound("There is no folder at this path.")
		}
		items := make([]filter.Item, len(entries))
		for i, e := range entries {
			items[i] = filter.Item{Name: e.Name, Path: e.Path, IsDir: e.IsDir, Changed: e.ModifiedAt}
		}

		type step struct {
			From    string `json:"from"`
			To      string `json:"to"`
			Project string `json:"project,omitempty"`
			Rule    string `json:"rule,omitempty"`
			Note    string `json:"note,omitempty"`
			IsDir   bool   `json:"isDir,omitempty"`
		}
		steps := []step{}
		moved := map[uuid.UUID]*model.Project{}
		var walkErr error

		for i, d := range filter.Plan(rules, items) {
			e := entries[i]
			if !d.Matched || d.Skip {
				continue
			}
			target := p
			if d.Project != "" {
				found, err := s.resolveProjectRef(c, d.Project)
				if err != nil {
					steps = append(steps, step{From: e.Path, Rule: d.Rule, IsDir: e.IsDir,
						Note: "no project called " + d.Project})
					continue
				}
				target = found
			}
			dest := e.Name
			if d.Folder != "" {
				dest = d.Folder + "/" + e.Name
			}
			if target.ID == p.ID && dest == e.Path {
				continue // already where the rule wants it
			}
			steps = append(steps, step{
				From: e.Path, To: dest, Rule: d.Rule, IsDir: e.IsDir,
				Project: map[bool]string{true: "", false: target.Slug}[target.ID == p.ID],
			})
			if !in.Apply {
				continue
			}
			author, email := authorOf(c)
			op := files.Op{Author: author, Email: email, Commit: true,
				Message: "Filter: " + e.Path + " → " + dest}
			if target.ID == p.ID {
				if err := s.Files.Move(c.UserContext(), p, e.Path, dest, op); err != nil {
					walkErr = err
				}
				continue
			}
			// Into another project: copy first, and only remove the original
			// once the copy is on disk.
			if err := s.Files.Copy(c.UserContext(), p, e.Path, target, dest, op); err != nil {
				walkErr = err
				continue
			}
			if err := s.Files.Remove(c.UserContext(), p, e.Path, e.IsDir, op); err != nil {
				walkErr = err
			}
			moved[target.ID] = target
		}

		if walkErr != nil {
			return httpx.Internal("the filter could not be applied").WithCause(walkErr)
		}
		if in.Apply {
			s.reindex(c, p)
			s.Vars.Refresh(c.UserContext(), p)
			for _, t := range moved {
				s.reindex(c, t)
				s.Vars.Refresh(c.UserContext(), t)
			}
		}
		return c.JSON(fiber.Map{"applied": in.Apply, "steps": steps})
	})
}

// rulesOf takes either a stored filter's id or slug, or nothing.
func (s *Server) rulesOf(c *fiber.Ctx, ref string) ([]filter.Rule, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, httpx.BadRequest("Which filter?")
	}
	var f *model.Filter
	var err error
	if id, perr := uuid.Parse(ref); perr == nil {
		f, err = s.Store.FilterByID(c.UserContext(), id)
	} else {
		f, err = s.Store.FilterBySlug(c.UserContext(), slug.Make(ref))
	}
	if err != nil {
		return nil, httpx.NotFound("There is no filter called %q.", ref)
	}
	var rules []filter.Rule
	if uerr := json.Unmarshal(f.Rules, &rules); uerr != nil {
		return nil, httpx.Internal("the filter's rules could not be read").WithCause(uerr)
	}
	return rules, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

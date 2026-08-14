package api

import (
	"context"
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

// mountProjectFilter is the other half: a project says which filters it uses,
// and runs them over its own files. It answers with what it would do unless
// told to do it, because moving files is not something to discover afterwards.
func (s *Server) mountProjectFilter(r fiber.Router) {
	r.Get("/filters", func(c *fiber.Ctx) error {
		p := project(c)
		list, err := s.Store.FiltersForProject(c.UserContext(), p.ID)
		if err != nil {
			return httpx.Internal("the project's filters could not be read").WithCause(err)
		}
		return c.JSON(fiber.Map{"filters": list})
	})

	r.Post("/filters", requireOwner, func(c *fiber.Ctx) error {
		p := project(c)
		var in struct {
			Filter    string `json:"filter"`
			Automatic bool   `json:"automatic"`
			// Where this project sends what the filter matches. A rule that
			// names a project itself still wins.
			Target string `json:"target"`
			Folder string `json:"folder"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The request could not be read.")
		}
		f, err := s.filterOf(c, in.Filter)
		if err != nil {
			return err
		}
		var target *uuid.UUID
		if strings.TrimSpace(in.Target) != "" {
			found, err := s.resolveProjectRef(c, in.Target)
			if err != nil {
				return err
			}
			target = &found.ID
		}
		if err := s.Store.AddFilterToProject(c.UserContext(), p.ID, f.ID, in.Automatic,
			target, strings.Trim(in.Folder, "/")); err != nil {
			return httpx.Internal("the filter could not be added").WithCause(err)
		}
		return c.Status(fiber.StatusCreated).JSON(f)
	})

	r.Delete("/filters/:id", requireOwner, func(c *fiber.Ctx) error {
		p := project(c)
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return httpx.BadRequest("That is not a filter id.")
		}
		if err := s.Store.RemoveFilterFromProject(c.UserContext(), p.ID, id); err != nil {
			return httpx.Internal("the filter could not be removed").WithCause(err)
		}
		return httpx.OK(c)
	})

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
		// Without a name, the project runs the filters it has picked up, in
		// order — what an earlier one takes, a later one does not see.
		var rules []filter.Rule
		if strings.TrimSpace(in.Filter) != "" {
			named, err := s.rulesOf(c, in.Filter)
			if err != nil {
				return err
			}
			rules = named
		} else {
			mine, err := s.Store.FiltersForProject(c.UserContext(), p.ID)
			if err != nil {
				return httpx.Internal("the project's filters could not be read").WithCause(err)
			}
			rules = withTargets(mine)
			if len(rules) == 0 {
				return httpx.BadRequest("This project has no filters yet. Add one first.")
			}
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

// filterOf resolves a filter by id or slug for the routes that need the whole
// thing rather than its rules.
func (s *Server) filterOf(c *fiber.Ctx, ref string) (*model.Filter, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, httpx.BadRequest("Which filter?")
	}
	if id, err := uuid.Parse(ref); err == nil {
		f, err := s.Store.FilterByID(c.UserContext(), id)
		if err != nil {
			return nil, httpx.NotFound("There is no such filter.")
		}
		return f, nil
	}
	f, err := s.Store.FilterBySlug(c.UserContext(), slug.Make(ref))
	if err != nil {
		return nil, httpx.NotFound("There is no filter called %q.", ref)
	}
	return f, nil
}

// SortProject runs the filters a project has picked up and marked automatic.
//
// It is the same walk the button does, without a request behind it: the
// scheduler calls it after a run, and the core hands it to capabilities as
// Env.SortProject so nothing outside this file knows what a filter is.
func (s *Server) SortProject(ctx context.Context, p *model.Project) (int, error) {
	mine, err := s.Store.FiltersForProject(ctx, p.ID)
	if err != nil {
		return 0, err
	}
	automatic := make([]store.ProjectFilter, 0, len(mine))
	for _, f := range mine {
		if f.Automatic {
			automatic = append(automatic, f)
		}
	}
	rules := withTargets(automatic)
	if len(rules) == 0 {
		return 0, nil
	}

	fs := s.WS.Open(p.ID)
	entries, err := fs.List("")
	if err != nil {
		return 0, err
	}
	items := make([]filter.Item, len(entries))
	for i, e := range entries {
		items[i] = filter.Item{Name: e.Name, Path: e.Path, IsDir: e.IsDir, Changed: e.ModifiedAt}
	}

	op := files.Op{Author: "the project's filters", Email: "filters@home-projects", Commit: true}
	moved := 0
	for i, d := range filter.Plan(rules, items) {
		e := entries[i]
		if !d.Matched || d.Skip {
			continue
		}
		target := p
		if d.Project != "" {
			found, err := s.projectByRef(ctx, d.Project)
			if err != nil {
				continue // named a project that is not here; the button says so
			}
			target = found
		}
		dest := e.Name
		if d.Folder != "" {
			dest = d.Folder + "/" + e.Name
		}
		if target.ID == p.ID {
			if dest == e.Path {
				continue
			}
			if err := s.Files.Move(ctx, p, e.Path, dest, op); err == nil {
				moved++
			}
			continue
		}
		if err := s.Files.Copy(ctx, p, e.Path, target, dest, op); err != nil {
			continue
		}
		if err := s.Files.Remove(ctx, p, e.Path, e.IsDir, op); err == nil {
			moved++
		}
	}
	return moved, nil
}

// projectByRef finds a project named group/project or project, without a
// request behind it.
func (s *Server) projectByRef(ctx context.Context, ref string) (*model.Project, error) {
	groupSlug, projectSlug := "", strings.Trim(strings.TrimSpace(ref), "/")
	if a, b, ok := strings.Cut(projectSlug, "/"); ok {
		groupSlug, projectSlug = a, b
	}
	all, err := s.Store.ListProjects(ctx, nil, false, true)
	if err != nil {
		return nil, err
	}
	for i := range all {
		p := all[i]
		if !strings.EqualFold(p.Slug, projectSlug) && !strings.EqualFold(p.Title, projectSlug) {
			continue
		}
		if groupSlug != "" && !strings.EqualFold(p.GroupSlug, groupSlug) {
			continue
		}
		return &p, nil
	}
	return nil, httpx.NotFound("There is no project called %q.", ref)
}

// withTargets flattens a project's filters into one list of rules, filling in
// the destination each was given here.
//
// This is what makes a filter reusable: the rules say *what* ("folders called
// Grundlagen-something"), and the project that picked them up says *where*.
func withTargets(mine []store.ProjectFilter) []filter.Rule {
	var out []filter.Rule
	for _, f := range mine {
		var part []filter.Rule
		if json.Unmarshal(f.Rules, &part) != nil {
			continue
		}
		for _, r := range part {
			if strings.TrimSpace(r.To) == "" || strings.EqualFold(strings.TrimSpace(r.To), "here") {
				switch {
				case f.TargetProject != "" && f.TargetFolder != "":
					r.To = "{" + f.TargetProject + "}/" + f.TargetFolder
				case f.TargetProject != "":
					r.To = "{" + f.TargetProject + "}"
				case f.TargetFolder != "":
					r.To = "./" + f.TargetFolder
				default:
					continue // nothing said here and nothing said there
				}
			}
			out = append(out, r)
		}
	}
	return out
}

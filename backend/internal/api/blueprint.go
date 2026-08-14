package api

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/blueprint"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
)

// The arrangement as one JSON document: which groups exist, which projects hang
// in them, what each can do, and the links between them. The shape, not the
// contents — files travel by git and by the zip download.
func (s *Server) mountBlueprint(r fiber.Router) {
	r.Get("/export", requireOwner, func(c *fiber.Ctx) error {
		doc, err := blueprint.ExportWhat(c.UserContext(), s.Store, c.Query("group"), blueprint.What{
			Schedulers: c.QueryBool("schedulers", true),
			Accounts:   c.QueryBool("accounts", true),
			Filters:    c.QueryBool("filters", true),
		})
		if err != nil {
			return httpx.Internal("the export could not be written").WithCause(err)
		}
		if c.QueryBool("download", false) {
			name := "home-projects"
			if g := c.Query("group"); g != "" {
				name = g
			}
			c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name+".json"))
		}
		c.Set("Content-Type", "application/json; charset=utf-8")
		body, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return httpx.Internal("the export could not be written").WithCause(err)
		}
		return c.Send(append(body, '\n'))
	})

	r.Get("/groups/:group/export", requireOwner, s.resolveGroup, func(c *fiber.Ctx) error {
		doc, err := blueprint.Export(c.UserContext(), s.Store, groupOf(c).Slug)
		if err != nil {
			return httpx.Internal("the export could not be written").WithCause(err)
		}
		return c.JSON(doc)
	})

	// Import says what it would do before it does anything. Applying it is a
	// separate request with ?apply=true, and it needs the password again.
	r.Post("/import", requireOwner, func(c *fiber.Ctx) error {
		var doc blueprint.Document
		if err := json.Unmarshal(c.Body(), &doc); err != nil {
			return httpx.BadRequest("This is not a home-projects document: %v", err)
		}
		if err := blueprint.Validate(&doc, capability.Exists, func(key string) bool {
			_, ok := capability.PresetByKey(key)
			return ok
		}); err != nil {
			return httpx.BadRequest("%v", err)
		}

		apply := c.QueryBool("apply", false)
		if apply {
			if err := s.stepUp(c, "importing an arrangement"); err != nil {
				return err
			}
		}

		result, err := s.applyDocument(c, &doc, !apply)
		if err != nil {
			return err
		}
		if apply {
			s.Store.Audit(c.UserContext(), auth.From(c).UserID(), "blueprint.imported",
				fmt.Sprintf("%d groups", len(doc.Groups)), auth.ClientIP(c), nil)
		}
		return c.JSON(result)
	})
}

// applyDocument is the import itself, wherever the document came from: pasted
// JSON, or the blueprint.json inside a bundle.
func (s *Server) applyDocument(c *fiber.Ctx, doc *blueprint.Document, dryRun bool) (*blueprint.Result, error) {
	if err := blueprint.Validate(doc, capability.Exists, func(key string) bool {
		_, ok := capability.PresetByKey(key)
		return ok
	}); err != nil {
		return nil, httpx.BadRequest("%v", err)
	}
	actor := auth.From(c)
	applier := &blueprint.Applier{
		Store:   s.Store,
		OwnerID: actor.User.ID,
		EnsureRepo: func(ctx context.Context, slug, title string) error {
			if err := s.Git.EnsureRepo(ctx, slug, title); err != nil {
				return err
			}
			return s.Git.InstallHooks(slug)
		},
		SeedProject: func(ctx context.Context, p *model.Project, presetKey string) error {
			preset, ok := capability.PresetByKey(presetKey)
			if !ok {
				preset, _ = capability.PresetByKey("data")
			}
			repo := p.GroupSlug
			if repo == "" {
				repo = "ungrouped"
			}
			if err := s.Git.EnsureRepo(ctx, repo, repo); err == nil {
				_ = s.Git.InstallHooks(repo)
			}
			for _, seed := range preset.Seed {
				if _, err := s.Files.Write(ctx, p, seed.Path, seed.Content(p), files.Op{
					Author: "the import", Email: "import@home-projects", Commit: false,
				}); err != nil {
					return err
				}
			}
			// The branch exists from the start, as it does for any project.
			_, _, err := s.Files.Commit(ctx, p, "Create "+p.Title, "the import", "import@home-projects")
			return err
		},
		EnsureFolder: func(ctx context.Context, p *model.Project, dir string) error {
			if s.Files.Exists(p, dir) {
				return nil
			}
			return s.Files.Mkdir(ctx, p, dir, files.Op{Commit: false})
		},
		ReloadSchedulers: func(ctx context.Context) error { return s.Sched.Reload(ctx) },
	}

	result, err := applier.Apply(c.UserContext(), doc, dryRun)
	if err != nil {
		return result, httpx.Internal("the import stopped partway: %v", err).WithDetail(result)
	}
	return result, nil
}

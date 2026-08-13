package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/access"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/secret"
	"github.com/offlinebot/home-projects/backend/internal/slug"
	"github.com/offlinebot/home-projects/backend/internal/store"
	"github.com/offlinebot/home-projects/backend/internal/variables"
)

func (s *Server) decorateProject(p *model.Project) {
	group := p.GroupSlug
	if group == "" {
		group = "ungrouped"
	}
	p.CloneURL = fmt.Sprintf("%s -b %s --single-branch", s.Git.CloneURL(p.GroupSlug), p.Slug)
	if p.SiteRoot != nil {
		p.SiteURL = fmt.Sprintf("%s/s/%s/%s/", s.Cfg.PublicURL, group, p.Slug)
	}
}

func (s *Server) mountProjects(r fiber.Router) {
	g := r.Group("/projects")

	g.Get("/", func(c *fiber.Ctx) error {
		ctx := c.UserContext()
		actor := auth.From(c)
		includeArchived := c.QueryBool("archived", false) && actor.IsUser()

		var list []model.Project
		var err error
		switch {
		case c.Query("capability") != "":
			list, err = s.Store.ProjectsWithCapability(ctx, c.Query("capability"))
		case c.Query("group") != "":
			grp, gerr := s.Store.GroupBySlug(ctx, c.Query("group"))
			if gerr != nil {
				return httpx.NotFound("No group at this address.")
			}
			list, err = s.Store.ListProjects(ctx, &grp.ID, false, includeArchived)
		case c.QueryBool("ungrouped", false):
			list, err = s.Store.ListProjects(ctx, nil, true, includeArchived)
		default:
			list, err = s.Store.ListAllProjects(ctx, includeArchived)
		}
		if err != nil {
			return httpx.Internal("the projects could not be read").WithCause(err)
		}
		list = access.FilterProjects(actor, list)
		for i := range list {
			s.decorateProject(&list[i])
		}
		return c.JSON(fiber.Map{"projects": list})
	})

	g.Post("/", requireOwner, func(c *fiber.Ctx) error {
		var in struct {
			Title        string   `json:"title"`
			Slug         string   `json:"slug"`
			Description  string   `json:"description"`
			GroupID      string   `json:"groupId"`
			Preset       string   `json:"preset"`
			Capabilities []string `json:"capabilities"`
			Visibility   string   `json:"visibility"`
			Password     string   `json:"password"`
			Color        string   `json:"color"`
			Icon         string   `json:"icon"`
			GitTracked   bool     `json:"gitTracked"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The project could not be read.")
		}
		if strings.TrimSpace(in.Title) == "" {
			return httpx.BadRequest("A project needs a name.")
		}
		ctx := c.UserContext()
		actor := auth.From(c)

		var groupID *uuid.UUID
		var grp *model.Group
		if in.GroupID != "" && in.GroupID != "ungrouped" {
			var err error
			grp, err = s.findGroup(ctx, in.GroupID)
			if err != nil {
				return err
			}
			if grp.ReadOnly {
				return httpx.ReadOnly("The group " + grp.Title)
			}
			groupID = &grp.ID
		}

		// The preset is the typed way in: it decides icon, default tab, which
		// capabilities start on and which files are seeded. It never decides
		// anything else, ever again.
		presetKey := in.Preset
		if presetKey == "" {
			presetKey = "data"
		}
		preset, ok := capability.PresetByKey(presetKey)
		if !ok {
			return httpx.BadRequest("There is no preset called %q.", presetKey)
		}

		capabilities := preset.Capabilities
		if in.Capabilities != nil {
			capabilities = in.Capabilities
		}
		for _, name := range capabilities {
			if !capability.Exists(name) {
				return httpx.BadRequest("There is no capability called %q on this server.", name)
			}
		}

		wanted := in.Slug
		if wanted == "" {
			wanted = slug.Make(in.Title)
		} else {
			wanted = slug.Make(wanted)
		}
		final, err := slug.Unique(wanted, func(s2 string) (bool, error) {
			return s.Store.ProjectSlugTaken(ctx, groupID, s2)
		})
		if err != nil {
			return httpx.BadRequest("%v", err)
		}

		visibility := model.Visibility(in.Visibility)
		if visibility == "" {
			visibility = model.VisibilityPrivate
		}
		if !visibility.Valid() {
			return httpx.BadRequest("Visibility has to be private, public or password.")
		}
		if in.Color != "" && !validColor(in.Color) {
			return httpx.BadRequest("That colour is not in the palette.")
		}
		color := in.Color
		if color == "" && grp != nil {
			color = grp.Color // a project defaults to its group's colour
		}
		icon := in.Icon
		if icon == "" {
			icon = preset.Icon
		}

		created, err := s.Store.CreateProject(ctx, store.NewProject{
			OwnerID:      actor.User.ID,
			GroupID:      groupID,
			Slug:         final,
			Title:        strings.TrimSpace(in.Title),
			Description:  in.Description,
			Capabilities: capabilities,
			Preset:       preset.Key,
			DefaultTab:   preset.DefaultTab,
			GitTracked:   in.GitTracked,
			Visibility:   visibility,
			Color:        color,
			Icon:         icon,
		})
		if err != nil {
			return httpx.Internal("the project could not be created").WithCause(err)
		}

		if visibility == model.VisibilityPassword && in.Password != "" {
			if hash, herr := secret.Hash(in.Password); herr == nil {
				pw := &hash
				created, _ = s.Store.UpdateProject(ctx, created.ID, store.ProjectPatch{PasswordHash: &pw})
			}
		}
		if preset.SiteRoot != "" {
			root := preset.SiteRoot
			ref := &root
			created, _ = s.Store.UpdateProject(ctx, created.ID, store.ProjectPatch{SiteRoot: &ref})
		}

		// Seed the preset's files, and make sure the group's repository exists.
		if _, err := s.WS.For(created.ID); err != nil {
			return httpx.Internal("the project folder could not be created").WithCause(err)
		}
		repoSlug := created.GroupSlug
		if repoSlug == "" {
			repoSlug = "ungrouped"
		}
		if err := s.Git.EnsureRepo(ctx, repoSlug, repoSlug); err == nil {
			_ = s.Git.InstallHooks(repoSlug)
		}
		for _, seed := range preset.Seed {
			if _, err := s.Files.Write(ctx, created, seed.Path, seed.Content(created), files.Op{
				Author: "the server", Email: "server@home-projects",
				Message: "Create " + created.Title, Commit: false,
			}); err != nil {
				return httpx.Internal("the starting files could not be written").WithCause(err)
			}
		}
		// The branch exists from the start — that is what makes
		// `git clone -b <project>` work right away. Commits after this one
		// happen on request, or automatically only if git tracking is on.
		if _, _, err := s.Files.Commit(ctx, created, "Create "+created.Title,
			"the server", "server@home-projects"); err != nil {
			return httpx.Internal("the project's branch could not be created").WithCause(err)
		}

		s.reindex(c, created)
		s.Vars.Refresh(ctx, created)
		s.decorateProject(created)
		s.Store.Audit(ctx, actor.UserID(), "project.created", created.Slug, auth.ClientIP(c),
			map[string]any{"preset": preset.Key})
		return c.Status(fiber.StatusCreated).JSON(created)
	})

	one := g.Group("/:project", s.resolveProject())

	one.Get("/", func(c *fiber.Ctx) error {
		p := project(c)
		s.decorateProject(p)
		spec, specErr := variables.Spec(c.UserContext(), s.Env, p)
		payload := fiber.Map{"project": p, "group": group(c)}
		if spec != nil {
			payload["tool"] = spec
		}
		if specErr != nil {
			payload["toolError"] = specErr.Error()
		}
		return c.JSON(payload)
	})

	one.Post("/unlock", func(c *fiber.Ctx) error {
		p := project(c)
		return s.unlock(c, p.ID, p.PasswordHash, "project-password", p.Slug)
	})

	one.Patch("/", requireOwner, func(c *fiber.Ctx) error {
		p := project(c)
		ctx := c.UserContext()
		var in struct {
			Title        *string   `json:"title"`
			Slug         *string   `json:"slug"`
			Description  *string   `json:"description"`
			Capabilities *[]string `json:"capabilities"`
			Preset       *string   `json:"preset"`
			DefaultTab   *string   `json:"defaultTab"`
			GitTracked   *bool     `json:"gitTracked"`
			SiteRoot     *string   `json:"siteRoot"`
			Visibility   *string   `json:"visibility"`
			Password     *string   `json:"password"`
			ReadOnly     *bool     `json:"readOnly"`
			AnonWrite    *bool     `json:"anonWrite"`
			Color        *string   `json:"color"`
			Icon         *string   `json:"icon"`
			Archived     *bool     `json:"archived"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The change could not be read.")
		}

		patch := store.ProjectPatch{
			Title: in.Title, Description: in.Description, GitTracked: in.GitTracked,
			ReadOnly: in.ReadOnly, Icon: in.Icon, Archived: in.Archived,
			DefaultTab: in.DefaultTab, Preset: in.Preset,
		}
		if in.Color != nil {
			if !validColor(*in.Color) {
				return httpx.BadRequest("That colour is not in the palette.")
			}
			patch.Color = in.Color
		}
		if in.Capabilities != nil {
			for _, name := range *in.Capabilities {
				if !capability.Exists(name) {
					return httpx.BadRequest("There is no capability called %q on this server.", name)
				}
			}
			patch.Capabilities = in.Capabilities
		}
		if in.SiteRoot != nil {
			if *in.SiteRoot == "" {
				var none *string
				patch.SiteRoot = &none
			} else {
				root := strings.Trim(*in.SiteRoot, "/")
				ref := &root
				patch.SiteRoot = &ref
			}
		}

		if in.Visibility != nil || in.Password != nil || in.AnonWrite != nil {
			if err := s.stepUp(c, "changing who can see this project"); err != nil {
				return err
			}
		}
		if in.Visibility != nil {
			v := model.Visibility(*in.Visibility)
			if !v.Valid() {
				return httpx.BadRequest("Visibility has to be private, public or password.")
			}
			patch.Visibility = &v
			if v != model.VisibilityPassword {
				var none *string
				patch.PasswordHash = &none
			}
			if v != model.VisibilityPublic {
				off := false
				patch.AnonWrite = &off // visitors can only write in a public project
			}
		}
		if in.Password != nil {
			if *in.Password == "" {
				var none *string
				patch.PasswordHash = &none
			} else {
				hash, err := secret.Hash(*in.Password)
				if err != nil {
					return httpx.Internal("the password could not be hashed").WithCause(err)
				}
				pw := &hash
				patch.PasswordHash = &pw
			}
		}
		if in.AnonWrite != nil {
			target := *in.AnonWrite
			effective := model.Visibility(strp(in.Visibility, string(p.Visibility)))
			if target && effective != model.VisibilityPublic {
				return httpx.BadRequest("Visitors can only write in a project that is public.")
			}
			patch.AnonWrite = &target
		}

		renamedFrom := ""
		if in.Slug != nil && *in.Slug != p.Slug {
			newSlug := slug.Make(*in.Slug)
			if err := slug.Validate(newSlug); err != nil {
				return httpx.BadRequest("%v", err)
			}
			taken, err := s.Store.ProjectSlugTaken(ctx, p.GroupID, newSlug)
			if err != nil {
				return httpx.Internal("the address could not be checked").WithCause(err)
			}
			if taken {
				return httpx.Conflict("Another project in this group already uses %q.", newSlug)
			}
			renamedFrom = p.Slug
			patch.Slug = &newSlug
		}

		updated, err := s.Store.UpdateProject(ctx, p.ID, patch)
		if err != nil {
			return httpx.Internal("the project could not be changed").WithCause(err)
		}
		if renamedFrom != "" {
			// The slug is the branch name, so the branch follows.
			if err := s.Git.RenameBranch(ctx, updated.GroupSlug, renamedFrom, updated.Slug); err != nil {
				return httpx.Internal("the branch could not be renamed").WithCause(err)
			}
		}
		if in.ReadOnly != nil && *in.ReadOnly {
			paused, _ := s.Store.PauseSchedulersForProject(ctx, updated.ID,
				"the project "+updated.Title+" is read-only")
			_ = s.Sched.Reload(ctx)
			if len(paused) > 0 {
				s.decorateProject(updated)
				names := make([]string, 0, len(paused))
				for _, sc := range paused {
					names = append(names, sc.Title)
				}
				return c.JSON(fiber.Map{"project": updated, "pausedSchedulers": names})
			}
		}
		s.reindex(c, updated)
		s.Vars.Refresh(ctx, updated)
		s.decorateProject(updated)
		s.Store.Audit(ctx, auth.From(c).UserID(), "project.changed", updated.Slug, auth.ClientIP(c), nil)
		return c.JSON(fiber.Map{"project": updated})
	})

	// Moving a project into another group is a branch move: the history goes
	// with it.
	one.Post("/move", requireOwner, func(c *fiber.Ctx) error {
		p := project(c)
		ctx := c.UserContext()
		var in struct {
			GroupID string `json:"groupId"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The target group could not be read.")
		}

		fromRepo := p.GroupSlug
		if fromRepo == "" {
			fromRepo = "ungrouped"
		}

		var target *uuid.UUID
		toRepo := "ungrouped"
		if in.GroupID != "" && in.GroupID != "ungrouped" {
			grp, err := s.findGroup(ctx, in.GroupID)
			if err != nil {
				return err
			}
			if grp.ReadOnly {
				return httpx.ReadOnly("The group " + grp.Title)
			}
			target = &grp.ID
			toRepo = grp.Slug
		}
		if fromRepo == toRepo {
			return c.JSON(fiber.Map{"project": p})
		}

		taken, err := s.Store.ProjectSlugTaken(ctx, target, p.Slug)
		if err != nil {
			return httpx.Internal("the address could not be checked").WithCause(err)
		}
		if taken {
			return httpx.Conflict("The target group already has a project called %q.", p.Slug)
		}
		if err := s.Git.EnsureRepo(ctx, toRepo, toRepo); err != nil {
			return httpx.Internal("the target repository could not be prepared").WithCause(err)
		}
		_ = s.Git.InstallHooks(toRepo)
		if err := s.Git.MoveBranch(ctx, fromRepo, toRepo, p.Slug); err != nil {
			return httpx.Internal("the branch could not be moved").WithCause(err)
		}
		updated, err := s.Store.UpdateProject(ctx, p.ID, store.ProjectPatch{GroupID: &target})
		if err != nil {
			return httpx.Internal("the project could not be moved").WithCause(err)
		}
		s.decorateProject(updated)
		s.Store.Audit(ctx, auth.From(c).UserID(), "project.moved", updated.Slug, auth.ClientIP(c),
			map[string]any{"from": fromRepo, "to": toRepo})
		return c.JSON(fiber.Map{"project": updated})
	})

	one.Post("/duplicate", requireOwner, func(c *fiber.Ctx) error {
		p := project(c)
		ctx := c.UserContext()
		var in struct {
			Title string `json:"title"`
		}
		_ = c.BodyParser(&in)
		if in.Title == "" {
			in.Title = p.Title + " (copy)"
		}
		final, err := slug.Unique(slug.Make(in.Title), func(s2 string) (bool, error) {
			return s.Store.ProjectSlugTaken(ctx, p.GroupID, s2)
		})
		if err != nil {
			return httpx.BadRequest("%v", err)
		}
		actor := auth.From(c)
		copyOf, err := s.Store.CreateProject(ctx, store.NewProject{
			OwnerID: actor.User.ID, GroupID: p.GroupID, Slug: final, Title: in.Title,
			Description: p.Description, Capabilities: p.Capabilities, Preset: p.Preset,
			DefaultTab: p.DefaultTab, GitTracked: p.GitTracked, Visibility: model.VisibilityPrivate,
			Color: p.Color, Icon: p.Icon,
		})
		if err != nil {
			return httpx.Internal("the copy could not be created").WithCause(err)
		}
		// A copy, without history — that is what "Duplicate" means here.
		if err := s.Files.Copy(ctx, p, "", copyOf, "", files.Op{Commit: false}); err != nil {
			return err
		}
		if _, _, err := s.Files.Commit(ctx, copyOf, "Copy of "+p.Title,
			"the server", "server@home-projects"); err != nil {
			return httpx.Internal("the copy's branch could not be created").WithCause(err)
		}
		s.reindex(c, copyOf)
		s.Vars.Refresh(ctx, copyOf)
		s.decorateProject(copyOf)
		return c.Status(fiber.StatusCreated).JSON(copyOf)
	})

	one.Get("/deletion-preview", requireOwner, func(c *fiber.Ctx) error {
		p := project(c)
		ctx := c.UserContext()
		fileCount, bytes := s.WS.Open(p.ID).Count()
		schedulers, _ := s.Store.ListSchedulersForProject(ctx, p.ID)
		incoming, _ := s.Store.LinksFrom(ctx, p.ID)
		head, _ := s.Git.BranchHead(ctx, p.GroupSlug, p.Slug)

		names := make([]string, 0, len(schedulers))
		for _, sc := range schedulers {
			label := sc.Title
			if label == "" {
				label = sc.Kind
			}
			names = append(names, label)
		}
		linkNames := make([]string, 0, len(incoming))
		for _, l := range incoming {
			linkNames = append(linkNames, l.TargetSlug+":"+l.TargetPath)
		}
		return c.JSON(fiber.Map{
			"files": fileCount, "bytes": bytes, "schedulers": names,
			"linksPointingHere": linkNames, "branch": p.Slug, "hasHistory": head != "",
			"downloadUrl": "/api/projects/" + p.ID.String() + "/download",
		})
	})

	one.Delete("/", requireOwner, func(c *fiber.Ctx) error {
		p := project(c)
		ctx := c.UserContext()
		if err := s.stepUp(c, "deleting the project "+p.Title); err != nil {
			return err
		}
		if c.Query("confirm") != p.Title && c.Query("confirm") != p.Slug {
			return httpx.BadRequest("Type the name of the project to confirm.")
		}
		repo := p.GroupSlug
		if repo == "" {
			repo = "ungrouped"
		}
		if err := s.Store.DeleteProject(ctx, p.ID); err != nil {
			return httpx.Internal("the project could not be deleted").WithCause(err)
		}
		_ = s.Git.DeleteBranch(ctx, repo, p.Slug)
		_ = s.WS.Destroy(p.ID)
		_ = s.Sched.Reload(ctx)
		s.Store.Audit(ctx, auth.From(c).UserID(), "project.deleted", p.Slug, auth.ClientIP(c), nil)
		return httpx.OK(c)
	})

	one.Get("/variables", func(c *fiber.Ctx) error {
		p := project(c)
		if c.QueryBool("refresh", false) {
			s.Vars.Refresh(c.UserContext(), p)
		}
		list, err := s.Store.VariablesForProject(c.UserContext(), p.ID)
		if err != nil {
			return httpx.Internal("the variables could not be read").WithCause(err)
		}
		return c.JSON(fiber.Map{"variables": list})
	})

	one.Get("/variables/:name/history", func(c *fiber.Ctx) error {
		p := project(c)
		points, err := s.Store.VariableHistory(c.UserContext(), p.ID, c.Params("name"), c.QueryInt("limit", 200))
		if err != nil {
			return httpx.Internal("the history could not be read").WithCause(err)
		}
		return c.JSON(fiber.Map{"points": points})
	})

	// The self-declared tool: project.yaml, its variables and its buttons.
	one.Get("/tool", func(c *fiber.Ctx) error {
		p := project(c)
		spec, err := variables.Spec(c.UserContext(), s.Env, p)
		payload := fiber.Map{"spec": spec, "present": spec != nil}
		if err != nil {
			payload["error"] = err.Error()
		}
		return c.JSON(payload)
	})

	s.mountFiles(one)
	s.mountZip(one)
	s.mountProjectGit(one)
}

func strp(v *string, fallback string) string {
	if v == nil {
		return fallback
	}
	return *v
}

// findGroup accepts an id or a slug, so callers do not have to care which the
// UI happens to send.
func (s *Server) findGroup(ctx context.Context, ref string) (*model.Group, error) {
	if id, err := uuid.Parse(ref); err == nil {
		g, err := s.Store.GroupByID(ctx, id)
		if err != nil {
			return nil, httpx.BadRequest("There is no such group.")
		}
		return g, nil
	}
	g, err := s.Store.GroupBySlug(ctx, ref)
	if err != nil {
		return nil, httpx.BadRequest("There is no such group.")
	}
	return g, nil
}

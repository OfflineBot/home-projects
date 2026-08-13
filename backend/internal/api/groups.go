package api

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/access"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/gitsrv"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/secret"
	"github.com/offlinebot/home-projects/backend/internal/slug"
	"github.com/offlinebot/home-projects/backend/internal/store"
	"github.com/offlinebot/home-projects/backend/internal/variables"
)

func (s *Server) mountGroups(r fiber.Router) {
	g := r.Group("/groups")

	g.Get("/", func(c *fiber.Ctx) error {
		ctx := c.UserContext()
		actor := auth.From(c)
		includeArchived := c.QueryBool("archived", false) && actor.IsUser()

		list, err := s.Store.ListGroups(ctx, includeArchived)
		if err != nil {
			return httpx.Internal("the groups could not be read").WithCause(err)
		}
		list = access.FilterGroups(actor, list)

		counts, err := s.Store.ProjectCounts(ctx, includeArchived)
		if err != nil {
			return httpx.Internal("the projects could not be counted").WithCause(err)
		}
		for i := range list {
			list[i].ProjectCount = counts[list[i].ID]
			list[i].CloneURL = s.Git.CloneURL(list[i].Slug)
		}

		// Projects without a group are shown under "Ungrouped" — that is not a
		// special area, only the projects whose group_id is NULL.
		ungrouped, err := s.Store.ListProjects(ctx, nil, true, includeArchived)
		if err != nil {
			return httpx.Internal("the ungrouped projects could not be read").WithCause(err)
		}
		ungrouped = access.FilterProjects(actor, ungrouped)

		return c.JSON(fiber.Map{
			"groups":    list,
			"ungrouped": briefs(ungrouped),
		})
	})

	g.Post("/", requireOwner, func(c *fiber.Ctx) error {
		var in struct {
			Title       string `json:"title"`
			Slug        string `json:"slug"`
			Description string `json:"description"`
			Visibility  string `json:"visibility"`
			Password    string `json:"password"`
			Color       string `json:"color"`
			Icon        string `json:"icon"`
			Pinned      bool   `json:"pinned"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The group could not be read.")
		}
		if strings.TrimSpace(in.Title) == "" {
			return httpx.BadRequest("A group needs a name.")
		}
		ctx := c.UserContext()

		wanted := in.Slug
		if wanted == "" {
			wanted = slug.Make(in.Title)
		} else {
			wanted = slug.Make(wanted)
		}
		final, err := slug.Unique(wanted, func(s2 string) (bool, error) {
			return s.Store.GroupSlugTaken(ctx, s2)
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

		actor := auth.From(c)
		created, err := s.Store.CreateGroup(ctx, store.NewGroup{
			OwnerID:     actor.User.ID,
			Slug:        final,
			Title:       strings.TrimSpace(in.Title),
			Description: in.Description,
			Visibility:  visibility,
			Color:       in.Color,
			Icon:        in.Icon,
			Pinned:      in.Pinned,
		})
		if err != nil {
			return httpx.Internal("the group could not be created").WithCause(err)
		}
		if visibility == model.VisibilityPassword && in.Password != "" {
			hash, herr := secret.Hash(in.Password)
			if herr == nil {
				pw := &hash
				created, _ = s.Store.UpdateGroup(ctx, created.ID, store.GroupPatch{PasswordHash: &pw})
			}
		}

		// Every group automatically becomes a bare repository. No action, no
		// switch.
		if err := s.Git.EnsureRepo(ctx, created.Slug, created.Title); err != nil {
			return httpx.Internal("the group's repository could not be created").WithCause(err)
		}
		if err := s.Git.InstallHooks(created.Slug); err != nil {
			return httpx.Internal("the repository's push guard could not be installed").WithCause(err)
		}
		created.CloneURL = s.Git.CloneURL(created.Slug)
		s.Store.Audit(ctx, actor.UserID(), "group.created", created.Slug, auth.ClientIP(c), nil)
		return c.Status(fiber.StatusCreated).JSON(created)
	})

	one := g.Group("/:group", s.resolveGroup)

	one.Get("/", func(c *fiber.Ctx) error {
		grp := groupOf(c)
		ctx := c.UserContext()
		actor := auth.From(c)

		projects, err := s.Store.ListProjects(ctx, &grp.ID, false, c.QueryBool("archived", false) && actor.IsUser())
		if err != nil {
			return httpx.Internal("the projects could not be read").WithCause(err)
		}
		projects = access.FilterProjects(actor, projects)
		grp.ProjectCount = len(projects)
		grp.CloneURL = s.Git.CloneURL(grp.Slug)
		for i := range projects {
			s.decorateProject(&projects[i])
		}
		return c.JSON(fiber.Map{"group": grp, "projects": projects})
	})

	one.Post("/unlock", func(c *fiber.Ctx) error {
		grp := groupOf(c)
		return s.unlock(c, grp.ID, grp.PasswordHash, "group-password", grp.Slug)
	})

	one.Patch("/", requireOwner, func(c *fiber.Ctx) error {
		grp := groupOf(c)
		ctx := c.UserContext()
		var in struct {
			Title            *string `json:"title"`
			Slug             *string `json:"slug"`
			Description      *string `json:"description"`
			Visibility       *string `json:"visibility"`
			Password         *string `json:"password"`
			ReadOnly         *bool   `json:"readOnly"`
			PushWithPassword *bool   `json:"pushWithPassword"`
			Color            *string `json:"color"`
			Icon             *string `json:"icon"`
			Pinned           *bool   `json:"pinned"`
			Archived         *bool   `json:"archived"`
			SiteProjectID    *string `json:"siteProjectId"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The change could not be read.")
		}

		patch := store.GroupPatch{
			Title: in.Title, Description: in.Description, ReadOnly: in.ReadOnly,
			Icon: in.Icon, Pinned: in.Pinned, Archived: in.Archived,
			PushWithPassword: in.PushWithPassword,
		}
		// Letting a password write is a sensitive step, like changing who may
		// see the group at all.
		if in.PushWithPassword != nil && *in.PushWithPassword != grp.PushWithPassword {
			if err := s.stepUp(c, "letting the repository password push"); err != nil {
				return err
			}
		}
		if in.Color != nil {
			if !validColor(*in.Color) {
				return httpx.BadRequest("That colour is not in the palette.")
			}
			patch.Color = in.Color
		}

		// Changing visibility is a sensitive step.
		if in.Visibility != nil || in.Password != nil {
			if err := s.stepUp(c, "changing who can see this group"); err != nil {
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

		if in.SiteProjectID != nil {
			if *in.SiteProjectID == "" {
				var none *uuid.UUID
				patch.SiteProjectID = &none
			} else {
				id, err := uuid.Parse(*in.SiteProjectID)
				if err != nil {
					return httpx.BadRequest("That is not a project id.")
				}
				p, err := s.Store.ProjectByID(ctx, id)
				if err != nil || p.GroupID == nil || *p.GroupID != grp.ID {
					return httpx.BadRequest("That project is not in this group.")
				}
				if p.SiteRoot == nil {
					return httpx.BadRequest("%s does not publish a folder yet.", p.Title)
				}
				ref := &id
				patch.SiteProjectID = &ref
			}
		}

		// The address is the repository name: changing it renames the repo and
		// breaks old clone addresses. The UI says so beforehand.
		renamedFrom := ""
		if in.Slug != nil && *in.Slug != grp.Slug {
			newSlug := slug.Make(*in.Slug)
			if err := slug.Validate(newSlug); err != nil {
				return httpx.BadRequest("%v", err)
			}
			taken, err := s.Store.GroupSlugTaken(ctx, newSlug)
			if err != nil {
				return httpx.Internal("the address could not be checked").WithCause(err)
			}
			if taken {
				return httpx.Conflict("Another group already uses the address %q.", newSlug)
			}
			renamedFrom = grp.Slug
			patch.Slug = &newSlug
		}

		updated, err := s.Store.UpdateGroup(ctx, grp.ID, patch)
		if err != nil {
			return httpx.Internal("the group could not be changed").WithCause(err)
		}
		if renamedFrom != "" {
			if err := s.Git.RenameRepo(renamedFrom, updated.Slug); err != nil {
				return httpx.Internal("the repository could not be renamed").WithCause(err)
			}
		}
		if in.ReadOnly != nil && *in.ReadOnly {
			s.pauseSchedulersInGroup(c, grp.ID, "the group "+updated.Title+" is read-only")
		}
		updated.CloneURL = s.Git.CloneURL(updated.Slug)
		s.Store.Audit(ctx, auth.From(c).UserID(), "group.changed", updated.Slug, auth.ClientIP(c), nil)
		return c.JSON(updated)
	})

	// What disappears if this group goes — named before anything happens.
	one.Get("/deletion-preview", requireOwner, func(c *fiber.Ctx) error {
		grp := groupOf(c)
		ctx := c.UserContext()
		projects, err := s.Store.ListProjects(ctx, &grp.ID, false, true)
		if err != nil {
			return httpx.Internal("the projects could not be read").WithCause(err)
		}
		var files int
		var bytes int64
		names := make([]string, 0, len(projects))
		for _, p := range projects {
			names = append(names, p.Title)
			n, b := s.WS.Open(p.ID).Count()
			files += n
			bytes += b
		}
		branches, _ := s.Git.Branches(ctx, grp.Slug)
		return c.JSON(fiber.Map{
			"projects": names, "files": files, "bytes": bytes,
			"repository": s.Git.CloneURL(grp.Slug), "branches": branches,
		})
	})

	one.Delete("/", requireOwner, func(c *fiber.Ctx) error {
		grp := groupOf(c)
		ctx := c.UserContext()
		if err := s.stepUp(c, "deleting the group "+grp.Title); err != nil {
			return err
		}
		if c.Query("confirm") != grp.Title && c.Query("confirm") != grp.Slug {
			return httpx.BadRequest("Type the name of the group to confirm.")
		}
		withProjects := c.QueryBool("withProjects", false)

		projects, err := s.Store.ListProjects(ctx, &grp.ID, false, true)
		if err != nil {
			return httpx.Internal("the projects could not be read").WithCause(err)
		}

		// A group with projects is never silently taken down with it. Either the
		// projects are deleted on purpose, or they move to Ungrouped and keep
		// their history.
		if len(projects) > 0 && !withProjects {
			renamed := map[string]string{}
			for _, p := range projects {
				// Ungrouped is one namespace: a project called "docs" moving out
				// of two different groups cannot keep that address twice. The one
				// that would collide is renamed, and the answer says so rather
				// than failing with a constraint the user never saw.
				free, err := slug.Unique(p.Slug, func(candidate string) (bool, error) {
					return s.Store.ProjectSlugTaken(ctx, nil, candidate)
				})
				if err != nil {
					return httpx.Internal("a free address could not be found").WithCause(err)
				}
				if free != p.Slug {
					if err := s.Git.RenameBranch(ctx, grp.Slug, p.Slug, free); err != nil {
						return httpx.Internal("the branch could not be renamed").WithCause(err)
					}
					renamed[p.Slug] = free
				}
				var none *uuid.UUID
				if _, err := s.Store.UpdateProject(ctx, p.ID, store.ProjectPatch{
					GroupID: &none, Slug: &free,
				}); err != nil {
					return httpx.Internal("the project could not be moved").WithCause(err)
				}
			}
			if err := s.Git.EnsureRepo(ctx, gitsrv.UngroupedRepo, "Ungrouped"); err == nil {
				_ = s.Git.InstallHooks(gitsrv.UngroupedRepo)
				for _, p := range projects {
					branch := p.Slug
					if to, ok := renamed[p.Slug]; ok {
						branch = to
					}
					_ = s.Git.MoveBranch(ctx, grp.Slug, gitsrv.UngroupedRepo, branch)
				}
			}
			if err := s.Store.DeleteGroup(ctx, grp.ID); err != nil {
				return httpx.Internal("the group could not be deleted").WithCause(err)
			}
			_ = s.Git.DeleteRepo(grp.Slug)
			s.Store.Audit(ctx, auth.From(c).UserID(), "group.deleted", grp.Slug, auth.ClientIP(c),
				map[string]any{"movedProjects": len(projects)})
			return c.JSON(fiber.Map{"movedToUngrouped": len(projects), "renamed": renamed})
		}

		// With the projects: the rows go too, not only their files. Leaving them
		// behind would put a project in the listing whose data is gone.
		for _, p := range projects {
			if err := s.Store.DeleteProject(ctx, p.ID); err != nil {
				return httpx.Internal("the project %s could not be deleted", p.Title).WithCause(err)
			}
			_ = s.WS.Destroy(p.ID)
		}
		if err := s.Store.DeleteGroup(ctx, grp.ID); err != nil {
			return httpx.Internal("the group could not be deleted").WithCause(err)
		}
		_ = s.Git.DeleteRepo(grp.Slug)
		_ = s.Sched.Reload(ctx)
		s.Store.Audit(ctx, auth.From(c).UserID(), "group.deleted", grp.Slug, auth.ClientIP(c),
			map[string]any{"withProjects": len(projects)})
		return c.JSON(fiber.Map{"deletedProjects": len(projects)})
	})

	// The whole point of a group: it collects the variables of its projects.
	one.Get("/variables", func(c *fiber.Ctx) error {
		grp := groupOf(c)
		actor := auth.From(c)
		visible := func(projectID uuid.UUID) bool {
			p, err := s.Store.ProjectByID(c.UserContext(), projectID)
			if err != nil {
				return false
			}
			return access.CanReadProject(actor, p)
		}
		view, err := variables.ForGroup(c.UserContext(), s.Store, grp.ID, visible)
		if err != nil {
			return httpx.Internal("the variables could not be read").WithCause(err)
		}
		return c.JSON(view)
	})

	one.Post("/variables", requireOwner, func(c *fiber.Ctx) error {
		grp := groupOf(c)
		var in model.GroupVariable
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The variable could not be read.")
		}
		if in.Name == "" {
			return httpx.BadRequest("A variable needs a name.")
		}
		in.GroupID = grp.ID
		created, err := s.Store.CreateGroupVariable(c.UserContext(), in)
		if err != nil {
			return httpx.Internal("the variable could not be stored").WithCause(err)
		}
		return c.Status(fiber.StatusCreated).JSON(created)
	})

	one.Delete("/variables/:id", requireOwner, func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return httpx.BadRequest("That is not a variable id.")
		}
		if err := s.Store.DeleteGroupVariable(c.UserContext(), id); err != nil {
			return httpx.NotFound("There is no such variable.")
		}
		return httpx.OK(c)
	})

	one.Get("/git", func(c *fiber.Ctx) error {
		grp := groupOf(c)
		branches, err := s.Git.Branches(c.UserContext(), grp.Slug)
		if err != nil {
			return httpx.Internal("the repository could not be read").WithCause(err)
		}
		commits, _ := s.Git.Log(c.UserContext(), grp.Slug, "main", 20)
		payload := fiber.Map{
			"cloneUrl": s.Git.CloneURL(grp.Slug),
			"exists":   s.Git.RepoExists(grp.Slug),
			"branches": branches,
			"commits":  commits,
			"hint":     "git clone -b <project-slug> --single-branch " + s.Git.CloneURL(grp.Slug),
		}
		if s.Cfg.SSHEnabled() {
			ssh := gitsrv.SSHCloneURL(s.Cfg.GitSSHHost, grp.Slug)
			payload["sshCloneUrl"] = ssh
			payload["sshHint"] = "git clone -b <project-slug> --single-branch " + ssh
		}
		return c.JSON(payload)
	})
}

func (s *Server) pauseSchedulersInGroup(c *fiber.Ctx, groupID uuid.UUID, reason string) {
	ctx := c.UserContext()
	projects, err := s.Store.ListProjects(ctx, &groupID, false, true)
	if err != nil {
		return
	}
	for _, p := range projects {
		_, _ = s.Store.PauseSchedulersForProject(ctx, p.ID, reason)
	}
	_ = s.Sched.Reload(ctx)
}

func briefs(list []model.Project) []model.ProjectBrief {
	out := make([]model.ProjectBrief, 0, len(list))
	for _, p := range list {
		out = append(out, model.ProjectBrief{
			ID: p.ID, Slug: p.Slug, Title: p.Title, Color: p.Color, Icon: p.Icon,
		})
	}
	return out
}

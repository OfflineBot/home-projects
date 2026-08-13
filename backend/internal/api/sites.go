package api

import (
	"path"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/gitsrv"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/workspace"
)

// Any project can be published as a static site: site_root points at a folder
// that is served under /s/<group>/<project>/. Publishing does not make the
// project public — only the served folder is, the rest stays as configured.
func (s *Server) mountSites(app *fiber.App) {
	app.Get("/s/:group/:project/*", func(c *fiber.Ctx) error {
		return s.serveSite(c, c.Params("group"), c.Params("project"), c.Params("*"))
	})
	app.Get("/s/:group/:project", func(c *fiber.Ctx) error {
		// Without the trailing slash relative links in the page would break.
		return c.Redirect("/s/"+c.Params("group")+"/"+c.Params("project")+"/", fiber.StatusMovedPermanently)
	})

	// A group can name one project that runs at the group's own address.
	app.Get("/s/:group/", func(c *fiber.Ctx) error {
		return s.serveGroupSite(c, c.Params("group"), "")
	})
	app.Get("/s/:group", func(c *fiber.Ctx) error {
		return c.Redirect("/s/"+c.Params("group")+"/", fiber.StatusMovedPermanently)
	})
}

func (s *Server) serveGroupSite(c *fiber.Ctx, groupSlug, rest string) error {
	ctx := c.UserContext()
	grp, err := s.Store.GroupBySlug(ctx, groupSlug)
	if err != nil || grp.SiteProjectID == nil {
		return httpx.NotFound("Nothing is published at this address.")
	}
	p, err := s.Store.ProjectByID(ctx, *grp.SiteProjectID)
	if err != nil {
		return httpx.NotFound("Nothing is published at this address.")
	}
	return s.sendSiteFile(c, p, rest)
}

func (s *Server) serveSite(c *fiber.Ctx, groupSlug, projectSlug, rest string) error {
	ctx := c.UserContext()

	var grp *model.Group
	if groupSlug != gitsrv.UngroupedRepo {
		g, err := s.Store.GroupBySlug(ctx, groupSlug)
		if err != nil {
			return httpx.NotFound("Nothing is published at this address.")
		}
		grp = g
	}

	var groupID *uuid.UUID
	if grp != nil {
		groupID = &grp.ID
	}
	p, err := s.Store.ProjectBySlug(ctx, groupID, projectSlug)
	if err != nil {
		return httpx.NotFound("Nothing is published at this address.")
	}
	return s.sendSiteFile(c, p, rest)
}

func (s *Server) sendSiteFile(c *fiber.Ctx, p *model.Project, rest string) error {
	if p.SiteRoot == nil {
		return httpx.NotFound("This project does not publish a folder.")
	}
	if p.Archived {
		return httpx.NotFound("This project is archived.")
	}
	root := strings.Trim(*p.SiteRoot, "/")

	rel := strings.TrimPrefix(rest, "/")
	if rel == "" {
		rel = "index.html"
	}
	full := path.Join(root, rel)

	fs := s.WS.Open(p.ID)
	entry, err := fs.Stat(full)
	if err == nil && entry.IsDir {
		full = path.Join(full, "index.html")
		entry, err = fs.Stat(full)
	}
	if err != nil {
		// SPA fallback: an unknown path inside a published folder falls back to
		// index.html, so client-side routing works.
		fallback := path.Join(root, "index.html")
		if _, ferr := fs.Stat(fallback); ferr == nil && path.Ext(rel) == "" {
			full = fallback
			entry, err = fs.Stat(full)
		}
		if err != nil {
			return httpx.NotFound("There is no page at this address.")
		}
	}

	handle, info, err := fs.OpenFile(full)
	if err != nil {
		return httpx.NotFound("There is no page at this address.")
	}
	c.Set("Content-Type", workspace.MimeOf(entry.Name))
	if strings.EqualFold(entry.Name, "index.html") {
		// The same rule as for the app itself: never serve a stale entry point.
		c.Set("Cache-Control", "no-cache, must-revalidate")
	} else {
		c.Set("Cache-Control", "public, max-age=300")
	}
	return c.SendStream(handle, int(info.Size()))
}

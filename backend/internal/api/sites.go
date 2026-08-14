package api

import (
	"path"
	"strings"
	"text/template"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/access"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/gitsrv"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/workspace"
)

// Any project can be published as a static site: site_root points at a folder
// that is served under /s/<group>/<project>/. Publishing does not make the
// project public — only the served folder is, the rest stays as configured.
func (s *Server) mountSites(app *fiber.App) {
	// The actor is attached here too: a protected site is opened with the same
	// unlock cookie as everything else, and a signed-in owner never sees the
	// password form for their own site.
	sites := app.Group("/s", s.Auth.Attach())

	// The password form posts back to the address it was shown at.
	sites.Post("/:group/:project/*", func(c *fiber.Ctx) error {
		return s.unlockSite(c, c.Params("group"), c.Params("project"))
	})
	sites.Post("/:group/:project", func(c *fiber.Ctx) error {
		return s.unlockSite(c, c.Params("group"), c.Params("project"))
	})

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

	// The site says where it is reachable and whether a password stands in
	// front of it. Both belong to the site, not to the project that happens to
	// hold the files.
	// Publishing is an act of its own: naming a folder puts *that folder* at
	// this address, whatever the project's visibility says about the rest of
	// it. The one thing visibility decides here is whether a password stands
	// in front — which is the whole reason a site is its own project.
	if p.Visibility == model.VisibilityPassword && !access.CanReadProject(auth.From(c), p) {
		return s.askSitePassword(c, p, "")
	}

	// The files may live in another project. What is published is still this
	// project's address; only the material comes from elsewhere.
	holder := p
	if p.SiteSourceID != nil && *p.SiteSourceID != p.ID {
		src, err := s.Store.ProjectByID(c.UserContext(), *p.SiteSourceID)
		if err != nil {
			return httpx.NotFound("The project this site shows no longer exists.")
		}
		if src.Archived {
			return httpx.NotFound("The project this site shows is archived.")
		}
		holder = src
	}
	root := strings.Trim(*p.SiteRoot, "/")

	rel := strings.TrimPrefix(rest, "/")
	if rel == "" {
		rel = "index.html"
	}
	full := path.Join(root, rel)

	fs := s.WS.Open(holder.ID)
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

// askSitePassword is the whole login for a protected site: one field, no app,
// no account. A visitor who has the password gets the same unlock cookie the
// rest of the server uses; everyone else sees this page and nothing else.
func (s *Server) askSitePassword(c *fiber.Ctx, p *model.Project, message string) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	c.Set("Cache-Control", "no-store")
	notice := ""
	if message != "" {
		notice = `<p class="bad">` + template.HTMLEscapeString(message) + `</p>`
	}
	page := `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + template.HTMLEscapeString(p.Title) + `</title>
<style>
 :root { color-scheme: dark }
 body { margin:0; min-height:100vh; display:grid; place-items:center;
        background:#1e1e2e; color:#cdd6f4; font:16px/1.6 system-ui,sans-serif }
 form { width:min(22rem,90vw); background:#181825; padding:1.6rem; border-radius:.8rem;
        border:1px solid #313244 }
 h1 { font-size:1.1rem; margin:0 0 1rem; color:#cba6f7 }
 input { width:100%; padding:.6rem .7rem; border-radius:.45rem; border:1px solid #45475a;
         background:#11111b; color:#cdd6f4; font:inherit; box-sizing:border-box }
 button { margin-top:.9rem; width:100%; padding:.6rem; border:0; border-radius:.45rem;
          background:#cba6f7; color:#11111b; font:inherit; font-weight:600; cursor:pointer }
 .bad { color:#f38ba8; margin:0 0 .8rem; font-size:.9rem }
</style></head><body>
<form method="post" action="">
 <h1>` + template.HTMLEscapeString(p.Title) + `</h1>` + notice + `
 <input type="password" name="password" placeholder="Password" autofocus required>
 <button type="submit">Open</button>
</form></body></html>`
	return c.Status(fiber.StatusUnauthorized).SendString(page)
}

// unlockSite takes the password from the form and, if it fits, sets the same
// unlock cookie the app uses. Then it sends the visitor back to the page they
// asked for.
func (s *Server) unlockSite(c *fiber.Ctx, groupSlug, projectSlug string) error {
	ctx := c.UserContext()
	var groupID *uuid.UUID
	if groupSlug != gitsrv.UngroupedRepo {
		grp, err := s.Store.GroupBySlug(ctx, groupSlug)
		if err != nil {
			return httpx.NotFound("Nothing is published at this address.")
		}
		groupID = &grp.ID
	}
	p, err := s.Store.ProjectBySlug(ctx, groupID, projectSlug)
	if err != nil || p.Visibility != model.VisibilityPassword {
		return httpx.NotFound("Nothing is published at this address.")
	}
	// A project without a password of its own is opened by its group's — the
	// same rule the app follows, so a protected group stays one thing.
	id, hash, scope := p.ID, p.PasswordHash, "project-password"
	if hash == "" && p.GroupID != nil {
		if grp, err := s.Store.GroupByID(ctx, *p.GroupID); err == nil && grp.PasswordHash != "" {
			id, hash, scope = grp.ID, grp.PasswordHash, "group-password"
		}
	}
	if err := s.unlock(c, id, hash, scope, p.Slug); err != nil {
		return s.askSitePassword(c, p, "That password does not open this page.")
	}
	return c.Redirect(c.OriginalURL(), fiber.StatusSeeOther)
}

// Package site is the static-site capability.
//
// The serving itself is core behaviour: a project with a `site_root` is served
// under /s/<group>/<project>/, whether or not this capability is switched on.
// What lives here is the preset, the preview and the checks that tell you
// whether the folder you picked actually contains a site.
package site

import (
	"context"
	"path"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/store"
	"github.com/offlinebot/home-projects/backend/internal/workspace"
)

type Capability struct{ capability.Base }

func New() Capability { return Capability{} }

func (Capability) Name() string   { return "site" }
func (Capability) Title() string  { return "Site" }
func (Capability) Icon() string   { return "globe" }
func (Capability) Owns() []string { return []string{"index.html"} }

const defaultIndex = `<!doctype html>
<html lang="en">
	<head>
		<meta charset="utf-8" />
		<meta name="viewport" content="width=device-width, initial-scale=1" />
		<title>%TITLE%</title>
		<style>
			:root { color-scheme: dark; }
			body {
				margin: 0; min-height: 100vh; display: grid; place-items: center;
				background: #1e1e2e; color: #cdd6f4;
				font: 16px/1.6 system-ui, sans-serif;
			}
			main { max-width: 40rem; padding: 2rem; }
			h1 { color: #cba6f7; margin: 0 0 .5rem; }
			code { background: #313244; padding: .15em .4em; border-radius: .3em; }
		</style>
	</head>
	<body>
		<main>
			<h1>%TITLE%</h1>
			<p>This page is served from the project's <code>public/</code> folder.</p>
			<p>Replace it — by uploading, editing, or pushing to the project's branch.</p>
		</main>
	</body>
</html>
`

func (Capability) Presets() []capability.Preset {
	return []capability.Preset{{
		Key:          "website",
		Title:        "Website",
		Description:  "A folder that is served as a static site, like GitHub Pages.",
		Icon:         "globe",
		DefaultTab:   "site",
		Capabilities: []string{"site"},
		SiteRoot:     "public",
		Seed: []capability.SeedFile{{
			Path: "public/index.html",
			Content: func(p *model.Project) []byte {
				return []byte(strings.ReplaceAll(defaultIndex, "%TITLE%", p.Title))
			},
		}},
	}}
}

// Routes are mounted under /api/projects/:project/site
func (Capability) Routes(env *capability.Env, r fiber.Router) {
	r.Get("/status", func(c *fiber.Ctx) error {
		if err := capability.RequireRead(c); err != nil {
			return err
		}
		p := capability.Project(c)
		return c.JSON(status(env, p))
	})

	// Which folders below the project could be served — the settings dialog
	// offers them instead of asking for a path to be typed.
	r.Get("/candidates", func(c *fiber.Ctx) error {
		if err := capability.RequireRead(c); err != nil {
			return err
		}
		p := capability.Project(c)
		fs := env.Files.Workspace().Open(p.ID)
		found := []string{}
		_ = fs.Walk("", func(e workspace.Entry) error {
			if e.IsDir {
				return nil
			}
			if strings.EqualFold(path.Base(e.Path), "index.html") {
				dir := path.Dir(e.Path)
				if dir == "." {
					dir = ""
				}
				found = append(found, dir)
			}
			return nil
		})
		return c.JSON(fiber.Map{"candidates": found})
	})
}

type Status struct {
	SiteRoot  string `json:"siteRoot"`
	URL       string `json:"url"`
	HasIndex  bool   `json:"hasIndex"`
	Published bool   `json:"published"`
	Note      string `json:"note,omitempty"`
}

func status(env *capability.Env, p *model.Project) Status {
	s := Status{}
	if p.SiteRoot != nil {
		s.SiteRoot = *p.SiteRoot
		s.Published = true
	}
	group := p.GroupSlug
	if group == "" {
		group = "ungrouped"
	}
	s.URL = env.Cfg.PublicURL + "/s/" + group + "/" + p.Slug + "/"
	if s.Published {
		index := path.Join(s.SiteRoot, "index.html")
		s.HasIndex = env.Files.Exists(p, index)
		if !s.HasIndex {
			s.Note = "There is no index.html in " + s.SiteRoot + " yet, so the address shows nothing."
		}
	} else {
		s.Note = "Pick a folder in the project's settings to publish it."
	}
	return s
}

func (Capability) Exports(ctx context.Context, env *capability.Env, p *model.Project) ([]store.VariableInput, error) {
	st := status(env, p)
	return []store.VariableInput{
		{Name: "published", Type: "bool", Value: st.Published && st.HasIndex, Source: "capability:site"},
		{Name: "site_url", Type: "text", Value: st.URL, Source: "capability:site"},
	}, nil
}

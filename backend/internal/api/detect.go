package api

import (
	"path"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/workspace"
)

// What a project turns out to contain.
//
// Capabilities are switched on by hand, which is fine until files arrive some
// other way: a git push, a zip, a scheduler. Then a project full of .ics files
// has no calendar in it and nobody said so. This looks at what is actually
// there and says which capabilities would find something to do — it never
// switches anything on by itself, because a project deciding to become a
// website because someone pushed an index.html is a surprise, not a feature.

type suggestion struct {
	Name    string   `json:"name"`
	Title   string   `json:"title"`
	Icon    string   `json:"icon"`
	Matched []string `json:"matched"`
	On      bool     `json:"on"`
}

func (s *Server) mountDetect(one fiber.Router) {
	one.Get("/detect", func(c *fiber.Ctx) error {
		p := project(c)
		return c.JSON(fiber.Map{"capabilities": s.detect(p)})
	})

}

func (s *Server) detect(p *model.Project) []suggestion {
	fs := s.WS.Open(p.ID)
	var files []string
	_ = fs.Walk("", func(e workspace.Entry) error {
		if !e.IsDir {
			files = append(files, e.Path)
		}
		return nil
	})

	out := []suggestion{}
	for _, c := range capability.All() {
		patterns := c.Owns()
		if len(patterns) == 0 {
			continue
		}
		var matched []string
		for _, f := range files {
			if len(matched) >= 5 {
				break
			}
			if ownsFile(patterns, f) {
				matched = append(matched, f)
			}
		}
		if len(matched) == 0 {
			continue
		}
		out = append(out, suggestion{
			Name: c.Name(), Title: c.Title(), Icon: c.Icon(),
			Matched: matched, On: p.Has(c.Name()),
		})
	}
	return out
}

// ownsFile matches a capability's patterns against a path. The patterns are the
// ones a capability already declares — "*.md", "events/*.ics" — so nothing new
// has to be written down anywhere.
func ownsFile(patterns []string, file string) bool {
	base := path.Base(file)
	for _, pattern := range patterns {
		if strings.Contains(pattern, "/") {
			if ok, _ := path.Match(pattern, file); ok {
				return true
			}
			continue
		}
		if ok, _ := path.Match(pattern, base); ok {
			return true
		}
	}
	return false
}

// mountGroupDetect answers for every project in a group at once.
func (s *Server) mountGroupDetect(one fiber.Router) {
	one.Get("/detect", func(c *fiber.Ctx) error {
		grp := groupOf(c)
		if grp == nil {
			return httpx.NotFound("No group at this address.")
		}
		projects, err := s.Store.ListProjects(c.UserContext(), &grp.ID, false, true)
		if err != nil {
			return httpx.Internal("the projects could not be read").WithCause(err)
		}
		out := []fiber.Map{}
		for i := range projects {
			found := s.detect(&projects[i])
			var missing []suggestion
			for _, f := range found {
				if !f.On {
					missing = append(missing, f)
				}
			}
			if len(missing) == 0 {
				continue
			}
			out = append(out, fiber.Map{
				"project": projects[i].Slug, "projectId": projects[i].ID, "capabilities": missing,
			})
		}
		return c.JSON(fiber.Map{"projects": out})
	})
}

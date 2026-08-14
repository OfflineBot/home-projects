// Package api wires the HTTP surface together.
//
// Every route that touches a project or a group goes through the resolver in
// middleware.go, so the permission check happens in exactly one place. The
// capabilities mount themselves underneath — the core never names one.
package api

import (
	"github.com/gofiber/fiber/v2"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/config"
	"github.com/offlinebot/home-projects/backend/internal/events"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/gitsrv"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/scheduler"
	"github.com/offlinebot/home-projects/backend/internal/store"
	"github.com/offlinebot/home-projects/backend/internal/variables"
	"github.com/offlinebot/home-projects/backend/internal/workspace"
)

type Server struct {
	Cfg   *config.Config
	Store *store.Store
	Auth  *auth.Service
	Files *files.Service
	Git   *gitsrv.Service
	WS    *workspace.Store
	Env   *capability.Env
	Sched *scheduler.Runner
	Vars  *variables.Collector
	Bus   *events.Bus
}

// Mount registers every route on the app.
func (s *Server) Mount(app *fiber.App) {
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"ok": true, "capabilities": capability.Names()})
	})

	// git over HTTP sits outside /api: it speaks git's protocol, not ours.
	s.mountGitTransport(app)
	// Static sites are served from the project files, outside /api as well.
	s.mountSites(app)
	s.mountOnewayPublic(app)

	api := app.Group("/api", s.Auth.Attach())

	s.mountMeta(api)
	s.mountAuth(api)
	s.mountGroups(api)
	s.mountProjects(api)
	s.mountLinks(api)
	s.mountAccounts(api)
	s.mountSchedulers(api)
	s.mountDashboard(api)
	s.mountSSH(api)
	s.mountGitAttempts(api)
	s.mountBlueprint(api)
	s.mountFilters(api)
	s.mountGraph(api)
	s.mountClientErrors(api)

	// Capability routes: /api/projects/:project/<name>/… and the shared
	// /api/capabilities/<name>/…. The core does not know a single name here.
	shared := api.Group("/capabilities")
	for _, c := range capability.All() {
		name := c.Name()
		group := api.Group("/projects/:project/"+name, s.resolveProject(requireCapability(name)))
		c.Routes(s.Env, group)
		c.SharedRoutes(s.Env, shared.Group("/"+name))
	}

	api.Use(func(c *fiber.Ctx) error {
		return httpx.NotFound("There is no endpoint %s %s.", c.Method(), c.Path())
	})
}

// requireCapability refuses a capability's routes on a project that has it
// switched off — the answer says so instead of returning something empty.
func requireCapability(name string) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		p := capability.Project(c)
		if p == nil {
			return httpx.NotFound("No project at this address.")
		}
		if !p.Has(name) {
			return httpx.New(fiber.StatusConflict, "capability_off",
				"The project %q does not have %q switched on.", p.Title, name)
		}
		return nil
	}
}

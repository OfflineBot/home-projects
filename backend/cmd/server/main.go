// Command server is home-projects.
//
// Everything is a project. Projects live in groups. A group is the virtual
// environment, a project is the container inside it.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	// The time zone database is embedded: calendars carry TZID values, and a
	// container without tzdata would silently read them as UTC.
	_ "time/tzdata"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/accounts"
	"github.com/offlinebot/home-projects/backend/internal/api"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/capabilities/all"
	"github.com/offlinebot/home-projects/backend/internal/capabilities/automation"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/config"
	"github.com/offlinebot/home-projects/backend/internal/db"
	"github.com/offlinebot/home-projects/backend/internal/events"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/filter"
	"github.com/offlinebot/home-projects/backend/internal/gitsrv"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/scheduler"
	"github.com/offlinebot/home-projects/backend/internal/secret"
	"github.com/offlinebot/home-projects/backend/internal/store"
	"github.com/offlinebot/home-projects/backend/internal/variables"
	"github.com/offlinebot/home-projects/backend/internal/workspace"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	if err := run(); err != nil {
		slog.Error("the server did not start", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The capabilities register themselves before anything else, because their
	// migrations and their presets are part of the startup.
	all.Register()
	slog.Info("capabilities", "installed", capability.Names())

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.MigrateCore(ctx, pool); err != nil {
		return err
	}
	for _, c := range capability.All() {
		fsys := c.Migrations()
		if fsys == nil {
			continue
		}
		if err := db.Migrate(ctx, pool, "capability:"+c.Name(), fsys); err != nil {
			return err
		}
	}

	st := store.New(pool)
	box, err := secret.NewBox(cfg.SecretKey)
	if err != nil {
		return err
	}
	ws, err := workspace.NewStore(cfg.DataDir)
	if err != nil {
		return err
	}
	bus := events.NewBus()
	git := gitsrv.New(cfg, ws)
	fileSvc := files.New(st, ws, git, bus)

	env := &capability.Env{
		Cfg: cfg, Store: st, Files: fileSvc, Bus: bus, Box: box,
	}

	// Every write — from the UI, from a capability, from a scheduler or from a
	// push — updates the indexes of the capabilities the project has switched
	// on. This is the only wiring between files and capabilities, and it goes
	// through the registry, so the core still names none of them.
	fileSvc.SetIndexer(func(ctx context.Context, p *model.Project, path string) {
		for _, name := range p.Capabilities {
			c, ok := capability.Get(name)
			if !ok {
				continue
			}
			if err := c.Index(ctx, env, p, path); err != nil {
				slog.Warn("index could not be updated", "capability", name, "project", p.Slug, "error", err)
			}
		}
	})

	sched := scheduler.New(env)
	env.RunScheduler = func(ctx context.Context, id uuid.UUID, trigger string) error {
		_, err := sched.Run(ctx, id, trigger)
		return err
	}
	env.UseAccount = func(ctx context.Context, id uuid.UUID, fn func(secret []byte) error) error {
		return accounts.Attempt(ctx, env, id, 2*time.Minute, fn)
	}
	env.Router = func(ctx context.Context, ref string) func([]capability.RouteItem) []capability.RouteTo {
		rules, err := filter.RulesFor(ctx, st, ref)
		if err != nil || len(rules) == 0 {
			return nil
		}
		return func(items []capability.RouteItem) []capability.RouteTo {
			in := make([]filter.Item, len(items))
			for i, item := range items {
				in[i] = filter.Item{Name: item.Name, Path: item.Path, Semester: item.Semester}
			}
			out := make([]capability.RouteTo, len(items))
			for i, d := range filter.Plan(rules, in) {
				out[i] = capability.RouteTo{
					Project: d.Project, Folder: d.Folder, Skip: d.Skip,
					Matched: d.Matched, Rule: d.Rule,
				}
			}
			return out
		}
	}

	if err := bootstrap(ctx, cfg, st, git, fileSvc); err != nil {
		return err
	}

	vars := variables.New(env, os.Getenv("ALLOW_PROJECT_COMMANDS") == "true")
	vars.Start(ctx, time.Minute)

	if err := sched.Start(ctx); err != nil {
		return err
	}
	defer sched.Stop()

	automation.Start(ctx, env)

	app := fiber.New(fiber.Config{
		AppName:               "home-projects",
		ErrorHandler:          httpx.ErrorHandler,
		BodyLimit:             int(cfg.MaxUploadSize) + (16 << 20),
		DisableStartupMessage: true,
		ReadTimeout:           5 * time.Minute,
		WriteTimeout:          10 * time.Minute,
		ProxyHeader:           "X-Forwarded-For",
	})
	app.Use(recover.New())
	app.Use(securityHeaders(cfg))
	// Compression is off for the git routes: git streams its own packs.
	app.Use(func(c *fiber.Ctx) error {
		if strings.HasPrefix(c.Path(), "/git/") {
			return c.Next()
		}
		return compress.New()(c)
	})
	if cfg.IsDev() {
		app.Use(cors.New(cors.Config{
			AllowOrigins:     "http://localhost:5173",
			AllowCredentials: true,
			AllowHeaders:     "Origin, Content-Type, Accept, Authorization, Git-Protocol",
		}))
	}
	// Every handler gets a context that is cancelled when the request ends.
	app.Use(func(c *fiber.Ctx) error {
		c.SetUserContext(c.Context())
		return c.Next()
	})

	server := &api.Server{
		Cfg: cfg, Store: st, Auth: auth.New(cfg, st), Files: fileSvc, Git: git,
		WS: ws, Env: env, Sched: sched, Vars: vars, Bus: bus,
	}
	// The project sorts what arrives in it. This is wired here because the walk
	// lives next to the button that does the same thing by hand.
	env.SortProject = server.SortProject
	server.Mount(app)

	go func() {
		<-ctx.Done()
		slog.Info("shutting down")
		_ = app.ShutdownWithTimeout(20 * time.Second)
	}()

	slog.Info("listening", "addr", cfg.Addr, "public", cfg.PublicURL,
		"data", cfg.DataDir, "git", cfg.GitDir)
	if err := app.Listen(cfg.Addr); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// securityHeaders sets what the brief asks for: HTTPS only, no framing, and an
// entry point that is never cached.
func securityHeaders(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("Referrer-Policy", "same-origin")
		c.Set("X-Frame-Options", "SAMEORIGIN")
		if cfg.CookieSecure {
			c.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		return c.Next()
	}
}

// bootstrap creates the owner account and the groups the server starts with.
//
// Those groups are ordinary groups: you could delete every one of them and the
// server would keep running. They are not areas in the code.
func bootstrap(ctx context.Context, cfg *config.Config, st *store.Store, git *gitsrv.Service, fileSvc *files.Service) error {
	count, err := st.CountUsers(ctx)
	if err != nil {
		return err
	}
	if count == 0 {
		username := cfg.OwnerUsername
		password := cfg.OwnerPassword
		if username == "" || password == "" {
			return errors.New("there is no user yet: set OWNER_USERNAME and OWNER_PASSWORD once, then start again")
		}
		if len(password) < 10 {
			return errors.New("OWNER_PASSWORD has to be at least 10 characters long")
		}
		hash, err := secret.Hash(password)
		if err != nil {
			return err
		}
		user, err := st.CreateUser(ctx, username, hash, username, true)
		if err != nil {
			return err
		}
		slog.Info("owner account created", "username", user.Username)
	}

	ownerID, err := st.OwnerID(ctx)
	if err != nil {
		return err
	}

	// The repository for projects without a group.
	if err := git.EnsureRepo(ctx, gitsrv.UngroupedRepo, "Ungrouped"); err != nil {
		return err
	}
	if err := git.InstallHooks(gitsrv.UngroupedRepo); err != nil {
		return err
	}

	groups, err := st.ListGroups(ctx, true)
	if err != nil {
		return err
	}
	if len(groups) > 0 {
		// Make sure every group still has its repository — a restored backup or
		// a fresh volume would otherwise be missing them.
		for _, g := range groups {
			if !git.RepoExists(g.Slug) {
				if err := git.EnsureRepo(ctx, g.Slug, g.Title); err != nil {
					return err
				}
			}
			if err := git.InstallHooks(g.Slug); err != nil {
				return err
			}
		}
		return nil
	}

	starters := []store.NewGroup{
		{Slug: "personal", Title: "Personal", Description: "Everything private.", Color: "mauve", Icon: "home", Pinned: true},
		{Slug: "studies", Title: "Studies", Description: "Timetable, material, grades.", Color: "blue", Icon: "graduation", Pinned: true},
		{Slug: "home", Title: "Home", Description: "Devices, lights, the PC.", Color: "green", Icon: "cpu"},
		{Slug: "web", Title: "Web", Description: "Sites and the code behind them.", Color: "peach", Icon: "globe"},
	}
	for _, g := range starters {
		g.OwnerID = ownerID
		created, err := st.CreateGroup(ctx, g)
		if err != nil {
			return err
		}
		if err := git.EnsureRepo(ctx, created.Slug, created.Title); err != nil {
			return err
		}
		if err := git.InstallHooks(created.Slug); err != nil {
			return err
		}
		slog.Info("starting group created", "slug", created.Slug)
	}
	return nil
}

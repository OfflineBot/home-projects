package api

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/accounts"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/scheduler"
	"github.com/offlinebot/home-projects/backend/internal/store"
)

// Accounts are a central menu, never part of a project: credentials have no
// business in a file tree that gets versioned, linked, published and cloned.
func (s *Server) mountAccounts(r fiber.Router) {
	g := r.Group("/accounts", requireOwner)

	g.Get("/", func(c *fiber.Ctx) error {
		list, err := s.Store.ListAccounts(c.UserContext())
		if err != nil {
			return httpx.Internal("the accounts could not be read").WithCause(err)
		}
		return c.JSON(fiber.Map{"accounts": list, "kinds": capability.AccountKinds()})
	})

	g.Post("/", func(c *fiber.Ctx) error {
		if err := s.stepUp(c, "storing credentials"); err != nil {
			return err
		}
		var in struct {
			Kind   string          `json:"kind"`
			Title  string          `json:"title"`
			Config json.RawMessage `json:"config"`
			Secret string          `json:"secret"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The account could not be read.")
		}
		kind, ok := capability.AccountKindByName(in.Kind)
		if !ok {
			return httpx.BadRequest("There is no account kind called %q on this server.", in.Kind)
		}
		if strings.TrimSpace(in.Title) == "" {
			in.Title = kind.Title
		}
		var sealed []byte
		if in.Secret != "" {
			var err error
			sealed, err = s.Env.Box.Seal([]byte(in.Secret))
			if err != nil {
				return httpx.Internal("the credential could not be encrypted").WithCause(err)
			}
		}
		actor := auth.From(c)
		created, err := s.Store.CreateAccount(c.UserContext(), actor.User.ID, in.Kind, in.Title, in.Config, sealed)
		if err != nil {
			return httpx.Internal("the account could not be stored").WithCause(err)
		}
		s.Store.Audit(c.UserContext(), actor.UserID(), "account.created", created.Title, auth.ClientIP(c),
			map[string]any{"kind": in.Kind})
		return c.Status(fiber.StatusCreated).JSON(created)
	})

	g.Patch("/:id", func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return httpx.BadRequest("That is not an account id.")
		}
		if err := s.stepUp(c, "changing an account"); err != nil {
			return err
		}
		var in struct {
			Title  string          `json:"title"`
			Config json.RawMessage `json:"config"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The change could not be read.")
		}
		existing, err := s.Store.AccountByID(c.UserContext(), id)
		if err != nil {
			return httpx.NotFound("There is no such account.")
		}
		if in.Title == "" {
			in.Title = existing.Title
		}
		if len(in.Config) == 0 {
			in.Config = existing.Config
		}
		updated, err := s.Store.UpdateAccountConfig(c.UserContext(), id, in.Title, in.Config)
		if err != nil {
			return httpx.Internal("the account could not be changed").WithCause(err)
		}
		return c.JSON(updated)
	})

	// The only way back after a consumed credential: type it in again.
	g.Post("/:id/secret", func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return httpx.BadRequest("That is not an account id.")
		}
		if err := s.stepUp(c, "entering a password"); err != nil {
			return err
		}
		var in struct {
			Secret string `json:"secret"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The password could not be read.")
		}
		if in.Secret == "" {
			return httpx.BadRequest("The password may not be empty.")
		}
		sealed, err := s.Env.Box.Seal([]byte(in.Secret))
		if err != nil {
			return httpx.Internal("the credential could not be encrypted").WithCause(err)
		}
		if err := s.Store.SetAccountSecret(c.UserContext(), id, sealed); err != nil {
			return httpx.NotFound("There is no such account.")
		}
		_ = s.Sched.Reload(c.UserContext())
		s.Store.Audit(c.UserContext(), auth.From(c).UserID(), "account.secret_set", id.String(), auth.ClientIP(c), nil)

		account, _ := s.Store.AccountByID(c.UserContext(), id)
		return c.JSON(fiber.Map{"account": account,
			"note": "The password is stored. It will be used exactly once per attempt — a failed attempt deletes it."})
	})

	// "Test connection" is the same attempt with the same consequences, and it
	// is announced as such.
	g.Post("/:id/test", func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return httpx.BadRequest("That is not an account id.")
		}
		if err := s.stepUp(c, "testing an account"); err != nil {
			return err
		}
		if err := accounts.Test(c.UserContext(), s.Env, id); err != nil {
			return err
		}
		account, _ := s.Store.AccountByID(c.UserContext(), id)
		return c.JSON(fiber.Map{"ok": true, "account": account})
	})

	g.Delete("/:id", func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return httpx.BadRequest("That is not an account id.")
		}
		if err := s.stepUp(c, "deleting an account"); err != nil {
			return err
		}
		ctx := c.UserContext()
		// The schedulers hanging off it are named and paused, not quietly
		// deleted along with it.
		attached, err := s.Store.ListSchedulersForAccount(ctx, id)
		if err != nil {
			return httpx.Internal("the schedulers could not be read").WithCause(err)
		}
		names := make([]string, 0, len(attached))
		for _, sc := range attached {
			label := sc.Title
			if label == "" {
				label = sc.Kind + " → " + sc.ProjectSlug
			}
			names = append(names, label)
			_, _ = s.Store.UpdateScheduler(ctx, sc.ID, store.SchedulerPatch{
				Enabled:   ptrBool(false),
				PausedFor: ptrString("the account it used was deleted"),
			})
		}
		if err := s.Store.DeleteAccount(ctx, id); err != nil {
			return httpx.NotFound("There is no such account.")
		}
		_ = s.Sched.Reload(ctx)
		s.Store.Audit(ctx, auth.From(c).UserID(), "account.deleted", id.String(), auth.ClientIP(c), nil)
		return c.JSON(fiber.Map{"pausedSchedulers": names})
	})
}

func (s *Server) mountSchedulers(r fiber.Router) {
	g := r.Group("/schedulers", requireOwner)

	g.Get("/", func(c *fiber.Ctx) error {
		ctx := c.UserContext()
		schedulers, err := s.Store.ListSchedulers(ctx)
		if err != nil {
			return httpx.Internal("the schedulers could not be read").WithCause(err)
		}
		list := make([]schedulerView, 0, len(schedulers))
		for _, sc := range schedulers {
			list = append(list, schedulerView{
				Scheduler: sc,
				NextRun:   s.Sched.Next(sc.ID),
				Running:   s.Sched.Running(sc.ID),
			})
		}
		return c.JSON(fiber.Map{"schedulers": list, "kinds": capability.SchedulerKinds()})
	})

	g.Post("/", func(c *fiber.Ctx) error {
		var in struct {
			ProjectID  string          `json:"projectId"`
			AccountID  string          `json:"accountId"`
			Title      string          `json:"title"`
			Kind       string          `json:"kind"`
			Schedule   string          `json:"schedule"`
			TargetPath string          `json:"targetPath"`
			Options    json.RawMessage `json:"options"`
			Enabled    *bool           `json:"enabled"`
			FilterID   string          `json:"filterId"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The scheduler could not be read.")
		}
		kind, ok := capability.SchedulerKindByName(in.Kind)
		if !ok {
			return httpx.BadRequest("There is no scheduler of kind %q on this server.", in.Kind)
		}
		p, err := s.lookupProject(c, in.ProjectID)
		if err != nil {
			return err
		}
		var accountID *uuid.UUID
		if in.AccountID != "" {
			id, err := uuid.Parse(in.AccountID)
			if err != nil {
				return httpx.BadRequest("That is not an account id.")
			}
			account, err := s.Store.AccountByID(c.UserContext(), id)
			if err != nil {
				return httpx.BadRequest("There is no such account.")
			}
			if len(kind.AccountKinds) > 0 && !contains(kind.AccountKinds, account.Kind) {
				return httpx.BadRequest("A %s scheduler cannot use a %s account.", kind.Title, account.Kind)
			}
			accountID = &id
		} else if kind.AccountRequired {
			return httpx.BadRequest("A %s scheduler needs an account from the accounts menu.", kind.Title)
		}
		if in.Schedule == "" {
			in.Schedule = "manual"
		}
		enabled := true
		if in.Enabled != nil {
			enabled = *in.Enabled
		}
		var filterID *uuid.UUID
		if in.FilterID != "" {
			id, err := uuid.Parse(in.FilterID)
			if err != nil {
				return httpx.BadRequest("That is not a filter id.")
			}
			filterID = &id
		}
		actor := auth.From(c)
		created, err := s.Store.CreateScheduler(c.UserContext(), store.NewScheduler{
			OwnerID: actor.User.ID, ProjectID: p.ID, AccountID: accountID, FilterID: filterID, Title: in.Title,
			Kind: in.Kind, Schedule: in.Schedule, TargetPath: in.TargetPath,
			Options: in.Options, Enabled: enabled,
		})
		if err != nil {
			return httpx.Internal("the scheduler could not be created").WithCause(err)
		}
		if err := s.Sched.Reload(c.UserContext()); err != nil {
			return httpx.Internal("the schedule could not be rebuilt").WithCause(err)
		}
		return c.Status(fiber.StatusCreated).JSON(created)
	})

	g.Patch("/:id", func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return httpx.BadRequest("That is not a scheduler id.")
		}
		var in struct {
			Title      *string          `json:"title"`
			Schedule   *string          `json:"schedule"`
			TargetPath *string          `json:"targetPath"`
			Options    *json.RawMessage `json:"options"`
			Enabled    *bool            `json:"enabled"`
			AccountID  *string          `json:"accountId"`
			FilterID   *string          `json:"filterId"`
			ProjectID  *string          `json:"projectId"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The change could not be read.")
		}
		patch := store.SchedulerPatch{
			Title: in.Title, Schedule: in.Schedule, TargetPath: in.TargetPath,
			Options: in.Options, Enabled: in.Enabled,
		}
		if in.Enabled != nil && *in.Enabled {
			patch.PausedFor = ptrString("")
		}
		// Moving it to another project: from the next run on it writes there.
		if in.ProjectID != nil && strings.TrimSpace(*in.ProjectID) != "" {
			target, err := s.resolveProjectRef(c, *in.ProjectID)
			if err != nil {
				return err
			}
			var group *model.Group
			if target.GroupID != nil {
				group, _ = s.Store.GroupByID(c.UserContext(), *target.GroupID)
			}
			if target.EffectiveReadOnly(group) {
				return httpx.ReadOnly("The project " + target.Title)
			}
			patch.ProjectID = &target.ID
		}
		if in.FilterID != nil {
			if *in.FilterID == "" {
				var none *uuid.UUID
				patch.FilterID = &none
			} else {
				fid, err := uuid.Parse(*in.FilterID)
				if err != nil {
					return httpx.BadRequest("That is not a filter id.")
				}
				ref := &fid
				patch.FilterID = &ref
			}
		}
		if in.AccountID != nil {
			if *in.AccountID == "" {
				var none *uuid.UUID
				patch.AccountID = &none
			} else {
				aid, err := uuid.Parse(*in.AccountID)
				if err != nil {
					return httpx.BadRequest("That is not an account id.")
				}
				ref := &aid
				patch.AccountID = &ref
			}
		}
		updated, err := s.Store.UpdateScheduler(c.UserContext(), id, patch)
		if err != nil {
			return httpx.NotFound("There is no such scheduler.")
		}
		if err := s.Sched.Reload(c.UserContext()); err != nil {
			return httpx.Internal("the schedule could not be rebuilt").WithCause(err)
		}
		return c.JSON(updated)
	})

	g.Delete("/:id", func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return httpx.BadRequest("That is not a scheduler id.")
		}
		if err := s.Store.DeleteScheduler(c.UserContext(), id); err != nil {
			return httpx.NotFound("There is no such scheduler.")
		}
		_ = s.Sched.Reload(c.UserContext())
		return httpx.OK(c)
	})

	// Running by hand is always possible, whatever the schedule says.
	g.Post("/:id/run", func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return httpx.BadRequest("That is not a scheduler id.")
		}
		// A second start while the first is still going is refused, not queued
		// and not run alongside: two runs write the same files, and between
		// them they would spend one single-use credential twice.
		if s.Sched.Running(id) {
			return httpx.Conflict("This scheduler is already running. Wait for it to finish.")
		}
		// A rebuild is the same run with a different question: not "what is
		// new?" but "make this match the source". It fetches everything again
		// and removes what the source no longer has.
		trigger := "manual"
		if c.QueryBool("fresh", false) {
			trigger = "rebuild"
		}
		run, runErr := s.Sched.Run(c.UserContext(), id, trigger)
		if errors.Is(runErr, scheduler.ErrAlreadyRunning) {
			return httpx.Conflict("This scheduler is already running. Wait for it to finish.")
		}
		if run == nil && runErr != nil {
			return httpx.Internal("%v", runErr)
		}
		status := fiber.StatusOK
		if runErr != nil {
			status = fiber.StatusBadGateway // the run happened, and it failed
		}
		return c.Status(status).JSON(fiber.Map{"run": run})
	})

	g.Get("/:id/runs", func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return httpx.BadRequest("That is not a scheduler id.")
		}
		runs, err := s.Store.ListRuns(c.UserContext(), id, c.QueryInt("limit", 50))
		if err != nil {
			return httpx.Internal("the runs could not be read").WithCause(err)
		}
		return c.JSON(fiber.Map{"runs": runs})
	})

	// Failed runs are visible, not silent.
	r.Get("/runs", requireOwner, func(c *fiber.Ctx) error {
		runs, err := s.Store.RecentRuns(c.UserContext(), c.QueryInt("limit", 50))
		if err != nil {
			return httpx.Internal("the runs could not be read").WithCause(err)
		}
		return c.JSON(fiber.Map{"runs": runs})
	})
}

// schedulerView adds the next scheduled time to a scheduler, so the UI can
// say when it runs again without recomputing cron itself.
type schedulerView struct {
	model.Scheduler
	NextRun *time.Time `json:"nextRun,omitempty"`
	// Running is true while a run is in flight. It is what lets the button be
	// dark before it is pressed rather than apologetic afterwards.
	Running bool `json:"running"`
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func ptrBool(v bool) *bool       { return &v }
func ptrString(v string) *string { return &v }

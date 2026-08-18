package automation

import (
	"crypto/hmac"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"
)

// dueNow reports whether a cron expression fires in the current minute.
func dueNow(spec string, now time.Time) bool {
	sched, err := cron.ParseStandard(spec)
	if err != nil {
		return false
	}
	minute := now.Truncate(time.Minute)
	next := sched.Next(minute.Add(-time.Second))
	return next.Truncate(time.Minute).Equal(minute)
}

// Routes are mounted under /api/projects/:project/automation
func (c Capability) Routes(env *capability.Env, r fiber.Router) {
	mountLights(r)

	r.Get("/rules", func(ctx *fiber.Ctx) error {
		if err := capability.RequireRead(ctx); err != nil {
			return err
		}
		p := capability.Project(ctx)
		spec, err := Read(ctx.UserContext(), env, p)
		payload := fiber.Map{"rules": []Rule{}, "actions": capability.Actions()}
		if spec != nil {
			payload["rules"] = spec.Rules
		}
		if err != nil {
			// A broken file does not break the project: it is reported, and the
			// file tree stays usable.
			payload["error"] = err.Error()
		}
		if p.AnonWrite {
			payload["warning"] = "This project accepts writes from visitors, so its rules are not run."
		}
		return ctx.JSON(payload)
	})

	// The rules are stored as YAML — the form writes the same file a human
	// would edit by hand.
	r.Put("/rules", func(ctx *fiber.Ctx) error {
		if err := capability.RequireWrite(ctx); err != nil {
			return err
		}
		p := capability.Project(ctx)
		var in struct {
			Rules []Rule `json:"rules"`
			YAML  string `json:"yaml"`
		}
		if err := ctx.BodyParser(&in); err != nil {
			return httpx.BadRequest("The rules could not be read.")
		}
		var body []byte
		if in.YAML != "" {
			var check Spec
			if err := yaml.Unmarshal([]byte(in.YAML), &check); err != nil {
				return httpx.BadRequest("This YAML cannot be read: %v", err)
			}
			body = []byte(in.YAML)
		} else {
			out, err := yaml.Marshal(Spec{Rules: in.Rules})
			if err != nil {
				return httpx.Internal("the rules could not be written").WithCause(err)
			}
			body = out
		}
		author, email := capability.AuthorOf(ctx)
		if _, err := env.Files.Write(ctx.UserContext(), p, File, body, files.Op{
			Author: author, Email: email, Message: "Edit automation rules", Commit: true,
		}); err != nil {
			return err
		}
		return httpx.OK(ctx)
	})

	// Every rule can be fired immediately — to try it out, without waiting for
	// the schedule.
	r.Post("/rules/:name/run", func(ctx *fiber.Ctx) error {
		if err := capability.RequireWrite(ctx); err != nil {
			return err
		}
		p := capability.Project(ctx)
		spec, err := Read(ctx.UserContext(), env, p)
		if err != nil {
			return httpx.BadRequest("%v", err)
		}
		name := ctx.Params("name")
		for _, rule := range spec.Rules {
			if rule.Name != name {
				continue
			}
			runID, runErr := RunRule(ctx.UserContext(), env, p, rule, "manual")
			run := readRun(ctx, env, runID)
			if runErr != nil {
				return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"run": run, "error": runErr.Error()})
			}
			return ctx.JSON(fiber.Map{"run": run})
		}
		return httpx.NotFound("There is no rule called %q.", name)
	})

	r.Get("/runs", func(ctx *fiber.Ctx) error {
		if err := capability.RequireRead(ctx); err != nil {
			return err
		}
		p := capability.Project(ctx)
		rows, err := env.Store.Pool().Query(ctx.UserContext(), `
			SELECT id, rule, trigger, status, started_at, finished_at, log
			FROM automation_runs WHERE project_id=$1 ORDER BY started_at DESC LIMIT $2`,
			p.ID, ctx.QueryInt("limit", 50))
		if err != nil {
			return httpx.Internal("the runs could not be read").WithCause(err)
		}
		defer rows.Close()
		out := []Run{}
		for rows.Next() {
			var run Run
			if err := rows.Scan(&run.ID, &run.Rule, &run.Trigger, &run.Status,
				&run.StartedAt, &run.FinishedAt, &run.Log); err != nil {
				return httpx.Internal("the runs could not be read").WithCause(err)
			}
			out = append(out, run)
		}
		return ctx.JSON(fiber.Map{"runs": out})
	})
}

type Run struct {
	ID         int64      `json:"id"`
	Rule       string     `json:"rule"`
	Trigger    string     `json:"trigger"`
	Status     string     `json:"status"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Log        string     `json:"log"`
}

func readRun(ctx *fiber.Ctx, env *capability.Env, id int64) *Run {
	var run Run
	err := env.Store.Pool().QueryRow(ctx.UserContext(), `
		SELECT id, rule, trigger, status, started_at, finished_at, log
		FROM automation_runs WHERE id=$1`, id).Scan(&run.ID, &run.Rule, &run.Trigger,
		&run.Status, &run.StartedAt, &run.FinishedAt, &run.Log)
	if err != nil {
		return nil
	}
	return &run
}

// SharedRoutes carry the webhook: its own URL with a secret, callable from
// outside without an account.
func (c Capability) SharedRoutes(env *capability.Env, r fiber.Router) {
	r.All("/hooks/:project/:rule", func(ctx *fiber.Ctx) error {
		list, err := env.Store.ProjectsBySlug(ctx.UserContext(), ctx.Params("project"))
		if err != nil || len(list) != 1 {
			return httpx.NotFound("No hook at this address.")
		}
		p := &list[0]
		spec, err := Read(ctx.UserContext(), env, p)
		if err != nil {
			return httpx.BadRequest("%v", err)
		}
		name := ctx.Params("rule")
		for _, rule := range spec.Rules {
			if rule.Name != name || rule.Trigger.Type != "webhook" {
				continue
			}
			given := ctx.Query("secret")
			if given == "" {
				given = ctx.Get("X-Hook-Secret")
			}
			if rule.Trigger.Secret == "" || !hmac.Equal([]byte(given), []byte(rule.Trigger.Secret)) {
				return httpx.Unauthorized("The secret does not match.")
			}
			runID, runErr := RunRule(ctx.UserContext(), env, p, rule, "webhook")
			if runErr != nil {
				return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": runErr.Error(), "run": runID})
			}
			return ctx.JSON(fiber.Map{"ok": true, "run": runID})
		}
		return httpx.NotFound("No webhook rule called %q.", name)
	})
}

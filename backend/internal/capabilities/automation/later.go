package automation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
)

// Something to happen later.
//
// "Everything on in five minutes" is not a schedule. It happens once, somebody
// asked for it by hand, and between the asking and the happening two things
// have to be true: you can see that it is coming, and you can call it off. A
// cron line offers neither.
//
// So a rule can be asked for with a delay, what is waiting can be listed, and
// all of it can be stopped at once — which is the button anybody wants at the
// moment they realise they should not have pressed the first one.

// Waiting is one thing that will happen unless it is stopped.
type Waiting struct {
	ID      int64     `json:"id"`
	Project uuid.UUID `json:"projectId"`
	Rule    string    `json:"rule"`
	DueAt   time.Time `json:"dueAt"`
	Note    string    `json:"note,omitempty"`
}

func addWaiting(ctx context.Context, env *capability.Env, p *model.Project, rule string, due time.Time, note string) (*Waiting, error) {
	var w Waiting
	err := env.Store.Pool().QueryRow(ctx, `
		INSERT INTO automation_pending (project_id, rule, due_at, note)
		VALUES ($1,$2,$3,$4) RETURNING id, project_id, rule, due_at, note`,
		p.ID, rule, due, note).Scan(&w.ID, &w.Project, &w.Rule, &w.DueAt, &w.Note)
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func listWaiting(ctx context.Context, env *capability.Env, projectID uuid.UUID) ([]Waiting, error) {
	rows, err := env.Store.Pool().Query(ctx, `
		SELECT id, project_id, rule, due_at, note FROM automation_pending
		WHERE project_id=$1 ORDER BY due_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Waiting{}
	for rows.Next() {
		var w Waiting
		if err := rows.Scan(&w.ID, &w.Project, &w.Rule, &w.DueAt, &w.Note); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// runDue fires everything whose moment has come, once. It runs from the same
// minute ticker the schedules use — one clock, not two.
func (e *Engine) runDue(ctx context.Context, now time.Time) {
	rows, err := e.env.Store.Pool().Query(ctx, `
		DELETE FROM automation_pending WHERE due_at <= $1
		RETURNING project_id, rule`, now)
	if err != nil {
		return
	}
	type job struct {
		project uuid.UUID
		rule    string
	}
	jobs := []job{}
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.project, &j.rule); err == nil {
			jobs = append(jobs, j)
		}
	}
	rows.Close()

	for _, j := range jobs {
		p, err := e.env.Store.ProjectByID(ctx, j.project)
		if err != nil {
			continue
		}
		spec, err := Read(ctx, e.env, p)
		if err != nil {
			continue
		}
		for _, rule := range spec.Rules {
			if rule.Name != j.rule {
				continue
			}
			go func(project *model.Project, r Rule) {
				_, _ = RunRule(ctx, e.env, project, r, "timer")
			}(p, rule)
			break
		}
	}
}

// mountLater hangs the timer routes off the project.
func mountLater(env *capability.Env, r fiber.Router) {
	// What is waiting to happen, and when.
	r.Get("/later", func(ctx *fiber.Ctx) error {
		if err := capability.RequireRead(ctx); err != nil {
			return err
		}
		out, err := listWaiting(ctx.UserContext(), env, capability.Project(ctx).ID)
		if err != nil {
			return httpx.Internal("what is waiting could not be read").WithCause(err)
		}
		return ctx.JSON(fiber.Map{"waiting": out, "now": time.Now()})
	})

	// Ask for a rule in so many minutes. Minutes because that is what a person
	// types into a box on a wall panel; seconds are allowed for the impatient.
	r.Post("/later", func(ctx *fiber.Ctx) error {
		if err := capability.RequireWrite(ctx); err != nil {
			return err
		}
		p := capability.Project(ctx)
		var in struct {
			Rule    string  `json:"rule"`
			Minutes float64 `json:"minutes"`
			Seconds float64 `json:"seconds"`
			Note    string  `json:"note"`
		}
		if err := ctx.BodyParser(&in); err != nil {
			return httpx.BadRequest("that is not something to do later")
		}
		in.Rule = strings.TrimSpace(in.Rule)
		if in.Rule == "" {
			return httpx.BadRequest("which rule should happen later?")
		}
		wait := time.Duration(in.Minutes*60+in.Seconds) * time.Second
		if wait <= 0 {
			return httpx.BadRequest("in how long? A number of minutes, above zero.")
		}
		if wait > 24*time.Hour {
			return httpx.BadRequest("that is more than a day away — make it a schedule instead")
		}
		spec, err := Read(ctx.UserContext(), env, p)
		if err != nil {
			return httpx.BadRequest("%v", err)
		}
		found := false
		for _, rule := range spec.Rules {
			if rule.Name == in.Rule {
				found = true
				break
			}
		}
		if !found {
			return httpx.NotFound("There is no rule called %q.", in.Rule)
		}
		w, err := addWaiting(ctx.UserContext(), env, p, in.Rule, time.Now().Add(wait), in.Note)
		if err != nil {
			return httpx.Internal("it could not be written down").WithCause(err)
		}
		return ctx.Status(fiber.StatusCreated).JSON(fiber.Map{"waiting": w})
	})

	// Called off — one of them, or the lot.
	r.Delete("/later/:id", func(ctx *fiber.Ctx) error {
		if err := capability.RequireWrite(ctx); err != nil {
			return err
		}
		tag, err := env.Store.Pool().Exec(ctx.UserContext(),
			`DELETE FROM automation_pending WHERE id=$1 AND project_id=$2`,
			ctx.Params("id"), capability.Project(ctx).ID)
		if err != nil {
			return httpx.Internal("it could not be called off").WithCause(err)
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("Nothing was waiting under that number.")
		}
		return httpx.OK(ctx)
	})

	r.Delete("/later", func(ctx *fiber.Ctx) error {
		if err := capability.RequireWrite(ctx); err != nil {
			return err
		}
		tag, err := env.Store.Pool().Exec(ctx.UserContext(),
			`DELETE FROM automation_pending WHERE project_id=$1`, capability.Project(ctx).ID)
		if err != nil {
			return httpx.Internal("they could not be called off").WithCause(err)
		}
		return ctx.JSON(fiber.Map{"stopped": tag.RowsAffected()})
	})
}

// Cards: the timer, which is an input and a button.
func timerCard() capability.Card {
	return capability.Card{
		Name: "timer", Title: "In a while", Icon: "clock", W: 4, H: 2,
		Description: "Type a number of minutes and press it: the rule happens then. What is waiting is shown, and can be stopped.",
		Options: []capability.AccountField{
			{Name: "projectId", Label: "Project", Type: "project", Required: true},
			{Name: "rule", Label: "Which rule", Type: "text", Required: true,
				Hint: "The name it has in that project's rules."},
			{Name: "minutes", Label: "In how many minutes", Type: "number", Placeholder: "5"},
			{Name: "ask", Label: "The minutes are", Type: "select", Options: []capability.Option{
				{Value: "yes", Label: "typed in each time — a box and a button"},
				{Value: "no", Label: "fixed — one button that says how long"},
			}},
			{Name: "title", Label: "Name", Type: "text", Placeholder: "Alles an"},
			{Name: "feedback", Label: "After pressing", Type: "select", Options: []capability.Option{
				{Value: "brief", Label: "show what is waiting"},
				{Value: "none", Label: "nothing"},
			}},
		},
	}
}

func timerOffers(p *model.Project, spec *Spec) []capability.Offer {
	out := []capability.Offer{}
	for _, r := range spec.Rules {
		if r.Trigger.Type != "" && r.Trigger.Type != "button" {
			continue
		}
		out = append(out, capability.Offer{
			Card: "timer", Title: fmt.Sprintf("%s, later", r.Name), Icon: "clock",
			Detail: "in so many minutes", W: 4, H: 2,
			Options: map[string]any{
				"projectId": p.ID.String(), "rule": r.Name, "minutes": 5, "title": r.Name,
			},
		})
	}
	return out
}

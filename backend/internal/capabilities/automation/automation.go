// Package automation is what replaces the old dedicated "Lights" and "PC"
// pages.
//
// A lamp is a project with HTTP actions, the PC one with wake-on-LAN and SSH.
// None of it is hard-coded: rules live in `automation.yaml` inside the
// project, so they are versioned, exportable and copyable along with it.
package automation

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/events"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/store"
	"gopkg.in/yaml.v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// File is where a project keeps its rules.
const File = "automation.yaml"

type Capability struct{ capability.Base }

func New() Capability { return Capability{} }

func (Capability) Name() string      { return "automation" }
func (Capability) Title() string     { return "Automation" }
func (Capability) Icon() string      { return "zap" }
func (Capability) Owns() []string    { return []string{File} }
func (Capability) Migrations() fs.FS { sub, _ := fs.Sub(migrations, "migrations"); return sub }

// Trigger is what starts a rule.
type Trigger struct {
	// Type is schedule | button | event | webhook.
	Type string `yaml:"type" json:"type"`
	// Schedule
	Cron string `yaml:"cron" json:"cron,omitempty"`
	// Event: which event, optionally narrowed to a project and a path.
	Event   string `yaml:"event" json:"event,omitempty"`
	Project string `yaml:"project" json:"project,omitempty"`
	Path    string `yaml:"path" json:"path,omitempty"`
	// Minutes before an event starts, for calendar-driven rules.
	Before int `yaml:"before" json:"before,omitempty"`
	// Webhook
	Secret string `yaml:"secret" json:"secret,omitempty"`
}

// Step is one action with its parameters. In YAML the parameters sit next to
// `run`, and the JSON the UI sends looks the same — so both sides read and
// write the same flat shape.
type Step struct {
	Run    string         `yaml:"run"`
	Params map[string]any `yaml:",inline"`
}

func (s Step) MarshalJSON() ([]byte, error) {
	flat := map[string]any{"run": s.Run}
	for k, v := range s.Params {
		if k != "run" {
			flat[k] = v
		}
	}
	return json.Marshal(flat)
}

func (s *Step) UnmarshalJSON(data []byte) error {
	var flat map[string]any
	if err := json.Unmarshal(data, &flat); err != nil {
		return err
	}
	s.Params = map[string]any{}
	for k, v := range flat {
		if k == "run" {
			s.Run, _ = v.(string)
			continue
		}
		s.Params[k] = v
	}
	return nil
}

type Rule struct {
	Name        string  `yaml:"name" json:"name"`
	Description string  `yaml:"description" json:"description,omitempty"`
	Enabled     *bool   `yaml:"enabled" json:"enabled,omitempty"`
	Trigger     Trigger `yaml:"trigger" json:"trigger"`
	Actions     []Step  `yaml:"actions" json:"actions"`
}

func (r Rule) On() bool { return r.Enabled == nil || *r.Enabled }

type Spec struct {
	Rules []Rule `yaml:"rules" json:"rules"`
}

func Read(ctx context.Context, env *capability.Env, p *model.Project) (*Spec, error) {
	if !env.Files.Exists(p, File) {
		return &Spec{Rules: []Rule{}}, nil
	}
	body, err := env.Files.ReadLocal(ctx, p, File)
	if err != nil {
		return nil, err
	}
	var spec Spec
	if err := yaml.Unmarshal(body, &spec); err != nil {
		return nil, fmt.Errorf("automation.yaml cannot be read: %w", err)
	}
	if spec.Rules == nil {
		spec.Rules = []Rule{}
	}
	for i, r := range spec.Rules {
		if r.Name == "" {
			return &spec, fmt.Errorf("rule %d has no name", i+1)
		}
		for _, a := range r.Actions {
			if _, ok := capability.ActionByName(a.Run); !ok {
				return &spec, fmt.Errorf("rule %q uses an action %q that is not installed", r.Name, a.Run)
			}
		}
	}
	return &spec, nil
}

// Cards: a button that runs a rule, which is how "wake the PC every morning"
// and "wake it now" end up being the same rule.
func (Capability) Cards() []capability.Card {
	return []capability.Card{{
		Name: "rule", Title: "A button", Icon: "play", W: 2, H: 1,
		Description: "Runs one rule of a project's automation.",
		Options: []capability.AccountField{
			{Name: "projectId", Label: "Project", Type: "project", Required: true},
			{Name: "rule", Label: "Which rule", Type: "text", Required: true,
				Hint: "The name it has in that project's rules."},
		},
	}}
}

// Offers: every rule with a button, ready to press from a board.
func (Capability) Offers(ctx context.Context, env *capability.Env, p *model.Project) []capability.Offer {
	spec, err := Read(ctx, env, p)
	if err != nil {
		return nil
	}
	out := []capability.Offer{}
	for _, r := range spec.Rules {
		if r.Trigger.Type != "" && r.Trigger.Type != "button" {
			continue
		}
		out = append(out, capability.Offer{
			Card: "rule", Title: r.Name, Icon: "play", Detail: "runs this rule", W: 2, H: 1,
			Options: map[string]any{"projectId": p.ID.String(), "rule": r.Name, "title": r.Name},
		})
	}
	return out
}

func (Capability) Presets() []capability.Preset {
	return []capability.Preset{{
		Key:          "system",
		Title:        "System / Device",
		Description:  "A device with rules: wake it, switch it, check whether it is up.",
		Icon:         "cpu",
		DefaultTab:   "automation",
		Capabilities: []string{"automation"},
		Seed: []capability.SeedFile{{
			Path: File,
			Content: func(p *model.Project) []byte {
				return []byte(starterYAML)
			},
		}, {
			Path: "project.yaml",
			Content: func(p *model.Project) []byte {
				return []byte(fmt.Sprintf(starterProjectYAML, p.Title))
			},
		}},
	}}
}

const starterYAML = `# Rules for this device. Every rule is a trigger plus actions.
# Try a rule out at any time with the button next to it — no waiting for a
# schedule.
rules:
  - name: Is it online?
    trigger:
      type: schedule
      cron: "*/5 * * * *"
    actions:
      - run: ping
        host: 192.168.178.50

  # - name: Wake it up
  #   trigger: { type: button }
  #   actions:
  #     - run: wol
  #       mac: "AA:BB:CC:DD:EE:FF"

  # - name: Shut it down
  #   trigger: { type: button }
  #   actions:
  #     - run: ssh
  #       host: 192.168.178.50
  #       user: root
  #       account: <the ssh account's id>
  #       command: systemctl poweroff
`

const starterProjectYAML = `title: %s
icon: cpu
preset: system
capabilities: [automation]

variables:
  online:
    type: bool
    from: ping
    host: 192.168.178.50
    every: 5m
`

// Exports gives the dashboard the state of the last runs.
func (Capability) Exports(ctx context.Context, env *capability.Env, p *model.Project) ([]store.VariableInput, error) {
	var total, failed int
	var lastAt *time.Time
	var lastStatus string
	rows, err := env.Store.Pool().Query(ctx, `
		SELECT status, started_at FROM automation_runs
		WHERE project_id=$1 ORDER BY started_at DESC LIMIT 50`, p.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var at time.Time
		if err := rows.Scan(&status, &at); err != nil {
			return nil, err
		}
		if lastAt == nil {
			t := at
			lastAt = &t
			lastStatus = status
		}
		total++
		if status != "ok" {
			failed++
		}
	}
	out := []store.VariableInput{
		{Name: "automation_runs", Type: "number", Value: total, Source: "capability:automation"},
		{Name: "automation_failed", Type: "number", Value: failed, Source: "capability:automation"},
	}
	if lastAt != nil {
		out = append(out,
			store.VariableInput{Name: "last_run", Type: "date", Value: *lastAt, Source: "capability:automation"},
			store.VariableInput{Name: "last_run_status", Type: "text", Value: lastStatus, Source: "capability:automation"},
		)
	}
	return out, nil
}

// ------------------------------------------------------------------- engine

// Engine keeps the schedule and listens for events. It is started once by the
// server.
type Engine struct {
	env *capability.Env
}

var engine *Engine

// Start wires the event triggers. Cron triggers are checked once a minute,
// which is as precise as a cron expression gets.
func Start(ctx context.Context, env *capability.Env) {
	engine = &Engine{env: env}

	env.Bus.Subscribe(func(e events.Event) {
		if e.Kind == events.FileChanged && strings.HasSuffix(e.Path, File) {
			return // do not let a rule react to its own file being written
		}
		engine.onEvent(context.Background(), e)
	})

	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				engine.tick(context.Background(), now)
			}
		}
	}()
}

func (e *Engine) projects(ctx context.Context) []model.Project {
	list, err := e.env.Store.ProjectsWithCapability(ctx, "automation")
	if err != nil {
		return nil
	}
	return list
}

func (e *Engine) tick(ctx context.Context, now time.Time) {
	for _, p := range e.projects(ctx) {
		spec, err := Read(ctx, e.env, &p)
		if err != nil {
			continue
		}
		for _, rule := range spec.Rules {
			if !rule.On() || rule.Trigger.Type != "schedule" || rule.Trigger.Cron == "" {
				continue
			}
			if dueNow(rule.Trigger.Cron, now) {
				project := p
				go func(r Rule) {
					if _, err := RunRule(ctx, e.env, &project, r, "schedule"); err != nil {
						_ = err // the failure is in the run log, where it is visible
					}
				}(rule)
			}
		}
	}
}

func (e *Engine) onEvent(ctx context.Context, ev events.Event) {
	for _, p := range e.projects(ctx) {
		spec, err := Read(ctx, e.env, &p)
		if err != nil {
			continue
		}
		for _, rule := range spec.Rules {
			if !rule.On() || rule.Trigger.Type != "event" {
				continue
			}
			if rule.Trigger.Event != "" && rule.Trigger.Event != ev.Kind {
				continue
			}
			if rule.Trigger.Project != "" {
				source, err := e.env.Store.ProjectByID(ctx, ev.ProjectID)
				if err != nil || (source.Slug != rule.Trigger.Project && source.ID.String() != rule.Trigger.Project) {
					continue
				}
			}
			if rule.Trigger.Path != "" && !strings.HasPrefix(ev.Path, rule.Trigger.Path) {
				continue
			}
			project := p
			go func(r Rule) {
				_, _ = RunRule(ctx, e.env, &project, r, "event")
			}(rule)
		}
	}
}

// RunRule executes a rule's actions in order. If one fails the chain stops and
// the error is in the log.
func RunRule(ctx context.Context, env *capability.Env, p *model.Project, rule Rule, trigger string) (int64, error) {
	var runID int64
	err := env.Store.Pool().QueryRow(ctx, `
		INSERT INTO automation_runs (project_id, rule, trigger, status)
		VALUES ($1,$2,$3,'running') RETURNING id`, p.ID, rule.Name, trigger).Scan(&runID)
	if err != nil {
		return 0, err
	}

	var log []string
	logf := func(format string, args ...any) {
		log = append(log, time.Now().Format("15:04:05")+"  "+fmt.Sprintf(format, args...))
	}

	// A project that accepts writes from visitors must not run actions: its
	// automation.yaml is not trustworthy.
	if p.AnonWrite {
		logf("this project accepts writes from visitors, so its rules are not run")
		finishRun(ctx, env, runID, "error", strings.Join(log, "\n"))
		return runID, fmt.Errorf("rules are not run in a project that visitors may write to")
	}

	var previous *capability.ActionResult
	var vars []store.VariableInput
	for i, step := range rule.Actions {
		action, ok := capability.ActionByName(step.Run)
		if !ok {
			logf("step %d: there is no action called %q", i+1, step.Run)
			finishRun(ctx, env, runID, "error", strings.Join(log, "\n"))
			return runID, fmt.Errorf("no action called %q", step.Run)
		}
		logf("step %d: %s", i+1, step.Run)
		result, err := action.Run(ctx, env, capability.ActionInput{
			Project:  p,
			Params:   step.Params,
			Previous: previous,
			Log:      logf,
		})
		if err != nil {
			logf("failed: %v", err)
			finishRun(ctx, env, runID, "error", strings.Join(log, "\n"))
			return runID, err
		}
		if result.Output != "" {
			logf("→ %s", trim(result.Output, 500))
		}
		vars = append(vars, result.Variables...)
		r := result
		previous = &r
	}

	for _, v := range vars {
		if v.Source == "" {
			v.Source = "automation:" + rule.Name
		}
		if err := env.Store.SetVariable(ctx, p.ID, v); err != nil {
			logf("variable %s could not be stored: %v", v.Name, err)
		}
	}
	finishRun(ctx, env, runID, "ok", strings.Join(log, "\n"))
	return runID, nil
}

func finishRun(ctx context.Context, env *capability.Env, runID int64, status, log string) {
	_, _ = env.Store.Pool().Exec(ctx,
		`UPDATE automation_runs SET finished_at=now(), status=$2, log=$3 WHERE id=$1`,
		runID, status, log)
}

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

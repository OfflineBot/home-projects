package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
)

// A one-way pull: someone types their credentials once, everything they have is
// fetched into a project, and nothing is kept.
//
// It is the answer to "I want your material, you do not want an account here".
// No account row, no scheduler, no password stored — so the single-use rule has
// nothing to protect: there is no second attempt to prevent, and typing it
// again costs nothing.
//
// The owner makes a link for one project and one kind. Whoever opens it sees a
// form and nothing else: not the project, not the server's other contents.

// onewayClaim is what a link carries, signed rather than stored: the project,
// the kind, and when it stops working.
type onewayClaim struct {
	Project uuid.UUID `json:"p"`
	Kind    string    `json:"k"`
	Until   int64     `json:"u"`
	Target  string    `json:"t,omitempty"`
}

func (s *Server) onewaySign(claim onewayClaim) string {
	body, _ := json.Marshal(claim)
	encoded := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, s.Cfg.SecretKey)
	mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))[:32]
}

func (s *Server) onewayRead(token string) (onewayClaim, error) {
	var claim onewayClaim
	encoded, signature, ok := strings.Cut(token, ".")
	if !ok {
		return claim, fmt.Errorf("this address is not a drop-off")
	}
	mac := hmac.New(sha256.New, s.Cfg.SecretKey)
	mac.Write([]byte(encoded))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))[:32]
	if !hmac.Equal([]byte(signature), []byte(want)) {
		return claim, fmt.Errorf("this address is not a drop-off")
	}
	body, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || json.Unmarshal(body, &claim) != nil {
		return claim, fmt.Errorf("this address is not a drop-off")
	}
	if claim.Until > 0 && time.Now().Unix() > claim.Until {
		return claim, fmt.Errorf("this drop-off has expired")
	}
	return claim, nil
}

// mountOneway is the owner's half: making a link, and running one directly.
func (s *Server) mountOneway(one fiber.Router) {
	one.Get("/oneway", requireOwner, func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"kinds": onewayKinds()})
	})

	one.Post("/oneway/link", requireOwner, func(c *fiber.Ctx) error {
		p := project(c)
		var in struct {
			Kind   string `json:"kind"`
			Days   int    `json:"days"`
			Target string `json:"target"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The request could not be read.")
		}
		if _, ok := onewayKindByName(in.Kind); !ok {
			return httpx.BadRequest("Nothing here can fetch a %q.", in.Kind)
		}
		days := in.Days
		if days <= 0 {
			days = 7
		}
		claim := onewayClaim{
			Project: p.ID, Kind: in.Kind, Target: strings.Trim(in.Target, "/"),
			Until: time.Now().AddDate(0, 0, days).Unix(),
		}
		return c.JSON(fiber.Map{
			"url":   s.Cfg.PublicURL + "/oneway/" + s.onewaySign(claim),
			"until": time.Unix(claim.Until, 0),
		})
	})

	// The same thing without a link, for the owner doing it themselves.
	one.Post("/oneway", requireOwner, func(c *fiber.Ctx) error {
		p := project(c)
		var in onewayInput
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The request could not be read.")
		}
		return s.runOneway(c, p, in)
	})
}

// mountOnewayPublic is the visitor's half: a form at an address, and nothing
// else. It sits outside /api because whoever opens it has no account here.
func (s *Server) mountOnewayPublic(app *fiber.App) {
	app.Get("/oneway/:token", func(c *fiber.Ctx) error {
		claim, err := s.onewayRead(c.Params("token"))
		if err != nil {
			return httpx.NotFound("%v", err)
		}
		kind, ok := onewayKindByName(claim.Kind)
		if !ok {
			return httpx.NotFound("Nothing here can fetch that any more.")
		}
		return s.onewayForm(c, c.Params("token"), kind, "")
	})

	app.Post("/oneway/:token", func(c *fiber.Ctx) error {
		claim, err := s.onewayRead(c.Params("token"))
		if err != nil {
			return httpx.NotFound("%v", err)
		}
		kind, ok := onewayKindByName(claim.Kind)
		if !ok {
			return httpx.NotFound("Nothing here can fetch that any more.")
		}
		p, err := s.Store.ProjectByID(c.UserContext(), claim.Project)
		if err != nil {
			return httpx.NotFound("The project this drop-off writes into is gone.")
		}

		in := onewayInput{Kind: claim.Kind, Target: claim.Target, Config: map[string]string{}}
		for _, f := range kind.Fields {
			in.Config[f.Name] = strings.TrimSpace(c.FormValue(f.Name))
		}
		in.Secret = c.FormValue("secret")
		if in.Secret == "" {
			return s.onewayForm(c, c.Params("token"), kind, "The password is missing.")
		}

		report, err := s.oneway(c, p, in)
		if err != nil {
			message := err.Error()
			if he, ok := err.(*httpx.Error); ok {
				message = he.Message
			}
			return s.onewayForm(c, c.Params("token"), kind, message)
		}
		return s.onewayDone(c, kind, report)
	})
}

type onewayInput struct {
	Kind    string            `json:"kind"`
	Config  map[string]string `json:"config"`
	Secret  string            `json:"secret"`
	Target  string            `json:"target"`
	Options map[string]any    `json:"options"`
}

func (s *Server) runOneway(c *fiber.Ctx, p *model.Project, in onewayInput) error {
	report, err := s.oneway(c, p, in)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"message": report.Message, "files": report.FilesChanged})
}

// oneway performs the whole thing: one sign-in, one fetch, nothing stored.
func (s *Server) oneway(c *fiber.Ctx, p *model.Project, in onewayInput) (capability.Report, error) {
	kind, ok := onewayKindByName(in.Kind)
	if !ok {
		return capability.Report{}, httpx.BadRequest("Nothing here can fetch a %q.", in.Kind)
	}
	runner, ok := onewayRunner(in.Kind)
	if !ok {
		return capability.Report{}, httpx.BadRequest("Nothing here knows how to fetch with a %q.", in.Kind)
	}
	for _, f := range kind.Fields {
		if f.Required && strings.TrimSpace(in.Config[f.Name]) == "" {
			return capability.Report{}, httpx.BadRequest("%s is missing.", f.Label)
		}
	}
	config, _ := json.Marshal(in.Config)

	// An account that exists for the length of this request and is written
	// nowhere. Everything downstream reads it exactly as it would a stored one.
	account := &model.Account{
		ID: uuid.Nil, Kind: in.Kind, Title: in.Kind + " (one-way)", Config: config,
	}
	if kind.Precheck != nil {
		if err := kind.Precheck(c.UserContext(), s.Env, account); err != nil {
			return capability.Report{}, httpx.BadRequest("%v", err)
		}
	}

	options := in.Options
	if options == nil {
		options = map[string]any{}
	}
	var log []string
	job := capability.Job{
		Scheduler: &model.Scheduler{TargetPath: strings.Trim(in.Target, "/")},
		Project:   p,
		Account:   account,
		Secret:    []byte(in.Secret),
		Options:   options,
		Trigger:   "oneway",
		Log:       func(format string, args ...any) { log = append(log, fmt.Sprintf(format, args...)) },
	}
	report, err := runner.Run(c.UserContext(), s.Env, job)
	if err != nil {
		return report, httpx.New(502, "oneway_failed", "%v", err)
	}
	s.reindex(c, p)
	s.Vars.Refresh(c.UserContext(), p)
	if _, serr := s.SortProject(c.UserContext(), p); serr != nil {
		_ = serr // a drop-off that arrived is not undone by a filter that did not run
	}
	s.Store.Audit(c.UserContext(), nil, "oneway.pull", p.Slug, "",
		map[string]any{"kind": in.Kind, "files": report.FilesChanged})
	return report, nil
}

// onewayKinds are the account kinds something here can fetch with. A kind with
// no scheduler behind it cannot be dropped off, because there would be nothing
// to do with the credentials.
func onewayKinds() []capability.AccountKind {
	out := []capability.AccountKind{}
	for _, k := range capability.AccountKinds() {
		if k.SecretLabel == "" {
			continue
		}
		if _, ok := onewayRunner(k.Name); ok {
			out = append(out, k)
		}
	}
	return out
}

func onewayKindByName(name string) (capability.AccountKind, bool) {
	for _, k := range onewayKinds() {
		if k.Name == name {
			return k, true
		}
	}
	return capability.AccountKind{}, false
}

// onewayRunner finds the scheduler kind that fetches with this account kind.
func onewayRunner(accountKind string) (capability.SchedulerKind, bool) {
	for _, sk := range capability.SchedulerKinds() {
		for _, name := range sk.AccountKinds {
			if name == accountKind && sk.Run != nil {
				return sk, true
			}
		}
	}
	return capability.SchedulerKind{}, false
}

// ------------------------------------------------------------------ the form

func (s *Server) onewayForm(c *fiber.Ctx, token string, kind capability.AccountKind, problem string) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	c.Set("Cache-Control", "no-store")
	esc := template.HTMLEscapeString

	fields := ""
	for _, f := range kind.Fields {
		fields += fmt.Sprintf(
			`<label>%s<input name="%s" type="%s" placeholder="%s" %s></label>`,
			esc(f.Label), esc(f.Name), inputType(f.Type), esc(f.Placeholder),
			map[bool]string{true: "required", false: ""}[f.Required])
	}
	notice := ""
	if problem != "" {
		notice = `<p class="bad">` + esc(problem) + `</p>`
	}
	return c.Status(map[bool]int{true: fiber.StatusBadRequest, false: fiber.StatusOK}[problem != ""]).
		SendString(onewayPage(esc(kind.Title), notice+`
<form method="post" action="/oneway/`+esc(token)+`">
  `+fields+`
  <label>`+esc(firstNonEmpty(kind.SecretLabel, "Password"))+`<input name="secret" type="password" required></label>
  <button type="submit">Send it over</button>
  <p class="note">Used once to sign in, then forgotten. Nothing is stored here — no account, no password.</p>
</form>`))
}

func (s *Server) onewayDone(c *fiber.Ctx, kind capability.AccountKind, report capability.Report) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(onewayPage(template.HTMLEscapeString(kind.Title),
		`<p class="good">Done.</p><p>`+template.HTMLEscapeString(report.Message)+`</p>
		 <p class="note">Your password was used for one sign-in and is gone.</p>`))
}

func onewayPage(title, body string) string {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>` + title + `</title>
<style>
 :root { color-scheme: dark }
 body { margin:0; min-height:100vh; display:grid; place-items:center;
        background:#1e1e2e; color:#cdd6f4; font:16px/1.6 system-ui,sans-serif }
 main { width:min(24rem,92vw); background:#181825; padding:1.6rem; border-radius:.8rem;
        border:1px solid #313244 }
 h1 { font-size:1.1rem; margin:0 0 1rem; color:#cba6f7 }
 label { display:block; margin-bottom:.8rem; font-size:.85rem; color:#a6adc8 }
 input { width:100%; margin-top:.3rem; padding:.55rem .7rem; border-radius:.45rem;
         border:1px solid #45475a; background:#11111b; color:#cdd6f4; font:inherit; box-sizing:border-box }
 button { width:100%; padding:.6rem; border:0; border-radius:.45rem; background:#cba6f7;
          color:#11111b; font:inherit; font-weight:600; cursor:pointer; margin-top:.4rem }
 .bad { color:#f38ba8; font-size:.9rem } .good { color:#a6e3a1 }
 .note { color:#6c7086; font-size:.8rem }
</style></head><body><main><h1>` + title + `</h1>` + body + `</main></body></html>`
}

func inputType(kind string) string {
	switch kind {
	case "password":
		return "password"
	case "number":
		return "number"
	case "url":
		return "url"
	default:
		return "text"
	}
}

package calendar

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/access"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/capabilities/calendar/ics"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
)

type eventInput struct {
	UID          string    `json:"uid"`
	Summary      string    `json:"summary"`
	Description  string    `json:"description"`
	Location     string    `json:"location"`
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	AllDay       bool      `json:"allDay"`
	RRule        string    `json:"rrule"`
	Color        string    `json:"color"`
	Alarms       []int     `json:"alarms"`
	Scope        string    `json:"scope"`        // "all" (default) or "single"
	RecurrenceID string    `json:"recurrenceId"` // which appearance, for scope=single
}

// Routes are mounted under /api/projects/:project/calendar
func (c Capability) Routes(env *capability.Env, r fiber.Router) {
	r.Get("/events", func(ctx *fiber.Ctx) error {
		if err := capability.RequireRead(ctx); err != nil {
			return err
		}
		p := capability.Project(ctx)
		from, to, err := rangeFromQuery(ctx.Query("from"), ctx.Query("to"))
		if err != nil {
			return httpx.BadRequest("%v", err)
		}
		occ, err := Between(ctx.UserContext(), env, []model.Project{*p}, from, to)
		if err != nil {
			return httpx.Internal("the calendar could not be read").WithCause(err)
		}
		return ctx.JSON(fiber.Map{"events": occ, "from": from, "to": to})
	})

	r.Post("/events", func(ctx *fiber.Ctx) error {
		if err := capability.RequireWrite(ctx); err != nil {
			return err
		}
		p := capability.Project(ctx)
		var in eventInput
		if err := ctx.BodyParser(&in); err != nil {
			return httpx.BadRequest("The event could not be read: %v", err)
		}
		if strings.TrimSpace(in.Summary) == "" {
			return httpx.BadRequest("An event needs a title.")
		}
		if in.Start.IsZero() {
			return httpx.BadRequest("An event needs a start.")
		}
		if in.End.IsZero() {
			in.End = in.Start.Add(time.Hour)
		}
		if in.End.Before(in.Start) {
			return httpx.BadRequest("The end of the event is before its start.")
		}
		uid := in.UID
		if uid == "" {
			uid = uuid.NewString() + "@home-projects"
		}
		author, email := capability.AuthorOf(ctx)
		err := mutate(ctx.UserContext(), env, p, uid, author, email, "Add event "+in.Summary, func(f *file) error {
			e := ics.Event{
				UID:         uid,
				Summary:     in.Summary,
				Description: in.Description,
				Location:    in.Location,
				Start:       in.Start,
				End:         in.End,
				AllDay:      in.AllDay,
				RRule:       normaliseRRule(in.RRule),
				Color:       in.Color,
				Alarms:      in.Alarms,
			}
			f.Cal.Children = append(f.Cal.Children, e.ToComponent())
			return nil
		})
		if err != nil {
			return writeError(err)
		}
		return ctx.Status(fiber.StatusCreated).JSON(fiber.Map{"uid": uid})
	})

	r.Patch("/events/:uid", func(ctx *fiber.Ctx) error {
		if err := capability.RequireWrite(ctx); err != nil {
			return err
		}
		p := capability.Project(ctx)
		uid := ctx.Params("uid")
		var in eventInput
		if err := ctx.BodyParser(&in); err != nil {
			return httpx.BadRequest("The change could not be read: %v", err)
		}
		author, email := capability.AuthorOf(ctx)
		err := mutate(ctx.UserContext(), env, p, uid, author, email, "Change event "+in.Summary, func(f *file) error {
			master := findEvent(f, uid, "")
			if master == nil {
				return errNoEvent
			}
			if in.Scope == "single" && in.RecurrenceID != "" {
				// One appearance out of a series: it becomes its own VEVENT
				// with a RECURRENCE-ID, exactly as RFC 5545 intends.
				rid, err := time.Parse("20060102T150405Z", in.RecurrenceID)
				if err != nil {
					return httpx.BadRequest("The appearance could not be identified.")
				}
				existing := findEvent(f, uid, in.RecurrenceID)
				base, err := ics.FromComponent(master)
				if err != nil {
					return err
				}
				ev := base
				ev.Comp = existing
				ev.RecurrenceID = &rid
				ev.RRule = ""
				ev.ExDates = nil
				applyInput(&ev, in)
				comp := ev.ToComponent()
				comp.Set("RECURRENCE-ID", ics.FormatUTC(rid))
				if existing == nil {
					f.Cal.Children = append(f.Cal.Children, comp)
				}
				return nil
			}
			ev, err := ics.FromComponent(master)
			if err != nil {
				return err
			}
			applyInput(&ev, in)
			ev.Sequence++
			ev.ToComponent()
			return nil
		})
		if err != nil {
			return writeError(err)
		}
		return httpx.OK(ctx)
	})

	r.Delete("/events/:uid", func(ctx *fiber.Ctx) error {
		if err := capability.RequireWrite(ctx); err != nil {
			return err
		}
		p := capability.Project(ctx)
		uid := ctx.Params("uid")
		scope := ctx.Query("scope", "all")
		recurrenceID := ctx.Query("recurrenceId")
		author, email := capability.AuthorOf(ctx)

		err := mutate(ctx.UserContext(), env, p, uid, author, email, "Delete event", func(f *file) error {
			if scope == "single" && recurrenceID != "" {
				master := findEvent(f, uid, "")
				if master == nil {
					return errNoEvent
				}
				rid, err := time.Parse("20060102T150405Z", recurrenceID)
				if err != nil {
					return httpx.BadRequest("The appearance could not be identified.")
				}
				ev, err := ics.FromComponent(master)
				if err != nil {
					return err
				}
				ev.ExDates = append(ev.ExDates, rid)
				ev.ToComponent()
				// A changed single appearance disappears with it.
				removeEvent(f, uid, recurrenceID)
				return nil
			}
			if !removeEvent(f, uid, "") {
				return errNoEvent
			}
			return nil
		})
		if err != nil {
			return writeError(err)
		}
		return httpx.OK(ctx)
	})

	// Move an event into another calendar project.
	r.Post("/events/:uid/move", func(ctx *fiber.Ctx) error {
		if err := capability.RequireWrite(ctx); err != nil {
			return err
		}
		source := capability.Project(ctx)
		var body struct {
			TargetProject string `json:"targetProject"`
		}
		if err := ctx.BodyParser(&body); err != nil {
			return httpx.BadRequest("The target could not be read.")
		}
		targetID, err := uuid.Parse(body.TargetProject)
		if err != nil {
			return httpx.BadRequest("The target project is not a valid id.")
		}
		target, err := env.Store.ProjectByID(ctx.UserContext(), targetID)
		if err != nil {
			return httpx.NotFound("The target project does not exist.")
		}
		if !target.Has("calendar") {
			return httpx.BadRequest("%s has no calendar.", target.Title)
		}
		var targetGroup *model.Group
		if target.GroupID != nil {
			targetGroup, _ = env.Store.GroupByID(ctx.UserContext(), *target.GroupID)
		}
		if err := access.RequireWriteProject(auth.From(ctx), target, targetGroup); err != nil {
			return err
		}

		uid := ctx.Params("uid")
		author, email := capability.AuthorOf(ctx)

		var moved *ics.Component
		err = mutate(ctx.UserContext(), env, source, uid, author, email, "Move event out", func(f *file) error {
			comp := findEvent(f, uid, "")
			if comp == nil {
				return errNoEvent
			}
			moved = comp
			removeEvent(f, uid, "")
			return nil
		})
		if err != nil {
			return writeError(err)
		}
		err = mutate(ctx.UserContext(), env, target, uid, author, email, "Move event in", func(f *file) error {
			f.Cal.Children = append(f.Cal.Children, moved)
			return nil
		})
		if err != nil {
			return writeError(err)
		}
		return httpx.OK(ctx)
	})

	// The download and the subscription URL return the same bytes: one
	// complete VCALENDAR, whatever the storage looks like inside.
	r.Get("/export.ics", func(ctx *fiber.Ctx) error {
		p := capability.Project(ctx)
		if err := requireReadOrToken(ctx, env, p); err != nil {
			return err
		}
		body, err := Export(ctx.UserContext(), env, p)
		if err != nil {
			return httpx.Internal("the calendar could not be exported").WithCause(err)
		}
		ctx.Set("Content-Type", "text/calendar; charset=utf-8")
		ctx.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", p.Slug+".ics"))
		return ctx.Send(body)
	})

	r.Post("/import", func(ctx *fiber.Ctx) error {
		if err := capability.RequireWrite(ctx); err != nil {
			return err
		}
		p := capability.Project(ctx)
		body, err := uploadBody(ctx)
		if err != nil {
			return err
		}
		incoming, err := ics.ParseCalendar(body)
		if err != nil {
			return httpx.BadRequest("This file is not a calendar: %v", err)
		}
		author, email := capability.AuthorOf(ctx)
		added := 0
		err = mutate(ctx.UserContext(), env, p, "", author, email, "Import calendar", func(f *file) error {
			for _, comp := range incoming.Kids("VEVENT") {
				uid := comp.Value("UID")
				if uid == "" {
					uid = uuid.NewString() + "@home-projects"
					comp.Set("UID", uid)
				}
				rid := ""
				if r := comp.Get("RECURRENCE-ID"); r != nil {
					rid = r.Value
				}
				removeEvent(f, uid, rid)
				f.Cal.Children = append(f.Cal.Children, comp)
				added++
			}
			for _, tz := range incoming.Kids("VTIMEZONE") {
				f.Cal.Children = append(f.Cal.Children, tz)
			}
			return nil
		})
		if err != nil {
			return writeError(err)
		}
		return ctx.JSON(fiber.Map{"imported": added})
	})

	// The subscription URL a phone or Google Calendar can be pointed at,
	// without an account on this server.
	r.Get("/subscription", func(ctx *fiber.Ctx) error {
		if err := capability.RequireRead(ctx); err != nil {
			return err
		}
		p := capability.Project(ctx)
		token, err := subscriptionToken(ctx.UserContext(), env, p.ID)
		if err != nil {
			return httpx.Internal("the subscription address could not be built").WithCause(err)
		}
		return ctx.JSON(fiber.Map{
			"url":    subscriptionURL(env, p.ID, token),
			"public": p.Visibility == model.VisibilityPublic,
		})
	})

	r.Post("/subscription/rotate", func(ctx *fiber.Ctx) error {
		if err := capability.RequireWrite(ctx); err != nil {
			return err
		}
		p := capability.Project(ctx)
		if err := rotateSubscriptionToken(ctx.UserContext(), env, p.ID); err != nil {
			return httpx.Internal("the address could not be renewed").WithCause(err)
		}
		token, _ := subscriptionToken(ctx.UserContext(), env, p.ID)
		return ctx.JSON(fiber.Map{"url": subscriptionURL(env, p.ID, token)})
	})

	r.Get("/settings", func(ctx *fiber.Ctx) error {
		if err := capability.RequireRead(ctx); err != nil {
			return err
		}
		p := capability.Project(ctx)
		var split bool
		var lastView string
		err := env.Store.Pool().QueryRow(ctx.UserContext(),
			`SELECT split, last_view FROM calendar_settings WHERE project_id=$1`, p.ID).Scan(&split, &lastView)
		if err != nil {
			split, lastView = false, "month"
		}
		return ctx.JSON(fiber.Map{"split": split, "lastView": lastView, "files": entries(env, p)})
	})

	r.Put("/settings", func(ctx *fiber.Ctx) error {
		if err := capability.RequireWrite(ctx); err != nil {
			return err
		}
		p := capability.Project(ctx)
		var in struct {
			Split    *bool   `json:"split"`
			LastView *string `json:"lastView"`
		}
		if err := ctx.BodyParser(&in); err != nil {
			return httpx.BadRequest("The settings could not be read.")
		}
		split := false
		view := "month"
		_ = env.Store.Pool().QueryRow(ctx.UserContext(),
			`SELECT split, last_view FROM calendar_settings WHERE project_id=$1`, p.ID).Scan(&split, &view)
		if in.Split != nil {
			split = *in.Split
		}
		if in.LastView != nil {
			view = *in.LastView
		}
		if _, err := env.Store.Pool().Exec(ctx.UserContext(), `
			INSERT INTO calendar_settings (project_id, split, last_view) VALUES ($1,$2,$3)
			ON CONFLICT (project_id) DO UPDATE SET split=EXCLUDED.split, last_view=EXCLUDED.last_view`,
			p.ID, split, view); err != nil {
			return httpx.Internal("the settings could not be stored").WithCause(err)
		}
		if in.Split != nil {
			if err := restructure(ctx.UserContext(), env, p, split); err != nil {
				return writeError(err)
			}
		}
		return ctx.JSON(fiber.Map{"split": split, "lastView": view})
	})
}

func subscriptionURL(env *capability.Env, projectID uuid.UUID, token string) string {
	return fmt.Sprintf("%s/api/capabilities/calendar/subscribe/%s.ics?token=%s",
		env.Cfg.PublicURL, projectID, token)
}

// SharedRoutes are mounted under /api/capabilities/calendar: the overlay of
// several calendars at once, and the subscription URL — which has to work
// without a session, so the token is checked here rather than by the core.
func (c Capability) SharedRoutes(env *capability.Env, r fiber.Router) {
	r.Get("/subscribe/:project.ics", func(ctx *fiber.Ctx) error {
		id, err := uuid.Parse(strings.TrimSuffix(ctx.Params("project"), ".ics"))
		if err != nil {
			return httpx.NotFound("No calendar at this address.")
		}
		p, err := env.Store.ProjectByID(ctx.UserContext(), id)
		if err != nil || !p.Has("calendar") {
			return httpx.NotFound("No calendar at this address.")
		}
		want, err := subscriptionToken(ctx.UserContext(), env, p.ID)
		given := ctx.Query("token")
		if err != nil || given == "" || !hmac.Equal([]byte(given), []byte(want)) {
			// A public calendar needs no token at all.
			if err := access.RequireReadProject(auth.From(ctx), p); err != nil {
				return err
			}
		}
		body, err := Export(ctx.UserContext(), env, p)
		if err != nil {
			return httpx.Internal("the calendar could not be exported").WithCause(err)
		}
		ctx.Set("Content-Type", "text/calendar; charset=utf-8")
		ctx.Set("Cache-Control", "no-cache")
		return ctx.Send(body)
	})

	r.Get("/events", func(ctx *fiber.Ctx) error {
		actor := auth.From(ctx)
		from, to, err := rangeFromQuery(ctx.Query("from"), ctx.Query("to"))
		if err != nil {
			return httpx.BadRequest("%v", err)
		}

		all, err := env.Store.ProjectsWithCapability(ctx.UserContext(), "calendar")
		if err != nil {
			return httpx.Internal("the calendar projects could not be read").WithCause(err)
		}
		wanted := map[string]bool{}
		for _, id := range strings.Split(ctx.Query("projects"), ",") {
			if id = strings.TrimSpace(id); id != "" {
				wanted[id] = true
			}
		}
		groupFilter := strings.TrimSpace(ctx.Query("group"))

		var chosen []model.Project
		for _, p := range all {
			if !access.CanReadProject(actor, &p) {
				continue
			}
			if len(wanted) > 0 && !wanted[p.ID.String()] && !wanted[p.Slug] {
				continue
			}
			if groupFilter != "" && p.GroupSlug != groupFilter {
				continue
			}
			chosen = append(chosen, p)
		}
		occ, err := Between(ctx.UserContext(), env, chosen, from, to)
		if err != nil {
			return httpx.Internal("the calendars could not be read").WithCause(err)
		}
		sources := make([]fiber.Map, 0, len(chosen))
		for _, p := range chosen {
			sources = append(sources, fiber.Map{
				"id": p.ID, "slug": p.Slug, "title": p.Title, "color": p.Color,
				"group": p.GroupSlug, "groupTitle": p.GroupTitle,
				"readOnly": p.ReadOnly || p.Archived,
			})
		}
		return ctx.JSON(fiber.Map{"events": occ, "sources": sources, "from": from, "to": to})
	})
}

// ------------------------------------------------------------------ helpers

func applyInput(e *ics.Event, in eventInput) {
	if in.Summary != "" {
		e.Summary = in.Summary
	}
	e.Description = in.Description
	e.Location = in.Location
	if !in.Start.IsZero() {
		e.Start = in.Start
	}
	if !in.End.IsZero() {
		e.End = in.End
	}
	e.AllDay = in.AllDay
	if in.Scope != "single" {
		e.RRule = normaliseRRule(in.RRule)
	}
	e.Color = in.Color
	e.Alarms = in.Alarms
}

func normaliseRRule(r string) string {
	r = strings.TrimSpace(strings.ToUpper(r))
	return strings.TrimPrefix(r, "RRULE:")
}

// findEvent returns the VEVENT with that UID; recurrenceID picks a single
// changed appearance, an empty one the series master.
func findEvent(f *file, uid, recurrenceID string) *ics.Component {
	for _, comp := range f.Cal.Kids("VEVENT") {
		if comp.Value("UID") != uid {
			continue
		}
		rid := comp.Value("RECURRENCE-ID")
		if rid == recurrenceID {
			return comp
		}
	}
	return nil
}

func removeEvent(f *file, uid, recurrenceID string) bool {
	kept := make([]*ics.Component, 0, len(f.Cal.Children))
	removed := false
	for _, ch := range f.Cal.Children {
		if strings.EqualFold(ch.Name, "VEVENT") && ch.Value("UID") == uid {
			if recurrenceID == "" || ch.Value("RECURRENCE-ID") == recurrenceID {
				removed = true
				continue
			}
		}
		kept = append(kept, ch)
	}
	f.Cal.Children = kept
	return removed
}

func writeError(err error) error {
	switch {
	case err == nil:
		return nil
	case err == errNoEvent:
		return httpx.NotFound("There is no event with that id in this calendar.")
	}
	var he *httpx.Error
	if ok := asHTTP(err, &he); ok {
		return he
	}
	return httpx.Internal("the calendar could not be written").WithCause(err)
}

func asHTTP(err error, target **httpx.Error) bool {
	if he, ok := err.(*httpx.Error); ok {
		*target = he
		return true
	}
	return false
}

func uploadBody(ctx *fiber.Ctx) ([]byte, error) {
	if fh, err := ctx.FormFile("file"); err == nil {
		f, err := fh.Open()
		if err != nil {
			return nil, httpx.BadRequest("The upload could not be read.")
		}
		defer f.Close()
		buf, err := io.ReadAll(io.LimitReader(f, 64<<20))
		if err != nil {
			return nil, httpx.BadRequest("The upload could not be read.")
		}
		return buf, nil
	}
	body := ctx.Body()
	if len(body) == 0 {
		return nil, httpx.BadRequest("No calendar file was sent.")
	}
	return body, nil
}

// restructure switches a project between one file and one file per event.
// Nothing changes outside: the export still returns one complete VCALENDAR.
func restructure(ctx context.Context, env *capability.Env, p *model.Project, split bool) error {
	m := lockFor(p.ID)
	m.Lock()
	defer m.Unlock()

	all, err := readFiles(ctx, env, p)
	if err != nil {
		return err
	}
	var events []*ics.Component
	var sourcePaths []string
	for _, f := range all {
		if f.ReadOnly {
			continue // subscriptions keep their own files
		}
		events = append(events, f.Cal.Kids("VEVENT")...)
		sourcePaths = append(sourcePaths, f.Path)
	}

	op := filesOp{Author: "the server", Email: "server@home-projects",
		Message: "Change how the calendar is stored", Commit: true}

	if split {
		for _, comp := range events {
			cal := ics.NewCalendar(p.Title)
			cal.Children = append(cal.Children, comp)
			rel := path.Join(SplitDir, safeUID(comp.Value("UID"))+".ics")
			if _, err := env.Files.Write(ctx, p, rel, cal.Bytes(), op); err != nil {
				return err
			}
		}
		if env.Files.Exists(p, MainFile) {
			if err := env.Files.Remove(ctx, p, MainFile, false, op); err != nil {
				return err
			}
		}
	} else {
		cal := ics.NewCalendar(p.Title)
		cal.Children = append(cal.Children, events...)
		if _, err := env.Files.Write(ctx, p, MainFile, cal.Bytes(), op); err != nil {
			return err
		}
		for _, rel := range sourcePaths {
			if strings.HasPrefix(rel, SplitDir+"/") {
				if err := env.Files.Remove(ctx, p, rel, false, op); err != nil {
					return err
				}
			}
		}
	}
	return Reindex(ctx, env, p)
}

// ------------------------------------------------------- subscription tokens

// The subscription token is derived, not stored: HMAC over the project id and
// a rotation counter. That way the URL can be shown again at any time, and
// rotating it makes every old link stop working at once.
func subscriptionSalt(ctx context.Context, env *capability.Env, projectID uuid.UUID) (string, error) {
	raw, err := env.Store.Setting(ctx, "calendar.ics-salt:"+projectID.String())
	if err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return "1", nil
	}
	return strings.Trim(string(raw), `"`), nil
}

func subscriptionToken(ctx context.Context, env *capability.Env, projectID uuid.UUID) (string, error) {
	salt, err := subscriptionSalt(ctx, env, projectID)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, env.Cfg.SecretKey)
	mac.Write([]byte("calendar-ics:" + projectID.String() + ":" + salt))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))[:32], nil
}

func rotateSubscriptionToken(ctx context.Context, env *capability.Env, projectID uuid.UUID) error {
	return env.Store.SetSetting(ctx, "calendar.ics-salt:"+projectID.String(), uuid.NewString())
}

// requireReadOrToken lets a calendar app fetch the file with its subscription
// token, and everyone else through the normal permission check.
func requireReadOrToken(ctx *fiber.Ctx, env *capability.Env, p *model.Project) error {
	if token := ctx.Query("token"); token != "" {
		want, err := subscriptionToken(ctx.UserContext(), env, p.ID)
		if err == nil && hmac.Equal([]byte(token), []byte(want)) {
			return nil
		}
	}
	return capability.RequireRead(ctx)
}

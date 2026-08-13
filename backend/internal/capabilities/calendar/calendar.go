// Package calendar is the calendar capability: a real calendar, stored as
// valid iCalendar.
//
// What the user opens is the grid, not a file listing — but what lies on disk
// is one `calendar.ics` per project, one VCALENDAR with all VEVENTs, which
// opens directly in Google Calendar, Thunderbird or Apple Calendar. The
// database only keeps an index over those files, and that index is rebuilt
// from them at any time.
package calendar

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/offlinebot/home-projects/backend/internal/capabilities/calendar/ics"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/store"
)

//go:embed migrations/*.sql
var migrations embed.FS

type filesOp = files.Op

var errReadOnlyEvent = httpx.ReadOnly("This event comes from a subscription and")

// Capability implements capability.Capability.
type Capability struct{ capability.Base }

func New() Capability { return Capability{} }

func (Capability) Name() string  { return "calendar" }
func (Capability) Title() string { return "Calendar" }
func (Capability) Icon() string  { return "calendar" }

// Owns names the files this capability is responsible for.
func (Capability) Owns() []string {
	return []string{MainFile, SplitDir + "/*.ics", SubscriptionDir + "/*.ics"}
}

func (Capability) Migrations() fs.FS {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil
	}
	return sub
}

// Index rebuilds the index whenever one of the capability's files changed —
// including changes that did not come through the calendar UI, such as an
// upload or a git push.
func (Capability) Index(ctx context.Context, env *capability.Env, p *model.Project, path string) error {
	if !strings.HasSuffix(strings.ToLower(path), ".ics") {
		return nil
	}
	return Reindex(ctx, env, p)
}

// Presets contributes the "Calendar" entry to the create dialog.
func (Capability) Presets() []capability.Preset {
	return []capability.Preset{{
		Key:          "calendar",
		Title:        "Calendar",
		Description:  "Appointments in a grid — stored as one calendar.ics you can subscribe to.",
		Icon:         "calendar",
		DefaultTab:   "calendar",
		Capabilities: []string{"calendar"},
		Seed: []capability.SeedFile{{
			Path: MainFile,
			Content: func(p *model.Project) []byte {
				return ics.NewCalendar(p.Title).Bytes()
			},
		}},
	}}
}

// Exports is what a calendar project offers the dashboard.
func (Capability) Exports(ctx context.Context, env *capability.Env, p *model.Project) ([]store.VariableInput, error) {
	now := time.Now()
	occ, err := Between(ctx, env, []model.Project{*p}, now.Add(-24*time.Hour), now.AddDate(0, 3, 0))
	if err != nil {
		return nil, err
	}

	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	todayEnd := todayStart.AddDate(0, 0, 1)

	var next *Occurrence
	todayCount := 0
	upcoming := []map[string]any{}
	for i := range occ {
		o := occ[i]
		if o.Start.Before(todayEnd) && o.End.After(todayStart) {
			todayCount++
		}
		if o.Start.After(now) || o.End.After(now) {
			if next == nil {
				next = &occ[i]
			}
			if len(upcoming) < 10 {
				upcoming = append(upcoming, map[string]any{
					"summary": o.Summary,
					"start":   o.Start,
					"end":     o.End,
					"allDay":  o.AllDay,
				})
			}
		}
	}

	out := []store.VariableInput{
		{Name: "today_count", Type: "number", Value: todayCount, Source: "capability:calendar"},
		{Name: "upcoming", Type: "list", Value: upcoming, Source: "capability:calendar"},
	}
	if next != nil {
		out = append(out,
			store.VariableInput{Name: "next_event", Type: "text", Value: next.Summary, Source: "capability:calendar"},
			store.VariableInput{Name: "next_event_at", Type: "date", Value: next.Start, Source: "capability:calendar"},
		)
	} else {
		out = append(out,
			store.VariableInput{Name: "next_event", Type: "text", Value: "", Source: "capability:calendar"},
			store.VariableInput{Name: "next_event_at", Type: "date", Value: nil, Source: "capability:calendar"},
		)
	}
	return out, nil
}

// SchedulerKinds brings the ICS subscription: a foreign calendar URL pulled on
// a schedule. Its events are read-only and are overwritten on the next run.
func (c Capability) SchedulerKinds() []capability.SchedulerKind {
	return []capability.SchedulerKind{{
		Name:         "ics",
		Title:        "ICS subscription",
		Description:  "Fetches a calendar from a URL and keeps it in the project as a read-only calendar. An account is only needed when the URL asks for a login.",
		AccountKinds: []string{"ics", "http"},
		Options: []capability.AccountField{
			{Name: "url", Label: "Calendar URL", Type: "url", Placeholder: "https://…/timetable.ics"},
			{Name: "name", Label: "Name for the file it writes", Type: "text"},
		},
		Run: runICSSubscription,
	}}
}

// AccountKinds brings the credentials a protected calendar URL needs. A public
// timetable needs none, which is why the scheduler does not insist on one.
func (Capability) AccountKinds() []capability.AccountKind {
	return []capability.AccountKind{{
		Name:        "ics",
		Title:       "ICS subscription",
		Description: "A calendar URL, with a login if the other side wants one.",
		Fields: []capability.AccountField{
			{Name: "url", Label: "Calendar URL", Type: "url", Required: true},
			{Name: "user", Label: "User (optional)", Type: "text"},
		},
		SecretLabel: "Password",
		Test:        testICSAccount,
	}}
}

// Actions contributes nothing to the automation engine — a calendar changes
// through its own routes, and the engine reaches it through the file actions
// every project has.
func (Capability) Actions() []capability.Action { return nil }

var errNoEvent = errors.New("no event with that id")

func rangeFromQuery(fromStr, toStr string) (time.Time, time.Time, error) {
	now := time.Now()
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).AddDate(0, -1, 0)
	to := from.AddDate(0, 3, 0)
	var err error
	if fromStr != "" {
		from, err = parseTimeParam(fromStr)
		if err != nil {
			return from, to, fmt.Errorf("from: %w", err)
		}
	}
	if toStr != "" {
		to, err = parseTimeParam(toStr)
		if err != nil {
			return from, to, fmt.Errorf("to: %w", err)
		}
	}
	if to.Before(from) {
		return from, to, errors.New("the end of the range is before its start")
	}
	if to.Sub(from) > 5*365*24*time.Hour {
		return from, to, errors.New("the range may span at most five years")
	}
	return from, to, nil
}

func parseTimeParam(v string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, v, time.UTC); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not a date", v)
}

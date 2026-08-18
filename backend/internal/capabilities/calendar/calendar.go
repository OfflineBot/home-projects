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
	"sort"
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
// A card for what is coming — one calendar's entries, or every calendar's.
func (Capability) Cards() []capability.Card {
	return []capability.Card{{
		Name: "agenda", Title: "What is coming", Icon: "calendar", W: 4, H: 3,
		Description: "The next entries, out of one calendar or out of all of them.",
		Options: []capability.AccountField{
			{Name: "projectId", Label: "Calendar", Type: "project",
				Hint: "Empty takes every calendar there is."},
			{Name: "days", Label: "How many days ahead", Type: "number", Placeholder: "14"},
		},
	}}
}

// Offers: what is coming, out of this calendar.
func (Capability) Offers(ctx context.Context, env *capability.Env, p *model.Project) []capability.Offer {
	return []capability.Offer{{
		Card: "agenda", Title: "What is coming", Icon: "calendar",
		Detail: "the next entries", W: 4, H: 3,
		Options: map[string]any{"projectId": p.ID.String(), "days": 14, "title": p.Title},
	}}
}

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
	// Three months back, because an overdue deadline stays overdue and a
	// semester that started in March is still the phase you are in.
	occ, err := Between(ctx, env, []model.Project{*p}, now.AddDate(0, -3, 0), now.AddDate(0, 3, 0))
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
		// A phase is the backdrop, not an entry in today's list, and counting
		// it would make "3 things today" mean "2 things and a semester".
		if o.Kind == ics.KindPhase {
			continue
		}
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
	return append(out, deadlineAndPhaseVariables(occ, now, todayStart, todayEnd)...), nil
}

// deadlineAndPhaseVariables are what makes a dashboard useful without a
// calendar widget on it: a red tile for what is overdue, a text tile for what
// comes next, a progress tile for the semester.
func deadlineAndPhaseVariables(occ []Occurrence, now, todayStart, todayEnd time.Time) []store.VariableInput {
	open, overdue := 0, 0
	var nextDeadline *Occurrence
	var phase *Occurrence
	var slots []Occurrence

	for i := range occ {
		o := occ[i]
		switch o.Kind {
		case ics.KindDeadline:
			if o.Done {
				continue
			}
			if o.Start.Before(now) {
				overdue++
				continue
			}
			open++
			if nextDeadline == nil || o.Start.Before(nextDeadline.Start) {
				nextDeadline = &occ[i]
			}
		case ics.KindPhase:
			// The phase you are in now. Several may overlap — "exam period"
			// inside "semester 3" — and the shorter one is the more specific
			// answer to "where am I".
			if o.Start.After(now) || o.End.Before(now) {
				continue
			}
			if phase == nil || o.End.Sub(o.Start) < phase.End.Sub(phase.Start) {
				phase = &occ[i]
			}
		case ics.KindSlot:
			if !o.AllDay && o.Start.Before(todayEnd) && o.End.After(todayStart) {
				slots = append(slots, o)
			}
		}
	}

	out := []store.VariableInput{
		{Name: "deadlines_open", Type: "number", Value: open, Source: "capability:calendar"},
		{Name: "deadlines_overdue", Type: "number", Value: overdue, Source: "capability:calendar"},
		{Name: "free_today", Type: "text", Value: largestGap(slots, todayStart, todayEnd, now), Source: "capability:calendar"},
	}
	if nextDeadline != nil {
		out = append(out,
			store.VariableInput{Name: "next_deadline", Type: "text", Value: nextDeadline.Summary, Source: "capability:calendar"},
			store.VariableInput{Name: "next_deadline_at", Type: "date", Value: nextDeadline.Start, Source: "capability:calendar"},
		)
	} else {
		out = append(out,
			store.VariableInput{Name: "next_deadline", Type: "text", Value: "", Source: "capability:calendar"},
			store.VariableInput{Name: "next_deadline_at", Type: "date", Value: nil, Source: "capability:calendar"},
		)
	}
	if phase != nil {
		span := phase.End.Sub(phase.Start)
		progress := 0
		if span > 0 {
			progress = int(now.Sub(phase.Start) * 100 / span)
		}
		out = append(out,
			store.VariableInput{Name: "current_phase", Type: "text", Value: phase.Summary, Source: "capability:calendar"},
			store.VariableInput{Name: "phase_progress", Type: "number", Value: clamp(progress, 0, 100), Source: "capability:calendar"},
			store.VariableInput{Name: "days_left_in_phase", Type: "number",
				Value: int(phase.End.Sub(now).Hours() / 24), Source: "capability:calendar"},
		)
	} else {
		out = append(out,
			store.VariableInput{Name: "current_phase", Type: "text", Value: "", Source: "capability:calendar"},
			store.VariableInput{Name: "phase_progress", Type: "number", Value: 0, Source: "capability:calendar"},
			store.VariableInput{Name: "days_left_in_phase", Type: "number", Value: 0, Source: "capability:calendar"},
		)
	}
	return out
}

// largestGap answers "when do I have time today" — the longest stretch between
// the appointments that are still ahead.
func largestGap(slots []Occurrence, dayStart, dayEnd, now time.Time) string {
	from := dayStart
	if now.After(from) {
		from = now
	}
	if !from.Before(dayEnd) {
		return ""
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i].Start.Before(slots[j].Start) })

	var best time.Duration
	var bestFrom time.Time
	cursor := from
	consider := func(until time.Time) {
		if d := until.Sub(cursor); d > best {
			best, bestFrom = d, cursor
		}
	}
	for _, s := range slots {
		if s.End.Before(cursor) {
			continue
		}
		if s.Start.After(cursor) {
			consider(s.Start)
		}
		if s.End.After(cursor) {
			cursor = s.End
		}
	}
	consider(dayEnd)

	if best < 15*time.Minute {
		return ""
	}
	hours := int(best.Hours())
	minutes := int(best.Minutes()) % 60
	length := fmt.Sprintf("%dh %02dm", hours, minutes)
	if hours == 0 {
		length = fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%s from %s", length, bestFrom.Format("15:04"))
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
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
	}, {
		// The university publishes the schedule; this brings it in as one of
		// the project's calendars. No login: it is a public timetable.
		Name:        "timetable",
		Title:       "Lecture timetable",
		Description: "Fetches a course's lectures from dhbw.app and keeps them as a read-only calendar — with the room, the lecturer and the exams.",
		Options: []capability.AccountField{
			{Name: "course", Label: "Course", Type: "text", Required: true,
				Placeholder: "RV-WDS125", Hint: "The way it is written on dhbw.app."},
			{Name: "name", Label: "Name for the file it writes", Type: "text", Placeholder: "timetable"},
			{Name: "weeksBack", Label: "Weeks of history to keep", Type: "text", Placeholder: "4"},
		},
		Run: runTimetable,
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

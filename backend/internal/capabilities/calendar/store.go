package calendar

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/capabilities/calendar/ics"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/workspace"
)

// MainFile is where a calendar project keeps its events by default: one
// VCALENDAR with every VEVENT in it, valid RFC 5545, openable in Google
// Calendar, Thunderbird or Apple Calendar without conversion.
const MainFile = "calendar.ics"

// SplitDir is the option for very large calendars: one file per event.
const SplitDir = "events"

// SubscriptionDir holds calendars pulled from somewhere else. Everything in
// here is read-only and gets overwritten on the next run.
const SubscriptionDir = "subscriptions"

// Writes are serialised per project: the server owns the file alone, so two
// simultaneous edits cannot corrupt it.
var (
	locksMu sync.Mutex
	locks   = map[uuid.UUID]*sync.Mutex{}
)

func lockFor(id uuid.UUID) *sync.Mutex {
	locksMu.Lock()
	defer locksMu.Unlock()
	m, ok := locks[id]
	if !ok {
		m = &sync.Mutex{}
		locks[id] = m
	}
	return m
}

// file is one .ics file of a project, parsed.
type file struct {
	Path     string
	Cal      *ics.Component
	ReadOnly bool
}

// readFiles parses every calendar file a project has.
func readFiles(ctx context.Context, env *capability.Env, p *model.Project) ([]*file, error) {
	var out []*file

	add := func(rel string, readOnly bool) error {
		body, err := env.Files.ReadLocal(ctx, p, rel)
		if err != nil {
			return err
		}
		cal, err := ics.ParseCalendar(body)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		out = append(out, &file{Path: rel, Cal: cal, ReadOnly: readOnly})
		return nil
	}

	if env.Files.Exists(p, MainFile) {
		if err := add(MainFile, false); err != nil {
			return nil, err
		}
	}
	fs := env.Files.Workspace().Open(p.ID)
	for _, dir := range []struct {
		name     string
		readOnly bool
	}{{SplitDir, false}, {SubscriptionDir, true}} {
		entries, err := fs.List(dir.name)
		if err != nil {
			continue // the folder simply does not exist
		}
		for _, e := range entries {
			if e.IsDir || !strings.HasSuffix(strings.ToLower(e.Name), ".ics") {
				continue
			}
			if err := add(path.Join(dir.name, e.Name), dir.readOnly); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// Reindex rebuilds the index from the files. It runs after every write and
// whenever a file changes some other way — an upload, a git push, a scheduler.
func Reindex(ctx context.Context, env *capability.Env, p *model.Project) error {
	files, err := readFiles(ctx, env, p)
	if err != nil {
		return err
	}
	type row struct {
		uid        string
		rid        *time.Time
		e          ics.Event
		sourceFile string
		readOnly   bool
	}
	var rows []row
	for _, f := range files {
		for _, comp := range f.Cal.Kids("VEVENT") {
			e, err := ics.FromComponent(comp)
			if err != nil {
				continue // a broken event does not break the project
			}
			if e.UID == "" {
				continue
			}
			rows = append(rows, row{uid: e.UID, rid: e.RecurrenceID, e: e, sourceFile: f.Path, readOnly: f.ReadOnly})
		}
	}

	tx, err := env.Store.Pool().Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op
	if _, err := tx.Exec(ctx, `DELETE FROM calendar_events WHERE project_id=$1`, p.ID); err != nil {
		return err
	}
	for _, r := range rows {
		exdates := make([]string, 0, len(r.e.ExDates))
		for _, ex := range r.e.ExDates {
			exdates = append(exdates, ics.FormatUTC(ex))
		}
		alarms := make([]string, 0, len(r.e.Alarms))
		for _, a := range r.e.Alarms {
			alarms = append(alarms, strconv.Itoa(a))
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO calendar_events (project_id, uid, recurrence_id, dtstart, dtend, all_day,
				summary, description, location, rrule, exdates, color, alarms, sequence,
				source_file, read_only)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
			p.ID, r.uid, r.rid, r.e.Start, r.e.End, r.e.AllDay, r.e.Summary, r.e.Description,
			r.e.Location, r.e.RRule, strings.Join(exdates, ","), r.e.Color,
			strings.Join(alarms, ","), r.e.Sequence, r.sourceFile, r.readOnly)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Row is one indexed VEVENT.
type Row struct {
	ProjectID   uuid.UUID
	UID         string
	Recurrence  *time.Time
	Start       time.Time
	End         time.Time
	AllDay      bool
	Summary     string
	Description string
	Location    string
	RRule       string
	ExDates     []time.Time
	Color       string
	Alarms      []int
	Sequence    int
	SourceFile  string
	ReadOnly    bool
}

func (r Row) event() ics.Event {
	return ics.Event{
		UID:          r.UID,
		Summary:      r.Summary,
		Description:  r.Description,
		Location:     r.Location,
		Start:        r.Start,
		End:          r.End,
		AllDay:       r.AllDay,
		RRule:        r.RRule,
		ExDates:      r.ExDates,
		RecurrenceID: r.Recurrence,
		Sequence:     r.Sequence,
		Color:        r.Color,
		Alarms:       r.Alarms,
	}
}

func loadRows(ctx context.Context, env *capability.Env, projectIDs []uuid.UUID) ([]Row, error) {
	rows, err := env.Store.Pool().Query(ctx, `
		SELECT project_id, uid, recurrence_id, dtstart, dtend, all_day, summary, description,
			location, rrule, exdates, color, alarms, sequence, source_file, read_only
		FROM calendar_events WHERE project_id = ANY($1) ORDER BY dtstart`, projectIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Row{}
	for rows.Next() {
		var r Row
		var exdates, alarms string
		if err := rows.Scan(&r.ProjectID, &r.UID, &r.Recurrence, &r.Start, &r.End, &r.AllDay,
			&r.Summary, &r.Description, &r.Location, &r.RRule, &exdates, &r.Color, &alarms,
			&r.Sequence, &r.SourceFile, &r.ReadOnly); err != nil {
			return nil, err
		}
		for _, ex := range strings.Split(exdates, ",") {
			if ex == "" {
				continue
			}
			if t, err := time.Parse("20060102T150405Z", ex); err == nil {
				r.ExDates = append(r.ExDates, t)
			}
		}
		for _, a := range strings.Split(alarms, ",") {
			if a == "" {
				continue
			}
			if n, err := strconv.Atoi(a); err == nil {
				r.Alarms = append(r.Alarms, n)
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Occurrence is what the grid draws.
type Occurrence struct {
	ProjectID    uuid.UUID `json:"projectId"`
	ProjectSlug  string    `json:"projectSlug,omitempty"`
	ProjectTitle string    `json:"projectTitle,omitempty"`
	Color        string    `json:"color,omitempty"`
	UID          string    `json:"uid"`
	RecurrenceID string    `json:"recurrenceId,omitempty"`
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	AllDay       bool      `json:"allDay"`
	Summary      string    `json:"summary"`
	Description  string    `json:"description,omitempty"`
	Location     string    `json:"location,omitempty"`
	RRule        string    `json:"rrule,omitempty"`
	Repeats      bool      `json:"repeats"`
	IsException  bool      `json:"isException,omitempty"`
	Alarms       []int     `json:"alarms,omitempty"`
	ReadOnly     bool      `json:"readOnly"`
	SourceFile   string    `json:"sourceFile"`
}

// Between expands everything in the given projects into the time range.
func Between(ctx context.Context, env *capability.Env, projects []model.Project, from, to time.Time) ([]Occurrence, error) {
	ids := make([]uuid.UUID, 0, len(projects))
	meta := map[uuid.UUID]model.Project{}
	for _, p := range projects {
		ids = append(ids, p.ID)
		meta[p.ID] = p
	}
	if len(ids) == 0 {
		return []Occurrence{}, nil
	}
	rows, err := loadRows(ctx, env, ids)
	if err != nil {
		return nil, err
	}

	// Group by project and UID so overrides can be applied to their series.
	type key struct {
		project uuid.UUID
		uid     string
	}
	masters := map[key]Row{}
	overrides := map[key][]Row{}
	for _, r := range rows {
		k := key{r.ProjectID, r.UID}
		if r.Recurrence != nil {
			overrides[k] = append(overrides[k], r)
			continue
		}
		masters[k] = r
	}

	out := []Occurrence{}
	for k, master := range masters {
		var ovEvents []ics.Event
		for _, o := range overrides[k] {
			ovEvents = append(ovEvents, o.event())
		}
		occs, err := ics.Expand(master.event(), ovEvents, from, to)
		if err != nil {
			// A repeat rule we cannot read must not hide the whole calendar.
			occs = []ics.Occurrence{{Start: master.Start, End: master.End, RecurrenceID: master.Start}}
		}
		p := meta[k.project]
		for _, occ := range occs {
			o := Occurrence{
				ProjectID:    k.project,
				ProjectSlug:  p.Slug,
				ProjectTitle: p.Title,
				Color:        firstNonEmpty(master.Color, p.Color),
				UID:          master.UID,
				Start:        occ.Start,
				End:          occ.End,
				AllDay:       master.AllDay,
				Summary:      master.Summary,
				Description:  master.Description,
				Location:     master.Location,
				RRule:        master.RRule,
				Repeats:      master.RRule != "",
				IsException:  occ.IsException,
				Alarms:       master.Alarms,
				ReadOnly:     master.ReadOnly,
				SourceFile:   master.SourceFile,
			}
			if master.RRule != "" {
				o.RecurrenceID = ics.FormatUTC(occ.RecurrenceID)
			}
			if occ.IsException {
				for _, ov := range overrides[k] {
					if ov.Recurrence != nil && ov.Recurrence.Equal(occ.RecurrenceID) {
						o.Summary = ov.Summary
						o.Description = ov.Description
						o.Location = ov.Location
						break
					}
				}
			}
			out = append(out, o)
		}
	}
	// Orphaned overrides (a single appearance whose series is gone) are still
	// events and are shown rather than swallowed.
	for k, list := range overrides {
		if _, ok := masters[k]; ok {
			continue
		}
		p := meta[k.project]
		for _, r := range list {
			if r.End.Before(from) || r.Start.After(to) {
				continue
			}
			out = append(out, Occurrence{
				ProjectID: k.project, ProjectSlug: p.Slug, ProjectTitle: p.Title,
				Color: firstNonEmpty(r.Color, p.Color), UID: r.UID,
				RecurrenceID: ics.FormatUTC(*r.Recurrence),
				Start:        r.Start, End: r.End, AllDay: r.AllDay, Summary: r.Summary,
				Description: r.Description, Location: r.Location, ReadOnly: r.ReadOnly,
				SourceFile: r.SourceFile, IsException: true,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Start.Equal(out[j].Start) {
			return out[i].Summary < out[j].Summary
		}
		return out[i].Start.Before(out[j].Start)
	})
	return out, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// ------------------------------------------------------------------- writing

// targetFile decides where a new event is stored: one file for the whole
// calendar, or one file per event when the project switched to split mode.
func targetFile(ctx context.Context, env *capability.Env, p *model.Project, uid string) (string, error) {
	var split bool
	err := env.Store.Pool().QueryRow(ctx,
		`SELECT split FROM calendar_settings WHERE project_id=$1`, p.ID).Scan(&split)
	if err != nil {
		split = false
	}
	if split {
		return path.Join(SplitDir, safeUID(uid)+".ics"), nil
	}
	return MainFile, nil
}

func safeUID(uid string) string {
	var b strings.Builder
	for _, r := range uid {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := b.String()
	if out == "" {
		out = uuid.NewString()
	}
	if len(out) > 100 {
		out = out[:100]
	}
	return out
}

// mutate loads the file an event lives in, hands it to fn and writes it back.
// Everything that changes a calendar goes through here, under the project's
// lock.
func mutate(ctx context.Context, env *capability.Env, p *model.Project, uid string, author, email, message string, fn func(f *file) error) error {
	m := lockFor(p.ID)
	m.Lock()
	defer m.Unlock()

	files, err := readFiles(ctx, env, p)
	if err != nil {
		return err
	}

	var target *file
	if uid != "" {
		for _, f := range files {
			for _, comp := range f.Cal.Kids("VEVENT") {
				if comp.Value("UID") == uid {
					target = f
					break
				}
			}
			if target != nil {
				break
			}
		}
	}
	if target == nil {
		rel, err := targetFile(ctx, env, p, uid)
		if err != nil {
			return err
		}
		for _, f := range files {
			if f.Path == rel {
				target = f
				break
			}
		}
		if target == nil {
			target = &file{Path: rel, Cal: ics.NewCalendar(p.Title)}
		}
	}
	if target.ReadOnly {
		return errReadOnlyEvent
	}

	if err := fn(target); err != nil {
		return err
	}
	if _, err := env.Files.Write(ctx, p, target.Path, target.Cal.Bytes(), capabilityOp(author, email, message)); err != nil {
		return err
	}
	return Reindex(ctx, env, p)
}

func capabilityOp(author, email, message string) filesOp {
	return filesOp{Author: author, Email: email, Message: message, Commit: true}
}

// Export merges everything into a single VCALENDAR — that is what a
// subscription URL and a download return, whatever the storage looks like
// inside.
func Export(ctx context.Context, env *capability.Env, p *model.Project) ([]byte, error) {
	files, err := readFiles(ctx, env, p)
	if err != nil {
		return nil, err
	}
	out := ics.NewCalendar(p.Title)
	for _, f := range files {
		for _, comp := range f.Cal.Kids("VEVENT") {
			out.Children = append(out.Children, comp)
		}
		// Time zone definitions travel with the events that need them.
		for _, tz := range f.Cal.Kids("VTIMEZONE") {
			out.Children = append(out.Children, tz)
		}
	}
	return out.Bytes(), nil
}

// entries lists the .ics files of a project, for the "Files" tab hint.
func entries(env *capability.Env, p *model.Project) []workspace.Entry {
	fs := env.Files.Workspace().Open(p.ID)
	list, err := fs.List("")
	if err != nil {
		return nil
	}
	return list
}

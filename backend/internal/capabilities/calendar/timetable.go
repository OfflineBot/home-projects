package calendar

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/offlinebot/home-projects/backend/internal/capabilities/calendar/ics"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/slug"
	"github.com/offlinebot/home-projects/backend/internal/store"
)

// The lecture timetable, from the university's own API.
//
// The old server scraped Rapla's HTML and then stopped: dhbw.app publishes the
// same schedule as JSON, filtered to one course, and that is what it ended up
// using. So this asks the same question — one request for the whole course —
// and writes what comes back as a calendar in the project, next to the
// subscribed ones. From there it is an ordinary calendar: it shows on the
// agenda, on a board, in the week view, and it can be exported.
//
// Read-only on purpose. The next run overwrites the file, because the timetable
// is the university's to change and a room that moved should move here too.

const timetableAPI = "https://api.dhbw.app/rapla/lectures/"

type lecture struct {
	// "EXAM" comes back as the type; the field is there on the campus-event
	// endpoint and harmless here.
	EntityType string   `json:"entityType"`
	StartTime  string   `json:"startTime"`
	EndTime    string   `json:"endTime"`
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Lecturer   string   `json:"lecturer"`
	Rooms      []string `json:"rooms"`
}

func runTimetable(ctx context.Context, env *capability.Env, job capability.Job) (capability.Report, error) {
	course := strings.TrimSpace(asString(job.Options["course"]))
	if course == "" {
		return capability.Report{}, fmt.Errorf("this scheduler has no course — \"RV-WDS125\", the way it is written on dhbw.app")
	}
	name := strings.TrimSpace(asString(job.Options["name"]))
	if name == "" {
		name = "timetable"
	}
	// How far back to keep. Forward is whatever the university has published;
	// backwards is a choice, and four weeks is what the old server kept.
	back := 4
	if weeks, ok := asInt(job.Options["weeksBack"]); ok && weeks >= 0 && weeks <= 104 {
		back = weeks
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, timetableAPI+course, nil)
	if err != nil {
		return capability.Report{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "home-projects/1.0")

	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return capability.Report{}, fmt.Errorf("the timetable could not be fetched: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return capability.Report{}, fmt.Errorf("there is no course called %q", course)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return capability.Report{}, fmt.Errorf("dhbw.app answered %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return capability.Report{}, fmt.Errorf("the answer could not be read: %w", err)
	}
	var lectures []lecture
	if err := json.Unmarshal(body, &lectures); err != nil {
		return capability.Report{}, fmt.Errorf("what came back is not a timetable: %w", err)
	}

	from := time.Now().AddDate(0, 0, -7*back)
	target := ics.NewCalendar(job.Project.Title + " · " + name)
	kept := 0
	for _, l := range lectures {
		start, err := time.Parse(time.RFC3339Nano, l.StartTime)
		if err != nil {
			continue
		}
		end, err := time.Parse(time.RFC3339Nano, l.EndTime)
		if err != nil {
			end = start.Add(90 * time.Minute)
		}
		start, end = start.In(time.Local), end.In(time.Local)
		if start.Before(from) {
			continue
		}
		target.Children = append(target.Children, lessonEvent(l, start, end, course).ToComponent())
		kept++
	}
	// In the order they happen, so the file reads like the term does.
	sort.SliceStable(target.Children, func(i, j int) bool {
		return target.Children[i].Value("DTSTART") < target.Children[j].Value("DTSTART")
	})

	rel := path.Join(SubscriptionDir, slug.Make(name)+".ics")
	m := lockFor(job.Project.ID)
	m.Lock()
	_, err = env.Files.Write(ctx, job.Project, rel, target.Bytes(), filesOp{
		Author: "the timetable", Email: "scheduler@home-projects",
		Message: "Update timetable " + name, Commit: true,
	})
	m.Unlock()
	if err != nil {
		return capability.Report{}, err
	}
	if err := Reindex(ctx, env, job.Project); err != nil {
		return capability.Report{}, err
	}

	job.Log("%d of %d lectures kept (%d weeks back)", kept, len(lectures), back)
	return capability.Report{
		Message:      fmt.Sprintf("%d lectures for %s", kept, course),
		FilesChanged: 1,
		Variables:    timetableVariables(target, kept),
	}, nil
}

// lessonEvent turns one lecture into the event a calendar shows: what it is,
// where, and who is holding it.
func lessonEvent(l lecture, start, end time.Time, course string) *ics.Event {
	title := strings.TrimSpace(l.Name)
	if l.EntityType == "EXAM" || strings.EqualFold(l.Type, "EXAM") {
		title = "Prüfung · " + title
	}
	where := strings.Join(l.Rooms, ", ")
	if where == "" && l.Type == "ONLINE" {
		where = "online"
	}
	described := []string{}
	if l.Lecturer != "" {
		described = append(described, l.Lecturer)
	}
	if l.Type != "" && l.Type != "ONLINE" {
		described = append(described, l.Type)
	}
	return &ics.Event{
		// The same lecture keeps the same id across runs, so a calendar that
		// was already subscribed to somewhere else updates rather than doubles.
		UID:         fmt.Sprintf("%s-%d@dhbw.app", slug.Make(course+"-"+l.Name), start.Unix()),
		Summary:     title,
		Location:    where,
		Description: strings.Join(described, " · "),
		Start:       start,
		End:         end,
	}
}

// timetableVariables: what a board can show without opening the calendar.
func timetableVariables(cal *ics.Component, kept int) []store.VariableInput {
	out := []store.VariableInput{{Name: "lectures", Type: "number", Value: kept, Source: "timetable"}}
	now := time.Now()
	for _, child := range cal.Kids("VEVENT") {
		when, _, err := ics.ParseTimeProp(child.Get("DTSTART"))
		if err != nil || when.Before(now) {
			continue
		}
		out = append(out, store.VariableInput{
			Name: "next_lecture", Type: "text", Source: "timetable",
			Value: child.Value("SUMMARY") + " · " + when.Local().Format("Mon 15:04"),
		})
		break
	}
	return out
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case string:
		var out int
		if _, err := fmt.Sscanf(n, "%d", &out); err == nil {
			return out, true
		}
	}
	return 0, false
}

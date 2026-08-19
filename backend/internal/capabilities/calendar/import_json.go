package calendar

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/capabilities/calendar/ics"
)

// A calendar exported by the old server.
//
// It wrote JSON, not iCalendar: `{"version":1,"events":[…]}`, one entry per
// thing the owner had put in by hand — appointments, phases, starts and
// deadlines, with a colour and, for lectures, who was holding them. A file like
// that is what somebody actually has lying around after moving here, and
// telling them to convert it first is telling them to give up.
//
// So the import takes it. The four kinds it had are the four kinds this
// calendar has, which is not a coincidence — they came from the same idea of
// what a calendar entry is.

type oldExport struct {
	Version int        `json:"version"`
	Events  []oldEvent `json:"events"`
}

type oldEvent struct {
	ID          string    `json:"id"`
	Source      string    `json:"source"`
	Title       string    `json:"title"`
	Location    string    `json:"location"`
	Lecturer    string    `json:"lecturer"`
	Description string    `json:"description"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time"`
	EntryType   string    `json:"entry_type"`
	Colour      string    `json:"color"`
}

// looksLikeOldExport: the first non-space character decides. An iCalendar file
// begins with BEGIN:VCALENDAR, and nothing that starts with a brace is one.
func looksLikeOldExport(body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

// eventsFromOldExport turns that file into the components the import writes.
func eventsFromOldExport(body []byte) ([]*ics.Component, error) {
	var file oldExport
	if err := json.Unmarshal(body, &file); err != nil {
		// A bare list of events is accepted too — that is what somebody gets
		// who copied the "events" part out of the file.
		var bare []oldEvent
		if err2 := json.Unmarshal(body, &bare); err2 != nil {
			return nil, fmt.Errorf("this is neither a calendar nor an export of one: %v", err)
		}
		file.Events = bare
	}
	if len(file.Events) == 0 {
		return nil, fmt.Errorf("this export has no events in it")
	}

	out := make([]*ics.Component, 0, len(file.Events))
	for _, e := range file.Events {
		if e.StartTime.IsZero() {
			continue
		}
		end := e.EndTime
		if end.IsZero() || end.Before(e.StartTime) {
			end = e.StartTime.Add(time.Hour)
		}
		described := []string{}
		if e.Lecturer != "" {
			described = append(described, e.Lecturer)
		}
		if e.Description != "" {
			described = append(described, e.Description)
		}
		// The id it had is kept, so importing the same file twice updates the
		// same entries instead of doubling them.
		uid := strings.TrimSpace(e.ID)
		if uid == "" {
			uid = uuid.NewString()
		}
		event := &ics.Event{
			UID:         uid + "@home-server",
			Summary:     strings.TrimSpace(e.Title),
			Location:    e.Location,
			Description: strings.Join(described, "\n"),
			Start:       e.StartTime.Local(),
			End:         end.Local(),
			Color:       e.Colour,
			Kind:        kindOfOldEntry(e.EntryType),
		}
		if event.Kind == ics.KindDeadline {
			// A deadline is a point that can be done; the old file put the
			// moment in both columns.
			event.IsTodo = true
			event.Start = end.Local()
			event.End = end.Local()
		}
		out = append(out, event.ToComponent())
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("none of the entries in this export had a time")
	}
	return out, nil
}

// The old server's four entry types, which are this calendar's four kinds.
func kindOfOldEntry(entry string) string {
	switch strings.ToLower(strings.TrimSpace(entry)) {
	case "phase":
		return ics.KindPhase
	case "deadline":
		return ics.KindDeadline
	case "start":
		return ics.KindMilestone
	default:
		return ics.KindSlot
	}
}

package ics

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/teambition/rrule-go"
)

// Event is the readable view of a VEVENT. The component it came from is kept
// so writing it back changes only the properties that were edited.
type Event struct {
	UID          string
	Summary      string
	Description  string
	Location     string
	Start        time.Time
	End          time.Time
	AllDay       bool
	RRule        string
	ExDates      []time.Time
	RDates       []time.Time
	RecurrenceID *time.Time
	Sequence     int
	Created      time.Time
	LastModified time.Time
	Color        string
	// Alarms are reminders in minutes before the start.
	Alarms []int
	Status string
	TZID   string

	Comp *Component `json:"-"`
}

// DefaultLocation is what a floating time means. It is set once at startup
// from the server's timezone.
var DefaultLocation = time.Local

// FromComponent reads a VEVENT.
func FromComponent(c *Component) (Event, error) {
	e := Event{Comp: c}
	e.UID = c.Value("UID")
	e.Summary = UnescapeText(c.Value("SUMMARY"))
	e.Description = UnescapeText(c.Value("DESCRIPTION"))
	e.Location = UnescapeText(c.Value("LOCATION"))
	e.Status = c.Value("STATUS")
	e.Color = c.Value("COLOR")
	if e.Color == "" {
		e.Color = c.Value("X-APPLE-CALENDAR-COLOR")
	}
	if seq := c.Value("SEQUENCE"); seq != "" {
		e.Sequence, _ = strconv.Atoi(seq)
	}

	start := c.Get("DTSTART")
	if start == nil {
		return e, fmt.Errorf("event %q has no DTSTART", e.UID)
	}
	t, allDay, err := ParseTimeProp(start)
	if err != nil {
		return e, err
	}
	e.Start, e.AllDay = t, allDay
	e.TZID = start.Param("TZID")

	if end := c.Get("DTEND"); end != nil {
		t, _, err := ParseTimeProp(end)
		if err != nil {
			return e, err
		}
		e.End = t
	} else if dur := c.Value("DURATION"); dur != "" {
		d, err := ParseDuration(dur)
		if err != nil {
			return e, err
		}
		e.End = e.Start.Add(d)
	} else if e.AllDay {
		e.End = e.Start.AddDate(0, 0, 1)
	} else {
		e.End = e.Start.Add(time.Hour)
	}

	if r := c.Get("RRULE"); r != nil {
		e.RRule = r.Value
	}
	for _, p := range c.All("EXDATE") {
		for _, v := range strings.Split(p.Value, ",") {
			if t, _, err := parseTimeValue(v, p.Param("TZID"), p.Param("VALUE") == "DATE"); err == nil {
				e.ExDates = append(e.ExDates, t)
			}
		}
	}
	for _, p := range c.All("RDATE") {
		for _, v := range strings.Split(p.Value, ",") {
			if t, _, err := parseTimeValue(v, p.Param("TZID"), p.Param("VALUE") == "DATE"); err == nil {
				e.RDates = append(e.RDates, t)
			}
		}
	}
	if rid := c.Get("RECURRENCE-ID"); rid != nil {
		if t, _, err := ParseTimeProp(rid); err == nil {
			e.RecurrenceID = &t
		}
	}
	if v := c.Value("CREATED"); v != "" {
		if t, _, err := parseTimeValue(v, "", false); err == nil {
			e.Created = t
		}
	}
	if v := c.Value("LAST-MODIFIED"); v != "" {
		if t, _, err := parseTimeValue(v, "", false); err == nil {
			e.LastModified = t
		}
	}
	for _, alarm := range c.Kids("VALARM") {
		if trig := alarm.Value("TRIGGER"); trig != "" {
			if d, err := ParseDuration(trig); err == nil {
				e.Alarms = append(e.Alarms, int(-d.Minutes()))
			}
		}
	}
	return e, nil
}

// ToComponent writes the event back, creating the component if it is new.
func (e *Event) ToComponent() *Component {
	c := e.Comp
	if c == nil {
		c = NewComponent("VEVENT")
		e.Comp = c
	}
	c.Set("UID", e.UID)
	c.Set("DTSTAMP", FormatUTC(time.Now()))
	c.Set("SUMMARY", EscapeText(e.Summary))

	setOrRemove(c, "DESCRIPTION", EscapeText(e.Description))
	setOrRemove(c, "LOCATION", EscapeText(e.Location))
	setOrRemove(c, "COLOR", e.Color)
	setOrRemove(c, "STATUS", e.Status)

	// Both properties are created first: taking a pointer into c.Props and
	// then appending another property would leave the pointer dangling.
	c.Set("DTSTART", "")
	c.Set("DTEND", "")
	start := c.Get("DTSTART")
	end := c.Get("DTEND")
	if e.AllDay {
		start.Value = e.Start.Format("20060102")
		start.Params = nil
		start.SetParam("VALUE", "DATE")
		endDate := e.End
		if !endDate.After(e.Start) {
			endDate = e.Start.AddDate(0, 0, 1)
		}
		end.Value = endDate.Format("20060102")
		end.Params = nil
		end.SetParam("VALUE", "DATE")
	} else {
		start.Params = nil
		start.Value = FormatUTC(e.Start)
		end.Params = nil
		end.Value = FormatUTC(e.End)
	}
	c.Remove("DURATION")

	c.Remove("RRULE")
	if e.RRule != "" {
		c.Set("RRULE", strings.TrimPrefix(strings.ToUpper(e.RRule), "RRULE:"))
	}
	c.Remove("EXDATE")
	for _, ex := range e.ExDates {
		p := Prop{Name: "EXDATE", Value: FormatUTC(ex)}
		if e.AllDay {
			p.Value = ex.Format("20060102")
			p.SetParam("VALUE", "DATE")
		}
		c.Props = append(c.Props, p)
	}

	c.Set("SEQUENCE", strconv.Itoa(e.Sequence))
	c.Set("LAST-MODIFIED", FormatUTC(time.Now()))
	if c.Value("CREATED") == "" {
		c.Set("CREATED", FormatUTC(time.Now()))
	}

	c.RemoveChildren("VALARM")
	for _, minutes := range e.Alarms {
		alarm := NewComponent("VALARM")
		alarm.Set("ACTION", "DISPLAY")
		alarm.Set("DESCRIPTION", EscapeText(e.Summary))
		alarm.Set("TRIGGER", fmt.Sprintf("-PT%dM", minutes))
		c.Children = append(c.Children, alarm)
	}
	return c
}

func setOrRemove(c *Component, name, value string) {
	if value == "" {
		c.Remove(name)
		return
	}
	c.Set(name, value)
}

// --------------------------------------------------------------- time values

func ParseTimeProp(p *Prop) (time.Time, bool, error) {
	return parseTimeValue(p.Value, p.Param("TZID"), strings.EqualFold(p.Param("VALUE"), "DATE"))
}

func parseTimeValue(value, tzid string, isDate bool) (time.Time, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false, fmt.Errorf("empty date value")
	}
	if isDate || len(value) == 8 {
		t, err := time.ParseInLocation("20060102", value, time.UTC)
		return t, true, err
	}
	if strings.HasSuffix(value, "Z") {
		t, err := time.ParseInLocation("20060102T150405Z", value, time.UTC)
		return t.UTC(), false, err
	}
	loc := DefaultLocation
	if tzid != "" {
		if l, err := time.LoadLocation(tzid); err == nil {
			loc = l
		}
	}
	t, err := time.ParseInLocation("20060102T150405", value, loc)
	return t.UTC(), false, err
}

func FormatUTC(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}

// ParseDuration reads an RFC 5545 duration such as -PT15M or P1DT2H.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	sign := time.Duration(1)
	if strings.HasPrefix(s, "-") {
		sign = -1
		s = s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	if !strings.HasPrefix(s, "P") {
		return 0, fmt.Errorf("%q is not a duration", s)
	}
	s = s[1:]
	var total time.Duration
	inTime := false
	num := ""
	for _, r := range s {
		switch {
		case r == 'T':
			inTime = true
		case r >= '0' && r <= '9':
			num += string(r)
		default:
			n, err := strconv.Atoi(num)
			if err != nil {
				return 0, fmt.Errorf("%q is not a duration", s)
			}
			num = ""
			switch r {
			case 'W':
				total += time.Duration(n) * 7 * 24 * time.Hour
			case 'D':
				total += time.Duration(n) * 24 * time.Hour
			case 'H':
				total += time.Duration(n) * time.Hour
			case 'M':
				if inTime {
					total += time.Duration(n) * time.Minute
				} else {
					total += time.Duration(n) * 30 * 24 * time.Hour
				}
			case 'S':
				total += time.Duration(n) * time.Second
			default:
				return 0, fmt.Errorf("unknown duration unit %q", string(r))
			}
		}
	}
	return sign * total, nil
}

// ---------------------------------------------------------------- recurrence

// Occurrence is one appearance of an event in a time range.
type Occurrence struct {
	Start time.Time
	End   time.Time
	// RecurrenceID identifies which appearance this is, for editing a single
	// one out of a series.
	RecurrenceID time.Time
	IsException  bool
}

// Expand returns every appearance of an event between from and to. Overrides
// are the VEVENTs with the same UID and a RECURRENCE-ID — a single changed
// appearance replaces the computed one.
func Expand(e Event, overrides []Event, from, to time.Time) ([]Occurrence, error) {
	duration := e.End.Sub(e.Start)
	if duration < 0 {
		duration = 0
	}

	exceptions := map[int64]Event{}
	for _, o := range overrides {
		if o.RecurrenceID != nil {
			exceptions[o.RecurrenceID.UTC().Unix()] = o
		}
	}

	if e.RRule == "" && len(e.RDates) == 0 {
		if e.End.Before(from) || e.Start.After(to) {
			return nil, nil
		}
		return []Occurrence{{Start: e.Start, End: e.End, RecurrenceID: e.Start}}, nil
	}

	set := rrule.Set{}
	set.DTStart(e.Start.UTC())
	if e.RRule != "" {
		opt, err := rrule.StrToRRule(strings.TrimPrefix(strings.ToUpper(e.RRule), "RRULE:"))
		if err != nil {
			return nil, fmt.Errorf("this event's repeat rule cannot be read: %w", err)
		}
		opt.OrigOptions.Dtstart = e.Start.UTC()
		r, err := rrule.NewRRule(opt.OrigOptions)
		if err != nil {
			return nil, fmt.Errorf("this event's repeat rule cannot be read: %w", err)
		}
		set.RRule(r)
	}
	for _, rd := range e.RDates {
		set.RDate(rd.UTC())
	}
	for _, ex := range e.ExDates {
		set.ExDate(ex.UTC())
	}

	// Widen the window so an appearance that started before `from` but is
	// still running shows up.
	starts := set.Between(from.Add(-duration-time.Hour), to, true)
	out := make([]Occurrence, 0, len(starts))
	for _, s := range starts {
		occ := Occurrence{Start: s, End: s.Add(duration), RecurrenceID: s}
		if ov, ok := exceptions[s.UTC().Unix()]; ok {
			occ.Start, occ.End, occ.IsException = ov.Start, ov.End, true
		}
		if occ.End.Before(from) || occ.Start.After(to) {
			continue
		}
		out = append(out, occ)
	}
	return out, nil
}

// NewCalendar builds an empty VCALENDAR with the properties every reader
// expects.
func NewCalendar(name string) *Component {
	c := NewComponent("VCALENDAR")
	c.Set("VERSION", "2.0")
	c.Set("PRODID", "-//home-projects//EN")
	c.Set("CALSCALE", "GREGORIAN")
	if name != "" {
		c.Set("X-WR-CALNAME", EscapeText(name))
	}
	return c
}

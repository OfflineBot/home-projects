package ics

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/teambition/rrule-go"
)

// The five kinds an entry can be. They exist because these things want to be
// *drawn* differently: a deadline is a moment with weight, not a block from
// 23:00 to 23:59, and a six-week phase drawn as a block would bury every
// lecture underneath it.
//
// The kind rides in X-HOME-KIND, a custom property RFC 5545 explicitly allows.
// A foreign client that ignores it still shows a correct calendar — just less
// prettily. Nothing is written that a reader needs in order to understand the
// entry.
const (
	KindSlot      = "slot"      // when am I where: start → end
	KindAllDay    = "all-day"   // what is today: a whole day or several
	KindDeadline  = "deadline"  // when is it due: a point, and it can be done
	KindPhase     = "phase"     // what period am I in: days to months, drawn as a band
	KindMilestone = "milestone" // when does it start: a point, nothing owed
)

// Custom properties. All of them are optional; dropping them loses decoration,
// never the appointment.
const (
	PropKind       = "X-HOME-KIND"
	PropAttachedTo = "X-HOME-ATTACHED-TO" // my note on a pulled entry
	PropLink       = "X-HOME-LINK"        // project path this entry belongs to
	PropPerson     = "X-HOME-PERSON"      // lecturer, so it can be filtered
)

// Event is the readable view of one entry — a VEVENT, or a VTODO when it is a
// deadline. The component it came from is kept so writing it back changes only
// the properties that were edited.
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

	// Kind is one of the five above. Empty on the way in means: work it out
	// from what the entry looks like.
	Kind string
	// IsTodo marks a VTODO. A deadline is stored as one because that is what
	// iCalendar has for it — and because an event cannot be completed, while a
	// todo can.
	IsTodo    bool
	Completed time.Time
	// Priority is RFC 5545's 1–9, shown as normal / important / critical.
	Priority   int
	Categories []string
	RelatedTo  string
	AttachedTo string
	Link       string
	Person     string

	Comp *Component `json:"-"`
}

// EffectiveKind is what to draw when the file said nothing: a timed entry is a
// slot, a full-day one is an all-day, a todo is a deadline.
func (e Event) EffectiveKind() string {
	switch e.Kind {
	case KindSlot, KindAllDay, KindDeadline, KindPhase, KindMilestone:
		return e.Kind
	}
	if e.IsTodo {
		return KindDeadline
	}
	if e.AllDay {
		return KindAllDay
	}
	return KindSlot
}

// Done reports whether a deadline has been ticked off.
func (e Event) Done() bool { return strings.EqualFold(e.Status, "COMPLETED") }

// DefaultLocation is what a floating time means. It is set once at startup
// from the server's timezone.
var DefaultLocation = time.Local

// FromComponent reads a VEVENT or a VTODO.
func FromComponent(c *Component) (Event, error) {
	e := Event{Comp: c, IsTodo: strings.EqualFold(c.Name, "VTODO")}
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
	e.Kind = strings.ToLower(strings.TrimSpace(c.Value(PropKind)))
	e.AttachedTo = c.Value(PropAttachedTo)
	e.Link = UnescapeText(c.Value(PropLink))
	e.Person = UnescapeText(c.Value(PropPerson))
	e.RelatedTo = c.Value("RELATED-TO")
	if p := c.Value("PRIORITY"); p != "" {
		e.Priority, _ = strconv.Atoi(p)
	}
	for _, prop := range c.All("CATEGORIES") {
		for _, cat := range strings.Split(prop.Value, ",") {
			if cat = strings.TrimSpace(UnescapeText(cat)); cat != "" {
				e.Categories = append(e.Categories, cat)
			}
		}
	}
	if v := c.Value("COMPLETED"); v != "" {
		if t, _, err := parseTimeValue(v, "", false); err == nil {
			e.Completed = t
		}
	}
	if e.Link == "" {
		if u := c.Value("URL"); strings.HasPrefix(u, "project:") {
			e.Link = u
		}
	}

	// A deadline is a point in time. DUE is where it lives; a VTODO that only
	// carries DTSTART is still read rather than thrown away.
	if e.IsTodo {
		due := c.Get("DUE")
		if due == nil {
			due = c.Get("DTSTART")
		}
		if due == nil {
			return e, fmt.Errorf("todo %q has neither DUE nor DTSTART", e.UID)
		}
		t, allDay, err := ParseTimeProp(due)
		if err != nil {
			return e, err
		}
		e.Start, e.End, e.AllDay = t, t, allDay
		e.TZID = due.Param("TZID")
		if r := c.Get("RRULE"); r != nil {
			e.RRule = r.Value
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

// ToComponent writes the entry back, creating the component if it is new.
//
// Changing a slot into a deadline changes the component itself — VEVENT
// becomes VTODO. It is edited in place, because the calendar file holds a
// pointer to it.
func (e *Event) ToComponent() *Component {
	want := "VEVENT"
	if e.IsTodo {
		want = "VTODO"
	}
	c := e.Comp
	if c == nil {
		c = NewComponent(want)
		e.Comp = c
	} else if !strings.EqualFold(c.Name, want) {
		c.Name = want
		// The properties of the old shape would be nonsense in the new one.
		for _, name := range []string{"DTSTART", "DTEND", "DURATION", "DUE", "COMPLETED", "PERCENT-COMPLETE"} {
			c.Remove(name)
		}
	}
	c.Set("UID", e.UID)
	c.Set("DTSTAMP", FormatUTC(time.Now()))
	c.Set("SUMMARY", EscapeText(e.Summary))

	setOrRemove(c, "DESCRIPTION", EscapeText(e.Description))
	setOrRemove(c, "LOCATION", EscapeText(e.Location))
	setOrRemove(c, "COLOR", e.Color)
	setOrRemove(c, "STATUS", e.Status)
	setOrRemove(c, "RELATED-TO", e.RelatedTo)
	setOrRemove(c, PropAttachedTo, e.AttachedTo)
	setOrRemove(c, PropLink, EscapeText(e.Link))
	setOrRemove(c, PropPerson, EscapeText(e.Person))

	// The kind is only written when it cannot be worked out from the entry
	// itself — a slot with a time and an all-day with a date say what they are.
	kind := e.EffectiveKind()
	if kind == KindPhase || kind == KindMilestone {
		c.Set(PropKind, kind)
	} else {
		c.Remove(PropKind)
	}

	c.Remove("CATEGORIES")
	if len(e.Categories) > 0 {
		escaped := make([]string, 0, len(e.Categories))
		for _, cat := range e.Categories {
			if cat = strings.TrimSpace(cat); cat != "" {
				escaped = append(escaped, EscapeText(cat))
			}
		}
		if len(escaped) > 0 {
			c.Set("CATEGORIES", strings.Join(escaped, ","))
		}
	}

	if e.Priority > 0 && e.Priority <= 9 {
		c.Set("PRIORITY", strconv.Itoa(e.Priority))
	} else {
		c.Remove("PRIORITY")
	}

	if e.IsTodo {
		e.writeTodoTimes(c)
		e.writeTail(c)
		return c
	}

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
	e.writeTail(c)
	return c
}

// writeTodoTimes writes what a deadline consists of: when it is due, and
// whether it has been done.
func (e *Event) writeTodoTimes(c *Component) {
	c.Remove("DTSTART")
	c.Remove("DTEND")
	c.Remove("DURATION")
	c.Set("DUE", "")
	due := c.Get("DUE")
	due.Params = nil
	if e.AllDay {
		due.Value = e.Start.Format("20060102")
		due.SetParam("VALUE", "DATE")
	} else {
		due.Value = FormatUTC(e.Start)
	}

	if e.Done() {
		c.Set("STATUS", "COMPLETED")
		c.Set("PERCENT-COMPLETE", "100")
		done := e.Completed
		if done.IsZero() {
			done = time.Now()
		}
		c.Set("COMPLETED", FormatUTC(done))
	} else {
		c.Remove("COMPLETED")
		c.Remove("PERCENT-COMPLETE")
		if c.Value("STATUS") == "COMPLETED" {
			c.Remove("STATUS")
		}
	}
}

// writeTail is everything both shapes share: repetition, bookkeeping, alarms.
func (e *Event) writeTail(c *Component) {
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
}

// AsEvent turns a deadline into a short VEVENT. Google Calendar ignores VTODO
// on a subscribed feed, so a phone would simply not show it — and you would
// find that out at the wrong moment. The conversion happens on the way out
// only; what lies in the file stays a todo.
func (e Event) AsEvent() *Component {
	c := NewComponent("VEVENT")
	c.Set("UID", e.UID)
	c.Set("DTSTAMP", FormatUTC(time.Now()))
	summary := e.Summary
	if e.Done() {
		summary = "✓ " + summary
	}
	c.Set("SUMMARY", EscapeText(summary))
	setOrRemove(c, "DESCRIPTION", EscapeText(e.Description))
	setOrRemove(c, "LOCATION", EscapeText(e.Location))
	setOrRemove(c, "COLOR", e.Color)
	if e.AllDay {
		start := c.Set("DTSTART", e.Start.Format("20060102"))
		start.SetParam("VALUE", "DATE")
		end := c.Set("DTEND", e.Start.AddDate(0, 0, 1).Format("20060102"))
		end.SetParam("VALUE", "DATE")
	} else {
		c.Set("DTSTART", FormatUTC(e.Start))
		// Fifteen minutes: long enough to be visible in a grid, short enough
		// not to pretend the deadline lasts.
		c.Set("DTEND", FormatUTC(e.Start.Add(15*time.Minute)))
	}
	if e.RRule != "" {
		c.Set("RRULE", strings.TrimPrefix(strings.ToUpper(e.RRule), "RRULE:"))
	}
	c.Set(PropKind, KindDeadline)
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

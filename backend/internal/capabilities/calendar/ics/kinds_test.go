package ics

import (
	"strings"
	"testing"
	"time"
)

// A deadline written by someone else's client has to survive the trip: read as
// a todo, written back as a todo, with DUE where DUE belongs.
const todoSample = "BEGIN:VCALENDAR\r\n" +
	"VERSION:2.0\r\n" +
	"PRODID:-//Test//EN\r\n" +
	"BEGIN:VTODO\r\n" +
	"UID:deadline-1\r\n" +
	"DTSTAMP:20260101T120000Z\r\n" +
	"DUE:20260320T225900Z\r\n" +
	"SUMMARY:Hand in the assignment\r\n" +
	"PRIORITY:1\r\n" +
	"CATEGORIES:Studium,Abgabe\r\n" +
	"RELATED-TO:phase-semester-3\r\n" +
	"X-KEEP-ME:yes\r\n" +
	"END:VTODO\r\n" +
	"END:VCALENDAR\r\n"

func TestTodoRoundTrip(t *testing.T) {
	cal, err := ParseCalendar([]byte(todoSample))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	todos := cal.Kids("VTODO")
	if len(todos) != 1 {
		t.Fatalf("expected 1 todo, got %d", len(todos))
	}
	e, err := FromComponent(todos[0])
	if err != nil {
		t.Fatalf("todo: %v", err)
	}
	if !e.IsTodo || e.EffectiveKind() != KindDeadline {
		t.Fatalf("a VTODO is a deadline, got kind %q todo=%v", e.EffectiveKind(), e.IsTodo)
	}
	if want := time.Date(2026, 3, 20, 22, 59, 0, 0, time.UTC); !e.Start.Equal(want) {
		t.Errorf("due = %v, want %v", e.Start, want)
	}
	if e.Priority != 1 || e.RelatedTo != "phase-semester-3" {
		t.Errorf("priority/related lost: %d %q", e.Priority, e.RelatedTo)
	}
	if len(e.Categories) != 2 || e.Categories[0] != "Studium" {
		t.Errorf("categories lost: %v", e.Categories)
	}
	if e.Done() {
		t.Error("nothing said COMPLETED")
	}

	// Ticking it off must not turn it into something else.
	e.Status = "COMPLETED"
	e.Completed = time.Date(2026, 3, 19, 10, 0, 0, 0, time.UTC)
	out := string(e.ToComponent().Bytes())
	for _, want := range []string{
		"BEGIN:VTODO", "DUE:20260320T225900Z", "STATUS:COMPLETED",
		"COMPLETED:20260319T100000Z", "PERCENT-COMPLETE:100",
		"PRIORITY:1", "CATEGORIES:Studium,Abgabe", "X-KEEP-ME:yes",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("written todo is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "DTEND") {
		t.Errorf("a deadline has no end:\n%s", out)
	}
}

// Google Calendar ignores VTODO on a feed, so the export can hand deadlines
// out as short events — without the file changing.
func TestDeadlineAsEvent(t *testing.T) {
	cal, _ := ParseCalendar([]byte(todoSample))
	e, err := FromComponent(cal.Kids("VTODO")[0])
	if err != nil {
		t.Fatalf("todo: %v", err)
	}
	out := string(e.AsEvent().Bytes())
	for _, want := range []string{
		"BEGIN:VEVENT", "DTSTART:20260320T225900Z", "DTEND:20260320T231400Z",
		"X-HOME-KIND:deadline",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("converted deadline is missing %q:\n%s", want, out)
		}
	}
	// The original is untouched.
	if !strings.EqualFold(e.Comp.Name, "VTODO") {
		t.Error("the conversion changed the stored component")
	}
}

// A phase and a milestone are ordinary events carrying one custom property —
// so a foreign client still shows a correct calendar.
func TestPhaseAndMilestone(t *testing.T) {
	phase := Event{
		UID:     "phase-1",
		Summary: "Semester 3",
		Start:   time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		End:     time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		AllDay:  true,
		Kind:    KindPhase,
	}
	out := string(phase.ToComponent().Bytes())
	if !strings.Contains(out, "BEGIN:VEVENT") || !strings.Contains(out, "X-HOME-KIND:phase") {
		t.Errorf("phase is not a marked event:\n%s", out)
	}
	if !strings.Contains(out, "DTSTART;VALUE=DATE:20260301") {
		t.Errorf("a phase runs in whole days:\n%s", out)
	}

	// Read back it is still a phase.
	cal, err := ParseCalendar([]byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\n" + out + "END:VCALENDAR\r\n"))
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	back, err := FromComponent(cal.Kids("VEVENT")[0])
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if back.EffectiveKind() != KindPhase {
		t.Errorf("kind lost on the way back: %q", back.EffectiveKind())
	}
}

// An ordinary appointment must not gain properties it never had. Whatever a
// foreign client wrote stays; nothing of ours is added.
func TestPlainEventStaysPlain(t *testing.T) {
	cal, _ := ParseCalendar([]byte(sample))
	e, err := FromComponent(cal.Kids("VEVENT")[0])
	if err != nil {
		t.Fatalf("event: %v", err)
	}
	if e.EffectiveKind() != KindSlot {
		t.Errorf("a timed event is a slot, got %q", e.EffectiveKind())
	}
	out := string(e.ToComponent().Bytes())
	for _, unwanted := range []string{"X-HOME-KIND", "PRIORITY", "CATEGORIES", "DUE", "COMPLETED"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("a plain event should not carry %s:\n%s", unwanted, out)
		}
	}
	if !strings.Contains(out, "X-CUSTOM-THING:keep me") {
		t.Errorf("a foreign property was dropped:\n%s", out)
	}
}

// Changing a slot into a deadline changes the component in place — the file
// holds a pointer to it, so a copy would be written into nothing.
func TestSlotBecomesDeadline(t *testing.T) {
	cal, _ := ParseCalendar([]byte(sample))
	comp := cal.Kids("VEVENT")[0]
	e, err := FromComponent(comp)
	if err != nil {
		t.Fatalf("event: %v", err)
	}
	e.Kind, e.IsTodo = KindDeadline, true
	e.End = e.Start
	got := e.ToComponent()
	if got != comp {
		t.Fatal("the component was replaced instead of changed")
	}
	out := string(cal.Bytes())
	if !strings.Contains(out, "BEGIN:VTODO") || strings.Contains(out, "BEGIN:VEVENT") {
		t.Errorf("the calendar still holds the old shape:\n%s", out)
	}
	if strings.Contains(out, "DTEND") || strings.Contains(out, "DTSTART") {
		t.Errorf("a todo has DUE, not DTSTART/DTEND:\n%s", out)
	}
	if !strings.Contains(out, "DUE:") {
		t.Errorf("the due date is missing:\n%s", out)
	}
}

package ics

import (
	"strings"
	"testing"
	"time"
)

const sample = "BEGIN:VCALENDAR\r\n" +
	"VERSION:2.0\r\n" +
	"PRODID:-//Test//EN\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:abc-123\r\n" +
	"DTSTAMP:20250101T120000Z\r\n" +
	"DTSTART;TZID=Europe/Berlin:20250106T090000\r\n" +
	"DTEND;TZID=Europe/Berlin:20250106T103000\r\n" +
	"SUMMARY:Lecture\\, room 3\r\n" +
	"DESCRIPTION:First line\\nSecond line\r\n" +
	"RRULE:FREQ=WEEKLY;COUNT=4\r\n" +
	"X-CUSTOM-THING:keep me\r\n" +
	"BEGIN:VALARM\r\n" +
	"ACTION:DISPLAY\r\n" +
	"TRIGGER:-PT15M\r\n" +
	"DESCRIPTION:Lecture\r\n" +
	"END:VALARM\r\n" +
	"END:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func TestParseAndRoundTrip(t *testing.T) {
	cal, err := ParseCalendar([]byte(sample))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	events := cal.Kids("VEVENT")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	e, err := FromComponent(events[0])
	if err != nil {
		t.Fatalf("event: %v", err)
	}
	if e.UID != "abc-123" {
		t.Errorf("uid = %q", e.UID)
	}
	if e.Summary != "Lecture, room 3" {
		t.Errorf("summary = %q — comma escaping is wrong", e.Summary)
	}
	if e.Description != "First line\nSecond line" {
		t.Errorf("description = %q — newline escaping is wrong", e.Description)
	}
	if e.AllDay {
		t.Error("event should not be all-day")
	}
	// 09:00 Berlin in January is 08:00 UTC.
	if got := e.Start.UTC().Format(time.RFC3339); got != "2025-01-06T08:00:00Z" {
		t.Errorf("start = %s", got)
	}
	if len(e.Alarms) != 1 || e.Alarms[0] != 15 {
		t.Errorf("alarms = %v", e.Alarms)
	}

	// Unknown properties survive a write.
	out := string(cal.Bytes())
	if !strings.Contains(out, "X-CUSTOM-THING:keep me") {
		t.Error("an unknown property was dropped — parsing is not lossless")
	}
	if !strings.Contains(out, "\r\n") {
		t.Error("output must use CRLF")
	}
}

func TestEditKeepsUnknownProperties(t *testing.T) {
	cal, _ := ParseCalendar([]byte(sample))
	e, _ := FromComponent(cal.Kids("VEVENT")[0])
	e.Summary = "Moved lecture"
	e.Start = time.Date(2025, 1, 7, 9, 0, 0, 0, time.UTC)
	e.End = time.Date(2025, 1, 7, 10, 0, 0, 0, time.UTC)
	e.ToComponent()

	out := string(cal.Bytes())
	if !strings.Contains(out, "X-CUSTOM-THING:keep me") {
		t.Error("editing dropped an unknown property")
	}
	if !strings.Contains(out, "SUMMARY:Moved lecture") {
		t.Errorf("summary not written:\n%s", out)
	}
	if !strings.Contains(out, "DTSTART:20250107T090000Z") {
		t.Errorf("start not written:\n%s", out)
	}
}

func TestFoldingRoundTrip(t *testing.T) {
	long := strings.Repeat("a very long summary ", 12)
	cal := NewCalendar("Test")
	e := Event{
		UID:     "long-1",
		Summary: long,
		Start:   time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC),
		End:     time.Date(2025, 3, 1, 11, 0, 0, 0, time.UTC),
	}
	cal.Children = append(cal.Children, e.ToComponent())

	reparsed, err := ParseCalendar(cal.Bytes())
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	got, err := FromComponent(reparsed.Kids("VEVENT")[0])
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != long {
		t.Errorf("folding broke the value:\n%q\n%q", got.Summary, long)
	}
}

func TestAllDay(t *testing.T) {
	data := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:d1\r\n" +
		"DTSTART;VALUE=DATE:20250401\r\nDTEND;VALUE=DATE:20250402\r\nSUMMARY:Holiday\r\n" +
		"END:VEVENT\r\nEND:VCALENDAR\r\n"
	cal, err := ParseCalendar([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	e, err := FromComponent(cal.Kids("VEVENT")[0])
	if err != nil {
		t.Fatal(err)
	}
	if !e.AllDay {
		t.Fatal("expected an all-day event")
	}
	if e.Start.Format("2006-01-02") != "2025-04-01" {
		t.Errorf("start = %s", e.Start)
	}
	e.ToComponent()
	if !strings.Contains(string(cal.Bytes()), "DTSTART;VALUE=DATE:20250401") {
		t.Errorf("all-day start not written back:\n%s", cal.Bytes())
	}
}

func TestExpandWeekly(t *testing.T) {
	cal, _ := ParseCalendar([]byte(sample))
	e, err := FromComponent(cal.Kids("VEVENT")[0])
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	occ, err := Expand(e, nil, from, to)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(occ) != 4 {
		t.Fatalf("expected 4 occurrences, got %d", len(occ))
	}
	if occ[1].Start.Sub(occ[0].Start) != 7*24*time.Hour {
		t.Errorf("occurrences are not a week apart: %v", occ[1].Start.Sub(occ[0].Start))
	}
	if occ[0].End.Sub(occ[0].Start) != 90*time.Minute {
		t.Errorf("duration lost: %v", occ[0].End.Sub(occ[0].Start))
	}
}

func TestExpandRespectsExdateAndOverride(t *testing.T) {
	cal, _ := ParseCalendar([]byte(sample))
	e, _ := FromComponent(cal.Kids("VEVENT")[0])
	// drop the second appearance
	e.ExDates = []time.Time{e.Start.Add(7 * 24 * time.Hour)}

	overrideStart := e.Start.Add(14 * 24 * time.Hour)
	moved := overrideStart.Add(2 * time.Hour)
	rid := overrideStart
	override := Event{
		UID:          e.UID,
		Start:        moved,
		End:          moved.Add(time.Hour),
		RecurrenceID: &rid,
	}

	occ, err := Expand(e, []Event{override}, e.Start.Add(-time.Hour), e.Start.Add(60*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(occ) != 3 {
		t.Fatalf("expected 3 occurrences after the exception, got %d", len(occ))
	}
	if !occ[1].IsException || !occ[1].Start.Equal(moved) {
		t.Errorf("the single moved appearance was not applied: %+v", occ[1])
	}
}

func TestParseDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"PT15M":   15 * time.Minute,
		"-PT15M":  -15 * time.Minute,
		"P1DT2H":  26 * time.Hour,
		"P2W":     14 * 24 * time.Hour,
		"PT1H30M": 90 * time.Minute,
	}
	for in, want := range cases {
		got, err := ParseDuration(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%s = %v, want %v", in, got, want)
		}
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := ParseCalendar([]byte("just some text\n")); err == nil {
		t.Error("expected an error for a file that is not iCalendar")
	}
}

package moodle

import (
	"testing"

	lib "github.com/OfflineBot/nicht-libs/moodle"
)

// The rules are typed by a person into a text box, so they have to survive
// being typed by a person.
func TestParseRoutes(t *testing.T) {
	routes, bad := parseRoutes(`
# where the semesters go
2 -> semester-2
Semester3: semester-3
WDS125 -> wissenschaftliches-arbeiten
this line has no arrow
* -> moodle-archiv
`)
	if len(routes) != 4 {
		t.Fatalf("expected 4 rules, got %d: %+v", len(routes), routes)
	}
	if len(bad) != 1 || bad[0] != "this line has no arrow" {
		t.Errorf("the unusable line was not reported: %v", bad)
	}
	if routes[0].semester != 2 || routes[0].project != "semester-2" {
		t.Errorf("a bare number is a semester: %+v", routes[0])
	}
	if routes[1].semester != 3 {
		t.Errorf("\"Semester3\" is a semester too: %+v", routes[1])
	}
	if routes[2].text != "wds125" {
		t.Errorf("anything else matches the name: %+v", routes[2])
	}
	if !routes[3].all {
		t.Errorf("* is the catch-all: %+v", routes[3])
	}
}

func TestPick(t *testing.T) {
	routes, _ := parseRoutes("2 -> semester-2\n3 -> semester-3\nArbeitssicherheit -> pflicht\n* -> archiv")

	cases := []struct {
		course lib.Course
		want   string
	}{
		{lib.Course{Shortname: "WDS125", SemesterNumber: 2}, "semester-2"},
		{lib.Course{Shortname: "WDS134", SemesterNumber: 3}, "semester-3"},
		{lib.Course{Shortname: "RV-AS", Fullname: "Einführung in die Arbeitssicherheit"}, "pflicht"},
		{lib.Course{Shortname: "QM", SemesterNumber: 6}, "archiv"},
	}
	for _, c := range cases {
		got, ok := pick(routes, c.course)
		if !ok || got != c.want {
			t.Errorf("%s → %q (matched=%v), want %q", c.course.Shortname, got, ok, c.want)
		}
	}

	// Without a catch-all, a course that matches nothing is left alone rather
	// than dumped somewhere.
	narrow, _ := parseRoutes("2 -> semester-2")
	if _, ok := pick(narrow, lib.Course{Shortname: "QM", SemesterNumber: 6}); ok {
		t.Error("a course with no rule must not be routed anywhere")
	}
}

// A section called "Woche 1 / Einführung" must not become two folders, and a
// name that is only dots must not escape the course folder.
func TestFolderName(t *testing.T) {
	cases := map[string]string{
		"Woche 1 / Einführung":  "Woche 1 - Einführung",
		"  Organisatorisches  ": "Organisatorisches",
		"..":                    "",
		"":                      "",
		"C:\\Kurs":              "C--Kurs",
	}
	for in, want := range cases {
		if got := folderName(in); got != want {
			t.Errorf("folderName(%q) = %q, want %q", in, got, want)
		}
	}
}

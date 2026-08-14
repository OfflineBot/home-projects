package filter

import (
	"strings"
	"testing"
)

// The rules are typed by a person, so they have to survive being typed by a
// person: a piece of a name, a number, a folder, a catch-all.
func TestApply(t *testing.T) {
	rules, bad := ParseText(`
# where things go
2 -> {G/semester2}
Grundlagen In -> {G/semester1}
Übung -> /uebungen
Alt -> {G/archiv}/2024
Werbung -> skip
this line is not a rule
* -> {G/rest}
`)
	if len(bad) != 1 {
		t.Fatalf("the unusable line was not reported: %v", bad)
	}
	if len(rules) != 6 {
		t.Fatalf("expected 6 rules, got %d", len(rules))
	}

	cases := []struct {
		item Item
		want Destination
	}{
		{Item{Name: "WDS125 - Grundlagen Informatik (INA)"}, Destination{Project: "G/semester1"}},
		{Item{Name: "grundlagen-informatik-betriebssysteme"}, Destination{Project: "G/semester1"}},
		{Item{Name: "Anything", Semester: 2}, Destination{Project: "G/semester2"}},
		{Item{Name: "Übung 3.pdf"}, Destination{Folder: "uebungen"}},
		{Item{Name: "Altes Skript"}, Destination{Project: "G/archiv", Folder: "2024"}},
		{Item{Name: "Werbung.pdf"}, Destination{Skip: true}},
		{Item{Name: "something else"}, Destination{Project: "G/rest"}},
	}
	for _, c := range cases {
		got, ok := Apply(rules, c.item)
		if !ok {
			t.Errorf("%q matched nothing", c.item.Name)
			continue
		}
		if got.Project != c.want.Project || got.Folder != c.want.Folder || got.Skip != c.want.Skip {
			t.Errorf("%q → %+v, want %+v", c.item.Name, got, c.want)
		}
	}
}

// Without a catch-all, something that matches nothing is left alone rather than
// swept somewhere. The caller decides what "no rule" means.
func TestNoRuleIsNotADestination(t *testing.T) {
	rules, _ := ParseText("2 -> {G/semester2}")
	if _, ok := Apply(rules, Item{Name: "Wissenschaftliches Arbeiten", Semester: 6}); ok {
		t.Error("a filter with nothing to say must say nothing")
	}
}

// Order is the whole logic: the first line that matches wins, even when a later
// one is more specific.
func TestFirstMatchWins(t *testing.T) {
	rules, _ := ParseText("* -> {G/everything}\nGrundlagen -> {G/semester1}")
	got, _ := Apply(rules, Item{Name: "Grundlagen Analysis"})
	if got.Project != "G/everything" {
		t.Errorf("got %q, want the first rule to win", got.Project)
	}
}

func TestTextRoundTrip(t *testing.T) {
	rules, _ := ParseText("2 -> {G/semester2}\nÜbung -> /uebungen")
	again, bad := ParseText(Text(rules))
	if len(bad) != 0 || len(again) != 2 || again[1].To != "/uebungen" {
		t.Errorf("rules did not survive being written back out: %+v %v", again, bad)
	}
}

// The three shapes a match can take, and the one that decides between them.
func TestPatterns(t *testing.T) {
	items := []Item{
		{Name: "Grundlagen Analysis"},
		{Name: "Grundlagen Informatik"},
		{Name: "Fortgeschrittene Informatik"},
		{Name: "Skript.pdf"},
		{Name: "WDS125 - Grundlagen Statistik"},
	}
	cases := []struct {
		rule string
		want []string
	}{
		{"Grundlagen* -> {G/p}", []string{"Grundlagen Analysis", "Grundlagen Informatik"}},
		{"*.pdf -> {G/p}", []string{"Skript.pdf"}},
		{"*Informatik* -> {G/p}", []string{"Grundlagen Informatik", "Fortgeschrittene Informatik"}},
		{`/^WDS\d+ - Grundlagen/ -> {G/p}`, []string{"WDS125 - Grundlagen Statistik"}},
		{"first Grundlagen* -> {G/p}", []string{"Grundlagen Analysis"}},
		{"last Grundlagen* -> {G/p}", []string{"Grundlagen Informatik"}},
	}
	for _, c := range cases {
		rules, bad := ParseText(c.rule)
		if len(bad) > 0 {
			t.Errorf("%q was not understood: %v", c.rule, bad)
			continue
		}
		var got []string
		for i, d := range Plan(rules, items) {
			if d.Matched {
				got = append(got, items[i].Name)
			}
		}
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%q matched %v, want %v", c.rule, got, c.want)
		}
	}
}

// A project is written in braces so it can never be read as a folder.
func TestDestinations(t *testing.T) {
	cases := map[string][3]string{
		"{Studies/semester1}":         {"Studies/semester1", "", ""},
		"{Studies/semester1}/skripte": {"Studies/semester1", "skripte", ""},
		"./skripte":                   {"", "skripte", ""},
		"/skripte":                    {"", "skripte", ""},
		"skip":                        {"", "", "skip"},
	}
	for in, want := range cases {
		project, folder, skip := Destinations(in)
		got := [3]string{project, folder, ""}
		if skip {
			got[2] = "skip"
		}
		if got != want {
			t.Errorf("%q → %v, want %v", in, got, want)
		}
	}
}

// A regular expression that does not compile is a line to fix, not a rule that
// quietly never matches.
func TestBrokenRegexIsReported(t *testing.T) {
	rules, bad := ParseText("/[unclosed/ -> {G/p}")
	if len(rules) != 0 || len(bad) != 1 {
		t.Fatalf("rules=%v bad=%v", rules, bad)
	}
	if !strings.Contains(bad[0], "error parsing regexp") {
		t.Errorf("the reason is not in the report: %q", bad[0])
	}
}

// Order is the whole logic: what an earlier rule took, a later one cannot.
func TestClaimedOnce(t *testing.T) {
	rules, _ := ParseText("first Grundlagen* -> {G/first}\nGrundlagen* -> {G/rest}")
	items := []Item{{Name: "Grundlagen A"}, {Name: "Grundlagen B"}, {Name: "Grundlagen C"}}
	plan := Plan(rules, items)
	if plan[0].Project != "G/first" {
		t.Errorf("the first went to %q", plan[0].Project)
	}
	if plan[1].Project != "G/rest" || plan[2].Project != "G/rest" {
		t.Errorf("the rest went to %q and %q", plan[1].Project, plan[2].Project)
	}
}

// A list is the answer to "these three, into that project" — a sentence people
// say, and one rule per line is not.
func TestList(t *testing.T) {
	rules, bad := ParseText("[alpha, beta gamma, delta*] -> {G/p}")
	if len(bad) != 0 || len(rules) != 1 {
		t.Fatalf("rules=%v bad=%v", rules, bad)
	}
	items := []Item{
		{Name: "alpha"},
		{Name: "beta gamma"},
		{Name: "delta something"},
		{Name: "epsilon"},
	}
	var got []string
	for i, d := range Plan(rules, items) {
		if d.Matched {
			got = append(got, items[i].Name)
		}
	}
	if strings.Join(got, "|") != "alpha|beta gamma|delta something" {
		t.Errorf("matched %v", got)
	}

	// And it survives being written back out as one line.
	again, _ := ParseText(Text(rules))
	if len(again) != 1 || again[0].Match != "[alpha, beta gamma, delta*]" {
		t.Errorf("the list did not survive: %+v", again)
	}
}

// Where a line sends things, in the forms people actually write.
//
// "moodle/<the rest>" was written meaning "a folder called moodle in this
// project" and read as "a project called moodle" — so every course was sent
// somewhere that did not exist and quietly dropped. Both readings are fair;
// what matters is that the reading is visible and that the run says which one
// it took.
func TestWhereALineSendsThings(t *testing.T) {
	cases := []struct {
		to      string
		project string
		folder  string
		skip    bool
	}{
		{"skip", "", "", true},
		{"{dhbw/semester1}", "dhbw/semester1", "", false},
		{"{dhbw/semester1}/vorlesungen", "dhbw/semester1", "vorlesungen", false},
		{"./moodle", "", "moodle", false},
		{"/moodle", "", "moodle", false},
		{"./moodle/kurse", "", "moodle/kurse", false},
		// Without a leading ./ the first word is a project — that is the rule,
		// and the run says so out loud when no such project is there.
		{"moodle/kurse", "moodle", "kurse", false},
	}
	for _, c := range cases {
		project, folder, skip := Destinations(c.to)
		if project != c.project || folder != c.folder || skip != c.skip {
			t.Errorf("%q → project=%q folder=%q skip=%v, wanted project=%q folder=%q skip=%v",
				c.to, project, folder, skip, c.project, c.folder, c.skip)
		}
	}
}

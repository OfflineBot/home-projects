package filter

import "testing"

// The rules are typed by a person, so they have to survive being typed by a
// person: a piece of a name, a number, a folder, a catch-all.
func TestApply(t *testing.T) {
	rules, bad := ParseText(`
# where things go
2 -> semester2
Grundlagen In -> semester1
Übung -> /uebungen
Alt -> archiv/2024
Werbung -> skip
this line is not a rule
* -> rest
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
		{Item{Name: "WDS125 - Grundlagen Informatik (INA)"}, Destination{Project: "semester1"}},
		{Item{Name: "grundlagen-informatik-betriebssysteme"}, Destination{Project: "semester1"}},
		{Item{Name: "Anything", Semester: 2}, Destination{Project: "semester2"}},
		{Item{Name: "Übung 3.pdf"}, Destination{Folder: "uebungen"}},
		{Item{Name: "Altes Skript"}, Destination{Project: "archiv", Folder: "2024"}},
		{Item{Name: "Werbung.pdf"}, Destination{Skip: true}},
		{Item{Name: "something else"}, Destination{Project: "rest"}},
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
	rules, _ := ParseText("2 -> semester2")
	if _, ok := Apply(rules, Item{Name: "Wissenschaftliches Arbeiten", Semester: 6}); ok {
		t.Error("a filter with nothing to say must say nothing")
	}
}

// Order is the whole logic: the first line that matches wins, even when a later
// one is more specific.
func TestFirstMatchWins(t *testing.T) {
	rules, _ := ParseText("* -> everything\nGrundlagen -> semester1")
	got, _ := Apply(rules, Item{Name: "Grundlagen Analysis"})
	if got.Project != "everything" {
		t.Errorf("got %q, want the first rule to win", got.Project)
	}
}

func TestTextRoundTrip(t *testing.T) {
	rules, _ := ParseText("2 -> semester2\nÜbung -> /uebungen")
	again, bad := ParseText(Text(rules))
	if len(bad) != 0 || len(again) != 2 || again[1].To != "/uebungen" {
		t.Errorf("rules did not survive being written back out: %+v %v", again, bad)
	}
}

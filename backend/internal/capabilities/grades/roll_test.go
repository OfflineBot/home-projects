package grades

import (
	"math"
	"testing"
)

// Three exams that are one subject: each has its own grade, and the subject has
// the one they add up to. Counting them as three would put the average out.
func TestPartsBecomeOneModule(t *testing.T) {
	rolled := Roll([]Module{
		{ID: "W4DSKI_104", Name: "Grundlagen Informatik", Credits: 8, Semester: "WiSe 2025/2026"},
		{Name: "Betriebssysteme", Grade: 2.0, Credits: 3, PartOf: "W4DSKI_104", Semester: "WiSe 2025/2026"},
		{Name: "Kommunikationssysteme", Grade: 3.0, Credits: 3, PartOf: "W4DSKI_104", Semester: "WiSe 2025/2026"},
		{Name: "Rechnerarchitektur", Grade: 1.0, Credits: 2, PartOf: "Grundlagen Informatik", Semester: "WiSe 2025/2026"},
		{Name: "Analysis", Grade: 2.5, Credits: 5, Semester: "SoSe 2026"},
	})
	if len(rolled) != 2 {
		t.Fatalf("expected two subjects, got %d", len(rolled))
	}
	parent := rolled[0]
	if len(parent.Parts) != 3 {
		t.Fatalf("the parts did not land under it: %+v", parent.Parts)
	}
	// (2·3 + 3·3 + 1·2) / 8 = 2.125
	if math.Abs(parent.Grade-2.13) > 0.01 || !parent.Computed {
		t.Errorf("worked out %v (computed=%v), want 2.13", parent.Grade, parent.Computed)
	}

	// And the average counts the subject once, not three times.
	avg, credits, counted := Average(Flat(rolled))
	if counted != 2 {
		t.Errorf("counted %d subjects, want 2", counted)
	}
	if math.Abs(credits-13) > 0.01 {
		t.Errorf("credits = %v, want 13", credits)
	}
	// (2.13·8 + 2.5·5) / 13
	if math.Abs(avg-2.27) > 0.02 {
		t.Errorf("average = %v, want about 2.27", avg)
	}
}

// A grade of its own beats what the pieces add up to: the certificate wins.
func TestGivenGradeWins(t *testing.T) {
	rolled := Roll([]Module{
		{ID: "M1", Name: "Modul", Grade: 1.7, Credits: 6},
		{Name: "Teil", Grade: 4.0, Credits: 6, PartOf: "M1"},
	})
	if rolled[0].Grade != 1.7 || rolled[0].Computed {
		t.Errorf("got %v (computed=%v), want the given 1.7", rolled[0].Grade, rolled[0].Computed)
	}
}

// A part that names something that is not there stays a subject of its own —
// better than a grade that quietly disappears.
func TestOrphanPartIsKept(t *testing.T) {
	rolled := Roll([]Module{{Name: "Teil", Grade: 2.0, Credits: 3, PartOf: "nothing here"}})
	if len(rolled) != 1 || rolled[0].Name != "Teil" {
		t.Errorf("the orphan was lost: %+v", rolled)
	}
}

// Semesters come out newest first — the one you are in is the one you look at —
// and anything without a year stays at the bottom.
func TestByTerm(t *testing.T) {
	terms := ByTerm(Roll([]Module{
		{Name: "d", Semester: "SoSe 2026", Grade: 2, Credits: 5},
		{Name: "a", Semester: "WiSe 2024/2025", Grade: 1, Credits: 5},
		{Name: "c", Semester: "WiSe 2025/2026", Grade: 3, Credits: 5},
		{Name: "b", Semester: "SoSe 2025", Grade: 2, Credits: 5},
		{Name: "x", Grade: 1, Credits: 5},
	}))
	var got []string
	for _, t := range terms {
		got = append(got, t.Name)
	}
	want := []string{"SoSe 2026", "WiSe 2025/2026", "SoSe 2025", "WiSe 2024/2025", "Without a semester"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
	if terms[0].Average != 2 || terms[0].Credits != 5 {
		t.Errorf("a term carries its own average: %+v", terms[0])
	}
}

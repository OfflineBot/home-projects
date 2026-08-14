package variables

import (
	"math"
	"strings"
	"testing"
)

func lookup(values map[string]float64) Lookup {
	return func(ref string) (float64, bool) {
		v, ok := values[ref]
		return v, ok
	}
}

func TestEval(t *testing.T) {
	values := map[string]float64{
		"studies/noten/durchschnitt":  2.3,
		"studies/noten/durchschnitt2": 1.7,
		"home/system/online":          1,
	}
	cases := map[string]float64{
		"1 + 2":                        3,
		"2 * 3 + 1":                    7,
		"1 + 2 * 3":                    7,
		"(1 + 2) * 3":                  9,
		"-4 + 10":                      6,
		"{studies/noten/durchschnitt}": 2.3,
		"({studies/noten/durchschnitt} + {studies/noten/durchschnitt2}) / 2": 2.0,
		"{home/system/online} * 100":                                         100,
		"10 / 4":                                                             2.5,
	}
	for expr, want := range cases {
		got, err := Eval(expr, lookup(values))
		if err != nil {
			t.Errorf("%q: %v", expr, err)
			continue
		}
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("%q = %v, want %v", expr, got, want)
		}
	}
}

// An expression that cannot be worked out says why, and names the reference.
// A zero would look like an answer.
func TestEvalSaysWhatIsMissing(t *testing.T) {
	_, err := Eval("{studies/noten/fehlt} + 1", lookup(map[string]float64{}))
	if err == nil || !strings.Contains(err.Error(), "studies/noten/fehlt") {
		t.Fatalf("got %v, want the missing reference named", err)
	}
	for expr, want := range map[string]string{
		"(1 + 2":    "not closed",
		"1 / 0":     "zero",
		"{unclosed": "not closed",
		"1 + ":      "missing",
		"":          "nothing",
		"1 +* 2":    "cannot read",
		"hello":     "cannot read",
	} {
		_, err := Eval(expr, lookup(map[string]float64{}))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q gave %v, want something about %q", expr, err, want)
		}
	}
}

func TestReferences(t *testing.T) {
	refs := References("({a/b/c} + {d/e/f}) / {a/b/c}")
	if len(refs) != 3 || refs[0] != "a/b/c" || refs[1] != "d/e/f" {
		t.Errorf("references = %v", refs)
	}
}

// Package filter is the third thing that lives in a menu of its own, next to
// accounts and schedulers: a named set of rules that answers one question —
// *where does this belong?*
//
// A scheduler asks it about a course. A project asks it about a file. The rules
// do not know which, and that is the point: the same three lines that split a
// Moodle pull across semester projects will sort a folder of downloads, because
// both are "here is a name, tell me where it goes".
//
// The syntax is what a person types, not what a parser prefers:
//
//	2 -> semester2                 a bare number is the semester
//	Grundlagen In -> semester1     a piece of the name is enough
//	Übung -> /uebungen             a folder in the same place
//	Alt -> archiv/2024             another project, and a folder in it
//	Werbung -> skip                leave it where it is
//	* -> rest                      the catch-all
package filter

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/slug"
	"github.com/offlinebot/home-projects/backend/internal/store"
)

// Rule is one line. First match wins, so order is the whole logic.
type Rule struct {
	// Match is what the line said on the left: a piece of a name, a number, or
	// "*". It is kept as typed so the dialog shows it back unchanged.
	Match string `json:"match"`
	// Field narrows what is compared: "name" (the default), "path", or
	// "semester". A bare number implies "semester".
	Field string `json:"field,omitempty"`
	// To is the destination: "project", "project/folder", "/folder" for the
	// same place, or "skip".
	To string `json:"to"`
}

// Item is the thing being placed. A course fills Name and Semester; a file
// fills Name and Path. Nothing has to fill everything.
type Item struct {
	Name     string
	Path     string
	Semester int
}

// Destination is the answer. Project empty means "where it already is".
type Destination struct {
	Project string
	Folder  string
	Skip    bool
	// Rule is the line that decided, for the log and the preview.
	Rule string
}

// Apply returns where an item belongs, and whether any rule claimed it. A rule
// that claims nothing is not an error — it means the filter has nothing to say,
// and the caller decides what that means.
func Apply(rules []Rule, item Item) (Destination, bool) {
	for _, r := range rules {
		if !r.matches(item) {
			continue
		}
		d := Destination{Rule: r.Match + " -> " + r.To}
		to := strings.TrimSpace(r.To)
		if strings.EqualFold(to, "skip") {
			d.Skip = true
			return d, true
		}
		if strings.HasPrefix(to, "/") {
			d.Folder = strings.Trim(to, "/")
			return d, true
		}
		project, folder, _ := strings.Cut(to, "/")
		d.Project = strings.TrimSpace(project)
		d.Folder = strings.Trim(folder, "/")
		return d, true
	}
	return Destination{}, false
}

func (r Rule) matches(item Item) bool {
	if strings.TrimSpace(r.Match) == "*" {
		return true
	}
	field := strings.ToLower(strings.TrimSpace(r.Field))
	needle := strings.ToLower(strings.TrimSpace(r.Match))

	if n, err := strconv.Atoi(strings.TrimPrefix(needle, "semester")); err == nil && field != "name" && field != "path" {
		return item.Semester == n
	}
	switch field {
	case "path":
		return contains(item.Path, needle)
	case "semester":
		n, err := strconv.Atoi(needle)
		return err == nil && item.Semester == n
	default:
		return contains(item.Name, needle) || contains(item.Path, needle)
	}
}

// contains is deliberately forgiving: "Grundlagen In" finds "Grundlagen
// Informatik", and so does "grundlagen-informatik" pasted out of a file tree.
// Being strict here only produces rules that look right and do nothing.
func contains(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	h := strings.ToLower(haystack)
	if strings.Contains(h, needle) {
		return true
	}
	return strings.Contains(slug.Make(h), slug.Make(needle))
}

// ParseText reads the text form. It returns the rules it understood and the
// lines it did not, because a line silently dropped is a rule someone thinks
// is working.
func ParseText(text string) ([]Rule, []string) {
	var rules []Rule
	var bad []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		left, right, ok := strings.Cut(line, "->")
		if !ok {
			left, right, ok = strings.Cut(line, ":")
		}
		if !ok {
			bad = append(bad, line)
			continue
		}
		left, right = strings.TrimSpace(left), strings.TrimSpace(right)
		if left == "" || right == "" {
			bad = append(bad, line)
			continue
		}
		rules = append(rules, Rule{Match: left, To: right})
	}
	return rules, bad
}

// Text writes the rules back out the way they were typed.
func Text(rules []Rule) string {
	var b strings.Builder
	for _, r := range rules {
		b.WriteString(r.Match)
		b.WriteString(" -> ")
		b.WriteString(r.To)
		b.WriteString("\n")
	}
	return b.String()
}

// RulesFor loads a filter by id or slug. It lives here rather than in the store
// so both the server's wiring and the API ask the same question the same way.
func RulesFor(ctx context.Context, st *store.Store, ref string) ([]Rule, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil
	}
	var f *model.Filter
	var err error
	if id, perr := uuid.Parse(ref); perr == nil {
		f, err = st.FilterByID(ctx, id)
	} else {
		f, err = st.FilterBySlug(ctx, slug.Make(ref))
	}
	if err != nil {
		return nil, err
	}
	var rules []Rule
	if err := json.Unmarshal(f.Rules, &rules); err != nil {
		return nil, err
	}
	return rules, nil
}

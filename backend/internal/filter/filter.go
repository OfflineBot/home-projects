// Package filter is the third thing that lives in a menu of its own, next to
// accounts and schedulers: a named set of rules that answers one question —
// *where does this belong?*
//
// A scheduler asks it about a course. A project asks it about a folder or a
// file. The rules do not know which, and that is the point.
//
// One rule per line, first match wins. What is matched is on the left, where it
// goes is on the right, and a project is written in braces so it can never be
// confused with a folder:
//
//	Grundlagen*        -> {Studies/semester1}
//	*.pdf              -> ./skripte
//	first Grundlagen*  -> {Studies/semester1}
//	last 2 *.pdf       -> ./neu
//	Werbung            -> skip
//	*                  -> {Studies/rest}
//
// On the left: a bare word matches anywhere in the name, `Grundlagen*` matches
// the start, `*.pdf` the end, `*Kap*` the middle, a number on its own matches
// the semester, and `*` matches everything.
//
// A rule may take only some of what it matches: `first`, `last`, `newest` or
// `oldest`, optionally with a count. `first Grundlagen*` is the first folder
// called Grundlagen-something, by name; `newest 3 *.pdf` the three most
// recently changed. Without one, a rule takes everything it matches.
//
// For the case none of that covers, a pattern between slashes is a regular
// expression: `/^WDS\d+ - Grundlagen/ -> {Studies/semester1}`. It is the way
// out, not the way in — Go's regular expressions cannot backtrack, so a bad one
// costs nothing, and one that does not compile is reported rather than ignored.
//
// On the right: `{group/project}` is another project, `./folder` a folder where
// it already is, `{group/project}/folder` both, and `skip` means leave it be.
package filter

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

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
	// Pick narrows a rule to some of what it matches: first, last, newest,
	// oldest. Empty means all of them.
	Pick string `json:"pick,omitempty"`
	// Count goes with Pick; 0 means one.
	Count int `json:"count,omitempty"`
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
	IsDir    bool
	// Changed is used by "newest" and "oldest"; the zero time is fine when
	// nobody asks for those.
	Changed time.Time
}

// Destination is the answer. Project empty means "where it already is".
type Destination struct {
	Project string
	Folder  string
	Skip    bool
	// Matched says whether any rule claimed the item at all.
	Matched bool
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
		project, folder, skip := Destinations(r.To)
		d.Project, d.Folder, d.Skip = project, folder, skip
		return d, true
	}
	return Destination{}, false
}

// Destinations reads the right-hand side of a rule.
//
//	{Studies/semester1}          another project
//	{Studies/semester1}/skripte  another project, and a folder in it
//	./skripte  or  /skripte      a folder where it already is
//	skip                         leave it alone
func Destinations(to string) (project, folder string, skip bool) {
	to = strings.TrimSpace(to)
	if strings.EqualFold(to, "skip") {
		return "", "", true
	}
	if strings.HasPrefix(to, "{") {
		end := strings.Index(to, "}")
		if end > 0 {
			project = strings.Trim(to[1:end], "/ ")
			folder = strings.Trim(to[end+1:], "/ ")
			return project, folder, false
		}
	}
	if strings.HasPrefix(to, "./") || strings.HasPrefix(to, "/") {
		return "", strings.Trim(strings.TrimPrefix(to, "."), "/ "), false
	}
	// Written without braces it is still a project — the braces are for
	// clarity, not for the parser to insist on.
	project, folder, _ = strings.Cut(to, "/")
	return strings.TrimSpace(project), strings.Trim(folder, "/ "), false
}

func (r Rule) matches(item Item) bool {
	needle := strings.TrimSpace(r.Match)
	// A pattern between slashes is a regular expression.
	if len(needle) > 1 && strings.HasPrefix(needle, "/") && strings.HasSuffix(needle, "/") {
		re, err := regexp.Compile("(?i)" + needle[1:len(needle)-1])
		if err != nil {
			return false
		}
		return re.MatchString(item.Name) || re.MatchString(item.Path)
	}
	needle = strings.ToLower(needle)
	if needle == "*" || needle == "" {
		return needle == "*"
	}
	// A number on its own is the semester — the one thing that is not a name.
	if n, err := strconv.Atoi(strings.TrimPrefix(needle, "semester")); err == nil {
		return item.Semester == n
	}

	name := strings.ToLower(item.Name)
	path := strings.ToLower(item.Path)
	starts := strings.HasSuffix(needle, "*")
	ends := strings.HasPrefix(needle, "*")
	core := strings.Trim(needle, "*")

	switch {
	case starts && ends: // *Kap*
		return contains(name, core) || contains(path, core)
	case starts: // Grundlagen*
		return strings.HasPrefix(name, core) || strings.HasPrefix(slug.Make(name), slug.Make(core))
	case ends: // *.pdf
		return strings.HasSuffix(name, core)
	default: // anywhere, forgivingly
		return contains(name, needle) || contains(path, needle)
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
		if left == "" {
			bad = append(bad, line)
			continue
		}
		if right == "" {
			// "Grundlagen* ->" is a pattern with no destination: the project
			// that picks the filter up says where it goes.
			right = "here"
		}
		rule := Rule{Match: left, To: right}
		// "first Grundlagen*", "last 2 *.pdf" — the quantifier is the first
		// word, and the count the second if it is a number.
		if word, rest, ok := strings.Cut(left, " "); ok {
			switch strings.ToLower(word) {
			case "first", "last", "newest", "oldest":
				rule.Pick = strings.ToLower(word)
				if n, tail, ok := strings.Cut(strings.TrimSpace(rest), " "); ok {
					if count, err := strconv.Atoi(n); err == nil {
						rule.Count = count
						rest = tail
					}
				}
				rule.Match = strings.TrimSpace(rest)
			}
		}
		if p := strings.TrimSpace(rule.Match); len(p) > 1 && strings.HasPrefix(p, "/") && strings.HasSuffix(p, "/") {
			if _, err := regexp.Compile(p[1 : len(p)-1]); err != nil {
				bad = append(bad, line+"  ("+err.Error()+")")
				continue
			}
		}
		rules = append(rules, rule)
	}
	return rules, bad
}

// Text writes the rules back out the way they were typed.
func Text(rules []Rule) string {
	var b strings.Builder
	for _, r := range rules {
		b.WriteString(r.line())
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

// Plan answers for a whole list at once.
//
// It exists because "the first folder called Grundlagen-something" is not a
// question about one folder — it can only be answered by looking at all of
// them. Rules are taken in order; each one claims what it matches out of what
// is left, so the first match still wins.
func Plan(rules []Rule, items []Item) []Destination {
	out := make([]Destination, len(items))
	claimed := make([]bool, len(items))

	for _, r := range rules {
		// Everything this rule matches that no earlier rule took.
		var candidates []int
		for i, item := range items {
			if !claimed[i] && r.matches(item) {
				candidates = append(candidates, i)
			}
		}
		if len(candidates) == 0 {
			continue
		}
		for _, i := range r.narrow(candidates, items) {
			project, folder, skip := Destinations(r.To)
			out[i] = Destination{
				Project: project, Folder: folder, Skip: skip,
				Rule: strings.TrimSpace(r.line()), Matched: true,
			}
			claimed[i] = true
		}
	}
	return out
}

// narrow applies first/last/newest/oldest. Without one, a rule takes the lot.
func (r Rule) narrow(candidates []int, items []Item) []int {
	pick := strings.ToLower(strings.TrimSpace(r.Pick))
	if pick == "" {
		return candidates
	}
	sorted := append([]int(nil), candidates...)
	switch pick {
	case "newest", "oldest":
		sort.SliceStable(sorted, func(a, b int) bool {
			return items[sorted[a]].Changed.After(items[sorted[b]].Changed)
		})
		if pick == "oldest" {
			reverse(sorted)
		}
	default: // first, last — by name, which is the order things are listed in
		sort.SliceStable(sorted, func(a, b int) bool {
			return strings.ToLower(items[sorted[a]].Name) < strings.ToLower(items[sorted[b]].Name)
		})
		if pick == "last" {
			reverse(sorted)
		}
	}
	count := r.Count
	if count <= 0 {
		count = 1
	}
	if count > len(sorted) {
		count = len(sorted)
	}
	return sorted[:count]
}

func reverse(list []int) {
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
}

func (r Rule) line() string {
	left := r.Match
	if r.Pick != "" {
		if r.Count > 1 {
			left = r.Pick + " " + strconv.Itoa(r.Count) + " " + left
		} else {
			left = r.Pick + " " + left
		}
	}
	return left + " -> " + r.To
}

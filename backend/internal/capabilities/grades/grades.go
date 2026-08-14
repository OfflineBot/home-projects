// Package grades is the grades capability: a `grades.json` in the project,
// shown as a table with an average.
//
// The file is the truth here too — write it by hand, push it over git or let
// the Dualis scheduler fill it, the view is the same.
package grades

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/store"
)

// File is what a grades project keeps its data in.
const File = "grades.json"

type Module struct {
	ID       string  `json:"id,omitempty"`
	Name     string  `json:"name"`
	Grade    float64 `json:"grade,omitempty"`
	GradeRaw string  `json:"gradeText,omitempty"`
	Credits  float64 `json:"credits"`
	Semester string  `json:"semester,omitempty"`
	Status   string  `json:"status,omitempty"` // passed | failed | pending
	// PartOf names the module this one is a piece of, by id or by name. Three
	// exams that together are one subject are three modules here and one on the
	// certificate; without this they would each count as a subject of their own
	// and the average would be wrong.
	PartOf string `json:"partOf,omitempty"`
	// Weight decides how much a part counts inside its module. Empty means its
	// credits, and failing that, equally.
	Weight float64 `json:"weight,omitempty"`
}

// Rolled is a module with whatever belongs under it, and the grade that comes
// out of them.
type Rolled struct {
	Module
	Parts []Module `json:"parts,omitempty"`
	// Computed is true when the grade was worked out from the parts rather than
	// given — worth saying, because it is not what the certificate shows yet.
	Computed bool `json:"computed,omitempty"`
}

type Sheet struct {
	Modules []Module `json:"modules"`
	Source  string   `json:"source,omitempty"`
	FetchAt string   `json:"fetchedAt,omitempty"`
}

type Capability struct{ capability.Base }

func New() Capability { return Capability{} }

func (Capability) Name() string   { return "grades" }
func (Capability) Title() string  { return "Grades" }
func (Capability) Icon() string   { return "award" }
func (Capability) Owns() []string { return []string{File} }

func (Capability) Presets() []capability.Preset {
	return []capability.Preset{{
		Key:          "grades",
		Title:        "Grades",
		Description:  "Modules, credits and the average — from grades.json.",
		Icon:         "award",
		DefaultTab:   "grades",
		Capabilities: []string{"grades"},
		Seed: []capability.SeedFile{{
			Path: File,
			Content: func(p *model.Project) []byte {
				b, _ := json.MarshalIndent(Sheet{Modules: []Module{}}, "", "  ")
				return append(b, '\n')
			},
		}},
	}}
}

func read(ctx context.Context, env *capability.Env, p *model.Project) (Sheet, error) {
	var sheet Sheet
	body, err := env.Files.ReadLocal(ctx, p, File)
	if err != nil {
		return Sheet{Modules: []Module{}}, nil // no file yet is not an error
	}
	if err := json.Unmarshal(body, &sheet); err != nil {
		return sheet, httpx.BadRequest("grades.json cannot be read: %v", err)
	}
	if sheet.Modules == nil {
		sheet.Modules = []Module{}
	}
	return sheet, nil
}

func write(ctx context.Context, env *capability.Env, p *model.Project, sheet Sheet, author, email, message string) error {
	body, err := json.MarshalIndent(sheet, "", "  ")
	if err != nil {
		return err
	}
	_, err = env.Files.Write(ctx, p, File, append(body, '\n'), files.Op{
		Author: author, Email: email, Message: message, Commit: true,
	})
	return err
}

// Roll folds the parts into the modules they belong to.
//
// A part is found by the id or the name it names, and a module with parts takes
// its grade from them — weighted by the parts' own weight, or their credits, or
// equally — unless it was given a grade of its own, which then wins: what the
// certificate says beats what the pieces add up to.
func Roll(modules []Module) []Rolled {
	byKey := map[string]int{}
	for i, m := range modules {
		if m.PartOf != "" {
			continue
		}
		if m.ID != "" {
			byKey[strings.ToLower(m.ID)] = i
		}
		byKey[strings.ToLower(strings.TrimSpace(m.Name))] = i
	}

	out := make([]Rolled, 0, len(modules))
	at := map[int]int{} // index in modules → index in out
	for i, m := range modules {
		if m.PartOf != "" {
			continue
		}
		at[i] = len(out)
		out = append(out, Rolled{Module: m})
	}
	for _, m := range modules {
		if m.PartOf == "" {
			continue
		}
		parent, ok := byKey[strings.ToLower(strings.TrimSpace(m.PartOf))]
		if !ok {
			// It names something that is not here. Better a module of its own
			// than a grade that quietly disappears.
			out = append(out, Rolled{Module: m})
			continue
		}
		i := at[parent]
		out[i].Parts = append(out[i].Parts, m)
	}

	for i := range out {
		if len(out[i].Parts) == 0 || out[i].Grade > 0 {
			continue
		}
		var sum, weight float64
		for _, part := range out[i].Parts {
			if part.Grade <= 0 {
				continue
			}
			w := part.Weight
			if w <= 0 {
				w = part.Credits
			}
			if w <= 0 {
				w = 1
			}
			sum += part.Grade * w
			weight += w
		}
		if weight == 0 {
			continue
		}
		out[i].Grade = math.Round(sum/weight*100) / 100
		out[i].Computed = true
		if out[i].Credits == 0 {
			for _, part := range out[i].Parts {
				out[i].Credits += part.Credits
			}
		}
		if out[i].Status == "" {
			out[i].Status = "passed"
			for _, part := range out[i].Parts {
				if part.Status == "failed" {
					out[i].Status = "failed"
				}
			}
		}
	}
	return out
}

// Flat is the rolled modules as a plain list — what the average is taken over,
// each subject counted once.
func Flat(rolled []Rolled) []Module {
	out := make([]Module, 0, len(rolled))
	for _, r := range rolled {
		out = append(out, r.Module)
	}
	return out
}

// Term is one semester with what was taken in it.
type Term struct {
	Name    string   `json:"name"`
	Modules []Rolled `json:"modules"`
	Average float64  `json:"average"`
	Credits float64  `json:"credits"`
}

// ByTerm groups the modules by semester, oldest first. A semester is written
// "SoSe 2026" or "WiSe 2025/2026"; anything else keeps its place at the end
// rather than being dropped.
func ByTerm(rolled []Rolled) []Term {
	order := map[string]float64{}
	groups := map[string][]Rolled{}
	var names []string
	for _, r := range rolled {
		name := strings.TrimSpace(r.Semester)
		if name == "" {
			name = "Without a semester"
		}
		if _, seen := groups[name]; !seen {
			names = append(names, name)
			order[name] = termOrder(name)
		}
		groups[name] = append(groups[name], r)
	}
	sort.SliceStable(names, func(a, b int) bool { return order[names[a]] < order[names[b]] })

	out := make([]Term, 0, len(names))
	for _, name := range names {
		avg, credits, _ := Average(Flat(groups[name]))
		out = append(out, Term{Name: name, Modules: groups[name], Average: avg, Credits: credits})
	}
	return out
}

// termOrder turns "WiSe 2025/2026" and "SoSe 2026" into something sortable, by
// when the semester starts: a winter term begins in the October of its first
// year, a summer term in the April of its only one. So WiSe 2025/2026 comes
// after SoSe 2025 and before SoSe 2026, which is the order they were sat in.
func termOrder(name string) float64 {
	lower := strings.ToLower(name)
	year := 0
	for _, field := range strings.FieldsFunc(lower, func(r rune) bool { return r < '0' || r > '9' }) {
		if n, err := strconv.Atoi(field); err == nil && n > 1900 && n < 2200 {
			if year == 0 || n < year {
				year = n
			}
		}
	}
	if year == 0 {
		return 1e9 // no year: after everything that has one
	}
	switch {
	case strings.Contains(lower, "wise") || strings.Contains(lower, "winter"):
		return float64(year) + 0.8 // October
	case strings.Contains(lower, "sose") || strings.Contains(lower, "sommer") || strings.Contains(lower, "summer"):
		return float64(year) + 0.3 // April
	}
	return float64(year)
}

// Average is the credit-weighted mean over the modules that carry a grade.
// Modules that are passed without a numeric grade still count for the credits.
func Average(modules []Module) (avg float64, credits float64, counted int) {
	var weightedSum, weightSum float64
	for _, m := range modules {
		if m.Grade > 0 {
			w := m.Credits
			if w <= 0 {
				w = 1
			}
			weightedSum += m.Grade * w
			weightSum += w
			credits += m.Credits
			counted++
			continue
		}
		if m.Status == "passed" {
			credits += m.Credits
		}
	}
	if weightSum == 0 {
		return 0, credits, 0
	}
	return math.Round(weightedSum/weightSum*100) / 100, credits, counted
}

// Routes are mounted under /api/projects/:project/grades
func (Capability) Routes(env *capability.Env, r fiber.Router) {
	r.Get("/", func(c *fiber.Ctx) error {
		if err := capability.RequireRead(c); err != nil {
			return err
		}
		p := capability.Project(c)
		sheet, err := read(c.UserContext(), env, p)
		if err != nil {
			return err
		}
		rolled := Roll(sheet.Modules)
		avg, credits, counted := Average(Flat(rolled))
		return c.JSON(fiber.Map{
			"modules": rolled, "terms": ByTerm(rolled), "raw": sheet.Modules,
			"average": avg, "credits": credits,
			"counted": counted, "source": sheet.Source, "fetchedAt": sheet.FetchAt,
		})
	})

	r.Put("/", func(c *fiber.Ctx) error {
		if err := capability.RequireWrite(c); err != nil {
			return err
		}
		p := capability.Project(c)
		var sheet Sheet
		if err := c.BodyParser(&sheet); err != nil {
			return httpx.BadRequest("The grades could not be read: %v", err)
		}
		if sheet.Modules == nil {
			sheet.Modules = []Module{}
		}
		author, email := capability.AuthorOf(c)
		if err := write(c.UserContext(), env, p, sheet, author, email, "Edit grades"); err != nil {
			return err
		}
		avg, credits, counted := Average(sheet.Modules)
		return c.JSON(fiber.Map{"average": avg, "credits": credits, "counted": counted})
	})
}

func (Capability) Exports(ctx context.Context, env *capability.Env, p *model.Project) ([]store.VariableInput, error) {
	sheet, err := read(ctx, env, p)
	if err != nil {
		return nil, err
	}
	avg, credits, counted := Average(sheet.Modules)
	open := 0
	for _, m := range sheet.Modules {
		if m.Status == "pending" || (m.Grade <= 0 && m.Status == "") {
			open++
		}
	}
	return []store.VariableInput{
		{Name: "average", Type: "number", Value: avg, Source: "capability:grades", History: true},
		{Name: "credits", Type: "number", Value: credits, Unit: "ECTS", Source: "capability:grades"},
		{Name: "modules", Type: "number", Value: len(sheet.Modules), Source: "capability:grades"},
		{Name: "graded", Type: "number", Value: counted, Source: "capability:grades"},
		{Name: "open", Type: "number", Value: open, Source: "capability:grades"},
	}, nil
}

// SchedulerKinds brings the Dualis fetch. Its credentials follow the
// single-use rule without exception — Dualis locks an account after a few
// failed attempts.
func (Capability) SchedulerKinds() []capability.SchedulerKind {
	return []capability.SchedulerKind{{
		Name:            "dualis",
		Title:           "Dualis",
		Description:     "Fetches the grades from Dualis into grades.json. The password is used once: any attempt that does not end in a confirmed sign-in deletes it.",
		AccountKinds:    []string{"dualis"},
		AccountRequired: true,
		Run:             runDualis,
	}}
}

// AccountKinds brings the Dualis credentials. They are the strictest case of
// the single-use rule: Dualis locks the account after a few failed attempts.
func (Capability) AccountKinds() []capability.AccountKind {
	return []capability.AccountKind{{
		Name:        "dualis",
		Title:       "Dualis",
		Description: "User and password for Dualis. Dualis locks the account after a few failed attempts, so the password here is used exactly once per attempt: anything that is not a confirmed sign-in deletes it.",
		Fields: []capability.AccountField{
			{Name: "user", Label: "User", Type: "text", Required: true},
		},
		SecretLabel: "Password",
		Locks:       true,
		Test:        testDualis,
	}}
}

func parseGrade(s string) float64 {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", "."))
	if s == "" || s == "-" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

func sortModules(mods []Module) {
	sort.SliceStable(mods, func(i, j int) bool {
		if mods[i].Semester != mods[j].Semester {
			return mods[i].Semester < mods[j].Semester
		}
		return mods[i].Name < mods[j].Name
	})
}

func fmtFloat(f float64) string { return fmt.Sprintf("%.2f", f) }

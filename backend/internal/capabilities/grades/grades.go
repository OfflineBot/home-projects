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
		avg, credits, counted := Average(sheet.Modules)
		return c.JSON(fiber.Map{
			"modules": sheet.Modules, "average": avg, "credits": credits,
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

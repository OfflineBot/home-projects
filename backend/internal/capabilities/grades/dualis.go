package grades

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/OfflineBot/nicht-libs/dualis"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/store"
)

// testDualis is the "Test connection" of a Dualis account: the same single
// attempt, with the same consequences.
func testDualis(ctx context.Context, env *capability.Env, a *model.Account, secret []byte) error {
	var cfg struct {
		User string `json:"user"`
	}
	if err := json.Unmarshal(a.Config, &cfg); err != nil || cfg.User == "" {
		return fmt.Errorf("this account has no user name")
	}
	session, err := dualis.Login(cfg.User, string(secret))
	if err != nil {
		return fmt.Errorf("sign-in failed: %w", err)
	}
	if session == nil {
		return fmt.Errorf("sign-in gave no session — treating it as failed")
	}
	return nil
}

// runDualis signs in to Dualis once and writes the grades into the project.
//
// The credential was reserved before this function was called. Whatever
// happens in here, the password is gone afterwards unless the report comes
// back with Authenticated set — and that is set only after Login returned a
// session, which is the one unambiguous "signed in" Dualis gives us.
func runDualis(ctx context.Context, env *capability.Env, job capability.Job) (capability.Report, error) {
	if job.Account == nil || len(job.Secret) == 0 {
		return capability.Report{}, fmt.Errorf("this scheduler needs a Dualis account with a password")
	}
	var cfg struct {
		User string `json:"user"`
	}
	_ = json.Unmarshal(job.Account.Config, &cfg)
	if cfg.User == "" {
		return capability.Report{}, fmt.Errorf("the account has no user name")
	}

	job.Log("signing in to Dualis as %s (one attempt, no retry)", cfg.User)

	type loginResult struct {
		session *dualis.Session
		err     error
	}
	done := make(chan loginResult, 1)
	go func() {
		s, err := dualis.Login(cfg.User, string(job.Secret))
		done <- loginResult{s, err}
	}()

	var session *dualis.Session
	select {
	case res := <-done:
		if res.err != nil {
			// Wrong password, a changed page, a network hiccup — all the same:
			// the attempt did not end in a confirmed sign-in.
			return capability.Report{}, fmt.Errorf("sign-in failed: %w", res.err)
		}
		if res.session == nil {
			return capability.Report{}, fmt.Errorf("sign-in gave no session — treating it as failed")
		}
		session = res.session
	case <-time.After(90 * time.Second):
		return capability.Report{}, fmt.Errorf("Dualis did not answer within 90 seconds")
	case <-ctx.Done():
		return capability.Report{}, fmt.Errorf("the run was stopped during sign-in")
	}

	// From here on the sign-in is confirmed. Anything that fails now is a
	// problem with reading the pages, not with the password — but the report
	// still says so explicitly.
	job.Log("signed in")

	all, err := dualis.GetAllGrades(session)
	if err != nil {
		return capability.Report{Authenticated: true},
			fmt.Errorf("signed in, but the grades could not be read: %w", err)
	}

	sheet := Sheet{
		Modules: []Module{},
		Source:  "dualis",
		FetchAt: time.Now().Format(time.RFC3339),
	}
	for _, sem := range all.Semesters {
		for _, m := range sem.Modules {
			mod := Module{
				ID:       m.ID,
				Name:     m.Name,
				Grade:    parseGrade(m.Grade),
				GradeRaw: m.Grade,
				Credits:  m.ECTS,
				Semester: sem.Semester.Name,
				Status:   m.Status,
			}
			if mod.Status == "" {
				mod.Status = "pending"
			}
			sheet.Modules = append(sheet.Modules, mod)
		}
	}
	sortModules(sheet.Modules)

	if err := write(ctx, env, job.Project, sheet, "the Dualis scheduler", "scheduler@home-projects",
		"Update grades from Dualis"); err != nil {
		return capability.Report{Authenticated: true}, err
	}

	avg, credits, counted := Average(sheet.Modules)
	vars := []store.VariableInput{
		{Name: "average", Type: "number", Value: avg, Source: "capability:grades", History: true},
		{Name: "credits", Type: "number", Value: credits, Unit: "ECTS", Source: "capability:grades"},
		{Name: "modules", Type: "number", Value: len(sheet.Modules), Source: "capability:grades"},
		{Name: "graded", Type: "number", Value: counted, Source: "capability:grades"},
	}

	names := make([]string, 0, len(all.Semesters))
	for _, s := range all.Semesters {
		names = append(names, s.Semester.Name)
	}
	return capability.Report{
		Message: fmt.Sprintf("%d modules across %s, average %s",
			len(sheet.Modules), strings.Join(names, ", "), fmtFloat(avg)),
		FilesChanged:  1,
		Authenticated: true,
		Variables:     vars,
	}, nil
}

// Package moodle is the connection to Moodle: course material pulled into a
// project as files.
//
// It has no view of its own — the material is files, and every project can
// already do files. What it brings is an account kind and a scheduler kind,
// which is exactly what a capability is for.
package moodle

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	lib "github.com/OfflineBot/nicht-libs/moodle"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/slug"
	"github.com/offlinebot/home-projects/backend/internal/store"
)

type Capability struct{ capability.Base }

func New() Capability { return Capability{} }

func (Capability) Name() string  { return "moodle" }
func (Capability) Title() string { return "Moodle" }
func (Capability) Icon() string  { return "graduation" }

func (Capability) AccountKinds() []capability.AccountKind {
	return []capability.AccountKind{{
		Name:        "moodle",
		Title:       "Moodle",
		Description: "User and password for a Moodle instance. Moodle locks accounts after failed attempts too, so the password is single-use here as well.",
		Fields: []capability.AccountField{
			{Name: "url", Label: "Moodle address", Type: "url", Required: true, Placeholder: "https://moodle.example.com"},
			{Name: "user", Label: "User", Type: "text", Required: true},
		},
		SecretLabel: "Password",
		Locks:       true,
		Test:        testMoodle,
	}}
}

func (Capability) SchedulerKinds() []capability.SchedulerKind {
	return []capability.SchedulerKind{{
		Name:            "moodle",
		Title:           "Moodle material",
		Description:     "Signs in once, then downloads the courses' files into the project — one folder per course.",
		AccountKinds:    []string{"moodle"},
		AccountRequired: true,
		Options: []capability.AccountField{
			{Name: "onlyCurrent", Label: "Only courses that are still running", Type: "bool"},
			{Name: "courses", Label: "Only these courses (short names, comma separated)", Type: "text",
				Placeholder: "leave empty for all of them"},
		},
		Run: runMoodle,
	}}
}

type config struct {
	URL  string `json:"url"`
	User string `json:"user"`
}

func readConfig(a *model.Account) (config, error) {
	var cfg config
	if err := json.Unmarshal(a.Config, &cfg); err != nil {
		return cfg, fmt.Errorf("the account's settings cannot be read: %w", err)
	}
	cfg.URL = strings.TrimRight(cfg.URL, "/")
	if cfg.URL == "" {
		return cfg, fmt.Errorf("the account has no Moodle address")
	}
	if cfg.User == "" {
		return cfg, fmt.Errorf("the account has no user name")
	}
	return cfg, nil
}

// signIn is the one attempt. A token back means an unambiguous success;
// anything else — including an answer we cannot read — counts as used up.
func signIn(cfg config, secret []byte) (string, error) {
	pair, err := lib.GetToken(cfg.URL, cfg.User, string(secret))
	if err != nil {
		return "", fmt.Errorf("sign-in failed: %w", err)
	}
	if pair == nil || pair.Token == "" {
		return "", fmt.Errorf("sign-in gave no token — treating it as failed")
	}
	return pair.Token, nil
}

func testMoodle(ctx context.Context, env *capability.Env, a *model.Account, secret []byte) error {
	cfg, err := readConfig(a)
	if err != nil {
		return err
	}
	_, err = signIn(cfg, secret)
	return err
}

func runMoodle(ctx context.Context, env *capability.Env, job capability.Job) (capability.Report, error) {
	if job.Account == nil || len(job.Secret) == 0 {
		return capability.Report{}, fmt.Errorf("this scheduler needs a Moodle account with a password")
	}
	cfg, err := readConfig(job.Account)
	if err != nil {
		return capability.Report{}, err
	}

	job.Log("signing in to %s as %s (one attempt, no retry)", cfg.URL, cfg.User)
	token, err := signIn(cfg, job.Secret)
	if err != nil {
		return capability.Report{}, err
	}
	job.Log("signed in")

	// From here the sign-in is confirmed; anything that fails now is about the
	// material, not the password.
	courses, err := lib.GetCourses(cfg.URL, token)
	if err != nil {
		return capability.Report{Authenticated: true}, fmt.Errorf("signed in, but the courses could not be read: %w", err)
	}

	onlyCurrent := true
	if v, ok := job.Options["onlyCurrent"].(bool); ok {
		onlyCurrent = v
	}
	wanted := map[string]bool{}
	if list, ok := job.Options["courses"].([]any); ok {
		for _, item := range list {
			wanted[strings.TrimSpace(fmt.Sprint(item))] = true
		}
	}
	if text, ok := job.Options["courses"].(string); ok {
		for _, item := range strings.Split(text, ",") {
			if item = strings.TrimSpace(item); item != "" {
				wanted[item] = true
			}
		}
	}

	base := job.Scheduler.TargetPath
	written := 0
	skipped := 0

	for _, course := range courses {
		if onlyCurrent && !course.IsCurrent {
			continue
		}
		if len(wanted) > 0 && !wanted[fmt.Sprint(course.ID)] && !wanted[course.Shortname] {
			continue
		}
		folder := path.Join(base, slug.Make(course.Shortname))
		if course.Shortname == "" {
			folder = path.Join(base, slug.Make(course.Fullname))
		}

		items, err := lib.GetCourseFiles(cfg.URL, token, course.ID)
		if err != nil {
			job.Log("course %s: %v", course.Shortname, err)
			continue
		}
		for _, item := range items {
			if item.Filename == "" || item.Fileurl == "" {
				continue
			}
			rel := path.Join(folder, sanitise(item.Filename))
			// A file that is already here and unchanged is not fetched again.
			if env.Files.Exists(job.Project, rel) {
				skipped++
				continue
			}
			body, _, err := lib.DownloadFile(token, item.Fileurl)
			if err != nil {
				job.Log("%s: %v", rel, err)
				continue
			}
			_, err = env.Files.WriteFrom(ctx, job.Project, rel, io.LimitReader(body, 512<<20), files.Op{
				Author: "the Moodle scheduler", Email: "scheduler@home-projects", Commit: false,
			})
			body.Close()
			if err != nil {
				return capability.Report{Authenticated: true}, err
			}
			written++
		}
		job.Log("%s: %d files", course.Shortname, len(items))
	}

	if written > 0 && job.Project.GitTracked {
		_, _, _ = env.Files.Commit(ctx, job.Project,
			fmt.Sprintf("Moodle: %d new file(s)", written), "the Moodle scheduler", "scheduler@home-projects")
	}

	return capability.Report{
		Message:       fmt.Sprintf("%d new files, %d already there, across %d courses", written, skipped, len(courses)),
		FilesChanged:  written,
		Authenticated: true,
		Variables: []store.VariableInput{
			{Name: "courses", Type: "number", Value: len(courses), Source: "capability:moodle"},
			{Name: "fetched_at", Type: "date", Value: time.Now(), Source: "capability:moodle"},
		},
	}, nil
}

// sanitise keeps a Moodle filename usable as a path segment.
func sanitise(name string) string {
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, "\\", "-")
	name = strings.TrimSpace(strings.Trim(name, "."))
	if name == "" {
		name = "file"
	}
	if len(name) > 200 {
		name = name[:200]
	}
	return name
}

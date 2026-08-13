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
	"net/http"
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
		Description: "The user name and password you sign in to Moodle with.",
		Fields: []capability.AccountField{
			{Name: "url", Label: "Moodle address — only the part before the first slash",
				Type: "url", Required: true, Placeholder: "https://elearning.example.de"},
			{Name: "user", Label: "Your Moodle user name", Type: "text", Required: true},
		},
		SecretLabel: "Your Moodle password",
		Locks:       true,
		Precheck:    precheckMoodle,
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

// baseURL reduces whatever was pasted in to the address Moodle's web service
// lives under. People copy the address bar, and the address bar says
// .../login/index.php — appending /login/token.php to that gives a 404 page,
// which is not JSON, which used to look like a failed sign-in and cost a
// password. It does not any more.
func baseURL(raw string) string {
	url := strings.TrimSpace(raw)
	url = strings.TrimRight(url, "/")
	// Longest first: /course/index.php has to be recognised before /index.php
	// gets a chance to leave /course behind.
	for _, tail := range []string{
		"/login/index.php", "/login/token.php", "/course/index.php", "/user/profile.php",
		"/my/index.php", "/index.php", "/login", "/my",
	} {
		if strings.HasSuffix(strings.ToLower(url), tail) {
			url = url[:len(url)-len(tail)]
		}
	}
	return strings.TrimRight(url, "/")
}

func readConfig(a *model.Account) (config, error) {
	var cfg config
	if err := json.Unmarshal(a.Config, &cfg); err != nil {
		return cfg, fmt.Errorf("the account's settings cannot be read: %w", err)
	}
	cfg.URL = baseURL(cfg.URL)
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
		if strings.Contains(err.Error(), "invalid character '<'") {
			return "", fmt.Errorf("%s answered with a web page instead of Moodle's web service — "+
				"the address is wrong", cfg.URL)
		}
		return "", fmt.Errorf("sign-in failed: %w", err)
	}
	if pair == nil || pair.Token == "" {
		return "", fmt.Errorf("sign-in gave no token — treating it as failed")
	}
	return pair.Token, nil
}

// precheckMoodle asks the address whether it is a Moodle at all — without the
// password. Moodle's token endpoint answers a request with no credentials by
// complaining, in JSON, that the user name is missing. Anything else means the
// address is wrong, and finding that out must not cost the password.
func precheckMoodle(ctx context.Context, env *capability.Env, a *model.Account) error {
	cfg, err := readConfig(a)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL+"/login/token.php", nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("%s is not reachable: %w", cfg.URL, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	var answer map[string]any
	if json.Unmarshal(body, &answer) != nil {
		return fmt.Errorf("%s/login/token.php answered with a web page, not with Moodle's web service. "+
			"Use the address you sign in at, without /login/index.php — for example "+
			"https://elearning.dhbw-ravensburg.de", cfg.URL)
	}
	if code, _ := answer["errorcode"].(string); code != "" && code != "missingparam" && code != "invalidparameter" {
		// enablewsdocumentation / disabled service and friends land here.
		return fmt.Errorf("%s answered: %v. The mobile web service is probably switched off there, "+
			"and no password would get through", cfg.URL, answer["error"])
	}
	return nil
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

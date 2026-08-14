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
	"html"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	lib "github.com/OfflineBot/nicht-libs/moodle"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/slug"
	"github.com/offlinebot/home-projects/backend/internal/store"
	"github.com/offlinebot/home-projects/backend/internal/workspace"
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
		Description:     "Signs in once, then downloads the courses' material with the shape Moodle gives it: a folder per course, sections and folders inside. Rules can send each course into a different project.",
		AccountKinds:    []string{"moodle"},
		AccountRequired: true,
		Options: []capability.AccountField{
			{Name: "onlyCurrent", Label: "Running courses only", Type: "bool",
				Hint: "Off fetches every course you are enrolled in, past semesters included."},
			{Name: "courses", Label: "Only these", Type: "text",
				Placeholder: "short names, comma separated — empty means all",
				Hint:        "Course short names, comma separated. Empty means all of them."},
			{Name: "prune", Label: "Mirror", Type: "bool",
				Hint: "Makes the folders a mirror of Moodle instead of a heap that only grows. " +
					"Only inside the folders this scheduler writes — anything you keep beside them stays."},
			{Name: "flat", Label: "No folders", Type: "bool",
				Hint: "Off keeps Moodle's own shape: a folder per course, and the sections and " +
					"folders inside it as they are there."},
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
	return pull(ctx, env, job, cfg, token)
}

// pull is everything after the sign-in: the material. It is its own function
// because a one-off pull with a password typed on the spot does exactly the
// same thing, only without an account behind it.
func pull(ctx context.Context, env *capability.Env, job capability.Job, cfg config, token string) (capability.Report, error) {
	// From here the sign-in is confirmed; anything that fails now is about the
	// material, not the password.
	courses, err := lib.GetCourses(cfg.URL, token)
	if err != nil {
		return capability.Report{Authenticated: true}, fmt.Errorf("signed in, but the courses could not be read: %w", err)
	}

	// Absent means off — the same as the unticked box in the dialog. The other
	// way round, the server quietly did something the screen did not show.
	onlyCurrent, _ := job.Options["onlyCurrent"].(bool)
	// flat throws the shape away: no folder per course, no sections, one heap.
	flat, _ := job.Options["flat"].(bool)
	// prune makes the target a mirror rather than a heap that only grows: what
	// the course no longer has is removed here too. A rebuild always does it,
	// and also fetches every file again instead of trusting what is there.
	prune, _ := job.Options["prune"].(bool)
	fresh := job.Trigger == "rebuild"
	if fresh {
		prune = true
		job.Log("rebuild: every file is fetched again, and whatever Moodle no longer has is removed")
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
	// Where each course goes. A filter picked in the scheduler answers it; the
	// lines typed into the one-off pull answer it the same way.
	route := job.Route

	// Where each course goes is answered for all of them at once, because
	// "the first course called Grundlagen-something" cannot be answered one
	// course at a time.
	decided := map[int]capability.RouteTo{}
	if route != nil {
		items := make([]capability.RouteItem, len(courses))
		for i, course := range courses {
			name := html.UnescapeString(course.Shortname)
			if name == "" {
				name = html.UnescapeString(course.Fullname)
			}
			items[i] = capability.RouteItem{
				Name: name, Path: courseFolder(course), Semester: course.SemesterNumber,
			}
		}
		for i, to := range route(items) {
			decided[i] = to
		}
	}

	base := job.Scheduler.TargetPath
	written := 0
	skipped := 0

	where := "the project itself"
	if base != "" {
		where = base + "/"
	}
	if flat {
		job.Log("%d courses found — everything goes flat into %s, no folders at all", len(courses), where)
	} else {
		job.Log("%d courses found — one folder per course under %s, sections and folders as they are in Moodle",
			len(courses), where)
	}
	if route != nil {
		job.Log("a filter decides where each course goes")
	}

	// A course that is left out has to say so. Silence about skipped work reads
	// as "there was nothing", and that is how eighteen courses can disappear
	// behind a default nobody chose.
	var passedOver, filteredOut, unrouted []string
	// Two courses can own a file of the same name. Without this the later one
	// would look like "already there" and never arrive.
	seen := map[string]string{}
	taken := 0
	// Which projects were written into, so each gets one commit at the end.
	touched := map[uuid.UUID]*model.Project{}
	// What this scheduler fetched last time, by path. A file that is in here
	// and not on disk was moved away deliberately.
	fetchedBefore := map[string]bool{}
	if job.Scheduler != nil && job.Scheduler.ID != uuid.Nil {
		for _, rel := range readManifest(env, job.Project)[job.Scheduler.ID.String()] {
			fetchedBefore[rel] = true
		}
	}
	moved := 0
	// The folders this run is responsible for: only inside them may anything
	// be removed. Notes put next to a pulled course are not this run's to
	// delete.
	owned := map[uuid.UUID]map[string]*model.Project{}
	claim := func(p *model.Project, folder string) {
		if owned[p.ID] == nil {
			owned[p.ID] = map[string]*model.Project{}
		}
		owned[p.ID][folder] = p
	}

	for index, course := range courses {
		name := html.UnescapeString(course.Shortname)
		if name == "" {
			name = html.UnescapeString(course.Fullname)
		}
		if onlyCurrent && !course.IsCurrent {
			passedOver = append(passedOver, name)
			continue
		}
		if len(wanted) > 0 && !wanted[fmt.Sprint(course.ID)] && !wanted[course.Shortname] {
			filteredOut = append(filteredOut, name)
			continue
		}

		// Where this course belongs. Without a filter that is the scheduler's
		// own project; with one, whatever it answers.
		target := job.Project
		into := ""
		if route != nil {
			to := decided[index]
			if !to.Matched || to.Skip {
				unrouted = append(unrouted, fmt.Sprintf("%s (semester %d)", name, course.SemesterNumber))
				continue
			}
			into = to.Folder
			if to.Project != "" {
				target, err = projectBySlug(ctx, env, to.Project)
				if err != nil {
					job.Log("%s → %s: %v", name, to.Project, err)
					continue
				}
			}
		}

		taken++
		folder := path.Join(base, into, courseFolder(course))
		if flat {
			folder = path.Join(base, into)
		}
		claim(target, folder)

		items, err := courseTree(cfg.URL, token, course.ID, !flat)
		if err != nil {
			job.Log("course %s: %v", name, err)
			continue
		}
		for _, it := range items {
			rel := path.Join(folder, it.Rel)
			// A link activity is written, not downloaded.
			if it.Link != "" {
				if !fresh && env.Files.Exists(target, rel) {
					skipped++
					continue
				}
				note := fmt.Sprintf("# %s\n\n<%s>\n\nA link in Moodle, not a file — this note is the address.\n",
					it.Name, it.Link)
				if _, err := env.Files.Write(ctx, target, rel, []byte(note), files.Op{
					Author: "the Moodle scheduler", Email: "scheduler@home-projects", Commit: false,
				}); err != nil {
					return capability.Report{Authenticated: true}, err
				}
				touched[target.ID] = target
				written++
				continue
			}
			key := target.ID.String() + "|" + rel
			// One filename, one folder, two courses: the later one keeps its
			// name and gains the course it came from, instead of vanishing.
			if owner, clash := seen[key]; clash && owner != name {
				rel = path.Join(path.Dir(rel), withSuffix(path.Base(rel), slug.Make(name)))
				key = target.ID.String() + "|" + rel
			}
			seen[key] = name
			// A file that is already here and the same size is not fetched
			// again. A different size means Moodle has a newer one. A rebuild
			// trusts none of that and fetches everything.
			if !fresh && env.Files.Exists(target, rel) && sameSize(env, target, rel, it.Filesize) {
				skipped++
				continue
			}
			// It is not here — but this scheduler fetched it before, so
			// something moved it on purpose: a filter into another project, or
			// a hand. Fetching it again would put a second copy back where the
			// first one was deliberately taken from. A rebuild is how you ask
			// for it anyway.
			if !fresh && fetchedBefore[rel] {
				moved++
				continue
			}
			body, _, err := lib.DownloadFile(token, it.Fileurl)
			if err != nil {
				job.Log("%s: %v", rel, err)
				continue
			}
			_, err = env.Files.WriteFrom(ctx, target, rel, io.LimitReader(body, 512<<20), files.Op{
				Author: "the Moodle scheduler", Email: "scheduler@home-projects", Commit: false,
			})
			body.Close()
			if err != nil {
				return capability.Report{Authenticated: true}, err
			}
			touched[target.ID] = target
			written++
		}
		if target.ID != job.Project.ID {
			job.Log("%s: %d files → project %s", name, len(items), target.Slug)
		} else {
			job.Log("%s: %d files", name, len(items))
		}
	}

	if len(passedOver) > 0 {
		job.Log("%d of %d courses were passed over because they have ended: %s",
			len(passedOver), len(courses), strings.Join(passedOver, ", "))
		job.Log("untick \"only courses that are still running\" to include them")
	}
	if len(filteredOut) > 0 {
		job.Log("%d more were left out by the course filter: %s",
			len(filteredOut), strings.Join(filteredOut, ", "))
	}
	if moved > 0 {
		job.Log("%d file(s) were fetched before and have been moved elsewhere since — left alone", moved)
		job.Log("a rebuild fetches them again")
	}
	if len(unrouted) > 0 {
		job.Log("%d courses matched no rule and were not fetched: %s",
			len(unrouted), strings.Join(unrouted, ", "))
		job.Log("add a \"* -> some-project\" line to catch the rest")
	}

	// What this scheduler wrote, so the next run can tell its own leftovers from
	// everything else. Without it, changing the layout — a folder per course
	// where there used to be none — leaves the old shape lying next to the new
	// one, and no rule can safely guess which files were ours.
	removed := 0
	if prune {
		removed = pruneStrays(ctx, env, job, owned, seen)
	}
	writeManifests(ctx, env, job, owned, seen)

	for _, p := range owned {
		for _, target := range p {
			touched[target.ID] = target
			break
		}
	}
	for _, p := range touched {
		if p.GitTracked {
			_, _, _ = env.Files.Commit(ctx, p,
				fmt.Sprintf("Moodle: %d new file(s)", written), "the Moodle scheduler", "scheduler@home-projects")
		}
	}

	// A run that fetched nothing is a normal outcome, not a silent one: it
	// takes a second and has to say why.
	left := len(passedOver) + len(filteredOut) + len(unrouted)
	message := fmt.Sprintf("%d new files, %d were already there, from %d of %d courses",
		written, skipped, taken, len(courses))
	if written == 0 && skipped > 0 {
		message = fmt.Sprintf("nothing new — all %d files from %d of %d courses were already here",
			skipped, taken, len(courses))
	}
	if written == 0 && skipped == 0 {
		message = fmt.Sprintf("%d of %d courses had no files at all", taken, len(courses))
	}
	if left > 0 {
		message += fmt.Sprintf(" (%d courses left out)", left)
	}
	if moved > 0 {
		message += fmt.Sprintf(", %d already moved elsewhere", moved)
	}
	if len(touched) > 1 {
		message += fmt.Sprintf(", across %d projects", len(touched))
	}
	if removed > 0 {
		message += fmt.Sprintf(", %d removed", removed)
	}

	return capability.Report{
		Message:       message,
		FilesChanged:  written + removed,
		Authenticated: true,
		Variables: []store.VariableInput{
			{Name: "courses", Type: "number", Value: len(courses), Source: "capability:moodle"},
			{Name: "fetched_at", Type: "date", Value: time.Now(), Source: "capability:moodle"},
		},
	}, nil
}

// projectBySlug finds the project a routing rule names, and refuses the ones
// that cannot be written into rather than failing later, file by file.
func projectBySlug(ctx context.Context, env *capability.Env, name string) (*model.Project, error) {
	all, err := env.Store.ListProjects(ctx, nil, false, false)
	if err != nil {
		return nil, err
	}
	for i := range all {
		p := all[i]
		if !strings.EqualFold(p.Slug, name) && !strings.EqualFold(p.Title, name) {
			continue
		}
		var group *model.Group
		if p.GroupID != nil {
			group, _ = env.Store.GroupByID(ctx, *p.GroupID)
		}
		if p.EffectiveReadOnly(group) {
			return nil, fmt.Errorf("the project %s is read-only", p.Slug)
		}
		return &p, nil
	}
	return nil, fmt.Errorf("there is no project called %q on this server", name)
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

// Routes are mounted under /api/projects/:project/moodle.
//
// There is exactly one: a pull you run yourself, with the password typed on
// the spot. Nothing is stored — not the password, not an account, not a
// scheduler — so there is no credential that could be used up, and no second
// attempt to prevent. For everything that should happen on a schedule, an
// account is still the right answer.
func (Capability) Routes(env *capability.Env, r fiber.Router) {
	r.Post("/pull-once", func(ctx *fiber.Ctx) error {
		if err := capability.RequireWrite(ctx); err != nil {
			return err
		}
		p := capability.Project(ctx)

		var in struct {
			URL         string `json:"url"`
			User        string `json:"user"`
			Password    string `json:"password"`
			Target      string `json:"target"`
			Courses     string `json:"courses"`
			Routes      string `json:"routes"`
			Filter      string `json:"filter"`
			OnlyCurrent *bool  `json:"onlyCurrent"`
			Flat        bool   `json:"flat"`
			Prune       bool   `json:"prune"`
			Rebuild     bool   `json:"rebuild"`
		}
		if err := ctx.BodyParser(&in); err != nil {
			return httpx.BadRequest("The request could not be read.")
		}
		cfg := config{URL: baseURL(in.URL), User: strings.TrimSpace(in.User)}
		if cfg.URL == "" || cfg.User == "" || in.Password == "" {
			return httpx.BadRequest("Address, user name and password are all needed for a one-off pull.")
		}

		// The address is checked before the password is sent, for the same
		// reason as everywhere else: a typo in a URL must not look like a
		// failed sign-in.
		probe := model.Account{Config: []byte(fmt.Sprintf(`{"url":%q,"user":%q}`, cfg.URL, cfg.User))}
		if err := precheckMoodle(ctx.UserContext(), env, &probe); err != nil {
			return httpx.BadRequest("%v", err)
		}

		token, err := signIn(cfg, []byte(in.Password))
		if err != nil {
			return httpx.New(401, "sign_in_failed",
				"%v. Nothing was stored, so you can simply try again.", err)
		}

		var lines []string
		onlyCurrent := true
		if in.OnlyCurrent != nil {
			onlyCurrent = *in.OnlyCurrent
		}
		job := capability.Job{
			Project:   p,
			Scheduler: &model.Scheduler{TargetPath: strings.Trim(in.Target, "/")},
			Options: map[string]any{
				"onlyCurrent": onlyCurrent,
				"courses":     in.Courses,
				"routes":      in.Routes,
				"flat":        in.Flat,
				"prune":       in.Prune,
			},
			Trigger: map[bool]string{true: "rebuild", false: "manual"}[in.Rebuild],
			Route:   env.Router(ctx.UserContext(), in.Filter),
			Log: func(format string, args ...any) {
				lines = append(lines, fmt.Sprintf(format, args...))
			},
		}
		// Pulling the whole of Moodle takes minutes and hundreds of megabytes.
		// A browser that gives up waiting, or a proxy that closes the
		// connection, must not stop the work halfway and leave a project with
		// eleven of twenty-three courses in it.
		bg := context.WithoutCancel(ctx.UserContext())
		report, err := pull(bg, env, job, cfg, token)
		if err != nil {
			return httpx.New(502, "pull_failed", "Signed in, but the material could not be fetched: %v", err)
		}
		env.Store.Audit(ctx.UserContext(), nil, "moodle.pull_once", p.Title, "",
			map[string]any{"files": report.FilesChanged})
		return ctx.JSON(fiber.Map{
			"message": report.Message,
			"files":   report.FilesChanged,
			"log":     lines,
		})
	})
}

// withSuffix puts something between a name and its extension, so a second
// "Skript.pdf" becomes "Skript (wds125).pdf" rather than a collision.
func withSuffix(name, suffix string) string {
	ext := path.Ext(name)
	return strings.TrimSuffix(name, ext) + " (" + suffix + ")" + ext
}

// sameSize decides whether what is on disk is still what Moodle has. Moodle
// gives a size with every file; a lecture slide that grew by a chapter has a
// different one, and then it is fetched again.
func sameSize(env *capability.Env, p *model.Project, rel string, want int) bool {
	if want <= 0 {
		return true // Moodle said nothing about the size; leave what is here.
	}
	entry, err := env.Files.Workspace().Open(p.ID).Stat(rel)
	if err != nil {
		return false
	}
	return entry.Size == int64(want)
}

// pruneStrays removes what the source no longer has.
//
// The rule that makes this safe enough to offer: it only ever looks inside the
// folders this run wrote into — one per course, or the target folder when the
// shape was thrown away. A note you put next to a pulled course is inside such
// a folder and *will* go; a note next to the folder will not. That is why the
// switch is off unless asked for, and why the log names every file it took.
func pruneStrays(ctx context.Context, env *capability.Env, job capability.Job,
	owned map[uuid.UUID]map[string]*model.Project, seen map[string]string) int {

	removed := 0
	for projectID, folders := range owned {
		// What this scheduler itself wrote last time and did not write now —
		// wherever it ended up. A layout change lands here.
		if len(folders) > 0 {
			var any *model.Project
			for _, p := range folders {
				any = p
				break
			}
			for _, rel := range strayFromLastTime(env, job, any, seen) {
				if err := env.Files.Remove(ctx, any, rel, false, files.Op{
					Author: "the Moodle scheduler", Email: "scheduler@home-projects", Commit: false,
				}); err != nil {
					continue
				}
				job.Log("removed, left over from the previous layout: %s", rel)
				removed++
			}
		}
		for folder, target := range folders {
			fs := env.Files.Workspace().Open(projectID)
			var strays []string
			_ = fs.Walk(folder, func(e workspace.Entry) error {
				if e.IsDir {
					return nil
				}
				if e.Path == manifestPath {
					return nil
				}
				if _, mine := seen[projectID.String()+"|"+e.Path]; !mine {
					strays = append(strays, e.Path)
				}
				return nil
			})
			for _, rel := range strays {
				if err := env.Files.Remove(ctx, target, rel, false, files.Op{
					Author: "the Moodle scheduler", Email: "scheduler@home-projects", Commit: false,
				}); err != nil {
					job.Log("%s could not be removed: %v", rel, err)
					continue
				}
				job.Log("removed, the course no longer has it: %s", rel)
				removed++
			}
		}
	}
	return removed
}

// manifestPath is where a scheduler writes down what it put in a project. One
// file per project, keyed by scheduler, so two schedulers writing into the same
// project stay out of each other's way.
const manifestPath = ".home-projects/moodle.json"

type manifest map[string][]string

func readManifest(env *capability.Env, p *model.Project) manifest {
	m := manifest{}
	if !env.Files.Exists(p, manifestPath) {
		return m
	}
	body, _, err := env.Files.Read(context.Background(), p, manifestPath)
	if err != nil {
		return m
	}
	_ = json.Unmarshal(body, &m)
	return m
}

// writeManifests records this run's files, per project it wrote into.
func writeManifests(ctx context.Context, env *capability.Env, job capability.Job,
	owned map[uuid.UUID]map[string]*model.Project, seen map[string]string) {

	if job.Scheduler == nil || job.Scheduler.ID == uuid.Nil {
		return // a one-off pull owns nothing later
	}
	key := job.Scheduler.ID.String()
	for projectID, folders := range owned {
		var target *model.Project
		for _, p := range folders {
			target = p
			break
		}
		if target == nil {
			continue
		}
		mine := []string{}
		prefix := projectID.String() + "|"
		for k := range seen {
			if strings.HasPrefix(k, prefix) {
				mine = append(mine, strings.TrimPrefix(k, prefix))
			}
		}
		sort.Strings(mine)
		m := readManifest(env, target)
		m[key] = mine
		body, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			continue
		}
		if _, err := env.Files.Write(ctx, target, manifestPath, body, files.Op{
			Author: "the Moodle scheduler", Email: "scheduler@home-projects", Commit: false,
		}); err != nil {
			job.Log("the list of what was written could not be stored: %v", err)
		}
	}
}

// strayFromLastTime returns the files this scheduler wrote last time and did
// not write now — wherever they are. This is what makes changing the layout
// clean up after itself: the old flat files were ours, so they may go, while
// anything else in the project was never ours to touch.
func strayFromLastTime(env *capability.Env, job capability.Job, p *model.Project, seen map[string]string) []string {
	if job.Scheduler == nil || job.Scheduler.ID == uuid.Nil {
		return nil
	}
	previous := readManifest(env, p)[job.Scheduler.ID.String()]
	var out []string
	for _, rel := range previous {
		if _, still := seen[p.ID.String()+"|"+rel]; !still {
			out = append(out, rel)
		}
	}
	return out
}

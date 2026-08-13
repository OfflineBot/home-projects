// Package feed is the feed capability: entries from a source, kept as files.
//
// A run writes `feed.json` with the list and one file per article, so the
// project stays readable — and downloadable — without this capability.
package feed

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/slug"
	"github.com/offlinebot/home-projects/backend/internal/store"
)

const (
	File        = "feed.json"
	ArticlesDir = "articles"
)

type Entry struct {
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	Published time.Time `json:"published"`
	Summary   string    `json:"summary,omitempty"`
	File      string    `json:"file,omitempty"`
	Source    string    `json:"source,omitempty"`
}

type Feed struct {
	Title     string    `json:"title"`
	Source    string    `json:"source"`
	FetchedAt time.Time `json:"fetchedAt"`
	Entries   []Entry   `json:"entries"`
}

type Capability struct{ capability.Base }

func New() Capability { return Capability{} }

func (Capability) Name() string   { return "feed" }
func (Capability) Title() string  { return "Feed" }
func (Capability) Icon() string   { return "rss" }
func (Capability) Owns() []string { return []string{File, ArticlesDir + "/*"} }

func (Capability) Presets() []capability.Preset {
	return []capability.Preset{{
		Key:          "feed",
		Title:        "Feed",
		Description:  "Entries from a source — RSS, Atom or any JSON you point it at.",
		Icon:         "rss",
		DefaultTab:   "feed",
		Capabilities: []string{"feed"},
		Seed: []capability.SeedFile{{
			Path: File,
			Content: func(p *model.Project) []byte {
				body, _ := json.MarshalIndent(Feed{Title: p.Title, Entries: []Entry{}}, "", "  ")
				return append(body, '\n')
			},
		}},
	}}
}

func (Capability) AccountKinds() []capability.AccountKind {
	return []capability.AccountKind{{
		Name:        "http",
		Title:       "Generic HTTP",
		Description: "A base URL with headers or a token — for your own sources.",
		Fields: []capability.AccountField{
			{Name: "url", Label: "URL", Type: "url", Required: true},
			{Name: "header", Label: "Header name", Type: "text", Placeholder: "Authorization"},
		},
		SecretLabel: "Token",
		Test:        testHTTPAccount,
	}}
}

func (Capability) SchedulerKinds() []capability.SchedulerKind {
	return []capability.SchedulerKind{
		{
			Name:         "feed",
			Title:        "Feed",
			Description:  "Fetches an RSS or Atom feed into feed.json and writes the articles as files.",
			AccountKinds: nil,
			Run:          runFeed,
		},
		{
			Name:         "http",
			Title:        "HTTP → file",
			Description:  "Fetches a URL and stores the answer in the project.",
			AccountKinds: nil,
			Run:          runHTTPToFile,
		},
	}
}

func testHTTPAccount(ctx context.Context, env *capability.Env, a *model.Account, secret []byte) error {
	var cfg struct {
		URL    string `json:"url"`
		Header string `json:"header"`
	}
	if err := json.Unmarshal(a.Config, &cfg); err != nil || cfg.URL == "" {
		return fmt.Errorf("this account has no URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL, nil)
	if err != nil {
		return err
	}
	if cfg.Header != "" && len(secret) > 0 {
		req.Header.Set(cfg.Header, string(secret))
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("not reachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("the server refused the token (%s)", resp.Status)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("the server answered %s", resp.Status)
	}
	return nil
}

// ------------------------------------------------------------------ fetching

func fetch(ctx context.Context, env *capability.Env, job capability.Job) ([]byte, string, error) {
	url, _ := job.Options["url"].(string)
	usedCredential := false
	var headerName string
	if job.Account != nil {
		var cfg struct {
			URL    string `json:"url"`
			Header string `json:"header"`
		}
		_ = json.Unmarshal(job.Account.Config, &cfg)
		if url == "" {
			url = cfg.URL
		}
		headerName = cfg.Header
	}
	if url == "" {
		return nil, "", fmt.Errorf("this scheduler has no URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, url, err
	}
	req.Header.Set("User-Agent", "home-projects/1.0")
	if headerName != "" && len(job.Secret) > 0 {
		req.Header.Set(headerName, string(job.Secret))
		usedCredential = true
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return nil, url, fmt.Errorf("not reachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, url, fmt.Errorf("the server answered %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, url, err
	}
	_ = usedCredential
	return body, url, nil
}

func runFeed(ctx context.Context, env *capability.Env, job capability.Job) (capability.Report, error) {
	body, url, err := fetch(ctx, env, job)
	if err != nil {
		return capability.Report{}, err
	}
	parsed, err := Parse(body)
	if err != nil {
		return capability.Report{}, err
	}
	parsed.Source = url
	parsed.FetchedAt = time.Now()
	if parsed.Title == "" {
		parsed.Title = job.Project.Title
	}

	writeArticles := true
	if v, ok := job.Options["articles"].(bool); ok {
		writeArticles = v
	}
	changed := 0
	if writeArticles {
		for i := range parsed.Entries {
			e := &parsed.Entries[i]
			name := e.Published.Format("20060102") + "-" + slug.Make(e.Title) + ".md"
			rel := path.Join(ArticlesDir, name)
			e.File = rel
			if env.Files.Exists(job.Project, rel) {
				continue
			}
			article := fmt.Sprintf("# %s\n\n%s\n\n[%s](%s)\n", e.Title, e.Summary, e.URL, e.URL)
			if _, err := env.Files.Write(ctx, job.Project, rel, []byte(article), files.Op{
				Author: "the feed scheduler", Email: "scheduler@home-projects", Commit: false,
			}); err != nil {
				return capability.Report{}, err
			}
			changed++
		}
	}

	out, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return capability.Report{}, err
	}
	if _, err := env.Files.Write(ctx, job.Project, File, append(out, '\n'), files.Op{
		Author: "the feed scheduler", Email: "scheduler@home-projects",
		Message: "Update feed", Commit: true,
	}); err != nil {
		return capability.Report{}, err
	}
	job.Log("%d entries, %d new articles", len(parsed.Entries), changed)

	return capability.Report{
		Message:       fmt.Sprintf("%d entries, %d new", len(parsed.Entries), changed),
		FilesChanged:  changed + 1,
		Authenticated: true,
		Variables: []store.VariableInput{
			{Name: "entries", Type: "number", Value: len(parsed.Entries), Source: "capability:feed"},
			{Name: "fetched_at", Type: "date", Value: parsed.FetchedAt, Source: "capability:feed"},
		},
	}, nil
}

func runHTTPToFile(ctx context.Context, env *capability.Env, job capability.Job) (capability.Report, error) {
	body, url, err := fetch(ctx, env, job)
	if err != nil {
		return capability.Report{}, err
	}
	target := job.Scheduler.TargetPath
	if name, ok := job.Options["path"].(string); ok && name != "" {
		target = name
	}
	if target == "" || strings.HasSuffix(target, "/") {
		target = path.Join(target, "fetched.txt")
	}
	if _, err := env.Files.Write(ctx, job.Project, target, body, files.Op{
		Author: "the http scheduler", Email: "scheduler@home-projects",
		Message: "Fetch " + url, Commit: true,
	}); err != nil {
		return capability.Report{}, err
	}
	job.Log("%d bytes into %s", len(body), target)
	return capability.Report{
		Message: fmt.Sprintf("%d bytes into %s", len(body), target), FilesChanged: 1,
		Authenticated: true,
	}, nil
}

// --------------------------------------------------------------- RSS / Atom

type rssDoc struct {
	Channel struct {
		Title string `xml:"title"`
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			PubDate     string `xml:"pubDate"`
			Description string `xml:"description"`
		} `xml:"item"`
	} `xml:"channel"`
}

type atomDoc struct {
	Title   string `xml:"title"`
	Entries []struct {
		Title string `xml:"title"`
		Links []struct {
			Href string `xml:"href,attr"`
			Rel  string `xml:"rel,attr"`
		} `xml:"link"`
		Updated   string `xml:"updated"`
		Published string `xml:"published"`
		Summary   string `xml:"summary"`
		Content   string `xml:"content"`
	} `xml:"entry"`
}

// Parse reads RSS 2.0 and Atom. Anything else is reported as such.
func Parse(body []byte) (*Feed, error) {
	out := &Feed{Entries: []Entry{}}

	var rss rssDoc
	if err := xml.Unmarshal(body, &rss); err == nil && len(rss.Channel.Items) > 0 {
		out.Title = rss.Channel.Title
		for _, item := range rss.Channel.Items {
			out.Entries = append(out.Entries, Entry{
				Title:     strings.TrimSpace(item.Title),
				URL:       strings.TrimSpace(item.Link),
				Published: parseDate(item.PubDate),
				Summary:   strings.TrimSpace(item.Description),
			})
		}
		sortEntries(out.Entries)
		return out, nil
	}

	var atom atomDoc
	if err := xml.Unmarshal(body, &atom); err == nil && len(atom.Entries) > 0 {
		out.Title = atom.Title
		for _, e := range atom.Entries {
			link := ""
			for _, l := range e.Links {
				if l.Rel == "" || l.Rel == "alternate" {
					link = l.Href
					break
				}
			}
			summary := e.Summary
			if summary == "" {
				summary = e.Content
			}
			when := e.Published
			if when == "" {
				when = e.Updated
			}
			out.Entries = append(out.Entries, Entry{
				Title:     strings.TrimSpace(e.Title),
				URL:       strings.TrimSpace(link),
				Published: parseDate(when),
				Summary:   strings.TrimSpace(summary),
			})
		}
		sortEntries(out.Entries)
		return out, nil
	}

	return nil, fmt.Errorf("this is neither RSS nor Atom, or it has no entries")
}

func sortEntries(list []Entry) {
	sort.Slice(list, func(i, j int) bool { return list[i].Published.After(list[j].Published) })
}

func parseDate(v string) time.Time {
	v = strings.TrimSpace(v)
	layouts := []string{
		time.RFC1123Z, time.RFC1123, time.RFC3339, time.RFC822Z, time.RFC822,
		"2006-01-02T15:04:05Z07:00", "2006-01-02 15:04:05", "2006-01-02",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, v); err == nil {
			return t
		}
	}
	return time.Now()
}

// ------------------------------------------------------------------- routes

func read(ctx context.Context, env *capability.Env, p *model.Project) (*Feed, error) {
	if !env.Files.Exists(p, File) {
		return &Feed{Title: p.Title, Entries: []Entry{}}, nil
	}
	body, err := env.Files.ReadLocal(ctx, p, File)
	if err != nil {
		return nil, err
	}
	var f Feed
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, httpx.BadRequest("feed.json cannot be read: %v", err)
	}
	if f.Entries == nil {
		f.Entries = []Entry{}
	}
	return &f, nil
}

// Routes are mounted under /api/projects/:project/feed
func (Capability) Routes(env *capability.Env, r fiber.Router) {
	r.Get("/entries", func(c *fiber.Ctx) error {
		if err := capability.RequireRead(c); err != nil {
			return err
		}
		f, err := read(c.UserContext(), env, capability.Project(c))
		if err != nil {
			return err
		}
		return c.JSON(f)
	})
}

func (Capability) Exports(ctx context.Context, env *capability.Env, p *model.Project) ([]store.VariableInput, error) {
	f, err := read(ctx, env, p)
	if err != nil {
		return nil, nil // a broken file is reported by the view, not here
	}
	out := []store.VariableInput{
		{Name: "entries", Type: "number", Value: len(f.Entries), Source: "capability:feed"},
	}
	if len(f.Entries) > 0 {
		out = append(out,
			store.VariableInput{Name: "latest", Type: "text", Value: f.Entries[0].Title, Source: "capability:feed"},
			store.VariableInput{Name: "latest_at", Type: "date", Value: f.Entries[0].Published, Source: "capability:feed"},
		)
	}
	return out, nil
}

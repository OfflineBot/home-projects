// Package markdown is the notes capability: an editor over the *.md files a
// project already has, with backlinks — and nothing else. The vault on your
// machine stays in sync through the project's git branch, so Obsidian needs no
// special support here.
package markdown

import (
	"context"
	"embed"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/store"
	"github.com/offlinebot/home-projects/backend/internal/workspace"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Capability struct{ capability.Base }

func New() Capability { return Capability{} }

func (Capability) Name() string      { return "markdown" }
func (Capability) Title() string     { return "Notes" }
func (Capability) Icon() string      { return "notebook" }
func (Capability) Owns() []string    { return []string{"*.md", "*.markdown"} }
func (Capability) Migrations() fs.FS { sub, _ := fs.Sub(migrations, "migrations"); return sub }

func (Capability) Presets() []capability.Preset {
	return []capability.Preset{{
		Key:          "notes",
		Title:        "Notes / Vault",
		Description:  "Markdown with backlinks. Syncs with an Obsidian vault over git.",
		Icon:         "notebook",
		DefaultTab:   "markdown",
		Capabilities: []string{"markdown"},
		Seed: []capability.SeedFile{{
			Path: "README.md",
			Content: func(p *model.Project) []byte {
				return []byte("# " + p.Title + "\n\n" + p.Description + "\n")
			},
		}},
	}}
}

var (
	wikiLink = regexp.MustCompile(`\[\[([^\]|#]+)(?:[#|][^\]]*)?\]\]`)
	mdLink   = regexp.MustCompile(`\[[^\]]*\]\(([^)]+\.md)\)`)
	heading  = regexp.MustCompile(`(?m)^#\s+(.+)$`)
)

// Index keeps the backlink table in step with a note's content.
func (Capability) Index(ctx context.Context, env *capability.Env, p *model.Project, rel string) error {
	if !isMarkdown(rel) {
		return reindexAll(ctx, env, p)
	}
	body, err := env.Files.ReadLocal(ctx, p, rel)
	if err != nil {
		// The note is gone: its outgoing links go with it.
		_, derr := env.Store.Pool().Exec(ctx,
			`DELETE FROM markdown_links WHERE project_id=$1 AND source_path=$2`, p.ID, rel)
		return derr
	}
	return indexOne(ctx, env, p, rel, body)
}

func indexOne(ctx context.Context, env *capability.Env, p *model.Project, rel string, body []byte) error {
	targets := linkTargets(string(body))
	tx, err := env.Store.Pool().Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx,
		`DELETE FROM markdown_links WHERE project_id=$1 AND source_path=$2`, p.ID, rel); err != nil {
		return err
	}
	for _, t := range targets {
		if _, err := tx.Exec(ctx,
			`INSERT INTO markdown_links (project_id, source_path, target) VALUES ($1,$2,$3)`,
			p.ID, rel, t); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func reindexAll(ctx context.Context, env *capability.Env, p *model.Project) error {
	fsys := env.Files.Workspace().Open(p.ID)
	var notes []string
	_ = fsys.Walk("", func(e workspace.Entry) error {
		if !e.IsDir && isMarkdown(e.Path) {
			notes = append(notes, e.Path)
		}
		return nil
	})
	if _, err := env.Store.Pool().Exec(ctx,
		`DELETE FROM markdown_links WHERE project_id=$1`, p.ID); err != nil {
		return err
	}
	for _, rel := range notes {
		body, err := env.Files.ReadLocal(ctx, p, rel)
		if err != nil {
			continue
		}
		if err := indexOne(ctx, env, p, rel, body); err != nil {
			return err
		}
	}
	return nil
}

// linkTargets collects [[wiki links]] and [text](note.md) references.
func linkTargets(body string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		t = strings.TrimSpace(t)
		t = strings.TrimSuffix(t, ".md")
		if t == "" || seen[strings.ToLower(t)] {
			return
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	for _, m := range wikiLink.FindAllStringSubmatch(body, -1) {
		add(m[1])
	}
	for _, m := range mdLink.FindAllStringSubmatch(body, -1) {
		if strings.Contains(m[1], "://") {
			continue
		}
		add(path.Base(m[1]))
	}
	return out
}

func isMarkdown(rel string) bool {
	l := strings.ToLower(rel)
	return strings.HasSuffix(l, ".md") || strings.HasSuffix(l, ".markdown")
}

type note struct {
	Path       string    `json:"path"`
	Title      string    `json:"title"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modifiedAt"`
}

func listNotes(env *capability.Env, p *model.Project) []note {
	fsys := env.Files.Workspace().Open(p.ID)
	out := []note{}
	_ = fsys.Walk("", func(e workspace.Entry) error {
		if e.IsDir || !isMarkdown(e.Path) {
			return nil
		}
		out = append(out, note{Path: e.Path, Title: titleOf(e.Path), Size: e.Size, ModifiedAt: e.ModifiedAt})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path) })
	return out
}

func titleOf(rel string) string {
	return strings.TrimSuffix(path.Base(rel), path.Ext(rel))
}

// Routes are mounted under /api/projects/:project/markdown
func (Capability) Routes(env *capability.Env, r fiber.Router) {
	r.Get("/notes", func(c *fiber.Ctx) error {
		if err := capability.RequireRead(c); err != nil {
			return err
		}
		return c.JSON(fiber.Map{"notes": listNotes(env, capability.Project(c))})
	})

	r.Get("/note", func(c *fiber.Ctx) error {
		if err := capability.RequireRead(c); err != nil {
			return err
		}
		p := capability.Project(c)
		rel := c.Query("path")
		if !isMarkdown(rel) {
			return httpx.BadRequest("This is not a markdown file.")
		}
		body, res, err := env.Files.Read(c.UserContext(), p, rel)
		if err != nil {
			return err
		}
		title := titleOf(rel)
		if m := heading.FindStringSubmatch(string(body)); m != nil {
			title = strings.TrimSpace(m[1])
		}
		return c.JSON(fiber.Map{
			"path": rel, "title": title, "content": string(body),
			"linkedFrom": res.Link != nil,
		})
	})

	r.Put("/note", func(c *fiber.Ctx) error {
		if err := capability.RequireWrite(c); err != nil {
			return err
		}
		p := capability.Project(c)
		var in struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The note could not be read.")
		}
		if !isMarkdown(in.Path) {
			return httpx.BadRequest("A note has to end in .md.")
		}
		author, email := capability.AuthorOf(c)
		if _, err := env.Files.Write(c.UserContext(), p, in.Path, []byte(in.Content), files.Op{
			Author: author, Email: email, Message: "Edit " + in.Path, Commit: true,
		}); err != nil {
			return err
		}
		return httpx.OK(c)
	})

	r.Get("/backlinks", func(c *fiber.Ctx) error {
		if err := capability.RequireRead(c); err != nil {
			return err
		}
		p := capability.Project(c)
		rel := c.Query("path")
		name := titleOf(rel)
		rows, err := env.Store.Pool().Query(c.UserContext(),
			`SELECT DISTINCT source_path FROM markdown_links
			 WHERE project_id=$1 AND lower(target)=lower($2) ORDER BY source_path`, p.ID, name)
		if err != nil {
			return httpx.Internal("the backlinks could not be read").WithCause(err)
		}
		defer rows.Close()
		out := []string{}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				return httpx.Internal("the backlinks could not be read").WithCause(err)
			}
			out = append(out, s)
		}
		return c.JSON(fiber.Map{"backlinks": out})
	})

	// The whole link graph, for the notes overview.
	r.Get("/graph", func(c *fiber.Ctx) error {
		if err := capability.RequireRead(c); err != nil {
			return err
		}
		p := capability.Project(c)
		rows, err := env.Store.Pool().Query(c.UserContext(),
			`SELECT source_path, target FROM markdown_links WHERE project_id=$1`, p.ID)
		if err != nil {
			return httpx.Internal("the link graph could not be read").WithCause(err)
		}
		defer rows.Close()
		type edge struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		edges := []edge{}
		for rows.Next() {
			var e edge
			if err := rows.Scan(&e.From, &e.To); err != nil {
				return httpx.Internal("the link graph could not be read").WithCause(err)
			}
			edges = append(edges, e)
		}
		return c.JSON(fiber.Map{"notes": listNotes(env, p), "edges": edges})
	})
}

func (Capability) Exports(ctx context.Context, env *capability.Env, p *model.Project) ([]store.VariableInput, error) {
	notes := listNotes(env, p)
	var newest time.Time
	for _, n := range notes {
		if n.ModifiedAt.After(newest) {
			newest = n.ModifiedAt
		}
	}
	out := []store.VariableInput{
		{Name: "note_count", Type: "number", Value: len(notes), Source: "capability:markdown"},
	}
	if !newest.IsZero() {
		out = append(out, store.VariableInput{
			Name: "last_edited", Type: "date", Value: newest, Source: "capability:markdown",
		})
	}
	return out, nil
}

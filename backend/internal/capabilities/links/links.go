// Package links is a place to put a link so it can be found again.
//
// The thing somebody actually wants: "I want to buy this, write it down" — an
// address, a name, a line about why. It lives in links.json, so it travels with
// the project, can be exported, edited by hand, and read without this
// capability. A project that holds links can be public, and then it is a list
// somebody else can add to as well.
package links

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/store"
)

const File = "links.json"

type Link struct {
	ID      string    `json:"id"`
	URL     string    `json:"url"`
	Title   string    `json:"title"`
	Note    string    `json:"note,omitempty"`
	Tags    []string  `json:"tags,omitempty"`
	AddedAt time.Time `json:"addedAt"`
	// Done marks the ones dealt with — bought, read, watched. They stay, because
	// "what did I look at last month" is half the reason to keep a list.
	Done bool `json:"done,omitempty"`
}

type List struct {
	Links []Link `json:"links"`
}

type Capability struct{ capability.Base }

func New() Capability { return Capability{} }

func (Capability) Name() string   { return "links" }
func (Capability) Title() string  { return "Links" }
func (Capability) Icon() string   { return "link" }
func (Capability) Owns() []string { return []string{File} }

func (Capability) Presets() []capability.Preset {
	return []capability.Preset{{
		Key:          "links",
		Title:        "Links",
		Description:  "Addresses worth keeping: a name, a line about why, and a tag or two.",
		Icon:         "link",
		DefaultTab:   "links",
		Capabilities: []string{"links"},
		Seed: []capability.SeedFile{{
			Path:    File,
			Content: func(p *model.Project) []byte { return []byte("{\n  \"links\": []\n}\n") },
		}},
	}}
}

func (Capability) Cards() []capability.Card {
	return []capability.Card{{
		Name: "links-list", Title: "Saved links", Icon: "link", W: 4, H: 3,
		Description: "The newest of a link project, to click straight through.",
		Options: []capability.AccountField{
			{Name: "projectId", Label: "Project", Type: "project", Required: true},
			{Name: "tag", Label: "Only this tag", Type: "text"},
		},
	}}
}

func (Capability) Offers(ctx context.Context, env *capability.Env, p *model.Project) []capability.Offer {
	return []capability.Offer{{
		Card: "links-list", Title: "Saved links", Icon: "link", Detail: "the newest first", W: 4, H: 3,
		Options: map[string]any{"projectId": p.ID.String(), "title": p.Title},
	}}
}

func read(ctx context.Context, env *capability.Env, p *model.Project) List {
	out := List{Links: []Link{}}
	body, err := env.Files.ReadLocal(ctx, p, File)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(body, &out)
	if out.Links == nil {
		out.Links = []Link{}
	}
	sort.Slice(out.Links, func(i, j int) bool { return out.Links[i].AddedAt.After(out.Links[j].AddedAt) })
	return out
}

func write(ctx context.Context, env *capability.Env, p *model.Project, l List, author, email, why string) error {
	body, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	_, err = env.Files.Write(ctx, p, File, append(body, '\n'), files.Op{
		Author: author, Email: email, Message: why, Commit: true,
	})
	return err
}

// Routes are mounted under /api/projects/:project/links
func (Capability) Routes(env *capability.Env, r fiber.Router) {
	r.Get("/", func(c *fiber.Ctx) error {
		if err := capability.RequireRead(c); err != nil {
			return err
		}
		return c.JSON(read(c.UserContext(), env, capability.Project(c)))
	})

	r.Post("/", func(c *fiber.Ctx) error {
		if err := capability.RequireWrite(c); err != nil {
			return err
		}
		var in Link
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The link could not be read.")
		}
		in.URL = strings.TrimSpace(in.URL)
		if in.URL == "" {
			return httpx.BadRequest("A link needs an address.")
		}
		if !strings.Contains(in.URL, "://") {
			in.URL = "https://" + in.URL
		}
		parsed, err := url.Parse(in.URL)
		if err != nil || parsed.Host == "" {
			return httpx.BadRequest("%q is not an address.", in.URL)
		}
		if strings.TrimSpace(in.Title) == "" {
			// A name that is better than nothing: the site it points at.
			in.Title = strings.TrimPrefix(parsed.Host, "www.")
		}
		in.ID = uuid.NewString()
		in.AddedAt = time.Now().UTC()

		p := capability.Project(c)
		list := read(c.UserContext(), env, p)
		list.Links = append([]Link{in}, list.Links...)
		author, email := capability.AuthorOf(c)
		if err := write(c.UserContext(), env, p, list, author, email, "Keep "+in.Title); err != nil {
			return httpx.Internal("the link could not be saved").WithCause(err)
		}
		return c.Status(fiber.StatusCreated).JSON(in)
	})

	r.Patch("/:id", func(c *fiber.Ctx) error {
		if err := capability.RequireWrite(c); err != nil {
			return err
		}
		var in struct {
			Title *string   `json:"title"`
			Note  *string   `json:"note"`
			Tags  *[]string `json:"tags"`
			Done  *bool     `json:"done"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The change could not be read.")
		}
		p := capability.Project(c)
		list := read(c.UserContext(), env, p)
		found := false
		for i := range list.Links {
			if list.Links[i].ID != c.Params("id") {
				continue
			}
			found = true
			if in.Title != nil {
				list.Links[i].Title = *in.Title
			}
			if in.Note != nil {
				list.Links[i].Note = *in.Note
			}
			if in.Tags != nil {
				list.Links[i].Tags = *in.Tags
			}
			if in.Done != nil {
				list.Links[i].Done = *in.Done
			}
		}
		if !found {
			return httpx.NotFound("There is no such link.")
		}
		author, email := capability.AuthorOf(c)
		if err := write(c.UserContext(), env, p, list, author, email, "Change a link"); err != nil {
			return httpx.Internal("the link could not be saved").WithCause(err)
		}
		return c.JSON(list)
	})

	r.Delete("/:id", func(c *fiber.Ctx) error {
		if err := capability.RequireWrite(c); err != nil {
			return err
		}
		p := capability.Project(c)
		list := read(c.UserContext(), env, p)
		kept := make([]Link, 0, len(list.Links))
		for _, l := range list.Links {
			if l.ID != c.Params("id") {
				kept = append(kept, l)
			}
		}
		if len(kept) == len(list.Links) {
			return httpx.NotFound("There is no such link.")
		}
		list.Links = kept
		author, email := capability.AuthorOf(c)
		if err := write(c.UserContext(), env, p, list, author, email, "Drop a link"); err != nil {
			return httpx.Internal("the link could not be removed").WithCause(err)
		}
		return httpx.OK(c)
	})
}

func (Capability) Exports(ctx context.Context, env *capability.Env, p *model.Project) ([]store.VariableInput, error) {
	list := read(ctx, env, p)
	open := 0
	for _, l := range list.Links {
		if !l.Done {
			open++
		}
	}
	return []store.VariableInput{
		{Name: "links", Type: "number", Value: len(list.Links), Source: "capability:links", Reported: true},
		{Name: "open", Type: "number", Value: open, Source: "capability:links"},
	}, nil
}

var _ = fmt.Sprintf

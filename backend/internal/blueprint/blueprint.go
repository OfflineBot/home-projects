// Package blueprint is the shape of the server as one JSON document: which
// groups exist, which projects hang in them, what each project can do, and
// which links run between them.
//
// It is the shape, not the contents. Files live in git and in the zip download;
// what is here is the arrangement — the thing that is tedious to rebuild by
// hand and easy to describe.
//
// Two rules it keeps:
//
//   - No secrets. Not a password, not a hash, not a token. A group that is
//     password-protected says so and arrives without one, because a password in
//     an export is a password in whatever the export is pasted into.
//   - Import never deletes. It creates what is missing and updates what it
//     recognises by its address. Removing something stays a deliberate act with
//     a dialog in front of it.
package blueprint

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/store"
)

// Version is the document's own version, so an older file stays readable.
const Version = 1

type Document struct {
	Version    int       `json:"version"`
	ExportedAt time.Time `json:"exportedAt"`
	// Note is for whoever opens the file without knowing what it is.
	Note string `json:"note,omitempty"`

	Groups    []Group   `json:"groups"`
	Ungrouped []Project `json:"ungrouped,omitempty"`
	Links     []Link    `json:"links,omitempty"`
	// Filters are not owned by a group, so they travel beside them; a project
	// refers to one by its address.
	Filters []Filter `json:"filters,omitempty"`
	// Boards are the pages somebody built out of all this. They point at
	// projects by address rather than by id, so they still mean something on
	// the other server.
	Boards []Board `json:"boards,omitempty"`
	// Accounts travel without their passwords. What is described is where an
	// account points and what it is called, so the other side is one password
	// away from working rather than a form away.
	Accounts []Account `json:"accounts,omitempty"`
}

// Filter is the pattern itself. Where a project sends what it matches is said
// at the project, not here.
type Filter struct {
	Slug        string          `json:"slug"`
	Title       string          `json:"title"`
	Description string          `json:"description,omitempty"`
	Rules       json.RawMessage `json:"rules,omitempty"`
	Preview     []string        `json:"preview,omitempty"`
}

// Account is the shape of a credential and never the credential. It arrives
// needing its password, which is the same state as one whose attempt failed —
// a state the whole server already knows how to show.
type Account struct {
	Kind   string          `json:"kind"`
	Title  string          `json:"title"`
	Config json.RawMessage `json:"config,omitempty"`
}

// AttachedFilter is one filter a project uses, and where that project sends
// what it matches.
type AttachedFilter struct {
	Filter    string `json:"filter"`
	Automatic bool   `json:"automatic,omitempty"`
	Target    string `json:"target,omitempty"`
	Folder    string `json:"folder,omitempty"`
}

type Group struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Visibility  string `json:"visibility"`
	// NeedsPassword marks a group that was password-protected. The password
	// itself is not in here, so the import creates it private and says so.
	NeedsPassword    bool   `json:"needsPassword,omitempty"`
	ReadOnly         bool   `json:"readOnly,omitempty"`
	PushWithPassword bool   `json:"pushWithPassword,omitempty"`
	Pinned           bool   `json:"pinned,omitempty"`
	Archived         bool   `json:"archived,omitempty"`
	Color            string `json:"color,omitempty"`
	Icon             string `json:"icon,omitempty"`
	// SiteProject is the slug of the project served at the group's address.
	SiteProject string `json:"siteProject,omitempty"`

	Variables []Variable `json:"variables,omitempty"`
	Projects  []Project  `json:"projects,omitempty"`
}

type Project struct {
	Slug         string   `json:"slug"`
	Title        string   `json:"title"`
	Description  string   `json:"description,omitempty"`
	Preset       string   `json:"preset,omitempty"`
	Capabilities []string `json:"capabilities"`
	DefaultTab   string   `json:"defaultTab,omitempty"`
	GitTracked   bool     `json:"gitTracked,omitempty"`
	SiteRoot     string   `json:"siteRoot,omitempty"`
	// SiteSource is the project whose files this address serves, written as
	// group/project so a document stays readable and portable.
	SiteSource    string `json:"siteSource,omitempty"`
	Visibility    string `json:"visibility"`
	NeedsPassword bool   `json:"needsPassword,omitempty"`
	ReadOnly      bool   `json:"readOnly,omitempty"`
	AnonWrite     bool   `json:"anonWrite,omitempty"`
	Archived      bool   `json:"archived,omitempty"`
	Color         string `json:"color,omitempty"`
	Icon          string `json:"icon,omitempty"`

	Schedulers []Scheduler      `json:"schedulers,omitempty"`
	Filters    []AttachedFilter `json:"filters,omitempty"`
}

// Scheduler travels without its credentials. The account is named, and the
// import looks for one by that name; if there is none, the scheduler arrives
// paused and says why.
type Scheduler struct {
	Title      string          `json:"title,omitempty"`
	Kind       string          `json:"kind"`
	Schedule   string          `json:"schedule,omitempty"`
	TargetPath string          `json:"targetPath,omitempty"`
	Options    json.RawMessage `json:"options,omitempty"`
	Enabled    bool            `json:"enabled"`
	Account    string          `json:"account,omitempty"`
}

type Variable struct {
	Name   string   `json:"name"`
	Op     string   `json:"op"`
	Inputs []string `json:"inputs,omitempty"`
	Expr   string   `json:"expr,omitempty"`
	Unit   string   `json:"unit,omitempty"`
}

// Link names both ends by their address, not by an id, so a document stays
// readable and can be written by hand.
type Link struct {
	Kind   string `json:"kind"` // folder | file
	From   string `json:"from"` // project slug
	Path   string `json:"path"`
	To     string `json:"to"` // project slug
	AtPath string `json:"atPath"`
}

// ------------------------------------------------------------------- export

// What says how much of the arrangement to take along. A group set up on a
// laptop and carried to the server usually wants all of it; a group handed to
// somebody else usually wants none of the machinery.
type What struct {
	Schedulers bool
	Accounts   bool
	Filters    bool
}

// Everything is the answer for moving your own work to your own server.
func Everything() What { return What{Schedulers: true, Accounts: true, Filters: true} }

// Export writes the whole arrangement, or one group when groupSlug is given.
func Export(ctx context.Context, st *store.Store, groupSlug string) (*Document, error) {
	return ExportWhat(ctx, st, groupSlug, Everything())
}

// ExportWhat is Export with the parts named.
func ExportWhat(ctx context.Context, st *store.Store, groupSlug string, what What) (*Document, error) {
	doc := &Document{
		Version:    Version,
		ExportedAt: time.Now().UTC(),
		Note: "The shape of a home-projects server: groups, projects and the links between them. " +
			"No file contents and no passwords — those live in git and in the accounts menu.",
		Groups: []Group{},
	}

	groups, err := st.ListGroups(ctx, true)
	if err != nil {
		return nil, err
	}
	wanted := map[uuid.UUID]bool{}
	for _, g := range groups {
		if groupSlug != "" && g.Slug != groupSlug {
			continue
		}
		wanted[g.ID] = true

		entry := Group{
			Slug: g.Slug, Title: g.Title, Description: g.Description,
			Visibility: string(g.Visibility), NeedsPassword: g.HasPassword,
			ReadOnly: g.ReadOnly, PushWithPassword: g.PushWithPassword,
			Pinned: g.Pinned, Archived: g.Archived, Color: g.Color, Icon: g.Icon,
		}

		projects, err := st.ListProjects(ctx, &g.ID, false, true)
		if err != nil {
			return nil, err
		}
		for i := range projects {
			p := &projects[i]
			if g.SiteProjectID != nil && *g.SiteProjectID == p.ID {
				entry.SiteProject = p.Slug
			}
			exported, err := exportProject(ctx, st, p, what)
			if err != nil {
				return nil, err
			}
			entry.Projects = append(entry.Projects, exported)
		}

		defs, err := st.ListGroupVariables(ctx, g.ID)
		if err != nil {
			return nil, err
		}
		for _, d := range defs {
			entry.Variables = append(entry.Variables, Variable{
				Name: d.Name, Op: d.Op, Inputs: d.Inputs, Expr: d.Expr, Unit: d.Unit,
			})
		}
		doc.Groups = append(doc.Groups, entry)
	}

	if groupSlug == "" {
		loose, err := st.ListProjects(ctx, nil, true, true)
		if err != nil {
			return nil, err
		}
		for i := range loose {
			exported, err := exportProject(ctx, st, &loose[i], what)
			if err != nil {
				return nil, err
			}
			doc.Ungrouped = append(doc.Ungrouped, exported)
		}
	}

	if what.Filters {
		filters, err := st.ListFilters(ctx)
		if err != nil {
			return nil, err
		}
		used := map[string]bool{}
		for _, g := range doc.Groups {
			for _, p := range g.Projects {
				for _, f := range p.Filters {
					used[f.Filter] = true
				}
			}
		}
		for _, p := range doc.Ungrouped {
			for _, f := range p.Filters {
				used[f.Filter] = true
			}
		}
		for _, f := range filters {
			// A whole server takes all of them; one group takes the ones it uses.
			if groupSlug != "" && !used[f.Slug] {
				continue
			}
			doc.Filters = append(doc.Filters, Filter{
				Slug: f.Slug, Title: f.Title, Description: f.Description,
				Rules: f.Rules, Preview: f.Preview,
			})
		}
	}

	if what.Accounts {
		accounts, err := st.ListAccounts(ctx)
		if err != nil {
			return nil, err
		}
		named := map[string]bool{}
		for _, g := range doc.Groups {
			for _, p := range g.Projects {
				for _, s := range p.Schedulers {
					named[s.Account] = true
				}
			}
		}
		for _, a := range accounts {
			// One group takes the accounts its schedulers point at; a whole
			// server takes them all. Never the password, in either case.
			if groupSlug != "" && !named[a.Title] {
				continue
			}
			doc.Accounts = append(doc.Accounts, Account{Kind: a.Kind, Title: a.Title, Config: a.Config})
		}
	}

	if boards, err := exportBoards(ctx, st, groupSlug, groups); err == nil {
		doc.Boards = boards
	}

	// Links are only meaningful when both ends are in the document.
	inDocument := map[string]bool{}
	for _, g := range doc.Groups {
		for _, p := range g.Projects {
			inDocument[p.Slug] = true
		}
	}
	for _, p := range doc.Ungrouped {
		inDocument[p.Slug] = true
	}
	all, err := st.AllLinks(ctx)
	if err != nil {
		return nil, err
	}
	for _, l := range all {
		if !inDocument[l.SourceSlug] || !inDocument[l.TargetSlug] {
			continue
		}
		doc.Links = append(doc.Links, Link{
			Kind: l.Kind, From: l.SourceSlug, Path: l.SourcePath,
			To: l.TargetSlug, AtPath: l.TargetPath,
		})
	}
	return doc, nil
}

func exportProject(ctx context.Context, st *store.Store, p *model.Project, what What) (Project, error) {
	out := Project{
		Slug: p.Slug, Title: p.Title, Description: p.Description,
		Preset: p.Preset, Capabilities: p.Capabilities, DefaultTab: p.DefaultTab,
		GitTracked: p.GitTracked, Visibility: string(p.Visibility),
		NeedsPassword: p.HasPassword, ReadOnly: p.ReadOnly, AnonWrite: p.AnonWrite,
		Archived: p.Archived, Color: p.Color, Icon: p.Icon,
	}
	if p.SiteRoot != nil {
		out.SiteRoot = *p.SiteRoot
	}
	if p.SiteSourceID != nil {
		if src, err := st.ProjectByID(ctx, *p.SiteSourceID); err == nil {
			out.SiteSource = src.Slug
			if src.GroupSlug != "" {
				out.SiteSource = src.GroupSlug + "/" + src.Slug
			}
		}
	}
	if out.Capabilities == nil {
		out.Capabilities = []string{}
	}

	if what.Schedulers {
		schedulers, err := st.ListSchedulersForProject(ctx, p.ID)
		if err != nil {
			return out, err
		}
		for _, s := range schedulers {
			out.Schedulers = append(out.Schedulers, Scheduler{
				Title: s.Title, Kind: s.Kind, Schedule: s.Schedule, TargetPath: s.TargetPath,
				Options: s.Options, Enabled: s.Enabled, Account: s.AccountName,
			})
		}
	}
	if what.Filters {
		attached, err := st.FiltersForProject(ctx, p.ID)
		if err != nil {
			return out, err
		}
		for _, f := range attached {
			out.Filters = append(out.Filters, AttachedFilter{
				Filter: f.Slug, Automatic: f.Automatic,
				Target: f.TargetProject, Folder: f.TargetFolder,
			})
		}
	}
	return out, nil
}

// -------------------------------------------------------------------- import

// Step is one thing the import would do, or did.
type Step struct {
	Action string `json:"action"` // create | update | skip
	What   string `json:"what"`   // group | project | link | variable | scheduler
	Name   string `json:"name"`
	Note   string `json:"note,omitempty"`
}

type Result struct {
	Steps []Step `json:"steps"`
	// DryRun says whether any of it happened.
	DryRun   bool     `json:"dryRun"`
	Warnings []string `json:"warnings,omitempty"`
}

func (r *Result) add(action, what, name, note string) {
	r.Steps = append(r.Steps, Step{Action: action, What: what, Name: name, Note: note})
}

// Validate reads a document and complains about what could not be applied,
// before anything is.
func Validate(doc *Document, knownCapability func(string) bool, knownPreset func(string) bool) error {
	if doc.Version > Version {
		return fmt.Errorf("this document is version %d, and this server understands %d", doc.Version, Version)
	}
	seen := map[string]string{}
	for _, g := range doc.Groups {
		if g.Slug == "" {
			return fmt.Errorf("a group has no address")
		}
		for _, p := range g.Projects {
			if p.Slug == "" {
				return fmt.Errorf("a project in %q has no address", g.Slug)
			}
			if where, clash := seen[p.Slug]; clash {
				return fmt.Errorf("two projects are called %q (in %s and %s) — an address is a branch name and has to be unique", p.Slug, where, g.Slug)
			}
			seen[p.Slug] = g.Slug
			for _, c := range p.Capabilities {
				if !knownCapability(c) {
					return fmt.Errorf("project %q wants a capability %q that this server does not have", p.Slug, c)
				}
			}
			if p.Preset != "" && !knownPreset(p.Preset) {
				return fmt.Errorf("project %q was created as %q, which this server does not know", p.Slug, p.Preset)
			}
		}
	}
	for _, p := range doc.Ungrouped {
		if p.Slug == "" {
			return fmt.Errorf("an ungrouped project has no address")
		}
	}
	for _, l := range doc.Links {
		if l.From == "" || l.To == "" || l.Path == "" {
			return fmt.Errorf("a link is missing one of its ends")
		}
		if l.Kind != "folder" && l.Kind != "file" {
			return fmt.Errorf("a link is neither a folder nor a file")
		}
	}
	return nil
}

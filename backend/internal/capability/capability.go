// Package capability holds the contract every capability implements and the
// registry the core knows them through.
//
// The core knows no capability names. It never asks "is this a calendar
// project?", only "does this project have a capability that says it owns this
// file / contributes this scheduler kind / exports these variables?". Adding a
// capability means: one folder, one registry line. Deleting one means removing
// exactly that — and the server still builds and runs.
package capability

import (
	"context"
	"io/fs"
	"sort"
	"sync"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/config"
	"github.com/offlinebot/home-projects/backend/internal/events"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/secret"
	"github.com/offlinebot/home-projects/backend/internal/store"
)

// Env is everything a capability may use. It is deliberately small: storage,
// files, configuration and the event bus. There is no way from here into
// another capability.
type Env struct {
	Cfg   *config.Config
	Store *store.Store
	Files *files.Service
	Bus   *events.Bus
	Box   *secret.Box
	// RunScheduler lets an automation trigger a scheduler without the
	// capability having to know the scheduler package. The server fills it in.
	RunScheduler func(ctx context.Context, schedulerID uuid.UUID, trigger string) error
	// UseAccount performs exactly one attempt with a stored credential, with
	// the consequences of the single-use rule. Capabilities never touch
	// accounts any other way.
	UseAccount func(ctx context.Context, accountID uuid.UUID, fn func(secret []byte) error) error
	// Router turns a filter's name or id into the function that answers "where
	// does this belong?". It returns nil when the name is empty or unknown —
	// the caller then puts things where it was going to anyway.
	Router func(ctx context.Context, nameOrID string) func([]RouteItem) []RouteTo
	// SortProject runs the filters a project has picked up and marked
	// automatic, over its own files. The core fills it in; nothing here knows
	// what a filter is.
	SortProject func(ctx context.Context, p *model.Project) (int, error)
}

// Preset is the typed way into creating a project. A preset only ever sets
// the icon, the default tab and which capabilities start switched on, plus the
// files it seeds. It never drives permissions, storage or queries.
type Preset struct {
	Key          string   `json:"key"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Icon         string   `json:"icon"`
	DefaultTab   string   `json:"defaultTab"`
	Capabilities []string `json:"capabilities"`
	// SiteRoot is set on the project when the preset publishes a folder.
	SiteRoot string     `json:"siteRoot,omitempty"`
	Seed     []SeedFile `json:"-"`
}

type SeedFile struct {
	Path    string
	Content func(p *model.Project) []byte
}

// SchedulerKind is one entry in the "what can a scheduler do" registry.
type SchedulerKind struct {
	Name string `json:"name"`
	// Title and Description are what the UI shows when picking a kind.
	Title       string `json:"title"`
	Description string `json:"description"`
	// AccountKinds names the account kinds this scheduler may use; empty means
	// it cannot use one at all.
	AccountKinds []string `json:"accountKinds"`
	// AccountRequired marks the kinds that cannot run without credentials. An
	// ICS subscription to a public URL needs none; a Dualis fetch does.
	AccountRequired bool `json:"accountRequired"`
	// Options describes what this kind can be told, so the UI can ask for it
	// without knowing which kind it is looking at.
	Options []AccountField `json:"options,omitempty"`
	// Run does the work. It writes files through Env.Files and returns a short
	// report for the run log.
	Run func(ctx context.Context, env *Env, job Job) (Report, error) `json:"-"`
}

// Job is one scheduler run.
type Job struct {
	Scheduler *model.Scheduler
	Project   *model.Project
	// Account is nil when the scheduler needs no credentials. Secret is the
	// decrypted credential — it was reserved before this call and counts as
	// used unless the run reports an unambiguous success.
	Account *model.Account
	Secret  []byte
	Options map[string]any
	// Trigger is why this run is happening: "schedule", "manual",
	// "automation" — or "rebuild", which asks for the target to be made to
	// match the source exactly rather than added to.
	Trigger string
	// Route answers "where does this belong?" when the scheduler points at a
	// filter. It is nil when it points at none, and the run then puts
	// everything where the scheduler itself points. A capability never learns
	// what a filter is — only that something can answer this question.
	Route func([]RouteItem) []RouteTo
	Log   func(format string, args ...any)
}

type Report struct {
	Message      string
	FilesChanged int
	// Authenticated must be set by any run that used a credential and got an
	// unambiguous "signed in". Without it the credential counts as used up.
	Authenticated bool
	Variables     []store.VariableInput
}

// AccountField describes one input in the accounts menu.
type AccountField struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Type        string `json:"type"` // text | url | password | number | bool
	Placeholder string `json:"placeholder,omitempty"`
	Required    bool   `json:"required"`
	// Default is what the field means when nothing was said. It is sent to the
	// dialog so the box on screen shows what the server will actually do — a
	// tick-box that reads "off" while the code treats it as "on" is how a
	// scheduler quietly skips eighteen courses.
	Default any `json:"default,omitempty"`
	// Hint explains the consequence, not the field.
	Hint string `json:"hint,omitempty"`
	// Options make the field a choice instead of a line to type into.
	Options []Option `json:"options,omitempty"`
}

// Option is one entry of a field that is a choice.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Card is one kind of card a board can hold.
//
// The core knows a handful — a piece of text, a link, a number — and every
// capability may offer its own. That is the whole of how a board stays modular:
// nothing about boards is listed in the core except the fact that cards exist.
type Card struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Icon        string `json:"icon"`
	Description string `json:"description,omitempty"`
	// Options are the questions the "add a card" dialog asks. The same shape
	// as an account's fields, because it is the same job.
	Options []AccountField `json:"options,omitempty"`
	// W and H are how big it starts, on a twelve-column grid.
	W int `json:"w,omitempty"`
	H int `json:"h,omitempty"`
	// From names the capability it came from, filled in by the registry.
	From string `json:"from,omitempty"`
}

// Offer is one thing a project can put on a board, ready to place: the card
// kind, what to call it, and the options already filled in.
//
// This is what makes adding something to a board a matter of "which project,
// and what of it" instead of "which kind of card, and now fill in a form". A
// person knows they want the average out of their grades project; they should
// not have to know that the average is a "number" card pointing at a variable.
type Offer struct {
	Card    string         `json:"card"`
	Title   string         `json:"title"`
	Icon    string         `json:"icon,omitempty"`
	Detail  string         `json:"detail,omitempty"`
	Options map[string]any `json:"options"`
	W       int            `json:"w,omitempty"`
	H       int            `json:"h,omitempty"`
}

// coreCards are the ones that belong to nothing in particular: they show what
// any project reports, or nothing at all.
var coreCards = []Card{
	{Name: "text", Title: "Text", Icon: "notebook", W: 4, H: 2,
		Description: "A note in your own words. Markdown.",
		Options: []AccountField{
			{Name: "text", Label: "The text", Type: "textarea"},
		}},
	{Name: "link", Title: "Links", Icon: "link", W: 3, H: 2,
		Description: "A handful of addresses, inside this server or out.",
		Options: []AccountField{
			{Name: "links", Label: "One per line: title | address", Type: "textarea",
				Placeholder: "Moodle | https://moodle.dhbw-ravensburg.de"},
		}},
	{Name: "image", Title: "A picture", Icon: "camera", W: 4, H: 3,
		Description: "A picture from an address, filling its card or fitted into it.",
		Options: []AccountField{
			{Name: "url", Label: "Address", Type: "url", Required: true, Placeholder: "https://…/photo.jpg"},
			{Name: "fit", Label: "How it sits", Type: "select", Options: []Option{
				{Value: "cover", Label: "fills the card, cropped"},
				{Value: "contain", Label: "whole picture, letterboxed"},
			}},
			{Name: "link", Label: "Goes to", Type: "url", Hint: "Optional: pressing it opens this."},
		}},
	{Name: "clock", Title: "The time", Icon: "clock", W: 3, H: 2,
		Description: "The time and the date, as they are now.",
		Options: []AccountField{
			{Name: "title", Label: "Name", Type: "text", Placeholder: "Zuhause"},
			{Name: "seconds", Label: "With seconds", Type: "select", Options: []Option{
				{Value: "no", Label: "no"}, {Value: "yes", Label: "yes"},
			}},
		}},
	{Name: "spacer", Title: "A gap", Icon: "more", W: 12, H: 1,
		Description: "Nothing, with a line through it or without — for giving a page room to breathe.",
		Options: []AccountField{
			{Name: "line", Label: "With a line", Type: "select", Options: []Option{
				{Value: "no", Label: "no"}, {Value: "yes", Label: "yes"},
			}},
		}},
	{Name: "embed", Title: "Another page", Icon: "globe", W: 6, H: 5,
		Description: "Somebody else's page, in a frame of its own. It cannot reach this one.",
		Options: []AccountField{
			{Name: "url", Label: "Address", Type: "url", Required: true, Placeholder: "https://…"},
		}},
	{Name: "view", Title: "A project, right here", Icon: "box", W: 6, H: 5,
		Description: "One of a project's views on the board itself — its mail, its calendar, its files.",
		Options: []AccountField{
			{Name: "projectId", Label: "Project", Type: "project", Required: true},
			{Name: "view", Label: "Which view", Type: "text", Required: true,
				Hint: "The capability's name: mail, calendar, files, machines…"},
		}},
	{Name: "html", Title: "Your own HTML", Icon: "code", W: 6, H: 4,
		Description: "A piece of a page, written by hand. Part of the board, or a frame of its own.",
		Options: []AccountField{
			{Name: "html", Label: "HTML", Type: "code"},
			{Name: "mode", Label: "Shown", Type: "select",
				Options: []Option{
					{Value: "inline", Label: "as part of the board — it takes the theme"},
					{Value: "frame", Label: "in a frame of its own — your CSS, your scripts"},
				}},
		}},
	{Name: "heading", Title: "Heading", Icon: "flag", W: 12, H: 1,
		Description: "A line to divide the board into parts.",
		Options:     []AccountField{{Name: "title", Label: "The heading", Type: "text"}}},
	{Name: "project", Title: "A project", Icon: "box", W: 3, H: 2,
		Description: "The way straight into a project, with the numbers it reports.",
		Options:     []AccountField{{Name: "projectId", Label: "Project", Type: "project", Required: true}}},
	{Name: "number", Title: "A number", Icon: "grid", W: 2, H: 2,
		Description: "One value a project reports, large enough to read across the room.",
		Options:     []AccountField{{Name: "variable", Label: "Variable", Type: "variable", Required: true}}},
	{Name: "history", Title: "A number over time", Icon: "zap", W: 4, H: 2,
		Description: "The same value, as a small graph.",
		Options:     []AccountField{{Name: "variable", Label: "Variable", Type: "variable", Required: true}}},
	{Name: "status", Title: "A status light", Icon: "circle", W: 2, H: 1,
		Description: "On or off, as a dot — for the things that are one or the other.",
		Options:     []AccountField{{Name: "variable", Label: "Variable", Type: "variable", Required: true}}},
	{Name: "list", Title: "A list", Icon: "notebook", W: 4, H: 3,
		Description: "A value that is several things.",
		Options:     []AccountField{{Name: "variable", Label: "Variable", Type: "variable", Required: true}}},
}

// AllCards is every kind of card there is: the core's and the capabilities'.
func AllCards() []Card {
	out := append([]Card{}, coreCards...)
	for _, c := range All() {
		for _, card := range c.Cards() {
			card.From = c.Name()
			out = append(out, card)
		}
	}
	return out
}

func CardExists(name string) bool {
	for _, card := range AllCards() {
		if card.Name == name {
			return true
		}
	}
	return false
}

// AccountKind is one entry in the accounts menu. Credentials live there and
// nowhere else — never on a project, because a project gets versioned, linked,
// published and cloned.
type AccountKind struct {
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Fields      []AccountField `json:"fields"`
	// SecretLabel names the one secret this kind stores. Empty means the kind
	// needs no secret at all.
	SecretLabel string `json:"secretLabel,omitempty"`
	// Locks marks kinds whose remote side locks the account after failed
	// attempts. It is only a hint for the UI — the single-use rule applies to
	// every password kind regardless.
	Locks bool `json:"locks"`
	// SecretIsKey marks kinds whose secret is a key rather than a password —
	// an SSH private key cannot be "used up" by a failed connection, and no
	// remote side locks an account over it. Everything else is single-use.
	SecretIsKey bool `json:"secretIsKey,omitempty"`
	// Precheck looks at everything that can be wrong *without* the credential —
	// an address that is not what it claims to be, a service switched off at
	// the other end. It runs before the credential is reserved, so finding out
	// that a URL was pasted wrong does not cost a password.
	Precheck func(ctx context.Context, env *Env, a *model.Account) error `json:"-"`
	// Providers are ready-made answers for the fields above: "Gmail" fills in
	// the servers and the ports, and what is left is the user name. Nobody
	// should have to look up a port number to read their mail.
	Providers []Provider `json:"providers,omitempty"`
	// Test performs exactly one sign-in attempt. Returning nil means an
	// unambiguous success; anything else consumes the credential.
	Test func(ctx context.Context, env *Env, a *model.Account, secret []byte) error `json:"-"`
}

// Action is one entry in the automation action registry.
type Action struct {
	Name        string                                                                    `json:"name"`
	Title       string                                                                    `json:"title"`
	Description string                                                                    `json:"description"`
	Params      []string                                                                  `json:"params"`
	Run         func(ctx context.Context, env *Env, in ActionInput) (ActionResult, error) `json:"-"`
}

type ActionInput struct {
	Project *model.Project
	Params  map[string]any
	// Previous is the result of the action before this one in the chain.
	Previous *ActionResult
	Log      func(format string, args ...any)
}

type ActionResult struct {
	Output    string
	Value     any
	Variables []store.VariableInput
}

// Capability is the contract from section 11 of the brief.
type Capability interface {
	// Name is what the project's capability list stores.
	Name() string
	// Title and Icon are for the UI.
	Title() string
	Icon() string
	// Owns lists the file patterns that belong to this capability.
	Owns() []string
	// Index turns a file into whatever index rows the capability keeps. The
	// file stays the truth; the index is rebuildable at any time.
	Index(ctx context.Context, env *Env, p *model.Project, path string) error
	// Routes mounts the capability's own endpoints under
	// /api/projects/:project/<name>/…
	Routes(env *Env, r fiber.Router)
	// SharedRoutes mounts endpoints that are not about a single project,
	// under /api/capabilities/<name>/… — a calendar overlay across several
	// projects lives there. The core mounts them without knowing any name.
	SharedRoutes(env *Env, r fiber.Router)
	// Exports is what the capability offers the dashboard.
	Exports(ctx context.Context, env *Env, p *model.Project) ([]store.VariableInput, error)
	// SchedulerKinds, AccountKinds and Actions extend those three registries.
	SchedulerKinds() []SchedulerKind
	AccountKinds() []AccountKind
	Actions() []Action
	// Presets the capability contributes to the create dialog.
	Presets() []Preset
	// Cards the capability offers to a board.
	Cards() []Card
	// Offers is what *this* project can put on a board — one entry per machine,
	// per rule, per anything the capability holds. Nothing generic: the
	// person picks the thing itself.
	Offers(ctx context.Context, env *Env, p *model.Project) []Offer
	// Migrations are applied under the capability's own namespace. nil is fine.
	Migrations() fs.FS
}

// ---------------------------------------------------------------- registry

var (
	mu       sync.RWMutex
	registry = map[string]Capability{}
	order    []string
)

// Register is the one line a capability adds to make itself known.
func Register(c Capability) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := registry[c.Name()]; exists {
		panic("capability registered twice: " + c.Name())
	}
	registry[c.Name()] = c
	order = append(order, c.Name())
	sort.Strings(order)
}

func Get(name string) (Capability, bool) {
	mu.RLock()
	defer mu.RUnlock()
	c, ok := registry[name]
	return c, ok
}

func All() []Capability {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Capability, 0, len(order))
	for _, name := range order {
		out = append(out, registry[name])
	}
	return out
}

func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, len(order))
	copy(out, order)
	return out
}

// Exists reports whether a capability name is known — used when a project's
// capability list is edited. A name nobody registered is refused instead of
// being stored and silently ignored.
func Exists(name string) bool {
	_, ok := Get(name)
	return ok
}

// Presets collects the create dialog's options: the plain one the core always
// offers, plus everything the registered capabilities contribute.
func Presets() []Preset {
	out := []Preset{{
		Key:          "data",
		Title:        "Data / Repo",
		Description:  "A plain project: files and folders, versioned on request.",
		Icon:         "box",
		DefaultTab:   "files",
		Capabilities: []string{},
	}}
	for _, c := range All() {
		out = append(out, c.Presets()...)
	}
	return out
}

func PresetByKey(key string) (Preset, bool) {
	for _, p := range Presets() {
		if p.Key == key {
			return p, true
		}
	}
	return Preset{}, false
}

// SchedulerKinds gathers the kinds every capability brings.
func SchedulerKinds() []SchedulerKind {
	out := []SchedulerKind{}
	for _, c := range All() {
		out = append(out, c.SchedulerKinds()...)
	}
	return out
}

func SchedulerKindByName(name string) (SchedulerKind, bool) {
	for _, k := range SchedulerKinds() {
		if k.Name == name {
			return k, true
		}
	}
	return SchedulerKind{}, false
}

// Actions gathers the automation actions every capability brings.
func Actions() []Action {
	out := []Action{}
	for _, c := range All() {
		out = append(out, c.Actions()...)
	}
	return out
}

// AccountKinds gathers the account kinds every capability brings.
func AccountKinds() []AccountKind {
	out := []AccountKind{}
	for _, c := range All() {
		out = append(out, c.AccountKinds()...)
	}
	return out
}

func AccountKindByName(name string) (AccountKind, bool) {
	for _, k := range AccountKinds() {
		if k.Name == name {
			return k, true
		}
	}
	return AccountKind{}, false
}

func ActionByName(name string) (Action, bool) {
	for _, a := range Actions() {
		if a.Name == name {
			return a, true
		}
	}
	return Action{}, false
}

// Info is what the UI needs to render tabs and the capability switches.
type Info struct {
	Name  string   `json:"name"`
	Title string   `json:"title"`
	Icon  string   `json:"icon"`
	Owns  []string `json:"owns"`
}

func Catalog() []Info {
	out := []Info{}
	for _, c := range All() {
		// A capability that owns no files says so with an empty list, not with
		// null: everything on the other side treats this as a list, and one
		// null is enough to take a page down.
		owns := c.Owns()
		if owns == nil {
			owns = []string{}
		}
		out = append(out, Info{Name: c.Name(), Title: c.Title(), Icon: c.Icon(), Owns: owns})
	}
	return out
}

// Base is embedded by capabilities so they only implement what they actually
// have. A capability that brings no schedulers writes no empty method.
type Base struct{}

func (Base) Owns() []string                  { return nil }
func (Base) SchedulerKinds() []SchedulerKind { return nil }
func (Base) AccountKinds() []AccountKind     { return nil }
func (Base) Cards() []Card                   { return nil }
func (Base) Offers(context.Context, *Env, *model.Project) []Offer {
	return nil
}
func (Base) Actions() []Action                     { return nil }
func (Base) Presets() []Preset                     { return nil }
func (Base) Migrations() fs.FS                     { return nil }
func (Base) Routes(env *Env, r fiber.Router)       {}
func (Base) SharedRoutes(env *Env, r fiber.Router) {}
func (Base) Index(ctx context.Context, env *Env, p *model.Project, path string) error {
	return nil
}
func (Base) Exports(ctx context.Context, env *Env, p *model.Project) ([]store.VariableInput, error) {
	return nil, nil
}

// RouteItem is what a capability knows about the thing it is placing.
type RouteItem struct {
	Name     string
	Path     string
	Semester int
}

// RouteTo is the answer, one per item in the same order. Matched false means
// no rule claimed it.
type RouteTo struct {
	Project string
	Folder  string
	Skip    bool
	Matched bool
	Rule    string
}

// Provider is one ready-made filling of an account kind's fields.
type Provider struct {
	Name   string            `json:"name"`
	Title  string            `json:"title"`
	Fields map[string]string `json:"fields"`
	// Note says what the provider expects that a form cannot fill in — an app
	// password, a setting to switch on first.
	Note string `json:"note,omitempty"`
}

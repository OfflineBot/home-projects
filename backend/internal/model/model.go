// Package model holds the structs the API speaks in.
//
// There is no `kind` field in here, in any struct, on purpose.
package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Visibility string

const (
	VisibilityPrivate  Visibility = "private"
	VisibilityPublic   Visibility = "public"
	VisibilityPassword Visibility = "password"
)

func (v Visibility) Valid() bool {
	switch v {
	case VisibilityPrivate, VisibilityPublic, VisibilityPassword:
		return true
	}
	return false
}

type User struct {
	ID          uuid.UUID `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"displayName"`
	TOTPEnabled bool      `json:"totpEnabled"`
	IsOwner     bool      `json:"isOwner"`
	CreatedAt   time.Time `json:"createdAt"`

	PasswordHash string `json:"-"`
	TOTPSecret   string `json:"-"`
}

type Session struct {
	ID         uuid.UUID  `json:"id"`
	UserID     uuid.UUID  `json:"-"`
	UserAgent  string     `json:"userAgent"`
	IP         string     `json:"ip"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt time.Time  `json:"lastUsedAt"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	StepUpAt   *time.Time `json:"stepUpAt,omitempty"`
	Current    bool       `json:"current"`
}

type Group struct {
	ID          uuid.UUID  `json:"id"`
	OwnerID     uuid.UUID  `json:"-"`
	Slug        string     `json:"slug"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Visibility  Visibility `json:"visibility"`
	HasPassword bool       `json:"hasPassword"`
	ReadOnly    bool       `json:"readOnly"`
	// PushWithPassword lets someone without an account push, using the
	// repository's own password. Off unless it is switched on deliberately.
	PushWithPassword bool       `json:"pushWithPassword"`
	Color            string     `json:"color"`
	Icon             string     `json:"icon"`
	SiteProjectID    *uuid.UUID `json:"siteProjectId,omitempty"`
	Pinned           bool       `json:"pinned"`
	Archived         bool       `json:"archived"`
	Position         int        `json:"position"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`

	// Filled in by the API when listing.
	ProjectCount int            `json:"projectCount"`
	CloneURL     string         `json:"cloneUrl,omitempty"`
	Projects     []ProjectBrief `json:"projects,omitempty"`

	PasswordHash string `json:"-"`
}

// ProjectBrief is the short form embedded in a group listing (id, slug, title,
// color, icon) — the tiles need no more than that.
type ProjectBrief struct {
	ID    uuid.UUID `json:"id"`
	Slug  string    `json:"slug"`
	Title string    `json:"title"`
	Color string    `json:"color"`
	Icon  string    `json:"icon"`
}

type Project struct {
	ID           uuid.UUID  `json:"id"`
	OwnerID      uuid.UUID  `json:"-"`
	GroupID      *uuid.UUID `json:"groupId,omitempty"`
	GroupSlug    string     `json:"groupSlug,omitempty"`
	GroupTitle   string     `json:"groupTitle,omitempty"`
	Slug         string     `json:"slug"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	Capabilities []string   `json:"capabilities"`
	Preset       string     `json:"preset"`
	DefaultTab   string     `json:"defaultTab"`
	GitTracked   bool       `json:"gitTracked"`
	SiteRoot     *string    `json:"siteRoot,omitempty"`
	// SiteSourceID names the project whose files are served, when that is not
	// this one. A site is an address and a folder; the material can live
	// wherever it is written, pulled or linked into.
	SiteSourceID *uuid.UUID `json:"siteSourceId,omitempty"`
	Visibility   Visibility `json:"visibility"`
	HasPassword  bool       `json:"hasPassword"`
	ReadOnly     bool       `json:"readOnly"`
	AnonWrite    bool       `json:"anonWrite"`
	Color        string     `json:"color"`
	Icon         string     `json:"icon"`
	Archived     bool       `json:"archived"`
	Position     int        `json:"position"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`

	SiteURL  string `json:"siteUrl,omitempty"`
	CloneURL string `json:"cloneUrl,omitempty"`

	PasswordHash string `json:"-"`
}

// EffectiveReadOnly folds in the group's freeze and the archive flag: an
// archived project is silently read-only, an archived or frozen group freezes
// everything inside it.
func (p *Project) EffectiveReadOnly(g *Group) bool {
	if p.ReadOnly || p.Archived {
		return true
	}
	if g != nil && (g.ReadOnly || g.Archived) {
		return true
	}
	return false
}

// Has reports whether a capability is switched on. Every question about what a
// project can do goes through here — never through the preset.
func (p *Project) Has(capability string) bool {
	for _, c := range p.Capabilities {
		if c == capability {
			return true
		}
	}
	return false
}

type Link struct {
	ID            uuid.UUID `json:"id"`
	Kind          string    `json:"kind"` // "folder" | "file"
	SourceProject uuid.UUID `json:"sourceProject"`
	SourceSlug    string    `json:"sourceSlug,omitempty"`
	SourceTitle   string    `json:"sourceTitle,omitempty"`
	SourcePath    string    `json:"sourcePath"`
	TargetProject uuid.UUID `json:"targetProject"`
	TargetSlug    string    `json:"targetSlug,omitempty"`
	TargetPath    string    `json:"targetPath"`
	CreatedAt     time.Time `json:"createdAt"`
}

type Token struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	Scope      string     `json:"scope"`
	ProjectID  *uuid.UUID `json:"projectId,omitempty"`
	GroupID    *uuid.UUID `json:"groupId,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
	// Only ever set once, in the response that created it.
	Secret string `json:"secret,omitempty"`
}

type Account struct {
	ID        uuid.UUID       `json:"id"`
	Kind      string          `json:"kind"`
	Title     string          `json:"title"`
	Config    json.RawMessage `json:"config"`
	State     string          `json:"state"`
	HasSecret bool            `json:"hasSecret"`
	// NeedsSecret is what the UI turns into "Enter password again".
	NeedsSecret     bool       `json:"needsSecret"`
	AttemptInFlight bool       `json:"attemptInFlight"`
	ConsumedAt      *time.Time `json:"consumedAt,omitempty"`
	LastOKAt        *time.Time `json:"lastOkAt,omitempty"`
	LastError       string     `json:"lastError"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`

	SchedulerCount int `json:"schedulerCount"`
}

type Scheduler struct {
	ID          uuid.UUID  `json:"id"`
	ProjectID   uuid.UUID  `json:"projectId"`
	ProjectSlug string     `json:"projectSlug,omitempty"`
	AccountID   *uuid.UUID `json:"accountId,omitempty"`
	AccountName string     `json:"accountName,omitempty"`
	// FilterID names the rules this scheduler's results are run through.
	FilterID   *uuid.UUID      `json:"filterId,omitempty"`
	FilterName string          `json:"filterName,omitempty"`
	Title      string          `json:"title"`
	Kind       string          `json:"kind"`
	Schedule   string          `json:"schedule"`
	TargetPath string          `json:"targetPath"`
	Options    json.RawMessage `json:"options"`
	Enabled    bool            `json:"enabled"`
	PausedFor  string          `json:"pausedReason"`
	LastRunAt  *time.Time      `json:"lastRunAt,omitempty"`
	LastStatus string          `json:"lastStatus"`
	CreatedAt  time.Time       `json:"createdAt"`
	UpdatedAt  time.Time       `json:"updatedAt"`
}

type SchedulerRun struct {
	ID           int64      `json:"id"`
	SchedulerID  uuid.UUID  `json:"schedulerId"`
	StartedAt    time.Time  `json:"startedAt"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
	Status       string     `json:"status"`
	Message      string     `json:"message"`
	FilesChanged int        `json:"filesChanged"`
	Trigger      string     `json:"trigger"`
	Log          string     `json:"log"`
}

type Variable struct {
	ProjectID   uuid.UUID       `json:"projectId"`
	ProjectSlug string          `json:"projectSlug,omitempty"`
	Name        string          `json:"name"`
	Type        string          `json:"type"`
	Value       json.RawMessage `json:"value"`
	Unit        string          `json:"unit"`
	Source      string          `json:"source"`
	Error       string          `json:"error,omitempty"`
	TTLSeconds  int             `json:"ttlSeconds"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

// QualifiedName is how a variable appears in its group: <project-slug>.<name>.
func (v Variable) QualifiedName() string {
	if v.ProjectSlug == "" {
		return v.Name
	}
	return v.ProjectSlug + "." + v.Name
}

type GroupVariable struct {
	ID      uuid.UUID `json:"id"`
	GroupID uuid.UUID `json:"groupId"`
	Name    string    `json:"name"`
	Op      string    `json:"op"`
	Inputs  []string  `json:"inputs"`
	Expr    string    `json:"expr"`
	Unit    string    `json:"unit"`
}

type DashboardTile struct {
	ID        uuid.UUID       `json:"id"`
	GroupID   uuid.UUID       `json:"groupId"`
	GroupSlug string          `json:"groupSlug,omitempty"`
	Variable  string          `json:"variable"`
	Title     string          `json:"title"`
	Kind      string          `json:"kind"`
	Options   json.RawMessage `json:"options"`
	X         int             `json:"x"`
	Y         int             `json:"y"`
	W         int             `json:"w"`
	H         int             `json:"h"`
}

// AuditEntry is what the security page lists.
type AuditEntry struct {
	ID        int64           `json:"id"`
	Action    string          `json:"action"`
	Subject   string          `json:"subject"`
	Detail    json.RawMessage `json:"detail"`
	IP        string          `json:"ip"`
	CreatedAt time.Time       `json:"createdAt"`
}

// Filter is a named set of rules that answers "where does this belong?". It
// lives in a menu of its own, like an account, because the same rules serve a
// scheduler and a folder of files alike.
type Filter struct {
	ID          uuid.UUID       `json:"id"`
	Slug        string          `json:"slug"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Rules       json.RawMessage `json:"rules"`
	// Preview names the projects this filter is tried against while it is
	// written. It has no effect when the filter runs.
	Preview []string `json:"preview"`
	// UsedBy counts the schedulers pointing at it, so deleting one can say
	// what it would affect.
	UsedBy    int       `json:"usedBy"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

package blueprint

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/store"
)

// Applier carries what an import needs from the rest of the server. It is a
// small set on purpose: creating a group has to create its repository, and
// creating a project has to seed the preset's files, and neither of those
// belongs in this package.
type Applier struct {
	Store   *store.Store
	OwnerID uuid.UUID

	// EnsureRepo is called for every group that is created.
	EnsureRepo func(ctx context.Context, slug, title string) error
	// SeedProject fills a freshly created project with its preset's files and
	// gives it its first commit, so the branch exists.
	SeedProject func(ctx context.Context, p *model.Project, preset string) error
	// EnsureFolder makes sure a link has somewhere to appear.
	EnsureFolder func(ctx context.Context, p *model.Project, dir string) error
	// ReloadSchedulers rebuilds the schedule once everything is in place.
	ReloadSchedulers func(ctx context.Context) error

	// planned holds the projects this run would create. A dry run has to know
	// them, or it would report every link into a not-yet-created project as
	// impossible — and a plan that misreports its own consequences is worse
	// than no plan.
	planned map[string]bool
}

// Apply creates what is missing and updates what it recognises by address. It
// never deletes: a document that no longer mentions something leaves that
// something alone.
func (a *Applier) Apply(ctx context.Context, doc *Document, dryRun bool) (*Result, error) {
	result := &Result{DryRun: dryRun, Steps: []Step{}}
	a.planned = map[string]bool{}

	for _, g := range doc.Groups {
		grp, err := a.applyGroup(ctx, g, dryRun, result)
		if err != nil {
			return result, err
		}
		for _, p := range g.Projects {
			if _, err := a.applyProject(ctx, p, grp, dryRun, result); err != nil {
				return result, err
			}
		}
		if err := a.applyVariables(ctx, g, grp, dryRun, result); err != nil {
			return result, err
		}
		// The site project can only be set once its project exists.
		if g.SiteProject != "" && grp != nil && !dryRun {
			if p, err := a.Store.ProjectBySlug(ctx, &grp.ID, g.SiteProject); err == nil && p.SiteRoot != nil {
				ref := &p.ID
				if _, err := a.Store.UpdateGroup(ctx, grp.ID, store.GroupPatch{SiteProjectID: &ref}); err != nil {
					return result, err
				}
			}
		}
	}

	for _, p := range doc.Ungrouped {
		if _, err := a.applyProject(ctx, p, nil, dryRun, result); err != nil {
			return result, err
		}
	}

	for _, l := range doc.Links {
		if err := a.applyLink(ctx, l, dryRun, result); err != nil {
			return result, err
		}
	}

	if !dryRun && a.ReloadSchedulers != nil {
		if err := a.ReloadSchedulers(ctx); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (a *Applier) applyGroup(ctx context.Context, g Group, dryRun bool, result *Result) (*model.Group, error) {
	existing, err := a.Store.GroupBySlug(ctx, g.Slug)
	if err == nil {
		if dryRun {
			result.add("update", "group", g.Slug, "already here — its settings are brought in line")
			return existing, nil
		}
		visibility := model.Visibility(g.Visibility)
		patch := store.GroupPatch{
			Title: &g.Title, Description: &g.Description, ReadOnly: &g.ReadOnly,
			PushWithPassword: &g.PushWithPassword, Pinned: &g.Pinned, Archived: &g.Archived,
		}
		if g.Color != "" {
			patch.Color = &g.Color
		}
		if g.Icon != "" {
			patch.Icon = &g.Icon
		}
		// A group that already carries a password keeps it, and keeps being
		// password-protected. Otherwise the visibility follows the document.
		if !(visibility == model.VisibilityPassword && !existing.HasPassword) {
			patch.Visibility = &visibility
		} else {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("%s stays as it is: the document says password-protected, and no password travels with a document", g.Slug))
		}
		updated, err := a.Store.UpdateGroup(ctx, existing.ID, patch)
		if err != nil {
			return nil, err
		}
		result.add("update", "group", g.Slug, "")
		return updated, nil
	}

	// A password-protected group arrives private: without the password nobody
	// could open it, and inventing one would be worse.
	visibility := model.Visibility(g.Visibility)
	note := ""
	if visibility == model.VisibilityPassword {
		visibility = model.VisibilityPrivate
		note = "created private — set a password to make it password-protected again"
		result.Warnings = append(result.Warnings, g.Slug+": "+note)
	}
	if dryRun {
		result.add("create", "group", g.Slug, note)
		return nil, nil
	}
	created, err := a.Store.CreateGroup(ctx, store.NewGroup{
		OwnerID: a.OwnerID, Slug: g.Slug, Title: g.Title, Description: g.Description,
		Visibility: visibility, Color: g.Color, Icon: g.Icon, Pinned: g.Pinned,
	})
	if err != nil {
		return nil, err
	}
	if a.EnsureRepo != nil {
		if err := a.EnsureRepo(ctx, created.Slug, created.Title); err != nil {
			return nil, err
		}
	}
	if g.ReadOnly || g.PushWithPassword || g.Archived {
		if _, err := a.Store.UpdateGroup(ctx, created.ID, store.GroupPatch{
			ReadOnly: &g.ReadOnly, PushWithPassword: &g.PushWithPassword, Archived: &g.Archived,
		}); err != nil {
			return nil, err
		}
	}
	result.add("create", "group", g.Slug, note)
	return created, nil
}

func (a *Applier) applyProject(ctx context.Context, p Project, grp *model.Group, dryRun bool, result *Result) (*model.Project, error) {
	var groupID *uuid.UUID
	label := p.Slug
	if grp != nil {
		groupID = &grp.ID
		label = grp.Slug + "/" + p.Slug
	}

	a.planned[p.Slug] = true

	existing, err := a.Store.ProjectBySlug(ctx, groupID, p.Slug)
	if err == nil {
		if dryRun {
			result.add("update", "project", label, "already here — its settings are brought in line")
			return existing, nil
		}
		updated, err := a.Store.UpdateProject(ctx, existing.ID, a.patchFor(ctx, p, existing))
		if err != nil {
			return nil, err
		}
		result.add("update", "project", label, "")
		if err := a.applySchedulers(ctx, p, updated, dryRun, result); err != nil {
			return nil, err
		}
		return updated, nil
	}

	visibility := model.Visibility(p.Visibility)
	note := ""
	if visibility == model.VisibilityPassword {
		visibility = model.VisibilityPrivate
		note = "created private — set a password to make it password-protected again"
	}
	if dryRun {
		result.add("create", "project", label, note)
		return nil, nil
	}

	created, err := a.Store.CreateProject(ctx, store.NewProject{
		OwnerID: a.OwnerID, GroupID: groupID, Slug: p.Slug, Title: p.Title,
		Description: p.Description, Capabilities: p.Capabilities, Preset: p.Preset,
		DefaultTab: p.DefaultTab, GitTracked: p.GitTracked, Visibility: visibility,
		Color: p.Color, Icon: p.Icon,
	})
	if err != nil {
		return nil, err
	}
	if a.SeedProject != nil {
		if err := a.SeedProject(ctx, created, p.Preset); err != nil {
			return nil, err
		}
	}
	if p.SiteRoot != "" || p.ReadOnly || p.AnonWrite || p.Archived {
		created, err = a.Store.UpdateProject(ctx, created.ID, a.patchFor(ctx, p, created))
		if err != nil {
			return nil, err
		}
	}
	result.add("create", "project", label, note)
	if err := a.applySchedulers(ctx, p, created, dryRun, result); err != nil {
		return nil, err
	}
	return created, nil
}

func (a *Applier) patchFor(ctx context.Context, p Project, existing *model.Project) store.ProjectPatch {
	visibility := model.Visibility(p.Visibility)
	patch := store.ProjectPatch{
		Title: &p.Title, Description: &p.Description, Capabilities: &p.Capabilities,
		GitTracked: &p.GitTracked, ReadOnly: &p.ReadOnly, Archived: &p.Archived,
	}
	if p.Preset != "" {
		patch.Preset = &p.Preset
	}
	if p.DefaultTab != "" {
		patch.DefaultTab = &p.DefaultTab
	}
	if p.Color != "" {
		patch.Color = &p.Color
	}
	if p.Icon != "" {
		patch.Icon = &p.Icon
	}
	root := p.SiteRoot
	if root == "" {
		var none *string
		patch.SiteRoot = &none
	} else {
		ref := &root
		patch.SiteRoot = &ref
	}
	// The project whose files this address serves. It is named, not numbered,
	// so a document written on one server means the same thing on another.
	if p.SiteSource == "" {
		var none *uuid.UUID
		patch.SiteSourceID = &none
	} else if src, err := a.findProject(ctx, p.SiteSource); err == nil && src != nil {
		ref := &src.ID
		patch.SiteSourceID = &ref
	}
	// Visitors may only write in a public project, and a password that does not
	// travel cannot be assumed.
	if !(visibility == model.VisibilityPassword && !existing.HasPassword) {
		patch.Visibility = &visibility
		anon := p.AnonWrite && visibility == model.VisibilityPublic
		patch.AnonWrite = &anon
	}
	return patch
}

func (a *Applier) applySchedulers(ctx context.Context, p Project, project *model.Project, dryRun bool, result *Result) error {
	if len(p.Schedulers) == 0 {
		return nil
	}
	existing := map[string]bool{}
	if project != nil {
		list, err := a.Store.ListSchedulersForProject(ctx, project.ID)
		if err != nil {
			return err
		}
		for _, s := range list {
			existing[s.Kind+"|"+s.Title] = true
		}
	}

	accounts, err := a.Store.ListAccounts(ctx)
	if err != nil {
		return err
	}
	byTitle := map[string]uuid.UUID{}
	for _, acc := range accounts {
		byTitle[acc.Title] = acc.ID
	}

	for _, s := range p.Schedulers {
		label := p.Slug + " · " + firstNonEmpty(s.Title, s.Kind)
		if existing[s.Kind+"|"+s.Title] {
			result.add("skip", "scheduler", label, "already there")
			continue
		}

		var accountID *uuid.UUID
		note := ""
		enabled := s.Enabled
		if s.Account != "" {
			if id, ok := byTitle[s.Account]; ok {
				accountID = &id
			} else {
				// Credentials never travel. Without the account this would run
				// into a wall, so it arrives paused and says why.
				enabled = false
				note = "paused: there is no account called " + s.Account + " here"
				result.Warnings = append(result.Warnings, label+" — "+note)
			}
		}
		if dryRun {
			result.add("create", "scheduler", label, note)
			continue
		}
		created, err := a.Store.CreateScheduler(ctx, store.NewScheduler{
			OwnerID: a.OwnerID, ProjectID: project.ID, AccountID: accountID, Title: s.Title,
			Kind: s.Kind, Schedule: s.Schedule, TargetPath: s.TargetPath,
			Options: s.Options, Enabled: enabled,
		})
		if err != nil {
			return err
		}
		if note != "" {
			reason := note
			if _, err := a.Store.UpdateScheduler(ctx, created.ID, store.SchedulerPatch{PausedFor: &reason}); err != nil {
				return err
			}
		}
		result.add("create", "scheduler", label, note)
	}
	return nil
}

func (a *Applier) applyVariables(ctx context.Context, g Group, grp *model.Group, dryRun bool, result *Result) error {
	for _, v := range g.Variables {
		label := g.Slug + "." + v.Name
		if dryRun || grp == nil {
			result.add("create", "variable", label, "")
			continue
		}
		if _, err := a.Store.CreateGroupVariable(ctx, model.GroupVariable{
			GroupID: grp.ID, Name: v.Name, Op: v.Op, Inputs: v.Inputs, Expr: v.Expr, Unit: v.Unit,
		}); err != nil {
			return err
		}
		result.add("create", "variable", label, "")
	}
	return nil
}

func (a *Applier) applyLink(ctx context.Context, l Link, dryRun bool, result *Result) error {
	label := l.From + ":" + l.Path + " → " + l.To + ":" + l.AtPath

	if dryRun {
		// Both ends have to exist by the time this runs — either already, or
		// because this same document creates them.
		for _, end := range []string{l.From, l.To} {
			if a.planned[end] {
				continue
			}
			if _, err := a.findProject(ctx, end); err != nil {
				result.add("skip", "link", label, "no project called "+end)
				result.Warnings = append(result.Warnings, label+" — no project called "+end)
				return nil
			}
		}
		result.add("create", "link", label, "")
		return nil
	}

	source, err := a.findProject(ctx, l.From)
	if err != nil {
		result.add("skip", "link", label, "no project called "+l.From)
		result.Warnings = append(result.Warnings, label+" — no project called "+l.From)
		return nil
	}
	target, err := a.findProject(ctx, l.To)
	if err != nil {
		result.add("skip", "link", label, "no project called "+l.To)
		result.Warnings = append(result.Warnings, label+" — no project called "+l.To)
		return nil
	}

	existing, err := a.Store.LinksInto(ctx, target.ID)
	if err != nil {
		return err
	}
	for _, e := range existing {
		if e.TargetPath == l.AtPath {
			result.add("skip", "link", label, "already there")
			return nil
		}
	}
	if parent := path.Dir(l.AtPath); parent != "." && parent != "" && a.EnsureFolder != nil {
		if err := a.EnsureFolder(ctx, target, parent); err != nil {
			return err
		}
	}
	if _, err := a.Store.CreateLink(ctx, a.OwnerID, l.Kind, source.ID, l.Path, target.ID, l.AtPath); err != nil {
		result.add("skip", "link", label, err.Error())
		return nil
	}
	result.add("create", "link", label, "")
	return nil
}

func (a *Applier) findProject(ctx context.Context, slug string) (*model.Project, error) {
	matches, err := a.Store.ProjectsBySlug(ctx, slug)
	if err != nil {
		return nil, err
	}
	if len(matches) != 1 {
		return nil, fmt.Errorf("%q does not name exactly one project", slug)
	}
	return &matches[0], nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

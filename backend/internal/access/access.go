// Package access answers one question: may this actor read or write this
// object? It is the only authority — the UI hiding a button is convenience,
// never the safeguard.
package access

import (
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
)

// Visibility governs each object on its own: a group's visibility applies to
// the group and to its repository, a project's to the project. A public
// project therefore stays reachable by its own address even when its group is
// private — what is hidden is the group listing, not the project.

// CanReadGroup reports whether the group may be listed and opened.
func CanReadGroup(a *auth.Actor, g *model.Group) bool {
	if a.IsUser() {
		return true
	}
	if a.Token != nil && a.Token.GroupID != nil && *a.Token.GroupID == g.ID {
		return true
	}
	switch g.Visibility {
	case model.VisibilityPublic:
		return true
	case model.VisibilityPassword:
		return a.HasUnlocked(g.ID)
	}
	return false
}

// NeedsGroupPassword separates "locked" from "does not exist", so the UI can
// ask for the password instead of showing a 404.
func NeedsGroupPassword(a *auth.Actor, g *model.Group) bool {
	return !a.IsUser() && g.Visibility == model.VisibilityPassword && !a.HasUnlocked(g.ID)
}

func CanReadProject(a *auth.Actor, p *model.Project) bool {
	if a.IsUser() {
		return true
	}
	if a.Token != nil && a.Token.ProjectID != nil && *a.Token.ProjectID == p.ID {
		return true
	}
	switch p.Visibility {
	case model.VisibilityPublic:
		return true
	case model.VisibilityPassword:
		return a.HasUnlocked(p.ID)
	}
	return false
}

func NeedsProjectPassword(a *auth.Actor, p *model.Project) bool {
	return !a.IsUser() && p.Visibility == model.VisibilityPassword && !a.HasUnlocked(p.ID)
}

// RequireReadProject returns the error the API answers with, so "locked",
// "hidden" and "not there" stay distinguishable.
func RequireReadProject(a *auth.Actor, p *model.Project) error {
	if CanReadProject(a, p) {
		return nil
	}
	if NeedsProjectPassword(a, p) {
		return httpx.New(401, "password_required", "This project is protected by a password.")
	}
	return httpx.NotFound("No project at this address.")
}

func RequireReadGroup(a *auth.Actor, g *model.Group) error {
	if CanReadGroup(a, g) {
		return nil
	}
	if NeedsGroupPassword(a, g) {
		return httpx.New(401, "password_required", "This group is protected by a password.")
	}
	return httpx.NotFound("No group at this address.")
}

// RequireWriteProject folds together every reason a write may be refused, in
// the order that produces the most useful message.
func RequireWriteProject(a *auth.Actor, p *model.Project, g *model.Group) error {
	if err := RequireReadProject(a, p); err != nil {
		return err
	}
	if p.ReadOnly {
		return httpx.ReadOnly("This project")
	}
	if p.Archived {
		return httpx.ReadOnly("This project is archived and therefore")
	}
	if g != nil && g.ReadOnly {
		return httpx.ReadOnly("The group " + g.Title)
	}
	if g != nil && g.Archived {
		return httpx.ReadOnly("The group " + g.Title + " is archived and therefore")
	}
	if a.IsUser() {
		return nil
	}
	if a.Token != nil && a.Token.ProjectID != nil && *a.Token.ProjectID == p.ID &&
		(a.Token.Scope == "write" || a.Token.Scope == "git") {
		return nil
	}
	// Visitors write only where a project explicitly allows it.
	if p.AnonWrite && p.Visibility == model.VisibilityPublic {
		return nil
	}
	return httpx.Forbidden("This project does not accept changes from visitors.")
}

// RequireWriteGroup guards the group's own settings.
func RequireWriteGroup(a *auth.Actor, g *model.Group) error {
	if !a.IsUser() {
		return httpx.Forbidden("Only the owner can change a group.")
	}
	return nil
}

// FilterGroups drops what the actor may not see. Unavailable entries are
// hidden, not greyed out.
func FilterGroups(a *auth.Actor, in []model.Group) []model.Group {
	out := make([]model.Group, 0, len(in))
	for _, g := range in {
		if CanReadGroup(a, &g) {
			out = append(out, g)
		}
	}
	return out
}

func FilterProjects(a *auth.Actor, in []model.Project) []model.Project {
	out := make([]model.Project, 0, len(in))
	for _, p := range in {
		if CanReadProject(a, &p) {
			out = append(out, p)
		}
	}
	return out
}

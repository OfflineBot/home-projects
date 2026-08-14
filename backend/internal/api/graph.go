package api

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/filter"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
)

// Projects are not islands: one links a folder out of another, an address
// serves a third's files, a scheduler writes into a fourth. None of that is
// visible until something breaks, which is the wrong moment.
//
// This is that picture, and the same knowledge answers "what happens if I move
// this?" — so both live here, built from one walk over the same edges.

type graphNode struct {
	ID       string `json:"id"`
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	Group    string `json:"group,omitempty"`
	Color    string `json:"color,omitempty"`
	Icon     string `json:"icon,omitempty"`
	External bool   `json:"external,omitempty"`
}

type graphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Kind is what the arrow means: link, serves, writes.
	Kind  string `json:"kind"`
	Label string `json:"label,omitempty"`
}

func (s *Server) mountGraph(r fiber.Router) {
	// One group's dependencies. Projects outside it that are involved come
	// along, marked, because an edge that leaves the group is exactly the one
	// worth seeing.
	r.Get("/groups/:group/graph", s.resolveGroup, func(c *fiber.Ctx) error {
		grp := groupOf(c)
		if grp == nil {
			return httpx.NotFound("No group at this address.")
		}
		nodes, edges, err := s.graphOf(c, &grp.ID)
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"nodes": nodes, "edges": edges})
	})
}

// graphOf builds the picture for one group, or for everything when groupID is
// nil.
func (s *Server) graphOf(c *fiber.Ctx, groupID *uuid.UUID) ([]graphNode, []graphEdge, error) {
	ctx := c.UserContext()
	all, err := s.Store.ListProjects(ctx, nil, false, true)
	if err != nil {
		return nil, nil, httpx.Internal("the projects could not be read").WithCause(err)
	}
	byID := map[uuid.UUID]*model.Project{}
	inGroup := map[uuid.UUID]bool{}
	for i := range all {
		p := &all[i]
		byID[p.ID] = p
		if groupID == nil || (p.GroupID != nil && *p.GroupID == *groupID) {
			inGroup[p.ID] = true
		}
	}

	nodes := map[uuid.UUID]graphNode{}
	include := func(p *model.Project) {
		if _, seen := nodes[p.ID]; seen {
			return
		}
		nodes[p.ID] = graphNode{
			ID: p.ID.String(), Slug: p.Slug, Title: p.Title, Group: p.GroupSlug,
			Color: p.Color, Icon: p.Icon, External: !inGroup[p.ID],
		}
	}
	for id := range inGroup {
		include(byID[id])
	}

	edges := []graphEdge{}
	add := func(from, to *model.Project, kind, label string) {
		if from == nil || to == nil || from.ID == to.ID {
			return
		}
		if !inGroup[from.ID] && !inGroup[to.ID] {
			return // nothing to do with this group
		}
		include(from)
		include(to)
		edges = append(edges, graphEdge{
			From: from.ID.String(), To: to.ID.String(), Kind: kind, Label: label,
		})
	}

	// A link: content of one project shown inside another.
	for id := range inGroup {
		links, err := s.Store.LinksInto(ctx, id)
		if err != nil {
			continue
		}
		for _, l := range links {
			add(byID[l.SourceProject], byID[l.TargetProject], "link", l.SourcePath)
		}
	}
	// A published address in front of another project's folder.
	for i := range all {
		p := &all[i]
		if p.SiteSourceID != nil {
			add(p, byID[*p.SiteSourceID], "serves", derefString(p.SiteRoot))
		}
	}
	// A scheduler writing into a project — its own, and whatever its filter
	// names.
	schedulers, err := s.Store.ListSchedulers(ctx)
	if err == nil {
		for _, sc := range schedulers {
			source := byID[sc.ProjectID]
			if source == nil {
				continue
			}
			label := sc.Title
			if label == "" {
				label = sc.Kind
			}
			if sc.FilterID == nil {
				continue
			}
			rules, err := filter.RulesFor(ctx, s.Store, sc.FilterID.String())
			if err != nil {
				continue
			}
			for _, r := range rules {
				to, _, _ := strings.Cut(r.To, "/")
				to = strings.TrimSpace(to)
				if to == "" || strings.EqualFold(to, "skip") {
					continue
				}
				for i := range all {
					if strings.EqualFold(all[i].Slug, to) {
						add(source, &all[i], "writes", label)
					}
				}
			}
		}
	}

	out := make([]graphNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n)
	}
	return out, edges, nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// mountMoveImpact answers the question a move should not be made without: what
// changes, and what stops working.
func (s *Server) mountMoveImpact(one fiber.Router) {
	one.Get("/move-impact", requireOwner, func(c *fiber.Ctx) error {
		p := project(c)
		ctx := c.UserContext()

		to := strings.TrimSpace(c.Query("group"))
		var target *model.Group
		if to != "" && to != "ungrouped" {
			g, err := s.findGroup(ctx, to)
			if err != nil {
				return err
			}
			target = g
		}

		type note struct {
			Level string `json:"level"` // breaks | changes | fine
			What  string `json:"what"`
		}
		notes := []note{}
		say := func(level, what string) { notes = append(notes, note{Level: level, What: what}) }

		fromSlug := p.GroupSlug
		if fromSlug == "" {
			fromSlug = "ungrouped"
		}
		toSlug := "ungrouped"
		if target != nil {
			toSlug = target.Slug
		}
		if fromSlug == toSlug {
			return c.JSON(fiber.Map{"notes": []note{{Level: "fine", What: "It is already there."}}})
		}

		// The address it is cloned from changes, because the repository is the
		// group. Anything checked out from the old one has to be re-pointed.
		say("changes", "the clone address becomes "+toSlug+"/"+p.Slug+".git — an existing checkout needs its remote changed")

		var targetID *uuid.UUID
		if target != nil {
			targetID = &target.ID
		}
		if taken, err := s.Store.ProjectSlugTaken(ctx, targetID, p.Slug); err == nil && taken {
			say("breaks", toSlug+" already has a project called "+p.Slug+" — the move is refused")
		}
		if target != nil && target.ReadOnly {
			say("breaks", target.Title+" is read-only — the move is refused")
		}

		// The group it leaves may be pointing at it.
		if p.GroupID != nil {
			if g, err := s.Store.GroupByID(ctx, *p.GroupID); err == nil &&
				g.SiteProjectID != nil && *g.SiteProjectID == p.ID {
				say("breaks", g.Title+" publishes this project at /s/"+g.Slug+"/ — that address stops working")
			}
		}

		// What travels with it, and what merely keeps working.
		if list, err := s.Store.ListSchedulersForProject(ctx, p.ID); err == nil && len(list) > 0 {
			names := make([]string, 0, len(list))
			for _, sc := range list {
				n := sc.Title
				if n == "" {
					n = sc.Kind
				}
				names = append(names, n)
			}
			say("fine", "these schedulers belong to the project and move with it: "+strings.Join(names, ", "))
		}

		if links, err := s.Store.LinksInto(ctx, p.ID); err == nil {
			for _, l := range links {
				say("fine", "the link from "+l.SourceSlug+" keeps working — links follow the project, not the group")
			}
		}
		if out, err := s.Store.LinksFrom(ctx, p.ID); err == nil {
			for _, l := range out {
				say("fine", "what it lends to "+l.TargetSlug+" keeps working")
			}
		}

		// Visibility and read-only are the group's to decide for its repository.
		if target != nil {
			if p.Visibility == model.VisibilityPrivate && target.Visibility == model.VisibilityPublic {
				say("changes", target.Title+" is public — its listing will name this project")
			}
			if target.ReadOnly && !p.ReadOnly {
				say("changes", target.Title+" is read-only, so the project becomes read-only with it")
			}
		}

		return c.JSON(fiber.Map{"notes": notes, "from": fromSlug, "to": toSlug})
	})
}

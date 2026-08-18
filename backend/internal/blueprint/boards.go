package blueprint

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/store"
)

// Boards travel with the arrangement.
//
// A board is the page somebody built, and a document that describes a server
// without it describes half of one. The catch is that a card points at things
// by id — this project, that group — and an id means nothing on the other
// server. So the ids are turned into addresses on the way out and back into ids
// on the way in; a card whose project did not come along keeps its shape and
// says it has nothing to show, which is better than a card that silently points
// at somebody else's project.

type Board struct {
	Scope string `json:"scope"`
	// Group is the group's address for a group board; empty for the front page.
	Group string     `json:"group,omitempty"`
	Tabs  []BoardTab `json:"tabs"`
}

type BoardTab struct {
	Title  string          `json:"title"`
	Icon   string          `json:"icon,omitempty"`
	Layout string          `json:"layout,omitempty"`
	Style  json.RawMessage `json:"style,omitempty"`
	Cards  []BoardCard     `json:"cards"`
}

type BoardCard struct {
	Kind       string          `json:"kind"`
	Options    json.RawMessage `json:"options,omitempty"`
	Style      json.RawMessage `json:"style,omitempty"`
	Visibility string          `json:"visibility,omitempty"`
	X          int             `json:"x"`
	Y          int             `json:"y"`
	W          int             `json:"w"`
	H          int             `json:"h"`
}

// exportBoards writes the boards of the owner that belong to what is being
// exported: the front page always, and the board of every group in the document.
func exportBoards(ctx context.Context, st *store.Store, groupSlug string, groups []model.Group) ([]Board, error) {
	owner, err := st.OwnerID(ctx)
	if err != nil {
		return nil, nil
	}
	out := []Board{}

	if groupSlug == "" {
		if home, err := st.BoardFor(ctx, owner, "home", nil, nil); err == nil {
			out = append(out, packBoard(ctx, st, "home", "", home))
		}
	}
	for i := range groups {
		g := groups[i]
		if groupSlug != "" && g.Slug != groupSlug {
			continue
		}
		if board, err := st.BoardFor(ctx, owner, "group", &g.ID, nil); err == nil {
			out = append(out, packBoard(ctx, st, "group", g.Slug, board))
		}
	}
	return out, nil
}

func packBoard(ctx context.Context, st *store.Store, scope, group string, board *model.Board) Board {
	packed := Board{Scope: scope, Group: group, Tabs: []BoardTab{}}
	for _, tab := range board.Tabs {
		out := BoardTab{Title: tab.Title, Icon: tab.Icon, Layout: tab.Layout, Style: tab.Style, Cards: []BoardCard{}}
		for _, card := range tab.Cards {
			out.Cards = append(out.Cards, BoardCard{
				Kind: card.Kind, Options: addressify(ctx, st, card.Options), Style: card.Style,
				Visibility: card.Visibility, X: card.X, Y: card.Y, W: card.W, H: card.H,
			})
		}
		packed.Tabs = append(packed.Tabs, out)
	}
	return packed
}

// addressify turns the ids a card points at into addresses, so the card still
// means the same thing on another server.
func addressify(ctx context.Context, st *store.Store, options json.RawMessage) json.RawMessage {
	var fields map[string]any
	if len(options) == 0 || json.Unmarshal(options, &fields) != nil {
		return options
	}
	if raw, ok := fields["projectId"].(string); ok {
		if id, err := uuid.Parse(raw); err == nil {
			if p, err := st.ProjectByID(ctx, id); err == nil {
				delete(fields, "projectId")
				fields["project"] = addressOfProject(p)
			}
		}
	}
	if raw, ok := fields["groupId"].(string); ok {
		if id, err := uuid.Parse(raw); err == nil {
			if g, err := st.GroupByID(ctx, id); err == nil {
				delete(fields, "groupId")
				fields["group"] = g.Slug
			}
		}
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return options
	}
	return body
}

func addressOfProject(p *model.Project) string {
	if p.GroupSlug == "" {
		return p.Slug
	}
	return p.GroupSlug + "/" + p.Slug
}

// applyBoards puts the boards back. A board that is already there is left
// alone: somebody's arrangement is not something an import overwrites.
func (a *Applier) applyBoards(ctx context.Context, doc *Document, dryRun bool, result *Result) error {
	for _, board := range doc.Boards {
		name := board.Scope
		if board.Group != "" {
			name = board.Group
		}

		var groupID *uuid.UUID
		if board.Scope == "group" {
			g, err := a.Store.GroupBySlug(ctx, board.Group)
			if err != nil {
				result.add("skip", "board", name, "there is no such group here")
				continue
			}
			groupID = &g.ID
		}

		if existing, err := a.Store.BoardFor(ctx, a.OwnerID, board.Scope, groupID, nil); err == nil {
			if hasCards(existing) {
				result.add("skip", "board", name, "there is a board here already")
				continue
			}
		}
		result.add("create", "board", name, "")
		if dryRun {
			continue
		}

		made, err := a.Store.EnsureBoard(ctx, a.OwnerID, board.Scope, groupID, nil)
		if err != nil {
			return err
		}
		for i, tab := range board.Tabs {
			id := uuid.Nil
			if i == 0 && len(made.Tabs) > 0 {
				// Every board is born with one tab; the first one that arrives
				// becomes it rather than sitting beside it.
				id = made.Tabs[0].ID
				layout := tab.Layout
				if err := a.Store.UpdateTab(ctx, id, store.TabPatch{
					Title: &tab.Title, Icon: &tab.Icon, Layout: &layout, Style: &tab.Style,
				}); err != nil {
					return err
				}
			} else {
				fresh, err := a.Store.CreateTab(ctx, made.ID, tab.Title, tab.Icon)
				if err != nil {
					return err
				}
				id = fresh.ID
				layout := tab.Layout
				if err := a.Store.UpdateTab(ctx, id, store.TabPatch{Layout: &layout, Style: &tab.Style}); err != nil {
					return err
				}
			}
			for _, card := range tab.Cards {
				if _, err := a.Store.CreateCard(ctx, model.BoardCard{
					TabID: id, Kind: card.Kind, Options: a.identify(ctx, card.Options),
					Style: card.Style, Visibility: card.Visibility,
					X: card.X, Y: card.Y, W: card.W, H: card.H,
				}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func hasCards(board *model.Board) bool {
	for _, tab := range board.Tabs {
		if len(tab.Cards) > 0 {
			return true
		}
	}
	return false
}

// identify is addressify backwards: the addresses become this server's ids.
func (a *Applier) identify(ctx context.Context, options json.RawMessage) json.RawMessage {
	var fields map[string]any
	if len(options) == 0 || json.Unmarshal(options, &fields) != nil {
		return options
	}
	if address, ok := fields["project"].(string); ok {
		delete(fields, "project")
		// The address carries its group, and that is the half that matters:
		// three projects can be called "notes", one per group.
		if p, err := a.projectAt(ctx, address); err == nil {
			fields["projectId"] = p.ID.String()
			if p.GroupID != nil {
				fields["groupId"] = p.GroupID.String()
			}
		}
	}
	if slug, ok := fields["group"].(string); ok {
		delete(fields, "group")
		if g, err := a.Store.GroupBySlug(ctx, strings.TrimSpace(slug)); err == nil {
			fields["groupId"] = g.ID.String()
		}
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return options
	}
	return body
}

// projectAt resolves "group/project", and falls back to the plain name when the
// address carries no group.
func (a *Applier) projectAt(ctx context.Context, address string) (*model.Project, error) {
	address = strings.TrimSpace(address)
	if group, project, found := strings.Cut(address, "/"); found {
		if g, err := a.Store.GroupBySlug(ctx, group); err == nil {
			if p, err := a.Store.ProjectBySlug(ctx, &g.ID, project); err == nil {
				return p, nil
			}
		}
		return a.findProject(ctx, project)
	}
	return a.findProject(ctx, address)
}

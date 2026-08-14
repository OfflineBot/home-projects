package api

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/access"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/store"
)

// A board is a page somebody arranged: the front page, or a group's.
//
// Three rules hold it together:
//
//   - The core does not know what a card is. It stores a kind and a blob of
//     options, and hands both to whatever draws it. The list of possible kinds
//     comes from the same registry as everything else, so a new capability
//     brings new cards without a line changing here.
//   - A card is never wider than what it shows. Marking one public does not
//     make a private project readable; the stricter of the two wins.
//   - A visitor without an account sees the owner's board, minus everything
//     they may not see. That is what makes a board something you can hand to
//     somebody, rather than a private page with a public copy beside it.
func (s *Server) mountBoards(r fiber.Router) {
	g := r.Group("/boards")

	// The board for a place. Signed in, it is made on first sight; as a
	// visitor, it is the owner's, filtered.
	g.Get("/", func(c *fiber.Ctx) error {
		ctx := c.UserContext()
		actor := auth.From(c)

		scope, groupID, projectID, err := s.placeFromQuery(c)
		if err != nil {
			return err
		}

		if actor.IsUser() {
			board, err := s.Store.EnsureBoard(ctx, actor.User.ID, scope, groupID, projectID)
			if err != nil {
				return httpx.Internal("the board could not be read").WithCause(err)
			}
			return c.JSON(board)
		}

		owner, err := s.Store.OwnerID(ctx)
		if err != nil {
			return httpx.Internal("the board could not be read").WithCause(err)
		}
		board, err := s.Store.BoardFor(ctx, owner, scope, groupID, projectID)
		if err != nil {
			// Nobody has arranged this place yet; an empty board is the honest
			// answer, not a 404.
			return c.JSON(model.Board{Scope: scope, GroupID: groupID, ProjectID: projectID,
				Tabs: []model.BoardTab{}})
		}
		s.filterBoard(c, board)
		return c.JSON(board)
	})

	// What can go on a board: the core's cards and every capability's, with the
	// options each one takes. The dialog is drawn from this, so a new card kind
	// needs no dialog of its own.
	g.Get("/cards", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"cards": capability.AllCards()})
	})

	g.Post("/:board/tabs", requireOwner, func(c *fiber.Ctx) error {
		board, err := s.myBoard(c)
		if err != nil {
			return err
		}
		var in struct {
			Title string `json:"title"`
			Icon  string `json:"icon"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The tab could not be read.")
		}
		if strings.TrimSpace(in.Title) == "" {
			in.Title = "New tab"
		}
		if in.Icon == "" {
			in.Icon = "grid"
		}
		tab, err := s.Store.CreateTab(c.UserContext(), board.ID, in.Title, in.Icon)
		if err != nil {
			return httpx.Internal("the tab could not be made").WithCause(err)
		}
		return c.Status(fiber.StatusCreated).JSON(tab)
	})

	g.Patch("/tabs/:tab", requireOwner, func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("tab"))
		if err != nil {
			return httpx.BadRequest("That is not a tab id.")
		}
		if _, owner, err := s.Store.TabBoard(c.UserContext(), id); err != nil || owner != auth.From(c).User.ID {
			return httpx.NotFound("There is no such tab.")
		}
		var in struct {
			Title    *string          `json:"title"`
			Icon     *string          `json:"icon"`
			Layout   *string          `json:"layout"`
			Style    *json.RawMessage `json:"style"`
			Position *int             `json:"position"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The change could not be read.")
		}
		if in.Layout != nil && *in.Layout != "grid" && *in.Layout != "flow" {
			return httpx.BadRequest("A tab is a grid or a flow.")
		}
		patch := store.TabPatch{Title: in.Title, Icon: in.Icon, Layout: in.Layout,
			Style: in.Style, Position: in.Position}
		if err := s.Store.UpdateTab(c.UserContext(), id, patch); err != nil {
			return httpx.Internal("the tab could not be changed").WithCause(err)
		}
		return httpx.OK(c)
	})

	g.Delete("/tabs/:tab", requireOwner, func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("tab"))
		if err != nil {
			return httpx.BadRequest("That is not a tab id.")
		}
		if _, owner, err := s.Store.TabBoard(c.UserContext(), id); err != nil || owner != auth.From(c).User.ID {
			return httpx.NotFound("There is no such tab.")
		}
		if err := s.Store.DeleteTab(c.UserContext(), id); err != nil {
			return httpx.NotFound("There is no such tab.")
		}
		return httpx.OK(c)
	})

	g.Post("/cards", requireOwner, func(c *fiber.Ctx) error {
		var in model.BoardCard
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The card could not be read.")
		}
		if in.TabID == uuid.Nil {
			return httpx.BadRequest("A card sits on a tab.")
		}
		if _, owner, err := s.Store.TabBoard(c.UserContext(), in.TabID); err != nil ||
			owner != auth.From(c).User.ID {
			return httpx.NotFound("There is no such tab.")
		}
		if !capability.CardExists(in.Kind) {
			return httpx.BadRequest("There is no card of kind %q.", in.Kind)
		}
		if err := checkVisibility(in.Visibility); err != nil {
			return err
		}
		card, err := s.Store.CreateCard(c.UserContext(), in)
		if err != nil {
			return httpx.Internal("the card could not be made").WithCause(err)
		}
		return c.Status(fiber.StatusCreated).JSON(card)
	})

	g.Patch("/cards/:card", requireOwner, func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("card"))
		if err != nil {
			return httpx.BadRequest("That is not a card id.")
		}
		if _, owner, err := s.Store.CardBoard(c.UserContext(), id); err != nil ||
			owner != auth.From(c).User.ID {
			return httpx.NotFound("There is no such card.")
		}
		var in struct {
			Kind       *string          `json:"kind"`
			Options    *json.RawMessage `json:"options"`
			Style      *json.RawMessage `json:"style"`
			Visibility *string          `json:"visibility"`
			TabID      *uuid.UUID       `json:"tabId"`
			X, Y, W, H *int
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The change could not be read.")
		}
		if in.Visibility != nil {
			if err := checkVisibility(*in.Visibility); err != nil {
				return err
			}
		}
		if in.Kind != nil && !capability.CardExists(*in.Kind) {
			return httpx.BadRequest("There is no card of kind %q.", *in.Kind)
		}
		if err := s.Store.UpdateCard(c.UserContext(), id, store.CardPatch{
			Kind: in.Kind, Options: in.Options, Style: in.Style, Visibility: in.Visibility, TabID: in.TabID,
			X: in.X, Y: in.Y, W: in.W, H: in.H,
		}); err != nil {
			return httpx.Internal("the card could not be changed").WithCause(err)
		}
		return httpx.OK(c)
	})

	g.Delete("/cards/:card", requireOwner, func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("card"))
		if err != nil {
			return httpx.BadRequest("That is not a card id.")
		}
		if _, owner, err := s.Store.CardBoard(c.UserContext(), id); err != nil ||
			owner != auth.From(c).User.ID {
			return httpx.NotFound("There is no such card.")
		}
		if err := s.Store.DeleteCard(c.UserContext(), id); err != nil {
			return httpx.NotFound("There is no such card.")
		}
		return httpx.OK(c)
	})

	// One drag moves several cards out of each other's way, so the arrangement
	// is saved in one request rather than five.
	g.Put("/:board/layout", requireOwner, func(c *fiber.Ctx) error {
		board, err := s.myBoard(c)
		if err != nil {
			return err
		}
		var in struct {
			Cards []store.Layout `json:"cards"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The arrangement could not be read.")
		}
		if err := s.Store.SaveLayout(c.UserContext(), board.ID, in.Cards); err != nil {
			return httpx.Internal("the arrangement could not be saved").WithCause(err)
		}
		return httpx.OK(c)
	})
}

func checkVisibility(v string) error {
	switch v {
	case "", "private", "public", "password":
		return nil
	}
	return httpx.BadRequest("A card is private, public or password.")
}

// placeFromQuery reads which board is meant: the front page, or a group's.
func (s *Server) placeFromQuery(c *fiber.Ctx) (string, *uuid.UUID, *uuid.UUID, error) {
	ctx := c.UserContext()
	if slug := strings.TrimSpace(c.Query("group")); slug != "" {
		g, err := s.Store.GroupBySlug(ctx, slug)
		if err != nil {
			return "", nil, nil, httpx.NotFound("There is no such group.")
		}
		if !access.CanReadGroup(auth.From(c), g) {
			return "", nil, nil, httpx.NotFound("There is no such group.")
		}
		return "group", &g.ID, nil, nil
	}
	if ref := strings.TrimSpace(c.Query("project")); ref != "" {
		id, err := uuid.Parse(ref)
		if err != nil {
			return "", nil, nil, httpx.BadRequest("That is not a project id.")
		}
		p, err := s.Store.ProjectByID(ctx, id)
		if err != nil || !access.CanReadProject(auth.From(c), p) {
			return "", nil, nil, httpx.NotFound("There is no such project.")
		}
		return "project", nil, &p.ID, nil
	}
	return "home", nil, nil, nil
}

// myBoard is the board named in the address, and it has to be the caller's.
func (s *Server) myBoard(c *fiber.Ctx) (*model.Board, error) {
	id, err := uuid.Parse(c.Params("board"))
	if err != nil {
		return nil, httpx.BadRequest("That is not a board id.")
	}
	board, err := s.Store.BoardByID(c.UserContext(), id)
	if err != nil || board.OwnerID != auth.From(c).User.ID {
		return nil, httpx.NotFound("There is no such board.")
	}
	return board, nil
}

// filterBoard takes out what the person looking may not see. A card is as open
// as it says *and* as open as what it shows, whichever is stricter.
func (s *Server) filterBoard(c *fiber.Ctx, board *model.Board) {
	readable := s.visibilityFilter(c)
	for i := range board.Tabs {
		kept := []model.BoardCard{}
		for _, card := range board.Tabs[i].Cards {
			if s.cardIsVisible(c, card, readable) {
				kept = append(kept, card)
			}
		}
		board.Tabs[i].Cards = kept
	}
}

func (s *Server) cardIsVisible(c *fiber.Ctx, card model.BoardCard, readable func(uuid.UUID) bool) bool {
	if card.Visibility == "private" || card.Visibility == "" {
		return false
	}
	ctx := c.UserContext()
	actor := auth.From(c)

	var options struct {
		ProjectID string `json:"projectId"`
		GroupID   string `json:"groupId"`
		Variable  string `json:"variable"`
	}
	_ = json.Unmarshal(card.Options, &options)

	// A card that shows nothing of anybody's — a piece of text, a link — is as
	// open as it says it is.
	if options.ProjectID == "" && options.Variable == "" {
		return true
	}

	if options.ProjectID != "" {
		id, err := uuid.Parse(options.ProjectID)
		if err != nil {
			return false
		}
		p, err := s.Store.ProjectByID(ctx, id)
		if err != nil {
			return false
		}
		if card.Visibility == "password" && actor.HasUnlocked(p.ID) {
			return true
		}
		return access.CanReadProject(actor, p)
	}

	slug, _, found := strings.Cut(options.Variable, ".")
	if !found || options.GroupID == "" {
		return false
	}
	groupID, err := uuid.Parse(options.GroupID)
	if err != nil {
		return false
	}
	p, err := s.Store.ProjectBySlug(ctx, &groupID, slug)
	if err != nil {
		return false
	}
	if card.Visibility == "password" && actor.HasUnlocked(p.ID) {
		return true
	}
	return readable(p.ID)
}

var _ = context.Background

// What one project can put on a board.
//
// This is the answer to "I know what I want": pick the project, see what it
// has — its buttons, its machines, its numbers — and place it. Nobody has to
// know that the average is a "number" card pointing at a variable.
//
// The core contributes two things: the project itself, and one entry per
// number it reports. Everything else comes from the capabilities, which is why
// a new one shows up here without this function changing.
func (s *Server) mountOffers(one fiber.Router) {
	one.Get("/offers", requireOwner, func(c *fiber.Ctx) error {
		ctx := c.UserContext()
		p := project(c)

		offers := []capability.Offer{{
			Card: "project", Title: p.Title, Icon: p.Icon, Detail: "the project itself",
			W: 3, H: 2, Options: map[string]any{"projectId": p.ID.String()},
		}}

		// Every number this project reports, as the card that suits it.
		if list, err := s.Store.VariablesForProject(ctx, p.ID); err == nil {
			for _, v := range list {
				card := "number"
				switch v.Type {
				case "bool":
					card = "status"
				case "list", "table":
					card = "list"
				}
				name := p.Slug + "." + v.Name
				offer := capability.Offer{
					Card: card, Title: v.Name, Icon: "grid", Detail: "a number this project reports",
					Options: map[string]any{"variable": name, "title": v.Name},
				}
				if p.GroupID != nil {
					offer.Options["groupId"] = p.GroupID.String()
				}
				offers = append(offers, offer)
			}
		}

		for _, cap := range capability.All() {
			if !p.Has(cap.Name()) {
				continue
			}
			offers = append(offers, cap.Offers(ctx, s.Env, p)...)
		}
		return c.JSON(fiber.Map{"offers": offers})
	})
}

// What this address is.
//
// A group can have an address of its own — dhbw.example.com — and whatever
// points there shows that group's board and nothing else: no navigation, no
// editing, only the cards that are public. The page asks this once at startup;
// everything else follows from the answer.
func (s *Server) mountHere(r fiber.Router) {
	r.Get("/here", func(c *fiber.Ctx) error {
		host := strings.ToLower(c.Hostname())
		if i := strings.IndexByte(host, ':'); i > 0 {
			host = host[:i]
		}
		g, err := s.Store.GroupByBoardHost(c.UserContext(), host)
		if err != nil {
			return c.JSON(fiber.Map{"kind": "app"})
		}
		return c.JSON(fiber.Map{
			"kind": "board", "group": g.Slug, "title": g.Title,
			"icon": g.Icon, "color": g.Color,
		})
	})
}

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
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
		// A token made for this group reads its board as its owner sees it:
		// something that may build a board has to be able to look at it.
		if err == nil && s.mayBuild(c, board) == nil {
			return c.JSON(board)
		}
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

	g.Post("/:board/tabs", func(c *fiber.Ctx) error {
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

	g.Patch("/tabs/:tab", func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("tab"))
		if err != nil {
			return httpx.BadRequest("That is not a tab id.")
		}
		if _, err := s.boardOfTab(c, id); err != nil {
			return err
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
		if in.Layout != nil && *in.Layout != "grid" && *in.Layout != "flow" &&
			*in.Layout != "free" && *in.Layout != "page" && *in.Layout != "panes" {
			return httpx.BadRequest("A tab is a grid, a flow, free, or a page.")
		}
		patch := store.TabPatch{Title: in.Title, Icon: in.Icon, Layout: in.Layout,
			Style: in.Style, Position: in.Position}
		if err := s.Store.UpdateTab(c.UserContext(), id, patch); err != nil {
			return httpx.Internal("the tab could not be changed").WithCause(err)
		}
		// Changing how a tab lays out changes what its numbers mean: columns on
		// a grid, pixels on a free surface. Cards that were already there are
		// converted, or they would sit two pixels wide and look like nothing.
		if in.Layout != nil {
			if err := s.rescale(c, id, *in.Layout); err != nil {
				return err
			}
		}
		return httpx.OK(c)
	})

	g.Delete("/tabs/:tab", func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("tab"))
		if err != nil {
			return httpx.BadRequest("That is not a tab id.")
		}
		if _, err := s.boardOfTab(c, id); err != nil {
			return err
		}
		if err := s.Store.DeleteTab(c.UserContext(), id); err != nil {
			return httpx.NotFound("There is no such tab.")
		}
		return httpx.OK(c)
	})

	g.Post("/cards", func(c *fiber.Ctx) error {
		var in model.BoardCard
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The card could not be read.")
		}
		if in.TabID == uuid.Nil {
			return httpx.BadRequest("A card sits on a tab.")
		}
		tab, err := s.tabByID(c, in.TabID)
		if err != nil {
			return err
		}
		if !capability.CardExists(in.Kind) {
			return httpx.BadRequest("There is no card of kind %q.", in.Kind)
		}
		if err := checkVisibility(in.Visibility); err != nil {
			return err
		}
		// A free surface measures in pixels. A card arriving with grid numbers
		// there would be two pixels wide — which is how a button ends up
		// "not showing" while everything about it is right.
		in.W, in.H = sizeFor(&tab, in.W, in.H)
		card, err := s.Store.CreateCard(c.UserContext(), in)
		if err != nil {
			return httpx.Internal("the card could not be made").WithCause(err)
		}
		return c.Status(fiber.StatusCreated).JSON(card)
	})

	g.Patch("/cards/:card", func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("card"))
		if err != nil {
			return httpx.BadRequest("That is not a card id.")
		}
		if _, err := s.boardOfCard(c, id); err != nil {
			return err
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

	g.Delete("/cards/:card", func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("card"))
		if err != nil {
			return httpx.BadRequest("That is not a card id.")
		}
		if _, err := s.boardOfCard(c, id); err != nil {
			return err
		}
		if err := s.Store.DeleteCard(c.UserContext(), id); err != nil {
			return httpx.NotFound("There is no such card.")
		}
		return httpx.OK(c)
	})

	// Turning a tab into the page it already is.
	//
	// Cards and HTML were two ways of saying the same thing, and having both
	// beside each other was the confusion. This makes one out of the other: the
	// cards become <hp-card> tags in a document that can then be written by
	// hand, or by an assistant. Nothing is lost — every card keeps its options,
	// and the tags draw the same components.
	g.Post("/tabs/:tab/as-html", func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("tab"))
		if err != nil {
			return httpx.BadRequest("That is not a tab id.")
		}
		board, err := s.boardOfTab(c, id)
		if err != nil {
			return err
		}
		var tab *model.BoardTab
		for i := range board.Tabs {
			if board.Tabs[i].ID == id {
				tab = &board.Tabs[i]
			}
		}
		if tab == nil {
			return httpx.NotFound("There is no such tab.")
		}
		if tab.Layout == "page" {
			return c.JSON(fiber.Map{"already": true})
		}

		ctx := c.UserContext()
		page := cardsAsHTML(tab)
		for _, card := range tab.Cards {
			if err := s.Store.DeleteCard(ctx, card.ID); err != nil {
				return httpx.Internal("the cards could not be folded in").WithCause(err)
			}
		}
		options, merr := json.Marshal(map[string]any{"html": page, "mode": "inline"})
		if merr != nil {
			return httpx.Internal("the page could not be written").WithCause(merr)
		}
		if _, err := s.Store.CreateCard(ctx, model.BoardCard{
			TabID: tab.ID, Kind: "html", Options: options, Visibility: "private",
			X: 0, Y: 0, W: 12, H: 8,
		}); err != nil {
			return httpx.Internal("the page could not be written").WithCause(err)
		}
		layout := "page"
		if err := s.Store.UpdateTab(ctx, tab.ID, store.TabPatch{Layout: &layout}); err != nil {
			return httpx.Internal("the tab could not be turned into a page").WithCause(err)
		}
		return c.JSON(fiber.Map{"html": page, "cards": len(tab.Cards)})
	})

	// A board that starts empty is a board nobody fills in. This puts what is
	// actually there on it — every project, with the numbers it reports — and
	// then it is something to rearrange rather than something to begin.
	g.Post("/:board/fill", func(c *fiber.Ctx) error {
		board, err := s.myBoard(c)
		if err != nil {
			return err
		}
		if len(board.Tabs) == 0 {
			return httpx.BadRequest("This board has no tab yet.")
		}
		tab := board.Tabs[0]
		if wanted := strings.TrimSpace(c.Query("tab")); wanted != "" {
			for _, t := range board.Tabs {
				if t.ID.String() == wanted {
					tab = t
				}
			}
		}

		ctx := c.UserContext()
		actor := auth.From(c)
		projects, err := s.Store.ListAllProjects(ctx, false)
		if err != nil {
			return httpx.Internal("the projects could not be read").WithCause(err)
		}

		x, y, made := 0, 0, 0
		place := func(kind string, options map[string]any, w, h int) error {
			if x+w > 12 {
				x, y = 0, y+2
			}
			// The same on a free surface: what is placed has to be visible.
			placedW, placedH := sizeFor(&tab, w, h)
			placedX, placedY := x, y
			if tab.Layout == "free" {
				placedX, placedY = x*COLUMN, y*ROW
			}
			body, merr := json.Marshal(options)
			if merr != nil {
				return merr
			}
			if _, err := s.Store.CreateCard(ctx, model.BoardCard{
				TabID: tab.ID, Kind: kind, Options: body, Visibility: "private",
				X: placedX, Y: placedY, W: placedW, H: placedH,
			}); err != nil {
				return err
			}
			x += w
			made++
			return nil
		}

		for i := range projects {
			p := &projects[i]
			if made >= 24 || p.Archived || !access.CanReadProject(actor, p) {
				continue
			}
			// A board belongs to its place: a group's board is about that group.
			if board.GroupID != nil && (p.GroupID == nil || *p.GroupID != *board.GroupID) {
				continue
			}
			if err := place("project", map[string]any{"projectId": p.ID.String()}, 3, 2); err != nil {
				return httpx.Internal("the board could not be filled").WithCause(err)
			}

			// And the two numbers that project reports first, because a number
			// is the reason somebody looks at a board at all.
			numbers := 0
			if list, err := s.Store.VariablesForProject(ctx, p.ID); err == nil {
				for _, v := range list {
					if numbers >= 2 || made >= 24 {
						break
					}
					kind := "number"
					if v.Type == "bool" {
						kind = "status"
					} else if v.Type == "list" || v.Type == "table" {
						continue
					}
					options := map[string]any{"variable": p.Slug + "." + v.Name, "title": v.Name}
					if p.GroupID != nil {
						options["groupId"] = p.GroupID.String()
					}
					if err := place(kind, options, 2, 2); err != nil {
						return httpx.Internal("the board could not be filled").WithCause(err)
					}
					numbers++
				}
			}
		}
		return c.JSON(fiber.Map{"cards": made})
	})

	// One drag moves several cards out of each other's way, so the arrangement
	// is saved in one request rather than five.
	g.Put("/:board/layout", func(c *fiber.Ctx) error {
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

// myBoard is the board named in the address, and the caller has to be allowed
// to build it.
func (s *Server) myBoard(c *fiber.Ctx) (*model.Board, error) {
	id, err := uuid.Parse(c.Params("board"))
	if err != nil {
		return nil, httpx.BadRequest("That is not a board id.")
	}
	board, err := s.Store.BoardByID(c.UserContext(), id)
	if err != nil {
		return nil, httpx.NotFound("There is no such board.")
	}
	if err := s.mayBuild(c, board); err != nil {
		return nil, err
	}
	return board, nil
}

// mayBuild is the whole authorisation of a board: the person who owns it, or a
// token made for that group with write scope. A token is not an account — it
// can build the board of its own group and reach nothing else.
func (s *Server) mayBuild(c *fiber.Ctx, board *model.Board) error {
	actor := auth.From(c)
	if actor.IsUser() && board.OwnerID == actor.User.ID {
		return nil
	}
	token := actor.Token
	if token != nil && token.Scope == "write" && token.GroupID != nil &&
		board.GroupID != nil && *token.GroupID == *board.GroupID {
		return nil
	}
	return httpx.NotFound("There is no such board.")
}

// boardOfTab and boardOfCard answer the same question when the address names a
// tab or a card instead of the board.
func (s *Server) boardOfTab(c *fiber.Ctx, tabID uuid.UUID) (*model.Board, error) {
	boardID, _, err := s.Store.TabBoard(c.UserContext(), tabID)
	if err != nil {
		return nil, httpx.NotFound("There is no such tab.")
	}
	board, err := s.Store.BoardByID(c.UserContext(), boardID)
	if err != nil {
		return nil, httpx.NotFound("There is no such tab.")
	}
	if err := s.mayBuild(c, board); err != nil {
		return nil, httpx.NotFound("There is no such tab.")
	}
	return board, nil
}

func (s *Server) boardOfCard(c *fiber.Ctx, cardID uuid.UUID) (*model.Board, error) {
	boardID, _, err := s.Store.CardBoard(c.UserContext(), cardID)
	if err != nil {
		return nil, httpx.NotFound("There is no such card.")
	}
	board, err := s.Store.BoardByID(c.UserContext(), boardID)
	if err != nil {
		return nil, httpx.NotFound("There is no such card.")
	}
	if err := s.mayBuild(c, board); err != nil {
		return nil, httpx.NotFound("There is no such card.")
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
			From: "yours", W: 3, H: 2, Options: map[string]any{"projectId": p.ID.String()},
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
				// The variable itself says whether it is the point of the
				// project or the system's own bookkeeping. Guessing from where
				// it came was close but wrong: an average out of Dualis is a
				// capability's export and is exactly what somebody wants.
				detail := "declared in project.yaml"
				from := "yours"
				switch {
				case v.Reported:
					detail, from = "the system's own count", "reported"
				case strings.HasPrefix(v.Source, "capability:"):
					detail = "what this project works out"
				case v.Source == "exports.json":
					detail = "from exports.json"
				}
				offer := capability.Offer{
					Card: card, Title: v.Name, Icon: "grid", Detail: detail, From: from,
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
			// And the view itself: the whole thing on the board, usable where
			// it stands rather than one click away.
			offers = append(offers, capability.Offer{
				Card: "view", Title: cap.Title() + ", right here", Icon: cap.Icon(),
				Detail: "the view itself, on the board", From: "yours", W: 6, H: 5,
				Options: map[string]any{"projectId": p.ID.String(), "view": cap.Name(),
					"title": p.Title + " · " + cap.Title()},
			})
		}
		offers = append(offers, capability.Offer{
			Card: "view", Title: "Its files, right here", Icon: "folder",
			Detail: "the file tree, on the board", From: "yours", W: 6, H: 5,
			Options: map[string]any{"projectId": p.ID.String(), "view": "files",
				"title": p.Title + " · Files"},
		})
		// And each folder on its own, because "the notes" or "the invoices" is
		// what somebody means far more often than "all the files".
		if entries, err := s.Env.Files.List(ctx, auth.From(c), p, ""); err == nil {
			for _, entry := range entries {
				if !entry.IsDir || strings.HasPrefix(entry.Name, ".") {
					continue
				}
				folder := entry.Name
				offers = append(offers, capability.Offer{
					Card: "view", Title: folder, Icon: "folder", Detail: "that folder, on the board",
					From: "yours", W: 6, H: 4,
					Options: map[string]any{"projectId": p.ID.String(), "view": "files",
						"folder": folder, "title": p.Title + " · " + folder},
				})
			}
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

// cardsAsHTML writes the cards of a tab out as a document.
//
// The order is the order they lie in, and the width they had becomes the width
// they take: a card that filled half the grid fills half the page. What it
// produces is meant to be edited afterwards — it is a starting point in the
// person's own hands, not a generated file to leave alone.
func cardsAsHTML(tab *model.BoardTab) string {
	rows := map[int][]model.BoardCard{}
	order := []int{}
	for _, card := range tab.Cards {
		if _, seen := rows[card.Y]; !seen {
			order = append(order, card.Y)
		}
		rows[card.Y] = append(rows[card.Y], card)
	}
	sort.Ints(order)

	var out strings.Builder
	for _, y := range order {
		line := rows[y]
		sort.Slice(line, func(i, j int) bool { return line[i].X < line[j].X })
		if len(line) > 1 {
			out.WriteString("<div class=\"row\">\n")
		}
		for _, card := range line {
			piece := cardAsHTML(card)
			if len(line) > 1 {
				width := card.W
				if width <= 0 || width > 12 {
					width = 3
				}
				out.WriteString("  <div style=\"flex:" + strconv.Itoa(width) +
					" 1 220px\">" + piece + "</div>\n")
				continue
			}
			out.WriteString(piece + "\n")
		}
		if len(line) > 1 {
			out.WriteString("</div>\n")
		}
	}
	return out.String()
}

func cardAsHTML(card model.BoardCard) string {
	var options map[string]any
	_ = json.Unmarshal(card.Options, &options)

	switch card.Kind {
	case "html":
		if text, ok := options["html"].(string); ok {
			return text
		}
		return ""
	case "heading":
		if title, ok := options["title"].(string); ok {
			return "<h2>" + title + "</h2>"
		}
		return ""
	case "text":
		if text, ok := options["text"].(string); ok {
			// Markdown was the card's language; a page speaks HTML, and the
			// smallest honest translation is a paragraph per line.
			lines := strings.Split(strings.TrimSpace(text), "\n")
			var out strings.Builder
			for _, line := range lines {
				if strings.TrimSpace(line) == "" {
					continue
				}
				out.WriteString("<p>" + strings.TrimPrefix(strings.TrimPrefix(line, "## "), "# ") + "</p>")
			}
			return out.String()
		}
		return ""
	case "number", "status":
		if name, ok := options["variable"].(string); ok {
			title, _ := options["title"].(string)
			if title == "" {
				title = name
			}
			return "<p>" + title + ": <strong>{{" + name + "}}</strong></p>"
		}
		return ""
	}

	attrs := []string{"kind=\"" + card.Kind + "\""}
	for key, raw := range options {
		if key == "title" || raw == nil || raw == "" {
			continue
		}
		name := key
		if key == "projectId" {
			name = "project"
		}
		attrs = append(attrs, name+"=\""+strings.ReplaceAll(fmt.Sprint(raw), "\"", "&quot;")+"\"")
	}
	sort.Strings(attrs)
	return "<hp-card " + strings.Join(attrs, " ") + "></hp-card>"
}

// A column and a row, in pixels: what the grid is worth on a free surface.
const (
	COLUMN = 96
	ROW    = 100
)

// sizeFor turns grid numbers into pixels when the tab is a free surface, and
// leaves them alone otherwise.
//
// The heuristic is safe because the two scales cannot be confused: nothing on a
// free surface is meant to be twelve pixels wide, and nothing on a grid is
// meant to be two hundred columns.
func sizeFor(tab *model.BoardTab, w, h int) (int, int) {
	if tab == nil || tab.Layout != "free" {
		return w, h
	}
	if w <= 0 {
		w = 3
	}
	if h <= 0 {
		h = 2
	}
	if w <= 12 {
		w *= COLUMN
	}
	if h <= 12 {
		h *= ROW
	}
	return w, h
}

// tabByID is the tab itself, once the caller has been allowed to build it.
func (s *Server) tabByID(c *fiber.Ctx, id uuid.UUID) (model.BoardTab, error) {
	board, err := s.boardOfTab(c, id)
	if err != nil {
		return model.BoardTab{}, err
	}
	for _, tab := range board.Tabs {
		if tab.ID == id {
			return tab, nil
		}
	}
	return model.BoardTab{}, httpx.NotFound("There is no such tab.")
}

// rescale converts the cards of a tab when its layout changes between the grid
// and the free surface.
func (s *Server) rescale(c *fiber.Ctx, tabID uuid.UUID, layout string) error {
	board, err := s.boardOfTab(c, tabID)
	if err != nil {
		return err
	}
	var tab *model.BoardTab
	for i := range board.Tabs {
		if board.Tabs[i].ID == tabID {
			tab = &board.Tabs[i]
		}
	}
	if tab == nil {
		return nil
	}
	ctx := c.UserContext()
	for _, card := range tab.Cards {
		x, y, w, h := card.X, card.Y, card.W, card.H
		switch {
		case layout == "free" && w <= 12 && h <= 12:
			x, y, w, h = x*COLUMN, y*ROW, w*COLUMN, h*ROW
		case layout != "free" && (w > 12 || h > 12):
			x, y = x/COLUMN, y/ROW
			w, h = max(1, w/COLUMN), max(1, h/ROW)
			if w > 12 {
				w = 12
			}
		default:
			continue
		}
		if err := s.Store.UpdateCard(ctx, card.ID, store.CardPatch{X: &x, Y: &y, W: &w, H: &h}); err != nil {
			return httpx.Internal("the cards could not be converted").WithCause(err)
		}
	}
	return nil
}

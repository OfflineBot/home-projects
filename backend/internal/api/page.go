package api

import (
	_ "embed"
	"encoding/json"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/store"
)

// A page: one board tab that is a single HTML document.
//
// This is the address an assistant is given. Two calls — read it, replace it —
// and nothing else to understand: no cards, no coordinates, no ids to keep
// track of. What comes back is what will be shown, and what is sent replaces
// it whole, which is the only shape that a program writing HTML can use
// without guessing.
//
// Who may write it: the owner, or an API token made for exactly this group,
// with write scope. A token is not a user — it inherits nothing — so a token
// for one group is a key to that group and to nothing else on the server.
//
//go:embed assistant.md
var assistantDoc string

func (s *Server) mountPage(r fiber.Router) {
	// The instructions, from the server that implements them: an assistant can
	// be pointed at this address instead of being told the rules second-hand.
	r.Get("/docs/assistant", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/markdown; charset=utf-8")
		return c.SendString(assistantDoc)
	})

	g := r.Group("/page")

	g.Get("/", func(c *fiber.Ctx) error {
		board, tab, err := s.pageTab(c, false)
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{
			"board": board.ID, "tab": tab.ID, "title": tab.Title,
			"html": pageHTML(tab),
		})
	})

	g.Put("/", func(c *fiber.Ctx) error {
		var in struct {
			HTML  string `json:"html"`
			Title string `json:"title"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The page could not be read.")
		}
		if len(in.HTML) > 2<<20 {
			return httpx.BadRequest("That page is larger than two megabytes.")
		}
		_, tab, err := s.pageTab(c, true)
		if err != nil {
			return err
		}

		options, merr := json.Marshal(map[string]any{"html": in.HTML, "mode": "inline"})
		if merr != nil {
			return httpx.Internal("the page could not be written").WithCause(merr)
		}
		card := pageCard(tab)
		if card == nil {
			if _, err := s.Store.CreateCard(c.UserContext(), model.BoardCard{
				TabID: tab.ID, Kind: "html", Options: options, Visibility: "public",
				X: 0, Y: 0, W: 12, H: 8,
			}); err != nil {
				return httpx.Internal("the page could not be written").WithCause(err)
			}
		} else {
			raw := json.RawMessage(options)
			if err := s.Store.UpdateCard(c.UserContext(), card.ID, storeCardOptions(&raw)); err != nil {
				return httpx.Internal("the page could not be written").WithCause(err)
			}
		}
		if strings.TrimSpace(in.Title) != "" {
			title := in.Title
			if err := s.Store.UpdateTab(c.UserContext(), tab.ID, storeTabTitle(&title)); err != nil {
				return httpx.Internal("the page could not be renamed").WithCause(err)
			}
		}
		s.Store.Audit(c.UserContext(), auth.From(c).UserID(), "page.written",
			tab.Title, auth.ClientIP(c), map[string]any{"bytes": len(in.HTML)})
		return c.JSON(fiber.Map{"tab": tab.ID, "html": in.HTML})
	})
}

// pageTab finds the tab a page lives on, and makes it when asked to.
//
// Which board is meant comes from the address, exactly as everywhere else:
// ?group=<slug> for a group's board, nothing for the front page. A named tab
// wins; otherwise the first tab that is a page is used, and failing that one is
// made.
func (s *Server) pageTab(c *fiber.Ctx, forWriting bool) (*model.Board, *model.BoardTab, error) {
	ctx := c.UserContext()
	actor := auth.From(c)

	scope, groupID, projectID, err := s.placeFromQuery(c)
	if err != nil {
		return nil, nil, err
	}
	if err := s.mayWritePage(c, scope, groupID, forWriting); err != nil {
		return nil, nil, err
	}

	owner, err := s.Store.OwnerID(ctx)
	if err != nil {
		return nil, nil, httpx.Internal("the board could not be read").WithCause(err)
	}
	if actor.IsUser() {
		owner = actor.User.ID
	}

	board, err := s.Store.EnsureBoard(ctx, owner, scope, groupID, projectID)
	if err != nil {
		return nil, nil, httpx.Internal("the board could not be read").WithCause(err)
	}

	wanted := strings.TrimSpace(c.Query("tab"))
	for i := range board.Tabs {
		tab := &board.Tabs[i]
		if wanted != "" && tab.ID.String() != wanted && !strings.EqualFold(tab.Title, wanted) {
			continue
		}
		if wanted != "" || tab.Layout == "page" {
			return board, tab, nil
		}
	}
	if !forWriting {
		return nil, nil, httpx.NotFound("There is no page here yet.")
	}

	title := wanted
	if title == "" {
		title = "Page"
	}
	tab, err := s.Store.CreateTab(ctx, board.ID, title, "code")
	if err != nil {
		return nil, nil, httpx.Internal("the page could not be made").WithCause(err)
	}
	layout := "page"
	if err := s.Store.UpdateTab(ctx, tab.ID, storeTabLayout(&layout)); err != nil {
		return nil, nil, httpx.Internal("the page could not be made").WithCause(err)
	}
	tab.Layout = layout
	return board, tab, nil
}

// mayWritePage is the whole authorisation of this corner: the owner, or a token
// made for this very group. Anything else is refused before a board is touched.
func (s *Server) mayWritePage(c *fiber.Ctx, scope string, groupID *uuid.UUID, forWriting bool) error {
	actor := auth.From(c)
	if actor.IsUser() {
		return nil
	}
	token := actor.Token
	if token == nil {
		if forWriting {
			return httpx.Unauthorized("Please sign in, or use a token made for this group.")
		}
		return nil // reading falls back to what is public, as everywhere
	}
	if scope != "group" || groupID == nil || token.GroupID == nil || *token.GroupID != *groupID {
		return httpx.Forbidden("This token is not for that group.")
	}
	if forWriting && token.Scope != "write" {
		return httpx.Forbidden("This token may read, not write.")
	}
	return nil
}

func pageCard(tab *model.BoardTab) *model.BoardCard {
	for i := range tab.Cards {
		if tab.Cards[i].Kind == "html" {
			return &tab.Cards[i]
		}
	}
	return nil
}

func pageHTML(tab *model.BoardTab) string {
	card := pageCard(tab)
	if card == nil {
		return ""
	}
	var options struct {
		HTML string `json:"html"`
	}
	_ = json.Unmarshal(card.Options, &options)
	return options.HTML
}

func storeCardOptions(options *json.RawMessage) store.CardPatch {
	return store.CardPatch{Options: options}
}
func storeTabTitle(title *string) store.TabPatch   { return store.TabPatch{Title: title} }
func storeTabLayout(layout *string) store.TabPatch { return store.TabPatch{Layout: layout} }

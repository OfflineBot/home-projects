package api

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/secret"
)

func (s *Server) mountMeta(r fiber.Router) {
	// One call the frontend makes at startup: what this server can do.
	r.Get("/meta", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"capabilities":   capability.Catalog(),
			"presets":        capability.Presets(),
			"schedulerKinds": capability.SchedulerKinds(),
			"accountKinds":   capability.AccountKinds(),
			"actions":        capability.Actions(),
			"colors":         PaletteColors,
			"icons":          Icons(),
			"publicUrl":      s.Cfg.PublicURL,
			"signedIn":       auth.From(c).IsUser(),
		})
	})
}

func (s *Server) mountAuth(r fiber.Router) {
	g := r.Group("/auth")

	g.Post("/login", func(c *fiber.Ctx) error {
		var in struct {
			Username string `json:"username"`
			Password string `json:"password"`
			TOTP     string `json:"totp"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The sign-in could not be read.")
		}
		res, err := s.Auth.Login(c.UserContext(), c, in.Username, in.Password, in.TOTP)
		if err != nil {
			return err
		}
		return c.JSON(res)
	})

	g.Post("/refresh", func(c *fiber.Ctx) error {
		res, err := s.Auth.Refresh(c.UserContext(), c)
		if err != nil {
			return err
		}
		return c.JSON(res)
	})

	g.Post("/logout", func(c *fiber.Ctx) error {
		s.Auth.Logout(c.UserContext(), c, auth.From(c))
		return httpx.OK(c)
	})

	g.Get("/me", func(c *fiber.Ctx) error {
		a := auth.From(c)
		if !a.IsUser() {
			return c.JSON(fiber.Map{"user": nil})
		}
		return c.JSON(fiber.Map{
			"user":      a.User,
			"steppedUp": a.SteppedUp(s.Cfg.StepUpTTL),
		})
	})

	g.Post("/step-up", requireOwner, func(c *fiber.Ctx) error {
		var in struct {
			Password string `json:"password"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The password could not be read.")
		}
		if err := s.Auth.StepUp(c.UserContext(), c, auth.From(c), in.Password); err != nil {
			return err
		}
		return c.JSON(fiber.Map{"ok": true, "validFor": int(s.Cfg.StepUpTTL.Seconds())})
	})

	g.Get("/sessions", requireOwner, func(c *fiber.Ctx) error {
		a := auth.From(c)
		list, err := s.Store.ListSessions(c.UserContext(), a.User.ID)
		if err != nil {
			return httpx.Internal("the sessions could not be read").WithCause(err)
		}
		for i := range list {
			list[i].Current = list[i].ID == a.SessionID
		}
		return c.JSON(fiber.Map{"sessions": list})
	})

	g.Delete("/sessions/:id", requireOwner, func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return httpx.BadRequest("That is not a session id.")
		}
		a := auth.From(c)
		if err := s.Store.RevokeSession(c.UserContext(), a.User.ID, id); err != nil {
			return httpx.NotFound("There is no such session.")
		}
		s.Store.Audit(c.UserContext(), a.UserID(), "session.revoked", id.String(), auth.ClientIP(c), nil)
		return httpx.OK(c)
	})

	g.Post("/sessions/revoke-all", requireOwner, func(c *fiber.Ctx) error {
		a := auth.From(c)
		if err := s.Store.RevokeAllSessions(c.UserContext(), a.User.ID, &a.SessionID); err != nil {
			return httpx.Internal("the sessions could not be revoked").WithCause(err)
		}
		s.Store.Audit(c.UserContext(), a.UserID(), "session.revoked_all", "", auth.ClientIP(c), nil)
		return httpx.OK(c)
	})

	g.Post("/password", requireOwner, func(c *fiber.Ctx) error {
		if err := s.stepUp(c, "changing the password"); err != nil {
			return err
		}
		var in struct {
			New string `json:"new"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The password could not be read.")
		}
		if len(in.New) < 10 {
			return httpx.BadRequest("The password has to be at least 10 characters long.")
		}
		hash, err := secret.Hash(in.New)
		if err != nil {
			return httpx.Internal("the password could not be hashed").WithCause(err)
		}
		a := auth.From(c)
		if err := s.Store.SetPassword(c.UserContext(), a.User.ID, hash); err != nil {
			return httpx.Internal("the password could not be stored").WithCause(err)
		}
		s.Store.Audit(c.UserContext(), a.UserID(), "password.changed", a.User.Username, auth.ClientIP(c), nil)
		return httpx.OK(c)
	})

	// --- second factor, optional -------------------------------------------
	g.Post("/totp/start", requireOwner, func(c *fiber.Ctx) error {
		if err := s.stepUp(c, "switching the second factor on"); err != nil {
			return err
		}
		a := auth.From(c)
		sec, url, err := auth.NewTOTPSecret("home-projects", a.User.Username)
		if err != nil {
			return httpx.Internal("the second factor could not be prepared").WithCause(err)
		}
		if err := s.Store.SetTOTP(c.UserContext(), a.User.ID, sec, false); err != nil {
			return httpx.Internal("the second factor could not be stored").WithCause(err)
		}
		return c.JSON(fiber.Map{"secret": sec, "url": url})
	})

	g.Post("/totp/enable", requireOwner, func(c *fiber.Ctx) error {
		var in struct {
			Code string `json:"code"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The code could not be read.")
		}
		a := auth.From(c)
		if !auth.VerifyTOTP(a.User.TOTPSecret, in.Code) {
			return httpx.BadRequest("The code is not right. The clock on the phone may be off.")
		}
		if err := s.Store.SetTOTP(c.UserContext(), a.User.ID, a.User.TOTPSecret, true); err != nil {
			return httpx.Internal("the second factor could not be switched on").WithCause(err)
		}
		s.Store.Audit(c.UserContext(), a.UserID(), "totp.enabled", a.User.Username, auth.ClientIP(c), nil)
		return httpx.OK(c)
	})

	g.Post("/totp/disable", requireOwner, func(c *fiber.Ctx) error {
		if err := s.stepUp(c, "switching the second factor off"); err != nil {
			return err
		}
		a := auth.From(c)
		if err := s.Store.SetTOTP(c.UserContext(), a.User.ID, "", false); err != nil {
			return httpx.Internal("the second factor could not be switched off").WithCause(err)
		}
		s.Store.Audit(c.UserContext(), a.UserID(), "totp.disabled", a.User.Username, auth.ClientIP(c), nil)
		return httpx.OK(c)
	})

	g.Get("/audit", requireOwner, func(c *fiber.Ctx) error {
		list, err := s.Store.ListAudit(c.UserContext(), c.QueryInt("limit", 200))
		if err != nil {
			return httpx.Internal("the log could not be read").WithCause(err)
		}
		return c.JSON(fiber.Map{"entries": list})
	})

	// --- tokens for machines -----------------------------------------------
	tokens := r.Group("/tokens", requireOwner)

	tokens.Get("/", func(c *fiber.Ctx) error {
		ctx := c.UserContext()
		list, err := s.Store.ListTokens(ctx, auth.From(c).User.ID)
		if err != nil {
			return httpx.Internal("the tokens could not be read").WithCause(err)
		}
		// What each one reaches, in words: a list of uuids tells nobody
		// anything, and this is the page where a person decides what to revoke.
		for i := range list {
			switch {
			case list[i].GroupID != nil:
				if g, err := s.Store.GroupByID(ctx, *list[i].GroupID); err == nil {
					list[i].Reaches = "the group " + g.Title
				}
			case list[i].ProjectID != nil:
				if p, err := s.Store.ProjectByID(ctx, *list[i].ProjectID); err == nil {
					list[i].Reaches = p.GroupSlug + "/" + p.Slug
				}
			}
		}
		return c.JSON(fiber.Map{"tokens": list})
	})

	tokens.Post("/", func(c *fiber.Ctx) error {
		if err := s.stepUp(c, "creating a token"); err != nil {
			return err
		}
		var in struct {
			Name      string `json:"name"`
			Scope     string `json:"scope"`
			ProjectID string `json:"projectId"`
			GroupID   string `json:"groupId"`
			Days      int    `json:"days"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The token could not be read.")
		}
		if in.Name == "" {
			return httpx.BadRequest("A token needs a name, so you can tell later what it was for.")
		}
		switch in.Scope {
		case "read", "write", "ics", "git", "webhook":
		default:
			return httpx.BadRequest("A token's scope has to be read, write, ics, git or webhook.")
		}
		var projectID, groupID *uuid.UUID
		if in.ProjectID != "" {
			id, err := uuid.Parse(in.ProjectID)
			if err != nil {
				return httpx.BadRequest("That is not a project id.")
			}
			projectID = &id
		}
		if in.GroupID != "" {
			id, err := uuid.Parse(in.GroupID)
			if err != nil {
				return httpx.BadRequest("That is not a group id.")
			}
			groupID = &id
		}
		if projectID == nil && groupID == nil {
			return httpx.BadRequest("A token belongs to one project or one group.")
		}
		var expires *time.Time
		if in.Days > 0 {
			t := time.Now().AddDate(0, 0, in.Days)
			expires = &t
		}
		plain := secret.Token(32)
		a := auth.From(c)
		tok, err := s.Store.CreateToken(c.UserContext(), a.User.ID, in.Name,
			secret.Fingerprint(plain), in.Scope, projectID, groupID, expires)
		if err != nil {
			return httpx.Internal("the token could not be created").WithCause(err)
		}
		s.Store.Audit(c.UserContext(), a.UserID(), "token.created", in.Name, auth.ClientIP(c),
			map[string]any{"scope": in.Scope})
		tok.Secret = plain // shown exactly once
		return c.Status(fiber.StatusCreated).JSON(tok)
	})

	tokens.Delete("/:id", func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return httpx.BadRequest("That is not a token id.")
		}
		a := auth.From(c)
		if err := s.Store.RevokeToken(c.UserContext(), a.User.ID, id); err != nil {
			return httpx.NotFound("There is no such token.")
		}
		s.Store.Audit(c.UserContext(), a.UserID(), "token.revoked", id.String(), auth.ClientIP(c), nil)
		return httpx.OK(c)
	})
}

// unlock takes the password of a protected group or project. It is throttled
// like every other authentication step.
func (s *Server) unlock(c *fiber.Ctx, id uuid.UUID, hash string, scope, subject string) error {
	var in struct {
		// The form tag is for the one caller that is not the app: the password
		// page in front of a protected site, which posts an ordinary form.
		Password string `json:"password" form:"password"`
	}
	if err := c.BodyParser(&in); err != nil {
		return httpx.BadRequest("The password could not be read.")
	}
	ctx := c.UserContext()
	fails, _ := s.Store.RecentFailures(ctx, scope, id.String(), 15*time.Minute)
	if fails >= 8 {
		return httpx.TooMany("Too many attempts. Try again in a few minutes.")
	}
	if hash == "" || !secret.Verify(in.Password, hash) {
		s.Store.RecordAttempt(ctx, scope, id.String(), auth.ClientIP(c), false)
		return httpx.Unauthorized("The password is not right.")
	}
	s.Store.RecordAttempt(ctx, scope, id.String(), auth.ClientIP(c), true)
	if err := s.Auth.Unlock(c, auth.From(c), id); err != nil {
		return err
	}
	s.Store.Audit(ctx, auth.From(c).UserID(), "unlocked", subject, auth.ClientIP(c), nil)
	return httpx.OK(c)
}

// PaletteColors are the Catppuccin accent names a group or project may use.
// Colors come from the palette, never from a free colour picker, so the tiles
// match the theme in every flavour.
var PaletteColors = []string{
	"rosewater", "flamingo", "pink", "mauve", "red", "maroon", "peach",
	"yellow", "green", "teal", "sky", "sapphire", "blue", "lavender",
}

// baseIcons are the icons the core offers for groups and projects. The
// capabilities add their own on top — see Icons().
var baseIcons = []string{
	"folder", "box", "notebook", "award", "globe", "rss", "cpu", "flame",
	"lightbulb", "server", "code", "database", "graduation", "home", "music",
	"camera", "heart", "star", "wrench", "zap",
}

// Icons is what the settings dialog offers: the core's set plus whatever icon
// each installed capability brought with it.
func Icons() []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(baseIcons)+4)
	for _, name := range baseIcons {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for _, c := range capability.Catalog() {
		if c.Icon != "" && !seen[c.Icon] {
			seen[c.Icon] = true
			out = append(out, c.Icon)
		}
	}
	return out
}

func validColor(name string) bool {
	for _, c := range PaletteColors {
		if c == name {
			return true
		}
	}
	return false
}

var _ = model.VisibilityPrivate

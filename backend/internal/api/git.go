package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/access"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/events"
	"github.com/offlinebot/home-projects/backend/internal/gitsrv"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/secret"
)

// --------------------------------------------------------- per-project routes

func (s *Server) mountProjectGit(r fiber.Router) {
	g := r.Group("/git")

	g.Get("/", func(c *fiber.Ctx) error {
		p := project(c)
		ctx := c.UserContext()
		repo := repoOf(p)
		commits, err := s.Git.Log(ctx, repo, p.Slug, c.QueryInt("limit", 30))
		if err != nil {
			return httpx.Internal("the history could not be read").WithCause(err)
		}
		head, headErr := s.Git.BranchHead(ctx, repo, p.Slug)
		return c.JSON(fiber.Map{
			"branch":       p.Slug,
			"repository":   s.Git.CloneURL(repo),
			"cloneCommand": "git clone -b " + p.Slug + " --single-branch " + s.Git.CloneURL(repo),
			"tracked":      p.GitTracked,
			"head":         head,
			"hasHistory":   headErr == nil,
			"commits":      commits,
		})
	})

	// Versioning is a decision: this is the button that makes one.
	g.Post("/commit", func(c *fiber.Ctx) error {
		if err := writable(c); err != nil {
			return err
		}
		p := project(c)
		var in struct {
			Message string `json:"message"`
		}
		_ = c.BodyParser(&in)
		if strings.TrimSpace(in.Message) == "" {
			in.Message = "Update " + p.Title
		}
		author, email := authorOf(c)
		hash, changed, err := s.Files.Commit(c.UserContext(), p, in.Message, author, email)
		if err != nil {
			return httpx.Internal("the commit failed").WithCause(err)
		}
		if !changed {
			return c.JSON(fiber.Map{"changed": false, "head": hash,
				"message": "Nothing has changed since the last commit."})
		}
		return c.JSON(fiber.Map{"changed": true, "head": hash})
	})
}

func repoOf(p *model.Project) string {
	if p.GroupSlug == "" {
		return gitsrv.UngroupedRepo
	}
	return p.GroupSlug
}

// ------------------------------------------------------------- git transport

// mountGitTransport serves smart HTTP under /git/<group-slug>.git/…
//
// Access is checked at the group, because the repository belongs to the group.
// Projects with a stricter visibility have their branches left out of the
// advertisement, so a clone of a public group does not hand out a private
// project.
func (s *Server) mountGitTransport(app *fiber.App) {
	handler := func(c *fiber.Ctx) error {
		repo := strings.TrimSuffix(c.Params("repo"), ".git")
		rest := c.Params("*")

		var service string
		advertise := false
		switch {
		case rest == "info/refs":
			service = strings.TrimPrefix(c.Query("service"), "git-")
			advertise = true
		case rest == "git-upload-pack":
			service = "upload-pack"
		case rest == "git-receive-pack":
			service = "receive-pack"
		default:
			// The dumb protocol is not served; every git since 1.6.6 speaks the
			// smart one, and saying so beats a silent 404.
			return httpx.NotFound("This server speaks only git's smart HTTP protocol.")
		}
		if !gitsrv.ValidService(service) {
			return httpx.BadRequest("Unknown git service.")
		}

		ctx := c.UserContext()
		grp, err := s.Store.GroupBySlug(ctx, repo)
		if err != nil && repo != gitsrv.UngroupedRepo {
			return gitAuthFail(c, "There is no repository at this address.", false)
		}

		writing := service == "receive-pack"
		actor, ok := s.gitActor(c, grp, writing)
		if !ok {
			return gitAuthFail(c, "This repository needs a login.", true)
		}

		projects, err := s.gitProjects(ctx, grp)
		if err != nil {
			return httpx.Internal("the projects could not be read").WithCause(err)
		}

		var hidden, allowed []string
		for i := range projects {
			p := &projects[i]
			if !access.CanReadProject(actor, p) {
				hidden = append(hidden, p.Slug)
				continue
			}
			if writing && s.pushable(actor, p, grp) {
				allowed = append(allowed, "refs/heads/"+p.Slug)
			}
		}
		if writing && grp != nil && !grp.ReadOnly && actor.IsUser() {
			allowed = append(allowed, "refs/heads/main")
		}

		if writing && len(allowed) == 0 {
			return gitAuthFail(c, "Nothing in this repository accepts a push: it is read-only or you may not write here.", false)
		}

		rpc := gitsrv.RPC{
			GroupSlug:   repo,
			Service:     service,
			Advertise:   advertise,
			Protocol:    c.Get("Git-Protocol"),
			HiddenRefs:  hidden,
			AllowedRefs: allowed,
			DenyMessage: "home-projects: this branch is read-only or does not belong to you.",
		}

		c.Set("Content-Type", gitsrv.ContentType(service, advertise))
		c.Set("Cache-Control", "no-cache, max-age=0, must-revalidate")

		var out bytes.Buffer
		if advertise {
			out.WriteString(gitsrv.AdvertisementPrefix(service))
			rpc.Stdout = &out
			if err := s.Git.Run(ctx, rpc); err != nil {
				slog.Warn("git advertise failed", "repo", repo, "error", err)
				return httpx.Internal("git could not answer").WithCause(err)
			}
			return c.Send(out.Bytes())
		}

		body, err := gitBody(c)
		if err != nil {
			return err
		}
		rpc.Stdin = body
		rpc.Stdout = &out
		if err := s.Git.Run(ctx, rpc); err != nil {
			slog.Warn("git rpc failed", "repo", repo, "service", service, "error", err)
			// git reports refusals in its own protocol; the message is already
			// in the stream when the hook rejected a ref.
			if out.Len() > 0 {
				return c.Send(out.Bytes())
			}
			return httpx.Internal("git could not answer").WithCause(err)
		}

		if writing {
			s.afterPush(c, repo, grp)
		}
		return c.Send(out.Bytes())
	}

	app.All("/git/:repo/*", handler)
}

func gitBody(c *fiber.Ctx) (io.Reader, error) {
	body := c.Body()
	if strings.Contains(c.Get("Content-Encoding"), "gzip") {
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, httpx.BadRequest("The request body could not be unpacked.")
		}
		defer zr.Close()
		plain, err := io.ReadAll(zr)
		if err != nil {
			return nil, httpx.BadRequest("The request body could not be unpacked.")
		}
		return bytes.NewReader(plain), nil
	}
	return bytes.NewReader(body), nil
}

// gitActor resolves basic auth. The password in the basic-auth field is the
// group's or the project's — not the user account's, unless the user name
// matches an account.
func (s *Server) gitActor(c *fiber.Ctx, grp *model.Group, writing bool) (*auth.Actor, bool) {
	actor := auth.From(c)
	ctx := c.UserContext()

	user, pass, hasBasic := basicAuth(c)
	if hasBasic {
		// Failed git logins are counted and throttled like every other one.
		if fails, _ := s.Store.RecentFailures(ctx, "git", user, 15*time.Minute); fails >= 8 {
			return nil, false
		}
		// A real account first, so `git push` works with the owner's login.
		if u, err := s.Store.UserByName(ctx, user); err == nil && secret.Verify(pass, u.PasswordHash) {
			s.Store.RecordAttempt(ctx, "git", user, auth.ClientIP(c), true)
			return &auth.Actor{User: u, Unlocked: actor.Unlocked}, true
		}
		// The password in the basic-auth field may also be the group's own.
		if grp != nil && grp.Visibility == model.VisibilityPassword && grp.PasswordHash != "" &&
			secret.Verify(pass, grp.PasswordHash) {
			s.Store.RecordAttempt(ctx, "git", grp.Slug, auth.ClientIP(c), true)
			return &auth.Actor{Unlocked: map[uuid.UUID]bool{grp.ID: true}}, true
		}
		// Tokens work too: the user name can be anything, the password is the
		// token.
		if tok, _, err := s.Store.TokenByHash(ctx, secret.Fingerprint(pass)); err == nil {
			return &auth.Actor{Token: tok}, true
		}
		s.Store.RecordAttempt(ctx, "git", user, auth.ClientIP(c), false)
		return nil, false
	}

	if actor.IsUser() {
		return actor, true
	}
	if writing {
		return nil, false // a push always needs credentials
	}
	if grp == nil {
		return nil, false
	}
	switch grp.Visibility {
	case model.VisibilityPublic:
		return actor, true
	case model.VisibilityPassword:
		if actor.HasUnlocked(grp.ID) {
			return actor, true
		}
	}
	return nil, false
}

func (s *Server) gitProjects(ctx context.Context, grp *model.Group) ([]model.Project, error) {
	if grp == nil {
		return s.Store.ListProjects(ctx, nil, true, true)
	}
	return s.Store.ListProjects(ctx, &grp.ID, false, true)
}

// pushable decides whether a branch may be written. Read-only means read-only
// everywhere: a push into a frozen project is refused, even with valid
// credentials.
func (s *Server) pushable(actor *auth.Actor, p *model.Project, grp *model.Group) bool {
	if p.EffectiveReadOnly(grp) {
		return false
	}
	if actor.IsUser() {
		return true
	}
	if actor.Token != nil && actor.Token.ProjectID != nil && *actor.Token.ProjectID == p.ID &&
		(actor.Token.Scope == "git" || actor.Token.Scope == "write") {
		return true
	}
	return false
}

// afterPush makes the working trees follow what was pushed.
func (s *Server) afterPush(c *fiber.Ctx, repo string, grp *model.Group) {
	ctx := c.UserContext()
	projects, err := s.gitProjects(ctx, grp)
	if err != nil {
		return
	}
	for i := range projects {
		p := &projects[i]
		if _, err := s.Git.BranchHead(ctx, repo, p.Slug); err != nil {
			continue
		}
		if s.Git.TreeMatchesBranch(ctx, p.ID, repo, p.Slug) {
			continue
		}
		if err := s.Git.Checkout(ctx, p.ID, repo, p.Slug); err != nil {
			slog.Warn("working tree could not follow the push", "project", p.Slug, "error", err)
			continue
		}
		slog.Info("working tree updated after push", "project", p.Slug)
		s.reindex(c, p)
		s.Vars.Refresh(ctx, p)
		s.Bus.Publish(events.Event{Kind: events.PushReceived, ProjectID: p.ID})
	}
}

func basicAuth(c *fiber.Ctx) (string, string, bool) {
	header := c.Get("Authorization")
	if !strings.HasPrefix(header, "Basic ") {
		return "", "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(header[6:]))
	if err != nil {
		return "", "", false
	}
	user, pass, ok := strings.Cut(string(decoded), ":")
	return user, pass, ok
}

func gitAuthFail(c *fiber.Ctx, message string, askForCredentials bool) error {
	if askForCredentials {
		c.Set("WWW-Authenticate", `Basic realm="home-projects"`)
		return httpx.Unauthorized("%s", message)
	}
	return httpx.Forbidden("%s", message)
}

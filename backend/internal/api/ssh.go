package api

import (
	"crypto/hmac"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/access"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/gitsrv"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
)

// Git over SSH.
//
// Keys are registered here. sshd asks this server for them on every connection
// (AuthorizedKeysCommand), so there is no authorized_keys file anywhere and
// nothing that can drift from the database — a key removed here stops working
// at once.
//
// Each key carries a forced command. That command asks the endpoints at the
// bottom of this file the same questions the HTTPS handler asks itself — which
// branches this key may see, which it may write — and reports back afterwards
// so the working trees follow. A push over SSH therefore obeys read-only and
// hidden branches exactly like a push over HTTPS.

func (s *Server) mountSSH(r fiber.Router) {
	keys := r.Group("/ssh-keys", requireOwner)

	keys.Get("/", func(c *fiber.Ctx) error {
		list, err := s.Store.ListSSHKeys(c.UserContext())
		if err != nil {
			return httpx.Internal("the keys could not be read").WithCause(err)
		}
		return c.JSON(fiber.Map{
			"keys":    list,
			"enabled": s.Cfg.SSHEnabled(),
			"host":    s.Cfg.GitSSHHost,
			"note":    sshNote(s),
		})
	})

	keys.Post("/", func(c *fiber.Ctx) error {
		if err := s.stepUp(c, "adding a key that may reach the repositories"); err != nil {
			return err
		}
		var in struct {
			Name string `json:"name"`
			Key  string `json:"key"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The key could not be read.")
		}
		normalised, fingerprint, err := gitsrv.ParsePublicKey(in.Key)
		if err != nil {
			return httpx.BadRequest("%v", err)
		}
		if strings.TrimSpace(in.Name) == "" {
			in.Name = "key"
		}
		actor := auth.From(c)
		key, err := s.Store.CreateSSHKey(c.UserContext(), actor.User.ID, in.Name, normalised, fingerprint)
		if err != nil {
			return httpx.Conflict("This key is already registered.")
		}
		s.Store.Audit(c.UserContext(), actor.UserID(), "ssh_key.added", fingerprint, auth.ClientIP(c), nil)
		return c.Status(fiber.StatusCreated).JSON(key)
	})

	keys.Delete("/:id", func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return httpx.BadRequest("That is not a key id.")
		}
		if err := s.Store.DeleteSSHKey(c.UserContext(), id); err != nil {
			return httpx.NotFound("There is no such key.")
		}
		s.Store.Audit(c.UserContext(), auth.From(c).UserID(), "ssh_key.removed", id.String(), auth.ClientIP(c), nil)
		return httpx.OK(c)
	})

	// ---- what sshd and the wrapper on the host call -------------------------
	ssh := r.Group("/git/ssh", s.requireSSHSecret)

	// sshd asks this on every connection (AuthorizedKeysCommand). There is no
	// authorized_keys file anywhere: a key that was just removed stops working
	// at once, and nothing can drift from what is in the database.
	ssh.Get("/keys", func(c *fiber.Ctx) error {
		list, err := s.Store.ListSSHKeys(c.UserContext())
		if err != nil {
			return httpx.Internal("the keys could not be read").WithCause(err)
		}
		entries := make([]gitsrv.AuthorizedKey, 0, len(list))
		for _, k := range list {
			entries = append(entries, gitsrv.AuthorizedKey{
				ID: k.ID.String(), Name: k.Name, PublicKey: k.PublicKey,
			})
		}
		c.Set("Content-Type", "text/plain; charset=utf-8")
		return c.SendString(gitsrv.AuthorizedKeys(s.Cfg.GitSSHWrapper, entries))
	})

	// authorize is asked before git runs at all: may this key touch this
	// repository, which branches must stay hidden, which may be written.
	ssh.Post("/authorize", func(c *fiber.Ctx) error {
		var in struct {
			KeyID   string `json:"keyId"`
			Repo    string `json:"repo"`
			Service string `json:"service"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The request could not be read.")
		}
		if !gitsrv.ValidService(in.Service) {
			return httpx.BadRequest("Unknown git service.")
		}
		repo, err := gitsrv.RepoFromSSHCommand(in.Repo)
		if err != nil {
			return httpx.BadRequest("%v", err)
		}
		keyID, err := uuid.Parse(in.KeyID)
		if err != nil {
			return httpx.BadRequest("That is not a key id.")
		}

		ctx := c.UserContext()
		ownerID, err := s.Store.SSHKeyOwner(ctx, keyID)
		if err != nil {
			return httpx.Unauthorized("This key is not registered any more.")
		}
		user, err := s.Store.UserByID(ctx, ownerID)
		if err != nil {
			return httpx.Unauthorized("The account behind this key no longer exists.")
		}
		// A key belongs to the owner, so the actor is that owner — the same
		// actor the HTTPS path builds from a session.
		actor := &auth.Actor{User: user}

		var grp *model.Group
		if repo != gitsrv.UngroupedRepo {
			grp, err = s.Store.GroupBySlug(ctx, repo)
			if err != nil {
				return c.JSON(fiber.Map{"allowed": false, "message": "There is no repository at this address."})
			}
		}
		if !s.Git.RepoExists(repo) {
			return c.JSON(fiber.Map{"allowed": false, "message": "There is no repository at this address."})
		}

		projects, err := s.gitProjects(ctx, grp)
		if err != nil {
			return httpx.Internal("the projects could not be read").WithCause(err)
		}
		writing := in.Service == "receive-pack"

		hidden := []string{}
		allowed := []string{}
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
		if writing && grp != nil && !grp.ReadOnly {
			allowed = append(allowed, "refs/heads/main")
		}
		if writing && len(allowed) == 0 {
			return c.JSON(fiber.Map{
				"allowed": false,
				"message": "Nothing in this repository accepts a push: it is read-only, or none of it is yours.",
			})
		}

		s.Store.Audit(ctx, &ownerID, "git.ssh", repo, auth.ClientIP(c),
			map[string]any{"service": in.Service, "key": keyID.String()})

		return c.JSON(fiber.Map{
			"allowed":     true,
			"repoPath":    s.Git.RepoPath(repo),
			"hiddenRefs":  hidden,
			"allowedRefs": allowed,
			"denyMessage": "home-projects: this branch is read-only or does not belong to you.",
		})
	})

	// synced is called after a push, so the working trees follow it — the same
	// step the HTTPS handler takes when receive-pack returns.
	ssh.Post("/synced", func(c *fiber.Ctx) error {
		var in struct {
			Repo string `json:"repo"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The request could not be read.")
		}
		repo, err := gitsrv.RepoFromSSHCommand(in.Repo)
		if err != nil {
			return httpx.BadRequest("%v", err)
		}
		var grp *model.Group
		if repo != gitsrv.UngroupedRepo {
			grp, _ = s.Store.GroupBySlug(c.UserContext(), repo)
		}
		s.afterPush(c, repo, grp)
		return httpx.OK(c)
	})
}

// requireSSHSecret guards the two endpoints the host wrapper calls. Without the
// shared secret they do not exist.
func (s *Server) requireSSHSecret(c *fiber.Ctx) error {
	if !s.Cfg.SSHEnabled() {
		return httpx.NotFound("Git over SSH is not set up on this server.")
	}
	given := c.Get("X-Git-Ssh-Secret")
	if given == "" || !hmac.Equal([]byte(given), []byte(s.Cfg.GitSSHSecret)) {
		return httpx.Unauthorized("Wrong secret.")
	}
	return c.Next()
}

func sshNote(s *Server) string {
	switch {
	case s.Cfg.GitSSHHost == "":
		return "Git over SSH is not set up: GIT_SSH_HOST is unset. Cloning over HTTPS works regardless."
	case s.Cfg.GitSSHSecret == "":
		return "GIT_SSH_HOST is set but GIT_SSH_SECRET is not, so the wrapper on the host could not authenticate. SSH stays off until both are there."
	}
	return ""
}

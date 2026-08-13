package api

import (
	"path"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/access"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/workspace"
)

// Links are the answer to "the same content in two places": not a second
// membership and not a copy, but a second name for one thing. Removing a link
// never deletes the original.
func (s *Server) mountLinks(r fiber.Router) {
	g := r.Group("/links")

	g.Get("/", requireOwner, func(c *fiber.Ctx) error {
		ctx := c.UserContext()
		if ref := c.Query("project"); ref != "" {
			p, err := s.lookupProject(c, ref)
			if err != nil {
				return err
			}
			into, err := s.Store.LinksInto(ctx, p.ID)
			if err != nil {
				return httpx.Internal("the links could not be read").WithCause(err)
			}
			from, err := s.Store.LinksFrom(ctx, p.ID)
			if err != nil {
				return httpx.Internal("the links could not be read").WithCause(err)
			}
			return c.JSON(fiber.Map{"into": into, "from": from})
		}
		all, err := s.Store.AllLinks(ctx)
		if err != nil {
			return httpx.Internal("the links could not be read").WithCause(err)
		}
		return c.JSON(fiber.Map{"links": all})
	})

	g.Post("/", requireOwner, func(c *fiber.Ctx) error {
		var in struct {
			Kind          string `json:"kind"` // folder | file
			SourceProject string `json:"sourceProject"`
			SourcePath    string `json:"sourcePath"`
			TargetProject string `json:"targetProject"`
			TargetPath    string `json:"targetPath"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The link could not be read.")
		}
		if in.Kind != "folder" && in.Kind != "file" {
			return httpx.BadRequest("A link is either a folder or a file.")
		}
		ctx := c.UserContext()

		source, err := s.lookupProject(c, in.SourceProject)
		if err != nil {
			return err
		}
		target, err := s.lookupProject(c, in.TargetProject)
		if err != nil {
			return err
		}
		if source.ID == target.ID {
			return httpx.BadRequest("A link inside one project would only be a second name for the same folder.")
		}
		actor := auth.From(c)
		if err := access.RequireReadProject(actor, source); err != nil {
			return err
		}
		var targetGroup = group(c)
		if target.GroupID != nil {
			targetGroup, _ = s.Store.GroupByID(ctx, *target.GroupID)
		}
		if err := access.RequireWriteProject(actor, target, targetGroup); err != nil {
			return err
		}

		sourcePath, err := workspace.Clean(in.SourcePath)
		if err != nil {
			return httpx.BadRequest("The source path is not valid.")
		}
		targetPath, err := workspace.Clean(in.TargetPath)
		if err != nil {
			return httpx.BadRequest("The target path is not valid.")
		}
		if targetPath == "" {
			targetPath = path.Base(sourcePath)
			if targetPath == "." || targetPath == "/" {
				targetPath = source.Slug
			}
		}
		if !s.Files.Exists(source, sourcePath) {
			return httpx.NotFound("There is nothing at %q in %s.", sourcePath, source.Title)
		}
		if s.Files.Exists(target, targetPath) {
			return httpx.Conflict("%s already has something at %q.", target.Title, targetPath)
		}

		// The folder the link appears in has to exist, otherwise the entry
		// would have nowhere to show up.
		if parent := path.Dir(targetPath); parent != "." && parent != "" {
			if !s.Files.Exists(target, parent) {
				if err := s.Files.Mkdir(ctx, target, parent, files.Op{Commit: false}); err != nil {
					return err
				}
			}
		}

		link, err := s.Store.CreateLink(ctx, actor.User.ID, in.Kind, source.ID, sourcePath, target.ID, targetPath)
		if err != nil {
			return httpx.Conflict("There is already a link at %q.", targetPath)
		}
		s.Store.Audit(ctx, actor.UserID(), "link.created", source.Slug+"→"+target.Slug, auth.ClientIP(c), nil)
		return c.Status(fiber.StatusCreated).JSON(link)
	})

	g.Delete("/:kind/:id", requireOwner, func(c *fiber.Ctx) error {
		kind := c.Params("kind")
		if kind != "folder" && kind != "file" {
			return httpx.BadRequest("A link is either a folder or a file.")
		}
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return httpx.BadRequest("That is not a link id.")
		}
		if err := s.Store.DeleteLink(c.UserContext(), kind, id); err != nil {
			return httpx.NotFound("There is no such link.")
		}
		// Only the link goes. The original stays exactly where it was.
		return c.JSON(fiber.Map{"ok": true, "originalKept": true})
	})
}

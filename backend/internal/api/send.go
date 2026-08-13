package api

import (
	"path"

	"github.com/gofiber/fiber/v2"
	"github.com/offlinebot/home-projects/backend/internal/access"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/workspace"
)

// Sending something from one project into another, in the three ways that are
// meaningfully different:
//
//	link  — a second name for the same thing. No copy: edits act on the source,
//	        and removing the link never touches the original.
//	copy  — a second, independent thing. They drift apart from now on.
//	move  — it leaves the first project and lives in the second.
//
// This is the shape the work has: one project pulls material in from outside,
// another is where it gets arranged. A link keeps the arrangement pointing at
// what the scheduler refreshes; a copy freezes it as it was.
func (s *Server) mountSend(r fiber.Router) {
	r.Post("/files/send", func(c *fiber.Ctx) error {
		source := project(c)
		ctx := c.UserContext()
		actor := auth.From(c)

		var in struct {
			Path          string `json:"path"`
			TargetProject string `json:"targetProject"`
			TargetPath    string `json:"targetPath"`
			Mode          string `json:"mode"` // link | copy | move
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The request could not be read.")
		}
		switch in.Mode {
		case "link", "copy", "move":
		default:
			return httpx.BadRequest("Say what to do: link, copy or move.")
		}

		from, err := workspace.Clean(in.Path)
		if err != nil || from == "" {
			return httpx.BadRequest("Name the file or folder to send.")
		}
		target, err := s.lookupProject(c, in.TargetProject)
		if err != nil {
			return err
		}
		if target.ID == source.ID {
			return httpx.BadRequest("That is the project it is already in.")
		}

		var targetGroup *model.Group
		if target.GroupID != nil {
			targetGroup, _ = s.Store.GroupByID(ctx, *target.GroupID)
		}
		if err := access.RequireWriteProject(actor, target, targetGroup); err != nil {
			return err
		}
		// Moving takes it out of the first project, so that one has to accept
		// changes too. Linking and copying only read it.
		if in.Mode == "move" {
			if err := writable(c); err != nil {
				return err
			}
		}

		entry, err := s.WS.Open(source.ID).Stat(from)
		if err != nil {
			return httpx.NotFound("There is nothing at %q in %s.", from, source.Title)
		}

		to, err := workspace.Clean(in.TargetPath)
		if err != nil {
			return httpx.BadRequest("That target path is not valid.")
		}
		if to == "" {
			to = entry.Name
		}
		if s.Files.Exists(target, to) {
			return httpx.Conflict("%s already has something at %q.", target.Title, to)
		}
		// The folder it lands in has to exist, or the entry would have nowhere
		// to appear.
		if parent := path.Dir(to); parent != "." && parent != "" && !s.Files.Exists(target, parent) {
			if err := s.Files.Mkdir(ctx, target, parent, files.Op{Commit: false}); err != nil {
				return err
			}
		}

		author, email := authorOf(c)
		op := files.Op{Author: author, Email: email, Commit: true}

		switch in.Mode {
		case "link":
			kind := "file"
			if entry.IsDir {
				kind = "folder"
			}
			link, err := s.Store.CreateLink(ctx, actor.User.ID, kind, source.ID, from, target.ID, to)
			if err != nil {
				return httpx.Conflict("There is already a link at %q.", to)
			}
			s.Store.Audit(ctx, actor.UserID(), "link.created", source.Slug+"→"+target.Slug, auth.ClientIP(c), nil)
			return c.Status(fiber.StatusCreated).JSON(fiber.Map{"mode": "link", "link": link})

		case "copy":
			op.Message = "Copy " + from + " from " + source.Title
			if err := s.Files.Copy(ctx, source, from, target, to, op); err != nil {
				return err
			}

		case "move":
			op.Message = "Move " + from + " from " + source.Title
			if err := s.Files.Copy(ctx, source, from, target, to, op); err != nil {
				return err
			}
			// Only once the copy is on disk does the original go.
			if err := s.Files.Remove(ctx, source, from, entry.IsDir, files.Op{
				Author: author, Email: email, Message: "Moved " + from + " to " + target.Title, Commit: true,
			}); err != nil {
				return err
			}
			s.reindex(c, source, from)
			s.Vars.Refresh(ctx, source)
		}

		s.reindex(c, target, to)
		s.Vars.Refresh(ctx, target)
		return c.JSON(fiber.Map{"mode": in.Mode, "path": to, "project": target.Slug})
	})
}

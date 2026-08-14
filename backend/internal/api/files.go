package api

import (
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"path"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/workspace"
)

// reindex asks every switched-on capability to rebuild whatever index it keeps
// over a project's files. Ordinary writes already do this through the files
// service; this is for the cases that change a project without writing —
// switching a capability on, a push, a duplicate.
func (s *Server) reindex(c *fiber.Ctx, p *model.Project, changed ...string) {
	paths := changed
	if len(paths) == 0 {
		paths = []string{""}
	}
	for _, name := range p.Capabilities {
		cap, ok := capability.Get(name)
		if !ok {
			continue
		}
		for _, rel := range paths {
			if err := cap.Index(c.UserContext(), s.Env, p, rel); err != nil {
				// An index that cannot be built is reported in the log, not
				// swallowed — but it does not undo the write that caused it.
				slog.Warn("index could not be updated", "capability", name,
					"project", p.Slug, "error", err)
			}
		}
	}
}

func (s *Server) mountFiles(r fiber.Router) {
	f := r.Group("/files")

	f.Get("/", func(c *fiber.Ctx) error {
		p := project(c)
		entries, err := s.Files.List(c.UserContext(), auth.From(c), p, c.Query("path"))
		if err != nil {
			return err
		}
		clean, _ := workspace.Clean(c.Query("path"))
		return c.JSON(fiber.Map{
			"path":     clean,
			"entries":  entries,
			"readOnly": writable(c) != nil,
			"parent":   parentOf(clean),
		})
	})

	// Searching the whole project, not the folder you happen to stand in.
	// Three hundred lecture slides in twenty folders is a filing cabinet, and a
	// filing cabinet without a search is a pile.
	f.Get("/search", func(c *fiber.Ctx) error {
		p := project(c)
		needle := strings.ToLower(strings.TrimSpace(c.Query("q")))
		if needle == "" {
			return c.JSON(fiber.Map{"entries": []workspace.Entry{}})
		}
		limit := c.QueryInt("limit", 300)
		fs := s.WS.Open(p.ID)
		found := []workspace.Entry{}
		_ = fs.Walk("", func(e workspace.Entry) error {
			if len(found) >= limit {
				return nil
			}
			// The path counts too: "analysis kap 3" should find a file called
			// "Kap 3.pdf" inside the folder "Analysis".
			if strings.Contains(strings.ToLower(e.Path), needle) || matchesWords(e.Path, needle) {
				found = append(found, e)
			}
			return nil
		})
		return c.JSON(fiber.Map{"entries": found, "limit": limit})
	})

	f.Get("/content", func(c *fiber.Ctx) error {
		p := project(c)
		body, res, err := s.Files.Read(c.UserContext(), p, c.Query("path"))
		if err != nil {
			return err
		}
		if len(body) > 4<<20 {
			return httpx.BadRequest("This file is too large for the editor. Download it instead.")
		}
		if !workspace.IsText(res.Path, body) {
			return httpx.BadRequest("This file is not text. Download it instead.")
		}
		return c.JSON(fiber.Map{
			"path": c.Query("path"), "content": string(body),
			"mimeType": workspace.MimeOf(res.Path),
			"linked":   res.Link != nil,
		})
	})

	f.Put("/content", func(c *fiber.Ctx) error {
		if err := writable(c); err != nil {
			return err
		}
		p := project(c)
		var in struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The file could not be read.")
		}
		author, email := authorOf(c)
		res, err := s.Files.Write(c.UserContext(), p, in.Path, []byte(in.Content), files.Op{
			Author: author, Email: email, Message: "Edit " + in.Path, Commit: true,
		})
		if err != nil {
			return err
		}
		s.reindex(c, res.Project, res.Path)
		return httpx.OK(c)
	})

	f.Post("/folder", func(c *fiber.Ctx) error {
		if err := writable(c); err != nil {
			return err
		}
		p := project(c)
		var in struct {
			Path string `json:"path"`
			Name string `json:"name"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The folder could not be read.")
		}
		if in.Name != "" {
			if err := workspace.ValidName(in.Name); err != nil {
				return httpx.BadRequest("%q is not a valid name.", in.Name)
			}
			in.Path = path.Join(in.Path, in.Name)
		}
		author, email := authorOf(c)
		if err := s.Files.Mkdir(c.UserContext(), p, in.Path, files.Op{
			Author: author, Email: email, Message: "Add folder " + in.Path, Commit: false,
		}); err != nil {
			return err
		}
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"path": in.Path})
	})

	// One dialog uploads whole folders: the browser sends each file with its
	// relative path, and they land under the target folder.
	f.Post("/upload", func(c *fiber.Ctx) error {
		if err := writable(c); err != nil {
			return err
		}
		p := project(c)
		form, err := c.MultipartForm()
		if err != nil {
			return httpx.BadRequest("The upload could not be read: %v", err)
		}
		target := ""
		if v := form.Value["path"]; len(v) > 0 {
			target = v[0]
		}
		folder := ""
		if v := form.Value["folder"]; len(v) > 0 {
			folder = v[0]
		}
		relPaths := form.Value["paths"]

		uploaded := []string{}
		author, email := authorOf(c)
		for i, fh := range form.File["files"] {
			name := fh.Filename
			if i < len(relPaths) && relPaths[i] != "" {
				name = relPaths[i]
			}
			dest := path.Join(target, folder, name)
			if _, err := workspace.Clean(dest); err != nil {
				return httpx.BadRequest("%q is not a valid path.", dest)
			}
			if err := s.storeUpload(c, p, dest, fh, author, email); err != nil {
				return err
			}
			uploaded = append(uploaded, dest)
		}
		if len(uploaded) == 0 {
			return httpx.BadRequest("No file was sent.")
		}
		// One commit for the whole upload, not one per file.
		if p.GitTracked {
			if _, _, err := s.Files.Commit(c.UserContext(), p,
				fmt.Sprintf("Upload %d file(s)", len(uploaded)), author, email); err != nil {
				return httpx.Internal("the upload could not be committed").WithCause(err)
			}
		}
		s.reindex(c, p, uploaded...)
		s.Vars.Refresh(c.UserContext(), p)
		return c.JSON(fiber.Map{"uploaded": uploaded})
	})

	f.Post("/move", func(c *fiber.Ctx) error {
		if err := writable(c); err != nil {
			return err
		}
		p := project(c)
		var in struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := c.BodyParser(&in); err != nil {
			return httpx.BadRequest("The move could not be read.")
		}
		author, email := authorOf(c)
		if err := s.Files.Move(c.UserContext(), p, in.From, in.To, files.Op{
			Author: author, Email: email, Message: "Move " + in.From + " to " + in.To, Commit: true,
		}); err != nil {
			return err
		}
		s.reindex(c, p, in.From, in.To)
		return httpx.OK(c)
	})

	f.Delete("/", func(c *fiber.Ctx) error {
		if err := writable(c); err != nil {
			return err
		}
		p := project(c)
		rel := c.Query("path")

		// Removing a link removes only the link. The original is never touched.
		links, err := s.Store.LinksInto(c.UserContext(), p.ID)
		if err != nil {
			return httpx.Internal("the links could not be read").WithCause(err)
		}
		clean, _ := workspace.Clean(rel)
		for _, l := range links {
			if l.TargetPath == clean {
				if err := s.Store.DeleteLink(c.UserContext(), l.Kind, l.ID); err != nil {
					return httpx.Internal("the link could not be removed").WithCause(err)
				}
				return c.JSON(fiber.Map{"removedLink": true})
			}
		}

		author, email := authorOf(c)
		if err := s.Files.Remove(c.UserContext(), p, rel, c.QueryBool("recursive", false), files.Op{
			Author: author, Email: email, Message: "Delete " + rel, Commit: true,
		}); err != nil {
			return err
		}
		s.reindex(c, p, clean)
		s.Vars.Refresh(c.UserContext(), p)
		return httpx.OK(c)
	})

	// The same bytes as /download, but meant to be *shown*: a PDF in a frame,
	// a picture, a recording. A project full of lecture slides is unusable if
	// the only thing you can do with a file is put it on your disk first.
	//
	// Only types the browser can render harmlessly go out inline. HTML and SVG
	// are executable in the origin's own context, so they are handed over as a
	// download like everything else.
	f.Get("/raw", func(c *fiber.Ctx) error {
		p := project(c)
		res, err := s.Files.Resolve(c.UserContext(), p, c.Query("path"))
		if err != nil {
			return err
		}
		fs := s.WS.Open(res.Project.ID)
		entry, err := fs.Stat(res.Path)
		if err != nil || entry.IsDir {
			return httpx.NotFound("There is no file at this path.")
		}
		handle, info, err := fs.OpenFile(res.Path)
		if err != nil {
			return httpx.NotFound("There is no file at this path.")
		}
		mime := workspace.MimeOf(entry.Name)
		disposition := "attachment"
		if showableInline(mime) {
			disposition = "inline"
		}
		c.Set("Content-Type", mime)
		c.Set("Content-Disposition", fmt.Sprintf("%s; filename=%q", disposition, entry.Name))
		c.Set("Content-Length", fmt.Sprintf("%d", info.Size()))
		// Nothing served from here may be sniffed into something executable,
		// and nothing in it may reach back into the page that framed it.
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("Content-Security-Policy", "sandbox; default-src 'none'; object-src 'self'; img-src 'self'; media-src 'self'")
		return c.SendStream(handle, int(info.Size()))
	})

	f.Get("/download", func(c *fiber.Ctx) error {
		p := project(c)
		rel := c.Query("path")
		res, err := s.Files.Resolve(c.UserContext(), p, rel)
		if err != nil {
			return err
		}
		fs := s.WS.Open(res.Project.ID)
		entry, err := fs.Stat(res.Path)
		if err != nil {
			return httpx.NotFound("There is no file at this path.")
		}
		if entry.IsDir {
			name := entry.Name
			if name == "" {
				name = p.Slug
			}
			c.Set("Content-Type", "application/zip")
			c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name+".zip"))
			return fs.Zip(c.Response().BodyWriter(), res.Path)
		}
		handle, info, err := fs.OpenFile(res.Path)
		if err != nil {
			return httpx.NotFound("There is no file at this path.")
		}
		c.Set("Content-Type", workspace.MimeOf(entry.Name))
		c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", entry.Name))
		c.Set("Content-Length", fmt.Sprintf("%d", info.Size()))
		return c.SendStream(handle, int(info.Size()))
	})

	// The whole project as a zip — also offered before deleting it.
	r.Get("/download", func(c *fiber.Ctx) error {
		p := project(c)
		c.Set("Content-Type", "application/zip")
		c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", p.Slug+".zip"))
		return s.WS.Open(p.ID).Zip(c.Response().BodyWriter(), "")
	})
}

func (s *Server) storeUpload(c *fiber.Ctx, p *model.Project, dest string, fh *multipart.FileHeader, author, email string) error {
	if fh.Size > s.Cfg.MaxUploadSize {
		return httpx.BadRequest("%s is larger than the upload limit of %d MB.",
			fh.Filename, s.Cfg.MaxUploadSize>>20)
	}
	handle, err := fh.Open()
	if err != nil {
		return httpx.BadRequest("%s could not be read.", fh.Filename)
	}
	defer handle.Close()
	_, err = s.Files.WriteFrom(c.UserContext(), p, dest, io.LimitReader(handle, s.Cfg.MaxUploadSize), files.Op{
		Author: author, Email: email, Commit: false,
	})
	return err
}

func parentOf(p string) string {
	if p == "" {
		return ""
	}
	parent := path.Dir(p)
	if parent == "." {
		return ""
	}
	return parent
}

var _ = strings.TrimSpace

// showableInline decides what a browser may render in place. The list is short
// and made of things that cannot execute: documents, pictures, sound, film,
// plain text. SVG is a picture that can carry script, so it is not on it.
func showableInline(mime string) bool {
	base, _, _ := strings.Cut(mime, ";")
	base = strings.ToLower(strings.TrimSpace(base))
	switch base {
	case "application/pdf", "text/plain":
		return true
	case "image/svg+xml":
		return false
	}
	for _, family := range []string{"image/", "audio/", "video/"} {
		if strings.HasPrefix(base, family) {
			return true
		}
	}
	return false
}

// matchesWords lets a search be typed the way a thing is remembered: several
// words, in any order, anywhere in the path.
func matchesWords(path, needle string) bool {
	lower := strings.ToLower(path)
	for _, word := range strings.Fields(needle) {
		if !strings.Contains(lower, word) {
			return false
		}
	}
	return true
}

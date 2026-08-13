package api

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/workspace"
)

// A project can be started from a zip: upload the archive, and its contents
// become the project's file tree. It is the counterpart of the download every
// project offers, so a project can be carried from one server to another.

const (
	maxZipEntries = 20000
	maxZipFile    = 2 << 30 // 2 GiB unpacked per file
)

func (s *Server) mountZip(r fiber.Router) {
	r.Post("/files/import-zip", func(c *fiber.Ctx) error {
		if err := writable(c); err != nil {
			return err
		}
		p := project(c)

		header, err := c.FormFile("file")
		if err != nil {
			return httpx.BadRequest("No zip file was sent.")
		}
		if header.Size > s.Cfg.MaxUploadSize {
			return httpx.BadRequest("The archive is larger than the upload limit of %d MB.",
				s.Cfg.MaxUploadSize>>20)
		}
		handle, err := header.Open()
		if err != nil {
			return httpx.BadRequest("The archive could not be read.")
		}
		defer handle.Close()

		body, err := io.ReadAll(io.LimitReader(handle, s.Cfg.MaxUploadSize))
		if err != nil {
			return httpx.BadRequest("The archive could not be read.")
		}
		reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			return httpx.BadRequest("This is not a zip archive: %v", err)
		}

		target := c.FormValue("path")
		strip := c.FormValue("strip") != "false" // drop a single wrapping folder
		clear := c.FormValue("clear") == "true"

		written, skipped, err := s.unzip(c, p, reader, target, strip, clear)
		if err != nil {
			return err
		}
		s.reindex(c, p)
		s.Vars.Refresh(c.UserContext(), p)
		return c.JSON(fiber.Map{"files": written, "skipped": skipped})
	})
}

func (s *Server) unzip(c *fiber.Ctx, p *model.Project, reader *zip.Reader, target string, strip, clear bool) (int, []string, error) {
	if len(reader.File) > maxZipEntries {
		return 0, nil, httpx.BadRequest("The archive has more than %d entries.", maxZipEntries)
	}

	// A zip made from one folder usually wraps everything in that folder. That
	// wrapper is dropped, so the project does not gain a pointless level.
	prefix := ""
	if strip {
		prefix = commonFolder(reader)
	}

	if clear {
		fs, err := s.WS.For(p.ID)
		if err != nil {
			return 0, nil, httpx.Internal("the project folder could not be opened").WithCause(err)
		}
		entries, err := fs.List("")
		if err == nil {
			for _, e := range entries {
				_ = fs.Remove(e.Path, true)
			}
		}
	}

	author, email := authorOf(c)
	written := 0
	skipped := []string{}

	for _, entry := range reader.File {
		name := strings.ReplaceAll(entry.Name, "\\", "/")
		if prefix != "" {
			if name == prefix || name == prefix+"/" {
				continue
			}
			name = strings.TrimPrefix(name, prefix+"/")
		}
		if name == "" || strings.HasSuffix(name, "/") {
			continue // a folder entry; the file writes create it
		}
		// Zip slip: an entry that would leave the project is refused, not
		// cleaned up quietly.
		rel := path.Join(target, name)
		if _, err := workspace.Clean(rel); err != nil {
			skipped = append(skipped, entry.Name)
			continue
		}
		if entry.Mode()&0o170000 == 0o120000 { // symlink
			skipped = append(skipped, entry.Name+" (symlink)")
			continue
		}
		if entry.UncompressedSize64 > maxZipFile {
			skipped = append(skipped, entry.Name+" (too large)")
			continue
		}

		rc, err := entry.Open()
		if err != nil {
			skipped = append(skipped, entry.Name)
			continue
		}
		_, err = s.Files.WriteFrom(c.UserContext(), p, rel, io.LimitReader(rc, maxZipFile), files.Op{
			Author: author, Email: email, Commit: false,
		})
		rc.Close()
		if err != nil {
			return written, skipped, err
		}
		written++
	}

	if written > 0 && p.GitTracked {
		if _, _, err := s.Files.Commit(c.UserContext(), p,
			fmt.Sprintf("Import %d file(s) from an archive", written), author, email); err != nil {
			return written, skipped, httpx.Internal("the import could not be committed").WithCause(err)
		}
	}
	return written, skipped, nil
}

// commonFolder returns the single top-level folder every entry sits in, or "".
func commonFolder(reader *zip.Reader) string {
	first := ""
	for _, entry := range reader.File {
		name := strings.TrimPrefix(strings.ReplaceAll(entry.Name, "\\", "/"), "./")
		if name == "" || strings.HasPrefix(name, "__MACOSX/") {
			continue
		}
		top, _, hasSlash := strings.Cut(name, "/")
		if !hasSlash {
			return "" // a file lies at the top level, so there is no wrapper
		}
		if first == "" {
			first = top
			continue
		}
		if top != first {
			return ""
		}
	}
	return first
}

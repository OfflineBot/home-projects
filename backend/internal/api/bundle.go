package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/blueprint"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/workspace"
)

// A group, packed up and carried somewhere else.
//
// The JSON export describes the arrangement and stops there, which is right
// for handing somebody a shape. It is wrong for the thing that actually
// happens: a group is set up on a laptop, in peace, and then it has to be on
// the server — with its files, its filters, its schedulers, and its accounts
// waiting for their password.
//
// So this is one archive:
//
//	blueprint.json                       the arrangement
//	files/<group>/<project>/…            what is in the projects
//
// It is a plain zip, and the JSON inside it is the same document the export
// already writes, so anything that can read one can read the other. Passwords
// are not in it. They cannot be: the export has never had them.

const bundleFileLimit = 2 << 30

func (s *Server) mountBundle(r fiber.Router) {
	r.Get("/export/bundle", requireOwner, func(c *fiber.Ctx) error {
		what := blueprint.What{
			Schedulers: c.QueryBool("schedulers", true),
			Accounts:   c.QueryBool("accounts", true),
			Filters:    c.QueryBool("filters", true),
		}
		group := c.Query("group")
		doc, err := blueprint.ExportWhat(c.UserContext(), s.Store, group, what)
		if err != nil {
			return httpx.Internal("the export could not be written").WithCause(err)
		}

		name := "home-projects"
		if group != "" {
			name = group
		}
		c.Set("Content-Type", "application/zip")
		c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name+".hpbundle.zip"))

		// Written into memory rather than streamed: a bundle is a thing you
		// hand over, and half of one that failed halfway is worse than an
		// error. The projects are the same ones a zip download already holds.
		buf := &bytes.Buffer{}
		zw := zip.NewWriter(buf)
		body, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return httpx.Internal("the export could not be written").WithCause(err)
		}
		entry, err := zw.Create("blueprint.json")
		if err != nil {
			return httpx.Internal("the archive could not be written").WithCause(err)
		}
		if _, err := entry.Write(append(body, '\n')); err != nil {
			return httpx.Internal("the archive could not be written").WithCause(err)
		}

		if c.QueryBool("files", true) {
			if err := s.packFiles(c.UserContext(), zw, group); err != nil {
				return httpx.Internal("the files could not be packed").WithCause(err)
			}
		}
		if err := zw.Close(); err != nil {
			return httpx.Internal("the archive could not be closed").WithCause(err)
		}
		s.Store.Audit(c.UserContext(), auth.From(c).UserID(), "bundle.exported",
			name, auth.ClientIP(c), nil)
		return c.Send(buf.Bytes())
	})

	r.Post("/import/bundle", requireOwner, func(c *fiber.Ctx) error {
		header, err := c.FormFile("file")
		if err != nil {
			return httpx.BadRequest("No bundle was sent.")
		}
		if header.Size > s.Cfg.MaxUploadSize {
			return httpx.BadRequest("The bundle is larger than the upload limit of %d MB.",
				s.Cfg.MaxUploadSize>>20)
		}
		file, err := header.Open()
		if err != nil {
			return httpx.BadRequest("The bundle could not be read.")
		}
		defer file.Close()
		raw, err := io.ReadAll(io.LimitReader(file, s.Cfg.MaxUploadSize))
		if err != nil {
			return httpx.BadRequest("The bundle could not be read.")
		}
		archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			return httpx.BadRequest("That is not a zip archive.")
		}

		var doc blueprint.Document
		found := false
		for _, f := range archive.File {
			if path.Base(f.Name) != "blueprint.json" || strings.Contains(f.Name, "..") {
				continue
			}
			in, oerr := f.Open()
			if oerr != nil {
				return httpx.BadRequest("The arrangement inside the bundle could not be read.")
			}
			body, rerr := io.ReadAll(io.LimitReader(in, 64<<20))
			in.Close()
			if rerr != nil || json.Unmarshal(body, &doc) != nil {
				return httpx.BadRequest("The arrangement inside the bundle could not be read.")
			}
			found = true
			break
		}
		if !found {
			return httpx.BadRequest("This zip has no blueprint.json in it, so it is not a bundle.")
		}

		apply := c.QueryBool("apply", false)
		if apply {
			if err := s.stepUp(c, "importing a bundle"); err != nil {
				return err
			}
		}
		result, err := s.applyDocument(c, &doc, !apply)
		if err != nil {
			return err
		}
		if !apply {
			// A dry run says what the files would do, without touching one.
			result.Steps = append(result.Steps, blueprint.Step{
				Action: "create", What: "files",
				Name: fmt.Sprintf("%d file(s) in the bundle", countBundleFiles(archive)),
			})
			return c.JSON(result)
		}

		written, err := s.unpackFiles(c.UserContext(), archive, auth.From(c))
		if err != nil {
			return httpx.Internal("the arrangement arrived, but the files did not: %v", err).
				WithDetail(result)
		}
		result.Steps = append(result.Steps, blueprint.Step{
			Action: "create", What: "files", Name: fmt.Sprintf("%d file(s) written", written),
		})
		s.Store.Audit(c.UserContext(), auth.From(c).UserID(), "bundle.imported",
			fmt.Sprintf("%d groups, %d files", len(doc.Groups), written), auth.ClientIP(c), nil)
		return c.JSON(result)
	})
}

// packFiles writes every project's working tree into the archive, under its own
// address, so the other side can put them back where they belong.
func (s *Server) packFiles(ctx context.Context, zw *zip.Writer, groupSlug string) error {
	projects, err := s.Store.ListAllProjects(ctx, true)
	if err != nil {
		return err
	}
	for i := range projects {
		p := &projects[i]
		if groupSlug != "" && p.GroupSlug != groupSlug {
			continue
		}
		base := "files/" + addressOf(p) + "/"
		fsys := s.Env.Files.Workspace().Open(p.ID)
		err := fsys.Walk("", func(e workspace.Entry) error {
			if e.IsDir || e.Size > bundleFileLimit {
				return nil
			}
			out, cerr := zw.CreateHeader(&zip.FileHeader{
				Name: base + e.Path, Method: zip.Deflate, Modified: e.ModifiedAt,
			})
			if cerr != nil {
				return cerr
			}
			in, _, oerr := fsys.OpenFile(e.Path)
			if oerr != nil {
				return oerr
			}
			defer in.Close()
			_, werr := io.Copy(out, in)
			return werr
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// unpackFiles puts the files back into the projects the blueprint has just
// created. A project that is not there is skipped rather than invented: the
// arrangement decides what exists, not the file tree.
func (s *Server) unpackFiles(ctx context.Context, archive *zip.Reader, actor *auth.Actor) (int, error) {
	written := 0
	touched := map[string]*model.Project{}
	for _, f := range archive.File {
		if f.FileInfo().IsDir() || !strings.HasPrefix(f.Name, "files/") {
			continue
		}
		rest := strings.TrimPrefix(f.Name, "files/")
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) < 3 || strings.Contains(rest, "..") {
			continue
		}
		address := parts[0] + "/" + parts[1]
		rel := parts[2]

		project, ok := touched[address]
		if !ok {
			found, err := s.projectAt(ctx, parts[0], parts[1])
			if err != nil {
				touched[address] = nil
				continue
			}
			project = found
			touched[address] = found
		}
		if project == nil {
			continue
		}

		in, err := f.Open()
		if err != nil {
			return written, err
		}
		body, err := io.ReadAll(io.LimitReader(in, bundleFileLimit))
		in.Close()
		if err != nil {
			return written, err
		}
		if _, err := s.Env.Files.Write(ctx, project, rel, body, files.Op{
			Author: "the import", Email: "import@home-projects",
			Message: "Bring in the files from a bundle", Commit: false,
		}); err != nil {
			return written, err
		}
		written++
	}

	// One commit per project, at the end: a bundle is one arrival, not eight
	// hundred.
	for _, p := range touched {
		if p == nil || !p.GitTracked {
			continue
		}
		_, _, _ = s.Env.Files.Commit(ctx, p, "Bring in the files from a bundle",
			"the import", "import@home-projects")
	}
	return written, nil
}

func (s *Server) projectAt(ctx context.Context, groupSlug, projectSlug string) (*model.Project, error) {
	if groupSlug == "ungrouped" {
		return s.Store.ProjectBySlug(ctx, nil, projectSlug)
	}
	g, err := s.Store.GroupBySlug(ctx, groupSlug)
	if err != nil {
		return nil, err
	}
	return s.Store.ProjectBySlug(ctx, &g.ID, projectSlug)
}

func addressOf(p *model.Project) string {
	if p.GroupSlug == "" {
		return "ungrouped/" + p.Slug
	}
	return p.GroupSlug + "/" + p.Slug
}

func countBundleFiles(archive *zip.Reader) int {
	n := 0
	for _, f := range archive.File {
		if !f.FileInfo().IsDir() && strings.HasPrefix(f.Name, "files/") {
			n++
		}
	}
	return n
}

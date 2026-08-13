// Package files is the one way into a project's file tree.
//
// It resolves links, keeps git tracking honest and publishes an event for
// every change. Capabilities, schedulers, automations and the API all go
// through here — nobody writes into a project's directory behind its back.
package files

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path"
	"sort"
	"strings"

	"github.com/offlinebot/home-projects/backend/internal/access"
	"github.com/offlinebot/home-projects/backend/internal/auth"
	"github.com/offlinebot/home-projects/backend/internal/events"
	"github.com/offlinebot/home-projects/backend/internal/gitsrv"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/store"
	"github.com/offlinebot/home-projects/backend/internal/workspace"
)

type Service struct {
	store *store.Store
	ws    *workspace.Store
	git   *gitsrv.Service
	bus   *events.Bus
	// index is set once at startup and lets every write update whatever the
	// project's capabilities keep an index of. It runs synchronously, so a
	// read right after a write already sees the new state.
	index func(ctx context.Context, p *model.Project, path string)
}

// SetIndexer wires the capability indexes in. The files package deliberately
// knows nothing about them beyond this one function.
func (s *Service) SetIndexer(fn func(ctx context.Context, p *model.Project, path string)) {
	s.index = fn
}

func New(st *store.Store, ws *workspace.Store, git *gitsrv.Service, bus *events.Bus) *Service {
	return &Service{store: st, ws: ws, git: git, bus: bus}
}

func (s *Service) Workspace() *workspace.Store { return s.ws }

// Op describes what to do around a change: who made it and whether the
// project's automatic commit should run.
type Op struct {
	Author  string
	Email   string
	Message string
	Commit  bool
}

// Resolved is where a path really lives after links have been followed.
type Resolved struct {
	Project *model.Project
	Path    string
	Link    *model.Link
}

// Resolve follows folder and file links. A path inside a linked folder ends up
// in the source project, with the rest of the path appended — edits act on the
// source, never on a copy.
func (s *Service) Resolve(ctx context.Context, p *model.Project, rel string) (*Resolved, error) {
	clean, err := workspace.Clean(rel)
	if err != nil {
		return nil, httpx.BadRequest("%v", err)
	}
	current := p
	currentPath := clean
	var through *model.Link

	for depth := 0; depth < 8; depth++ {
		links, err := s.store.LinksInto(ctx, current.ID)
		if err != nil {
			return nil, httpx.Internal("links could not be read").WithCause(err)
		}
		best := -1
		bestLen := -1
		for i, l := range links {
			match := l.Kind == "file" && l.TargetPath == currentPath
			if l.Kind == "folder" && (l.TargetPath == currentPath || strings.HasPrefix(currentPath, l.TargetPath+"/")) {
				match = true
			}
			if match && len(l.TargetPath) > bestLen {
				best, bestLen = i, len(l.TargetPath)
			}
		}
		if best < 0 {
			break
		}
		l := links[best]
		rest := strings.TrimPrefix(strings.TrimPrefix(currentPath, l.TargetPath), "/")
		source, err := s.store.ProjectByID(ctx, l.SourceProject)
		if err != nil {
			return nil, httpx.NotFound("The linked project no longer exists.")
		}
		currentPath = l.SourcePath
		if rest != "" {
			currentPath = path.Join(l.SourcePath, rest)
		}
		current = source
		through = &links[best]
	}
	return &Resolved{Project: current, Path: currentPath, Link: through}, nil
}

// List returns a folder's contents with the links pointing into it merged in.
func (s *Service) List(ctx context.Context, actor *auth.Actor, p *model.Project, dir string) ([]workspace.Entry, error) {
	res, err := s.Resolve(ctx, p, dir)
	if err != nil {
		return nil, err
	}
	if res.Project.ID != p.ID {
		if err := access.RequireReadProject(actor, res.Project); err != nil {
			return nil, err
		}
	}
	fs, err := s.ws.For(res.Project.ID)
	if err != nil {
		return nil, httpx.Internal("project folder could not be opened").WithCause(err)
	}
	entries, err := fs.List(res.Path)
	if err != nil {
		return nil, mapFSError(err)
	}

	// Links whose target sits directly in this folder show up as entries with
	// a link marker.
	if res.Link == nil {
		clean, _ := workspace.Clean(dir)
		links, err := s.store.LinksInto(ctx, p.ID)
		if err != nil {
			return nil, httpx.Internal("links could not be read").WithCause(err)
		}
		for _, l := range links {
			parent := path.Dir(l.TargetPath)
			if parent == "." {
				parent = ""
			}
			if parent != clean {
				continue
			}
			source, err := s.store.ProjectByID(ctx, l.SourceProject)
			if err != nil || !access.CanReadProject(actor, source) {
				continue
			}
			sfs := s.ws.Open(l.SourceProject)
			e, err := sfs.Stat(l.SourcePath)
			if err != nil {
				e = workspace.Entry{IsDir: l.Kind == "folder"}
			}
			e.Name = path.Base(l.TargetPath)
			e.Path = l.TargetPath
			e.LinkID = l.ID.String()
			e.LinkedFrom = l.SourceSlug + ":" + l.SourcePath
			e.LinkProject = l.SourceProject.String()
			entries = append(entries, e)
		}
		sort.SliceStable(entries, func(i, j int) bool {
			if entries[i].IsDir != entries[j].IsDir {
				return entries[i].IsDir
			}
			return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
		})
	}
	return entries, nil
}

// Read returns a file's bytes, following links.
func (s *Service) Read(ctx context.Context, p *model.Project, rel string) ([]byte, *Resolved, error) {
	res, err := s.Resolve(ctx, p, rel)
	if err != nil {
		return nil, nil, err
	}
	fs, err := s.ws.For(res.Project.ID)
	if err != nil {
		return nil, nil, httpx.Internal("project folder could not be opened").WithCause(err)
	}
	body, err := fs.ReadFile(res.Path)
	if err != nil {
		return nil, res, mapFSError(err)
	}
	return body, res, nil
}

// ReadLocal reads without resolving links — capabilities use it for the file
// they own themselves.
func (s *Service) ReadLocal(ctx context.Context, p *model.Project, rel string) ([]byte, error) {
	fs, err := s.ws.For(p.ID)
	if err != nil {
		return nil, httpx.Internal("project folder could not be opened").WithCause(err)
	}
	body, err := fs.ReadFile(rel)
	if err != nil {
		return nil, mapFSError(err)
	}
	return body, nil
}

func (s *Service) Exists(p *model.Project, rel string) bool {
	return s.ws.Open(p.ID).Exists(rel)
}

// Write stores a file and, if the project is tracked, commits it.
func (s *Service) Write(ctx context.Context, p *model.Project, rel string, data []byte, op Op) (*Resolved, error) {
	res, err := s.Resolve(ctx, p, rel)
	if err != nil {
		return nil, err
	}
	fs, err := s.ws.For(res.Project.ID)
	if err != nil {
		return nil, httpx.Internal("project folder could not be opened").WithCause(err)
	}
	if err := fs.WriteFile(res.Path, data); err != nil {
		return nil, mapFSError(err)
	}
	s.after(ctx, res.Project, res.Path, op)
	return res, nil
}

// WriteFrom is the streaming form, used by uploads.
func (s *Service) WriteFrom(ctx context.Context, p *model.Project, rel string, r io.Reader, op Op) (*Resolved, error) {
	res, err := s.Resolve(ctx, p, rel)
	if err != nil {
		return nil, err
	}
	fs, err := s.ws.For(res.Project.ID)
	if err != nil {
		return nil, httpx.Internal("project folder could not be opened").WithCause(err)
	}
	if err := fs.WriteFrom(res.Path, r); err != nil {
		return nil, mapFSError(err)
	}
	s.after(ctx, res.Project, res.Path, op)
	return res, nil
}

func (s *Service) Mkdir(ctx context.Context, p *model.Project, rel string, op Op) error {
	res, err := s.Resolve(ctx, p, rel)
	if err != nil {
		return err
	}
	fs, err := s.ws.For(res.Project.ID)
	if err != nil {
		return httpx.Internal("project folder could not be opened").WithCause(err)
	}
	if err := fs.Mkdir(res.Path); err != nil {
		return mapFSError(err)
	}
	s.after(ctx, res.Project, res.Path, op)
	return nil
}

// Remove deletes a file or folder. Removing a link removes only the link —
// that decision is made in the API, which knows whether the path is a link.
func (s *Service) Remove(ctx context.Context, p *model.Project, rel string, recursive bool, op Op) error {
	res, err := s.Resolve(ctx, p, rel)
	if err != nil {
		return err
	}
	fs, err := s.ws.For(res.Project.ID)
	if err != nil {
		return httpx.Internal("project folder could not be opened").WithCause(err)
	}
	if err := fs.Remove(res.Path, recursive); err != nil {
		return mapFSError(err)
	}
	s.after(ctx, res.Project, res.Path, op)
	return nil
}

func (s *Service) Move(ctx context.Context, p *model.Project, from, to string, op Op) error {
	src, err := s.Resolve(ctx, p, from)
	if err != nil {
		return err
	}
	dst, err := s.Resolve(ctx, p, to)
	if err != nil {
		return err
	}
	if src.Project.ID != dst.Project.ID {
		return httpx.BadRequest("Moving between projects is not a rename — copy the file or make a link instead.")
	}
	fs, err := s.ws.For(src.Project.ID)
	if err != nil {
		return httpx.Internal("project folder could not be opened").WithCause(err)
	}
	if err := fs.Move(src.Path, dst.Path); err != nil {
		return mapFSError(err)
	}
	s.after(ctx, src.Project, dst.Path, op)
	return nil
}

// Copy duplicates a path into another project.
func (s *Service) Copy(ctx context.Context, from *model.Project, fromPath string, to *model.Project, toPath string, op Op) error {
	src, err := s.ws.For(from.ID)
	if err != nil {
		return httpx.Internal("project folder could not be opened").WithCause(err)
	}
	dst, err := s.ws.For(to.ID)
	if err != nil {
		return httpx.Internal("project folder could not be opened").WithCause(err)
	}
	if err := src.CopyTo(fromPath, dst, toPath); err != nil {
		return mapFSError(err)
	}
	s.after(ctx, to, toPath, op)
	return nil
}

// after publishes the change and runs the automatic commit if the project asks
// for one. Versioning is a decision: without git_tracked nothing is committed.
func (s *Service) after(ctx context.Context, p *model.Project, rel string, op Op) {
	if s.index != nil {
		s.index(ctx, p, rel)
	}
	s.bus.Publish(events.Event{Kind: events.FileChanged, ProjectID: p.ID, Path: rel})
	if !op.Commit || !p.GitTracked {
		return
	}
	msg := op.Message
	if msg == "" {
		msg = "Update " + rel
	}
	if _, _, err := s.git.Commit(ctx, p.ID, p.GroupSlug, p.Slug, msg, op.Author, op.Email); err != nil {
		slog.Warn("automatic commit failed", "project", p.Slug, "error", err)
	}
}

// Commit captures the current state on the project's branch, whether or not
// automatic tracking is on.
func (s *Service) Commit(ctx context.Context, p *model.Project, message, author, email string) (string, bool, error) {
	return s.git.Commit(ctx, p.ID, p.GroupSlug, p.Slug, message, author, email)
}

func mapFSError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, workspace.ErrNotFound):
		return httpx.NotFound("There is no file or folder at this path.")
	case errors.Is(err, workspace.ErrExists):
		return httpx.Conflict("A file or folder with that name already exists.")
	case errors.Is(err, workspace.ErrNotEmpty):
		return httpx.Conflict("The folder is not empty. Confirm deleting it with everything in it.")
	case errors.Is(err, workspace.ErrOutside):
		return httpx.BadRequest("That path leaves the project.")
	case errors.Is(err, workspace.ErrBadPath):
		return httpx.BadRequest("That is not a valid path.")
	case errors.Is(err, workspace.ErrIsDir):
		return httpx.BadRequest("That is a folder, not a file.")
	case errors.Is(err, workspace.ErrNotDir):
		return httpx.BadRequest("That is a file, not a folder.")
	case errors.Is(err, workspace.ErrSamePlace):
		return httpx.BadRequest("Source and target are the same.")
	}
	return httpx.Internal("The file operation failed").WithCause(err)
}

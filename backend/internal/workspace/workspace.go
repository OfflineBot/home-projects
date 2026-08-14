// Package workspace is the file store. A project is a directory on disk and
// nothing else: what the user sees in the file tree is what lies there.
//
// Layout:
//
//	<data>/projects/<project-id>/tree    the file tree the user works in
//	<data>/projects/<project-id>/index   the git index for that project
//
// The git index deliberately sits *next to* the tree, not inside it: a project
// is a plain folder, without a .git directory turning up in its listing.
package workspace

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound  = errors.New("no such file or folder")
	ErrExists    = errors.New("a file or folder with that name already exists")
	ErrBadPath   = errors.New("invalid path")
	ErrNotDir    = errors.New("not a folder")
	ErrIsDir     = errors.New("is a folder")
	ErrNotEmpty  = errors.New("folder is not empty")
	ErrOutside   = errors.New("path leaves the project")
	ErrSamePlace = errors.New("source and target are the same")
)

// Entry is one row in a file listing.
type Entry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	IsDir      bool      `json:"isDir"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modifiedAt"`
	MimeType   string    `json:"mimeType,omitempty"`

	// Filled in for linked entries (section 2 of the brief). The core file
	// listing sets them; the workspace itself knows nothing about links.
	LinkID      string `json:"linkId,omitempty"`
	LinkedFrom  string `json:"linkedFrom,omitempty"`
	LinkProject string `json:"linkProject,omitempty"`
}

// Store hands out one FS per project.
type Store struct{ root string }

func NewStore(dataDir string) (*Store, error) {
	root := filepath.Join(dataDir, "projects")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
}

// Base is <data>/projects/<id> — the tree plus the git index live below it.
func (s *Store) Base(id uuid.UUID) string { return filepath.Join(s.root, id.String()) }

// TreeDir is the working tree of a project.
func (s *Store) TreeDir(id uuid.UUID) string { return filepath.Join(s.Base(id), "tree") }

// IndexFile is the git index belonging to that working tree.
func (s *Store) IndexFile(id uuid.UUID) string { return filepath.Join(s.Base(id), "index") }

// For returns the file interface of one project, creating its directory.
func (s *Store) For(id uuid.UUID) (*FS, error) {
	dir := s.TreeDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &FS{root: dir}, nil
}

// Open returns the file interface without creating anything.
func (s *Store) Open(id uuid.UUID) *FS { return &FS{root: s.TreeDir(id)} }

// Destroy removes a project's data for good.
func (s *Store) Destroy(id uuid.UUID) error {
	return os.RemoveAll(s.Base(id))
}

// FS is the file tree of a single project. Every path it takes is relative to
// the project root, slash-separated, without a leading slash.
type FS struct{ root string }

func (f *FS) Root() string { return f.root }

// Clean normalises a user-supplied path and refuses anything that would leave
// the project.
func Clean(p string) (string, error) {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	p = strings.TrimPrefix(p, "/")
	if p == "" || p == "." {
		return "", nil
	}
	if strings.ContainsRune(p, 0) {
		return "", ErrBadPath
	}
	c := path.Clean(p)
	if c == "." {
		return "", nil
	}
	if c == ".." || strings.HasPrefix(c, "../") || strings.HasPrefix(c, "/") {
		return "", ErrOutside
	}
	for _, seg := range strings.Split(c, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", ErrBadPath
		}
	}
	return c, nil
}

// ValidName checks a single file or folder name.
func ValidName(name string) error {
	if name == "" || name == "." || name == ".." {
		return ErrBadPath
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return ErrBadPath
	}
	if len(name) > 255 {
		return fmt.Errorf("%w: name is longer than 255 characters", ErrBadPath)
	}
	return nil
}

// abs turns a relative project path into an absolute one and makes sure no
// symlink carries it out of the project.
func (f *FS) abs(p string) (string, error) {
	clean, err := Clean(p)
	if err != nil {
		return "", err
	}
	full := filepath.Join(f.root, filepath.FromSlash(clean))
	if !within(f.root, full) {
		return "", ErrOutside
	}
	// A symlink inside the tree (only git can put one there) must not be a way
	// out of it. The deepest existing ancestor is resolved and checked.
	probe := full
	for {
		resolved, err := filepath.EvalSymlinks(probe)
		if err == nil {
			realRoot, rerr := filepath.EvalSymlinks(f.root)
			if rerr != nil {
				realRoot = f.root
			}
			if !within(realRoot, resolved) {
				return "", ErrOutside
			}
			break
		}
		if !os.IsNotExist(err) {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe || len(parent) < len(f.root) {
			break
		}
		probe = parent
	}
	return full, nil
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

func (f *FS) EnsureRoot() error { return os.MkdirAll(f.root, 0o755) }

// List returns the direct children of a folder, folders first, then by name.
// bookkeeping is the folder this server keeps its own notes in — what a
// scheduler fetched, and where. It travels with the project (git carries it,
// the zip contains it) but it is not content, so the listing leaves it out.
const bookkeeping = ".home-projects"

func (f *FS) List(dir string) ([]Entry, error) {
	full, err := f.abs(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			// The root of an empty project is simply empty, not missing.
			if dir == "" || dir == "." || dir == "/" {
				return []Entry{}, nil
			}
			return nil, ErrNotFound
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, ErrNotDir
	}
	items, err := os.ReadDir(full)
	if err != nil {
		return nil, err
	}
	clean, _ := Clean(dir)
	out := make([]Entry, 0, len(items))
	for _, it := range items {
		if clean == "" && it.Name() == bookkeeping {
			continue // the server's own notes, not the project's contents
		}
		fi, err := it.Info()
		if err != nil {
			continue
		}
		out = append(out, entryOf(clean, it.Name(), fi))
	}
	sortEntries(out)
	return out, nil
}

func entryOf(dir, name string, fi os.FileInfo) Entry {
	p := name
	if dir != "" {
		p = dir + "/" + name
	}
	e := Entry{
		Name:       name,
		Path:       p,
		IsDir:      fi.IsDir(),
		Size:       fi.Size(),
		ModifiedAt: fi.ModTime().UTC(),
	}
	if !e.IsDir {
		e.MimeType = MimeOf(name)
	}
	return e
}

func sortEntries(list []Entry) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].IsDir != list[j].IsDir {
			return list[i].IsDir
		}
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
}

// Stat describes one entry.
func (f *FS) Stat(p string) (Entry, error) {
	full, err := f.abs(p)
	if err != nil {
		return Entry{}, err
	}
	fi, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return Entry{}, ErrNotFound
		}
		return Entry{}, err
	}
	clean, _ := Clean(p)
	return entryOf(path.Dir(clean), path.Base(clean), fi), nil
}

func (f *FS) Exists(p string) bool {
	full, err := f.abs(p)
	if err != nil {
		return false
	}
	_, err = os.Stat(full)
	return err == nil
}

func (f *FS) ReadFile(p string) ([]byte, error) {
	full, err := f.abs(p)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		if isDirErr(err) {
			return nil, ErrIsDir
		}
		return nil, err
	}
	return b, nil
}

// OpenFile hands out a reader for downloads.
func (f *FS) OpenFile(p string) (*os.File, os.FileInfo, error) {
	full, err := f.abs(p)
	if err != nil {
		return nil, nil, err
	}
	fh, err := os.Open(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}
	fi, err := fh.Stat()
	if err != nil {
		fh.Close()
		return nil, nil, err
	}
	if fi.IsDir() {
		fh.Close()
		return nil, nil, ErrIsDir
	}
	return fh, fi, nil
}

// WriteFile writes atomically: a temp file next to the target, then rename.
// A half-written calendar.ics never exists.
func (f *FS) WriteFile(p string, data []byte) error {
	return f.WriteFrom(p, strings.NewReader(string(data)))
}

func (f *FS) WriteFrom(p string, r io.Reader) error {
	full, err := f.abs(p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, full)
}

func (f *FS) Mkdir(p string) error {
	full, err := f.abs(p)
	if err != nil {
		return err
	}
	if _, err := os.Stat(full); err == nil {
		return ErrExists
	}
	return os.MkdirAll(full, 0o755)
}

// Remove deletes a file, or a folder with everything in it when recursive.
func (f *FS) Remove(p string, recursive bool) error {
	clean, err := Clean(p)
	if err != nil {
		return err
	}
	if clean == "" {
		return ErrBadPath // never the project root itself
	}
	full, err := f.abs(clean)
	if err != nil {
		return err
	}
	fi, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	if fi.IsDir() && !recursive {
		items, err := os.ReadDir(full)
		if err != nil {
			return err
		}
		if len(items) > 0 {
			return ErrNotEmpty
		}
	}
	return os.RemoveAll(full)
}

// Move renames or moves a file or folder inside the project.
func (f *FS) Move(from, to string) error {
	src, err := f.abs(from)
	if err != nil {
		return err
	}
	dst, err := f.abs(to)
	if err != nil {
		return err
	}
	if src == dst {
		return ErrSamePlace
	}
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	if within(src, dst) && src != dst {
		return fmt.Errorf("%w: a folder cannot be moved into itself", ErrBadPath)
	}
	if _, err := os.Stat(dst); err == nil {
		return ErrExists
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// CopyTo copies a file or folder into another project's tree.
func (f *FS) CopyTo(from string, other *FS, to string) error {
	src, err := f.abs(from)
	if err != nil {
		return err
	}
	dst, err := other.abs(to)
	if err != nil {
		return err
	}
	return copyPath(src, dst)
}

func copyPath(src, dst string) error {
	fi, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	if fi.IsDir() {
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		items, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, it := range items {
			if err := copyPath(filepath.Join(src, it.Name()), filepath.Join(dst, it.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// Walk visits every file below dir, folders included.
func (f *FS) Walk(dir string, fn func(e Entry) error) error {
	base, err := f.abs(dir)
	if err != nil {
		return err
	}
	if _, err := os.Stat(base); os.IsNotExist(err) {
		return nil
	}
	return filepath.Walk(base, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if p == base {
			return nil
		}
		rel, err := filepath.Rel(f.root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		return fn(Entry{
			Name:       fi.Name(),
			Path:       rel,
			IsDir:      fi.IsDir(),
			Size:       fi.Size(),
			ModifiedAt: fi.ModTime().UTC(),
			MimeType:   MimeOf(fi.Name()),
		})
	})
}

// Count returns the number of files and their total size — the delete dialog
// says out loud what is about to disappear.
func (f *FS) Count() (files int, bytes int64) {
	_ = f.Walk("", func(e Entry) error {
		if !e.IsDir {
			files++
			bytes += e.Size
		}
		return nil
	})
	return
}

// Zip streams the whole project (or a subfolder) as a zip archive.
func (f *FS) Zip(w io.Writer, dir string) error {
	zw := zip.NewWriter(w)
	defer zw.Close()

	base, err := Clean(dir)
	if err != nil {
		return err
	}
	err = f.Walk(base, func(e Entry) error {
		name := e.Path
		if base != "" {
			name = strings.TrimPrefix(strings.TrimPrefix(e.Path, base), "/")
			if name == "" {
				return nil
			}
		}
		if e.IsDir {
			_, err := zw.Create(name + "/")
			return err
		}
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: e.ModifiedAt}
		out, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		in, _, err := f.OpenFile(e.Path)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(out, in)
		return err
	})
	if err != nil {
		return err
	}
	return zw.Close()
}

func isDirErr(err error) bool {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return strings.Contains(pe.Err.Error(), "is a directory")
	}
	return false
}

// MimeOf guesses a content type from the file extension, with the handful of
// types this server cares about spelled out.
func MimeOf(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".md", ".markdown":
		return "text/markdown; charset=utf-8"
	case ".ics":
		return "text/calendar; charset=utf-8"
	case ".eml":
		return "message/rfc822"
	case ".yaml", ".yml":
		return "application/yaml; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".ts", ".tsx":
		return "text/plain; charset=utf-8"
	case ".go", ".rs", ".py", ".sh", ".toml", ".env", ".txt", ".log", ".csv":
		return "text/plain; charset=utf-8"
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return "application/octet-stream"
}

// IsText decides whether the editor may open a file directly.
func IsText(name string, sample []byte) bool {
	if strings.HasPrefix(MimeOf(name), "text/") {
		return true
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".json", ".yaml", ".yml", ".ics", ".eml", ".svg", ".xml":
		return true
	}
	for _, b := range sample {
		if b == 0 {
			return false
		}
	}
	return len(sample) > 0
}

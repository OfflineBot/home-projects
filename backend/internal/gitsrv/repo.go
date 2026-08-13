// Package gitsrv is the git half of the server.
//
// The group carries the repository, the project is a branch inside it:
//
//	<GIT_DIR>/<group-slug>.git        bare repository, branch main is the group
//	  refs/heads/<project-slug>       one branch per project
//	<DATA_DIR>/projects/<id>/tree     the working tree the server edits
//	<DATA_DIR>/projects/<id>/index    that tree's git index
//
// The working tree has no .git directory of its own: GIT_DIR, GIT_WORK_TREE
// and GIT_INDEX_FILE are passed to every call instead. That keeps the file
// tree the user sees a plain folder, and lets several projects of one group be
// committed at the same time without sharing an index.
package gitsrv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/config"
	"github.com/offlinebot/home-projects/backend/internal/workspace"
)

var ErrNoBranch = errors.New("this project has no commits yet")

type Service struct {
	cfg *config.Config
	ws  *workspace.Store
}

func New(cfg *config.Config, ws *workspace.Store) *Service {
	return &Service{cfg: cfg, ws: ws}
}

func (s *Service) RepoPath(groupSlug string) string {
	return filepath.Join(s.cfg.GitDir, groupSlug+".git")
}

// UngroupedRepo is the repository for projects without a group. Ungrouped is
// not a special case in the data model — it is simply the repo that holds the
// branches of projects whose group_id is NULL.
const UngroupedRepo = "ungrouped"

func (s *Service) repoFor(groupSlug string) string {
	if groupSlug == "" {
		groupSlug = UngroupedRepo
	}
	return s.RepoPath(groupSlug)
}

// run executes git with the environment a bare repo plus external work tree
// needs.
func (s *Service) run(ctx context.Context, repo, workTree, indexFile string, stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, s.cfg.GitBinary, args...)
	env := append(os.Environ(),
		"GIT_DIR="+repo,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME="+os.TempDir(),
	)
	if workTree != "" {
		env = append(env, "GIT_WORK_TREE="+workTree)
		cmd.Dir = workTree
	} else {
		cmd.Dir = repo
	}
	if indexFile != "" {
		env = append(env, "GIT_INDEX_FILE="+indexFile)
	}
	cmd.Env = env
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.Bytes(), fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return out.Bytes(), nil
}

// EnsureRepo creates the bare repository of a group. It runs on every group
// creation — no action, no switch.
func (s *Service) EnsureRepo(ctx context.Context, groupSlug, title string) error {
	repo := s.repoFor(groupSlug)
	if _, err := os.Stat(filepath.Join(repo, "HEAD")); err == nil {
		return nil
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return err
	}
	if _, err := s.run(ctx, repo, "", "", nil, "init", "--bare", "--initial-branch=main", repo); err != nil {
		return err
	}
	// Pushing into the branch that is checked out elsewhere is normal here:
	// the working trees live outside the repo and are updated by the server.
	for _, kv := range [][2]string{
		{"receive.denyCurrentBranch", "ignore"},
		{"receive.denyNonFastForwards", "false"},
		{"http.receivepack", "true"},
		{"core.logAllRefUpdates", "true"},
	} {
		if _, err := s.run(ctx, repo, "", "", nil, "config", kv[0], kv[1]); err != nil {
			return err
		}
	}
	// A repository with one commit on main clones without warnings and gives
	// the group somewhere to keep its own notes.
	if title == "" {
		title = groupSlug
	}
	readme := fmt.Sprintf("# %s\n\nThis repository belongs to the group %q.\nEvery project in the group is a branch:\n\n    git clone -b <project-slug> --single-branch %s\n",
		title, groupSlug, s.CloneURL(groupSlug))
	return s.initialCommit(ctx, repo, "README.md", readme)
}

func (s *Service) initialCommit(ctx context.Context, repo, name, content string) error {
	blob, err := s.run(ctx, repo, "", "", []byte(content), "hash-object", "-w", "--stdin")
	if err != nil {
		return err
	}
	entry := fmt.Sprintf("100644 blob %s\t%s\n", strings.TrimSpace(string(blob)), name)
	tree, err := s.run(ctx, repo, "", "", []byte(entry), "mktree")
	if err != nil {
		return err
	}
	commit, err := s.commitTree(ctx, repo, strings.TrimSpace(string(tree)), "", "the server", "server@home-projects", "Create group")
	if err != nil {
		return err
	}
	_, err = s.run(ctx, repo, "", "", nil, "update-ref", "refs/heads/main", commit)
	return err
}

func (s *Service) commitTree(ctx context.Context, repo, tree, parent, authorName, authorMail, message string) (string, error) {
	args := []string{"commit-tree", tree}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	args = append(args, "-m", message)
	cmd := exec.CommandContext(ctx, s.cfg.GitBinary, args...)
	stamp := time.Now().Format("2006-01-02T15:04:05-07:00")
	cmd.Env = append(os.Environ(),
		"GIT_DIR="+repo,
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME="+os.TempDir(),
		"GIT_AUTHOR_NAME="+authorName,
		"GIT_AUTHOR_EMAIL="+authorMail,
		"GIT_COMMITTER_NAME="+authorName,
		"GIT_COMMITTER_EMAIL="+authorMail,
		"GIT_AUTHOR_DATE="+stamp,
		"GIT_COMMITTER_DATE="+stamp,
	)
	cmd.Dir = repo
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git commit-tree: %s", strings.TrimSpace(errBuf.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// RenameRepo follows a changed group address. Old clone addresses stop working
// — the settings dialog says so beforehand.
func (s *Service) RenameRepo(oldSlug, newSlug string) error {
	from, to := s.repoFor(oldSlug), s.repoFor(newSlug)
	if from == to {
		return nil
	}
	if _, err := os.Stat(from); os.IsNotExist(err) {
		return nil
	}
	if _, err := os.Stat(to); err == nil {
		return fmt.Errorf("a repository named %s already exists", newSlug)
	}
	return os.Rename(from, to)
}

func (s *Service) DeleteRepo(groupSlug string) error {
	if groupSlug == "" || groupSlug == UngroupedRepo {
		return nil
	}
	return os.RemoveAll(s.repoFor(groupSlug))
}

func (s *Service) CloneURL(groupSlug string) string {
	if groupSlug == "" {
		groupSlug = UngroupedRepo
	}
	return fmt.Sprintf("%s/git/%s.git", s.cfg.PublicURL, groupSlug)
}

func (s *Service) BranchHead(ctx context.Context, groupSlug, branch string) (string, error) {
	out, err := s.run(ctx, s.repoFor(groupSlug), "", "", nil, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return "", ErrNoBranch
	}
	return strings.TrimSpace(string(out)), nil
}

// Commit captures the current state of a project's working tree on its branch.
// It returns the new commit and whether anything actually changed.
func (s *Service) Commit(ctx context.Context, projectID uuid.UUID, groupSlug, branch, message, authorName, authorMail string) (string, bool, error) {
	repo := s.repoFor(groupSlug)
	tree := s.ws.TreeDir(projectID)
	index := s.ws.IndexFile(projectID)
	if err := os.MkdirAll(tree, 0o755); err != nil {
		return "", false, err
	}
	if _, err := os.Stat(filepath.Join(repo, "HEAD")); err != nil {
		return "", false, fmt.Errorf("the group's repository is missing: %w", err)
	}

	if _, err := s.run(ctx, repo, tree, index, nil, "add", "-A", "."); err != nil {
		return "", false, err
	}
	treeOut, err := s.run(ctx, repo, tree, index, nil, "write-tree")
	if err != nil {
		return "", false, err
	}
	treeHash := strings.TrimSpace(string(treeOut))

	parent, _ := s.BranchHead(ctx, groupSlug, branch)
	if parent != "" {
		if prev, err := s.run(ctx, repo, "", "", nil, "rev-parse", parent+"^{tree}"); err == nil {
			if strings.TrimSpace(string(prev)) == treeHash {
				return parent, false, nil // nothing changed
			}
		}
	}
	if message == "" {
		message = "Update"
	}
	if authorName == "" {
		authorName = "the server"
	}
	if authorMail == "" {
		authorMail = "server@home-projects"
	}
	commit, err := s.commitTree(ctx, repo, treeHash, parent, authorName, authorMail, message)
	if err != nil {
		return "", false, err
	}
	args := []string{"update-ref", "refs/heads/" + branch, commit}
	if parent != "" {
		args = append(args, parent)
	}
	if _, err := s.run(ctx, repo, "", "", nil, args...); err != nil {
		return "", false, err
	}
	return commit, true, nil
}

// Checkout makes the working tree follow the branch — used after a push from
// outside and after a project moves between repositories.
func (s *Service) Checkout(ctx context.Context, projectID uuid.UUID, groupSlug, branch string) error {
	repo := s.repoFor(groupSlug)
	tree := s.ws.TreeDir(projectID)
	index := s.ws.IndexFile(projectID)
	head, err := s.BranchHead(ctx, groupSlug, branch)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(tree, 0o755); err != nil {
		return err
	}
	_, err = s.run(ctx, repo, tree, index, nil, "read-tree", "--reset", "-u", head)
	return err
}

// TreeMatchesBranch reports whether the working tree is already at the branch
// head, so a push that changed nothing does not rewrite files.
func (s *Service) TreeMatchesBranch(ctx context.Context, projectID uuid.UUID, groupSlug, branch string) bool {
	head, err := s.BranchHead(ctx, groupSlug, branch)
	if err != nil {
		return false
	}
	repo := s.repoFor(groupSlug)
	tree := s.ws.TreeDir(projectID)
	index := s.ws.IndexFile(projectID)
	if _, err := s.run(ctx, repo, tree, index, nil, "add", "-A", "."); err != nil {
		return false
	}
	out, err := s.run(ctx, repo, tree, index, nil, "write-tree")
	if err != nil {
		return false
	}
	prev, err := s.run(ctx, repo, "", "", nil, "rev-parse", head+"^{tree}")
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == strings.TrimSpace(string(prev))
}

func (s *Service) DeleteBranch(ctx context.Context, groupSlug, branch string) error {
	if _, err := s.BranchHead(ctx, groupSlug, branch); err != nil {
		return nil
	}
	_, err := s.run(ctx, s.repoFor(groupSlug), "", "", nil, "update-ref", "-d", "refs/heads/"+branch)
	return err
}

func (s *Service) RenameBranch(ctx context.Context, groupSlug, from, to string) error {
	head, err := s.BranchHead(ctx, groupSlug, from)
	if err != nil {
		return nil // nothing committed yet, nothing to rename
	}
	repo := s.repoFor(groupSlug)
	if _, err := s.run(ctx, repo, "", "", nil, "update-ref", "refs/heads/"+to, head); err != nil {
		return err
	}
	_, err = s.run(ctx, repo, "", "", nil, "update-ref", "-d", "refs/heads/"+from, head)
	return err
}

// MoveBranch carries a project's history from one group's repository to
// another's: fetch the branch, then drop it at the source. History is not lost.
func (s *Service) MoveBranch(ctx context.Context, fromGroup, toGroup, branch string) error {
	if fromGroup == toGroup {
		return nil
	}
	src := s.repoFor(fromGroup)
	dst := s.repoFor(toGroup)
	if _, err := s.BranchHead(ctx, fromGroup, branch); err != nil {
		return nil // no commits yet — nothing to carry over
	}
	if _, err := s.run(ctx, dst, "", "", nil,
		"fetch", "--no-tags", src, "+refs/heads/"+branch+":refs/heads/"+branch); err != nil {
		return err
	}
	return s.DeleteBranch(ctx, fromGroup, branch)
}

type Commit struct {
	Hash    string    `json:"hash"`
	Short   string    `json:"short"`
	Message string    `json:"message"`
	Author  string    `json:"author"`
	At      time.Time `json:"at"`
}

// Log returns a branch's history, newest first.
func (s *Service) Log(ctx context.Context, groupSlug, branch string, limit int) ([]Commit, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if _, err := s.BranchHead(ctx, groupSlug, branch); err != nil {
		return []Commit{}, nil
	}
	const sep = "\x1f"
	out, err := s.run(ctx, s.repoFor(groupSlug), "", "", nil,
		"log", fmt.Sprintf("-%d", limit), "--format=%H"+sep+"%h"+sep+"%an"+sep+"%aI"+sep+"%s", "refs/heads/"+branch)
	if err != nil {
		return nil, err
	}
	list := []Commit{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, sep)
		if len(parts) < 5 {
			continue
		}
		at, _ := time.Parse(time.RFC3339, parts[3])
		list = append(list, Commit{Hash: parts[0], Short: parts[1], Author: parts[2], At: at, Message: parts[4]})
	}
	return list, nil
}

// Bundle writes a single-file clone of one branch — the download offered
// before deleting something.
func (s *Service) Bundle(ctx context.Context, groupSlug, branch, target string) error {
	if _, err := s.BranchHead(ctx, groupSlug, branch); err != nil {
		return ErrNoBranch
	}
	_, err := s.run(ctx, s.repoFor(groupSlug), "", "", nil,
		"bundle", "create", target, "refs/heads/"+branch)
	return err
}

// Branches lists the branches present in a group's repository.
func (s *Service) Branches(ctx context.Context, groupSlug string) ([]string, error) {
	out, err := s.run(ctx, s.repoFor(groupSlug), "", "", nil,
		"for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return nil, err
	}
	var list []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			list = append(list, l)
		}
	}
	return list, nil
}

// RepoExists reports whether a group's repository is on disk.
func (s *Service) RepoExists(groupSlug string) bool {
	_, err := os.Stat(filepath.Join(s.repoFor(groupSlug), "HEAD"))
	return err == nil
}

package store

import (
	"context"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/model"
)

const projectCols = `p.id, p.owner_id, p.group_id, p.slug, p.title, p.description, p.capabilities,
	p.preset, p.default_tab, p.git_tracked, p.site_root, p.site_source_project_id, p.visibility, p.password_hash,
	p.read_only, p.anon_write, p.color, p.icon, p.folder, p.archived, p.position,
	p.created_at, p.updated_at`

// projectSelect always joins the group so a project carries its group's slug
// without a second query.
const projectSelect = `SELECT ` + projectCols + `, COALESCE(g.slug,''), COALESCE(g.title,''),
	COALESCE(g.visibility,'')
	FROM projects p LEFT JOIN groups g ON g.id = p.group_id`

func scanProject(r scanner) (*model.Project, error) {
	var p model.Project
	var pw *string
	err := r.Scan(&p.ID, &p.OwnerID, &p.GroupID, &p.Slug, &p.Title, &p.Description, &p.Capabilities,
		&p.Preset, &p.DefaultTab, &p.GitTracked, &p.SiteRoot, &p.SiteSourceID, &p.Visibility, &pw,
		&p.ReadOnly, &p.AnonWrite, &p.Color, &p.Icon, &p.Folder, &p.Archived, &p.Position,
		&p.CreatedAt, &p.UpdatedAt, &p.GroupSlug, &p.GroupTitle, &p.GroupVisibility)
	if err != nil {
		return nil, norm(err)
	}
	p.PasswordHash = strp(pw)
	p.HasPassword = p.PasswordHash != ""
	// Who may see it, worked out once here so nothing downstream has to fetch
	// the group to find out.
	p.Effective = p.Visibility
	if p.Visibility == model.VisibilityGroup || p.Visibility == "" {
		p.Effective = model.VisibilityPrivate
		if p.GroupVisibility != "" {
			p.Effective = p.GroupVisibility
		}
	}
	if p.Capabilities == nil {
		p.Capabilities = []string{}
	}
	return &p, nil
}

type NewProject struct {
	OwnerID      uuid.UUID
	GroupID      *uuid.UUID
	Slug         string
	Title        string
	Description  string
	Capabilities []string
	Preset       string
	DefaultTab   string
	GitTracked   bool
	Visibility   model.Visibility
	Color        string
	Icon         string
}

func (s *Store) CreateProject(ctx context.Context, in NewProject) (*model.Project, error) {
	if in.Visibility == "" {
		in.Visibility = model.VisibilityPrivate
	}
	if in.Capabilities == nil {
		in.Capabilities = []string{}
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO projects (owner_id, group_id, slug, title, description, capabilities, preset,
			default_tab, git_tracked, visibility, color, icon, position)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,
			COALESCE((SELECT max(position)+1 FROM projects WHERE group_id IS NOT DISTINCT FROM $2),0))
		RETURNING id`,
		in.OwnerID, in.GroupID, in.Slug, in.Title, in.Description, in.Capabilities, in.Preset,
		in.DefaultTab, in.GitTracked, in.Visibility, in.Color, in.Icon).Scan(&id)
	if err != nil {
		return nil, norm(err)
	}
	return s.ProjectByID(ctx, id)
}

func (s *Store) ProjectByID(ctx context.Context, id uuid.UUID) (*model.Project, error) {
	return scanProject(s.pool.QueryRow(ctx, projectSelect+` WHERE p.id=$1`, id))
}

// ProjectBySlug finds a project inside a group; groupID nil means "Ungrouped".
func (s *Store) ProjectBySlug(ctx context.Context, groupID *uuid.UUID, slug string) (*model.Project, error) {
	return scanProject(s.pool.QueryRow(ctx,
		projectSelect+` WHERE p.group_id IS NOT DISTINCT FROM $1 AND p.slug=$2`, groupID, slug))
}

// ProjectsBySlug returns every project with that slug — used to resolve the
// short URL form /api/projects/<slug>, which is only allowed when it is
// unambiguous.
func (s *Store) ProjectsBySlug(ctx context.Context, slug string) ([]model.Project, error) {
	return s.queryProjects(ctx, projectSelect+` WHERE p.slug=$1`, slug)
}

func (s *Store) ProjectSlugTaken(ctx context.Context, groupID *uuid.UUID, slug string) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM projects WHERE group_id IS NOT DISTINCT FROM $1 AND slug=$2`,
		groupID, slug).Scan(&n)
	return n > 0, norm(err)
}

func (s *Store) queryProjects(ctx context.Context, q string, args ...any) ([]model.Project, error) {
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, norm(err)
	}
	defer rows.Close()
	out := []model.Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, norm(rows.Err())
}

// ListProjects returns the projects of one group, or of "Ungrouped" when
// groupID is nil and ungrouped is true, or all of them when both are unset.
func (s *Store) ListProjects(ctx context.Context, groupID *uuid.UUID, ungrouped, includeArchived bool) ([]model.Project, error) {
	q := projectSelect
	args := []any{}
	where := []string{}
	if groupID != nil {
		args = append(args, *groupID)
		where = append(where, "p.group_id = $"+strconv.Itoa(len(args)))
	} else if ungrouped {
		where = append(where, "p.group_id IS NULL")
	}
	if !includeArchived {
		where = append(where, "p.archived = false")
	}
	for i, w := range where {
		if i == 0 {
			q += " WHERE " + w
		} else {
			q += " AND " + w
		}
	}
	q += ` ORDER BY p.position ASC, lower(p.title) ASC`
	return s.queryProjects(ctx, q, args...)
}

func (s *Store) ListAllProjects(ctx context.Context, includeArchived bool) ([]model.Project, error) {
	q := projectSelect
	if !includeArchived {
		q += ` WHERE p.archived = false`
	}
	q += ` ORDER BY lower(g.title) ASC NULLS LAST, p.position ASC, lower(p.title) ASC`
	return s.queryProjects(ctx, q)
}

// ProjectsWithCapability is how schedulers, the automation engine and the
// variable collector find their work — by capability, never by preset.
func (s *Store) ProjectsWithCapability(ctx context.Context, capability string) ([]model.Project, error) {
	return s.queryProjects(ctx,
		projectSelect+` WHERE $1 = ANY(p.capabilities) AND p.archived = false`, capability)
}

func (s *Store) CountProjectsInGroup(ctx context.Context, groupID uuid.UUID, includeArchived bool) (int, error) {
	q := `SELECT count(*) FROM projects WHERE group_id=$1`
	if !includeArchived {
		q += ` AND archived=false`
	}
	var n int
	err := s.pool.QueryRow(ctx, q, groupID).Scan(&n)
	return n, norm(err)
}

// ProjectCounts returns the number of projects per group in one query.
func (s *Store) ProjectCounts(ctx context.Context, includeArchived bool) (map[uuid.UUID]int, error) {
	q := `SELECT group_id, count(*) FROM projects WHERE group_id IS NOT NULL`
	if !includeArchived {
		q += ` AND archived=false`
	}
	q += ` GROUP BY group_id`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, norm(err)
	}
	defer rows.Close()
	out := map[uuid.UUID]int{}
	for rows.Next() {
		var id uuid.UUID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, norm(err)
		}
		out[id] = n
	}
	return out, norm(rows.Err())
}

type ProjectPatch struct {
	GroupID      **uuid.UUID
	Slug         *string
	Title        *string
	Description  *string
	Capabilities *[]string
	Preset       *string
	DefaultTab   *string
	GitTracked   *bool
	SiteRoot     **string
	SiteSourceID **uuid.UUID
	Visibility   *model.Visibility
	PasswordHash **string
	ReadOnly     *bool
	AnonWrite    *bool
	Color        *string
	Icon         *string
	Folder       *string
	Archived     *bool
	Position     *int
}

func (s *Store) UpdateProject(ctx context.Context, id uuid.UUID, p ProjectPatch) (*model.Project, error) {
	set := ""
	args := []any{id}
	add := func(col string, val any) {
		args = append(args, val)
		if set != "" {
			set += ", "
		}
		set += col + " = $" + strconv.Itoa(len(args))
	}
	if p.GroupID != nil {
		add("group_id", *p.GroupID)
	}
	if p.Slug != nil {
		add("slug", *p.Slug)
	}
	if p.Title != nil {
		add("title", *p.Title)
	}
	if p.Description != nil {
		add("description", *p.Description)
	}
	if p.Capabilities != nil {
		add("capabilities", *p.Capabilities)
	}
	if p.Preset != nil {
		add("preset", *p.Preset)
	}
	if p.DefaultTab != nil {
		add("default_tab", *p.DefaultTab)
	}
	if p.GitTracked != nil {
		add("git_tracked", *p.GitTracked)
	}
	if p.SiteSourceID != nil {
		add("site_source_project_id", *p.SiteSourceID)
	}
	if p.SiteRoot != nil {
		add("site_root", *p.SiteRoot)
	}
	if p.Visibility != nil {
		add("visibility", *p.Visibility)
	}
	if p.PasswordHash != nil {
		add("password_hash", *p.PasswordHash)
	}
	if p.ReadOnly != nil {
		add("read_only", *p.ReadOnly)
	}
	if p.AnonWrite != nil {
		add("anon_write", *p.AnonWrite)
	}
	if p.Color != nil {
		add("color", *p.Color)
	}
	if p.Icon != nil {
		add("icon", *p.Icon)
	}
	if p.Folder != nil {
		add("folder", strings.TrimSpace(*p.Folder))
	}
	if p.Archived != nil {
		add("archived", *p.Archived)
	}
	if p.Position != nil {
		add("position", *p.Position)
	}
	if set == "" {
		return s.ProjectByID(ctx, id)
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE projects SET `+set+`, updated_at=now() WHERE id=$1`, args...); err != nil {
		return nil, norm(err)
	}
	return s.ProjectByID(ctx, id)
}

func (s *Store) DeleteProject(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, id)
	if err != nil {
		return norm(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ------------------------------------------------------- what a project gathers

// SourcesOf are the projects this one gathers into its own view, in the order
// they were given.
func (s *Store) SourcesOf(ctx context.Context, projectID uuid.UUID) ([]model.Project, error) {
	rows, err := s.pool.Query(ctx, projectSelect+`
		JOIN project_sources ps ON ps.source_id = p.id
		WHERE ps.project_id = $1 ORDER BY ps.position`, projectID)
	if err != nil {
		return nil, norm(err)
	}
	defer rows.Close()
	out := []model.Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, norm(rows.Err())
}

// SetSources replaces the whole list. A project cannot gather itself, and the
// same project twice is once.
func (s *Store) SetSources(ctx context.Context, projectID uuid.UUID, sources []uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return norm(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM project_sources WHERE project_id=$1`, projectID); err != nil {
		return norm(err)
	}
	for i, id := range sources {
		if id == projectID {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO project_sources (project_id, source_id, position)
			VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, projectID, id, i); err != nil {
			return norm(err)
		}
	}
	return norm(tx.Commit(ctx))
}

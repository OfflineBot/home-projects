package store

import (
	"context"
	"strconv"

	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/model"
)

const groupCols = `id, owner_id, slug, title, description, visibility, password_hash,
	read_only, color, icon, site_project_id, pinned, archived, position, created_at, updated_at`

func scanGroup(r scanner) (*model.Group, error) {
	var g model.Group
	var pw *string
	err := r.Scan(&g.ID, &g.OwnerID, &g.Slug, &g.Title, &g.Description, &g.Visibility, &pw,
		&g.ReadOnly, &g.Color, &g.Icon, &g.SiteProjectID, &g.Pinned, &g.Archived, &g.Position,
		&g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		return nil, norm(err)
	}
	g.PasswordHash = strp(pw)
	g.HasPassword = g.PasswordHash != ""
	return &g, nil
}

type NewGroup struct {
	OwnerID     uuid.UUID
	Slug        string
	Title       string
	Description string
	Visibility  model.Visibility
	Color       string
	Icon        string
	Pinned      bool
}

func (s *Store) CreateGroup(ctx context.Context, in NewGroup) (*model.Group, error) {
	if in.Visibility == "" {
		in.Visibility = model.VisibilityPrivate
	}
	if in.Color == "" {
		in.Color = "mauve"
	}
	if in.Icon == "" {
		in.Icon = "folder"
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO groups (owner_id, slug, title, description, visibility, color, icon, pinned,
			position)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8, COALESCE((SELECT max(position)+1 FROM groups),0))
		RETURNING `+groupCols,
		in.OwnerID, in.Slug, in.Title, in.Description, in.Visibility, in.Color, in.Icon, in.Pinned)
	return scanGroup(row)
}

func (s *Store) GroupByID(ctx context.Context, id uuid.UUID) (*model.Group, error) {
	return scanGroup(s.pool.QueryRow(ctx, `SELECT `+groupCols+` FROM groups WHERE id=$1`, id))
}

func (s *Store) GroupBySlug(ctx context.Context, slug string) (*model.Group, error) {
	return scanGroup(s.pool.QueryRow(ctx, `SELECT `+groupCols+` FROM groups WHERE slug=$1`, slug))
}

func (s *Store) GroupSlugTaken(ctx context.Context, slug string) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM groups WHERE slug=$1`, slug).Scan(&n)
	return n > 0, norm(err)
}

// ListGroups returns every group, archived ones only when asked.
func (s *Store) ListGroups(ctx context.Context, includeArchived bool) ([]model.Group, error) {
	q := `SELECT ` + groupCols + ` FROM groups`
	if !includeArchived {
		q += ` WHERE archived=false`
	}
	q += ` ORDER BY pinned DESC, position ASC, lower(title) ASC`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, norm(err)
	}
	defer rows.Close()
	out := []model.Group{}
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, norm(rows.Err())
}

// GroupPatch carries only the fields a request actually wants to change.
type GroupPatch struct {
	Slug          *string
	Title         *string
	Description   *string
	Visibility    *model.Visibility
	PasswordHash  **string // set: pointer to value; clear: pointer to nil
	ReadOnly      *bool
	Color         *string
	Icon          *string
	SiteProjectID **uuid.UUID
	Pinned        *bool
	Archived      *bool
	Position      *int
}

func (s *Store) UpdateGroup(ctx context.Context, id uuid.UUID, p GroupPatch) (*model.Group, error) {
	set := ""
	args := []any{id}
	add := func(expr string, val any) {
		args = append(args, val)
		if set != "" {
			set += ", "
		}
		set += expr + " = $" + strconv.Itoa(len(args))
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
	if p.Visibility != nil {
		add("visibility", *p.Visibility)
	}
	if p.PasswordHash != nil {
		add("password_hash", *p.PasswordHash)
	}
	if p.ReadOnly != nil {
		add("read_only", *p.ReadOnly)
	}
	if p.Color != nil {
		add("color", *p.Color)
	}
	if p.Icon != nil {
		add("icon", *p.Icon)
	}
	if p.SiteProjectID != nil {
		add("site_project_id", *p.SiteProjectID)
	}
	if p.Pinned != nil {
		add("pinned", *p.Pinned)
	}
	if p.Archived != nil {
		add("archived", *p.Archived)
	}
	if p.Position != nil {
		add("position", *p.Position)
	}
	if set == "" {
		return s.GroupByID(ctx, id)
	}
	row := s.pool.QueryRow(ctx,
		`UPDATE groups SET `+set+`, updated_at=now() WHERE id=$1 RETURNING `+groupCols, args...)
	return scanGroup(row)
}

func (s *Store) DeleteGroup(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM groups WHERE id=$1`, id)
	if err != nil {
		return norm(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

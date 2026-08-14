package store

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/model"
)

// Filters are stored the way accounts are: centrally, by name, belonging to
// nobody's project. What uses one only ever holds its id.

const filterSelect = `SELECT f.id, f.slug, f.title, f.description, f.rules, f.created_at, f.updated_at,
	(SELECT count(*) FROM project_filters pf WHERE pf.filter_id = f.id)
	FROM filters f`

func scanFilter(r scanner) (*model.Filter, error) {
	var f model.Filter
	if err := r.Scan(&f.ID, &f.Slug, &f.Title, &f.Description, &f.Rules,
		&f.CreatedAt, &f.UpdatedAt, &f.UsedBy); err != nil {
		return nil, norm(err)
	}
	if len(f.Rules) == 0 {
		f.Rules = json.RawMessage(`[]`)
	}
	return &f, nil
}

func (s *Store) ListFilters(ctx context.Context) ([]model.Filter, error) {
	rows, err := s.pool.Query(ctx, filterSelect+` ORDER BY f.title`)
	if err != nil {
		return nil, norm(err)
	}
	defer rows.Close()
	out := []model.Filter{}
	for rows.Next() {
		f, err := scanFilter(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, norm(rows.Err())
}

func (s *Store) FilterByID(ctx context.Context, id uuid.UUID) (*model.Filter, error) {
	return scanFilter(s.pool.QueryRow(ctx, filterSelect+` WHERE f.id=$1`, id))
}

func (s *Store) FilterBySlug(ctx context.Context, slug string) (*model.Filter, error) {
	return scanFilter(s.pool.QueryRow(ctx, filterSelect+` WHERE f.slug=$1`, slug))
}

type NewFilter struct {
	OwnerID     uuid.UUID
	Slug        string
	Title       string
	Description string
	Rules       json.RawMessage
}

func (s *Store) CreateFilter(ctx context.Context, in NewFilter) (*model.Filter, error) {
	if len(in.Rules) == 0 {
		in.Rules = json.RawMessage(`[]`)
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO filters (owner_id, slug, title, description, rules)
		VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		in.OwnerID, in.Slug, in.Title, in.Description, in.Rules).Scan(&id)
	if err != nil {
		return nil, norm(err)
	}
	return s.FilterByID(ctx, id)
}

type FilterPatch struct {
	Slug        *string
	Title       *string
	Description *string
	Rules       *json.RawMessage
}

func (s *Store) UpdateFilter(ctx context.Context, id uuid.UUID, p FilterPatch) (*model.Filter, error) {
	set := ""
	args := []any{id}
	add := func(col string, val any) {
		args = append(args, val)
		if set != "" {
			set += ", "
		}
		set += col + " = $" + strconv.Itoa(len(args))
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
	if p.Rules != nil {
		add("rules", *p.Rules)
	}
	if set == "" {
		return s.FilterByID(ctx, id)
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE filters SET `+set+`, updated_at=now() WHERE id=$1`, args...); err != nil {
		return nil, norm(err)
	}
	return s.FilterByID(ctx, id)
}

// DeleteFilter removes it. Schedulers pointing at it keep working — they simply
// stop sorting, which is what a scheduler without a filter does anyway.
func (s *Store) DeleteFilter(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM filters WHERE id=$1`, id)
	return norm(err)
}

// ------------------------------------------------ which filters a project uses

// ProjectFilter is one filter a project has picked up.
type ProjectFilter struct {
	model.Filter
	Position  int  `json:"position"`
	Automatic bool `json:"automatic"`
	// Where this project sends what the filter matches, when the rule itself
	// does not say.
	TargetProjectID *uuid.UUID `json:"targetProjectId,omitempty"`
	TargetProject   string     `json:"targetProject,omitempty"`
	TargetFolder    string     `json:"targetFolder,omitempty"`
}

func (s *Store) FiltersForProject(ctx context.Context, projectID uuid.UUID) ([]ProjectFilter, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT f.id, f.slug, f.title, f.description, f.rules, f.created_at, f.updated_at,
			(SELECT count(*) FROM project_filters x WHERE x.filter_id = f.id),
			pf.position, pf.automatic, pf.target_project_id, pf.target_folder,
			COALESCE(CASE WHEN tg.slug IS NULL OR tg.slug = '' THEN tp.slug
				ELSE tg.slug || '/' || tp.slug END, '')
		FROM project_filters pf
		JOIN filters f ON f.id = pf.filter_id
		LEFT JOIN projects tp ON tp.id = pf.target_project_id
		LEFT JOIN groups tg ON tg.id = tp.group_id
		WHERE pf.project_id = $1
		ORDER BY pf.position, f.title`, projectID)
	if err != nil {
		return nil, norm(err)
	}
	defer rows.Close()
	out := []ProjectFilter{}
	for rows.Next() {
		var pf ProjectFilter
		if err := rows.Scan(&pf.ID, &pf.Slug, &pf.Title, &pf.Description, &pf.Rules,
			&pf.CreatedAt, &pf.UpdatedAt, &pf.UsedBy, &pf.Position, &pf.Automatic,
			&pf.TargetProjectID, &pf.TargetFolder, &pf.TargetProject); err != nil {
			return nil, norm(err)
		}
		if len(pf.Rules) == 0 {
			pf.Rules = json.RawMessage(`[]`)
		}
		out = append(out, pf)
	}
	return out, norm(rows.Err())
}

func (s *Store) AddFilterToProject(ctx context.Context, projectID, filterID uuid.UUID,
	automatic bool, target *uuid.UUID, folder string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO project_filters (project_id, filter_id, automatic, target_project_id, target_folder, position)
		VALUES ($1,$2,$3,$4,$5, COALESCE((SELECT max(position)+1 FROM project_filters WHERE project_id=$1),0))
		ON CONFLICT (project_id, filter_id) DO UPDATE SET automatic = EXCLUDED.automatic,
			target_project_id = EXCLUDED.target_project_id, target_folder = EXCLUDED.target_folder`,
		projectID, filterID, automatic, target, folder)
	return norm(err)
}

func (s *Store) RemoveFilterFromProject(ctx context.Context, projectID, filterID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM project_filters WHERE project_id=$1 AND filter_id=$2`, projectID, filterID)
	return norm(err)
}

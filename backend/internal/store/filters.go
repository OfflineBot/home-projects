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
	(SELECT count(*) FROM schedulers s WHERE s.filter_id = f.id)
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

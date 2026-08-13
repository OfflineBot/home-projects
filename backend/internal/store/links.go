package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/model"
)

// A link is a second name for content that lives elsewhere. Removing one never
// touches the source — that rule lives in the API, this file only stores rows.

const linkCols = `l.id, l.source_project, l.source_path, l.target_project, l.target_path, l.created_at`

func (s *Store) linkQuery(ctx context.Context, kind, where string, args ...any) ([]model.Link, error) {
	table := "folder_links"
	if kind == "file" {
		table = "file_links"
	}
	q := `SELECT ` + linkCols + `, COALESCE(sp.slug,''), COALESCE(sp.title,''), COALESCE(tp.slug,'')
		FROM ` + table + ` l
		LEFT JOIN projects sp ON sp.id = l.source_project
		LEFT JOIN projects tp ON tp.id = l.target_project ` + where
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, norm(err)
	}
	defer rows.Close()
	out := []model.Link{}
	for rows.Next() {
		var l model.Link
		if err := rows.Scan(&l.ID, &l.SourceProject, &l.SourcePath, &l.TargetProject, &l.TargetPath,
			&l.CreatedAt, &l.SourceSlug, &l.SourceTitle, &l.TargetSlug); err != nil {
			return nil, norm(err)
		}
		l.Kind = kind
		out = append(out, l)
	}
	return out, norm(rows.Err())
}

func (s *Store) CreateLink(ctx context.Context, ownerID uuid.UUID, kind string, sourceProject uuid.UUID, sourcePath string, targetProject uuid.UUID, targetPath string) (*model.Link, error) {
	table := "folder_links"
	if kind == "file" {
		table = "file_links"
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO `+table+` (owner_id, source_project, source_path, target_project, target_path)
		VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		ownerID, sourceProject, sourcePath, targetProject, targetPath).Scan(&id)
	if err := norm(err); err != nil {
		return nil, err
	}
	links, err := s.linkQuery(ctx, kind, `WHERE l.id=$1`, id)
	if err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return nil, ErrNotFound
	}
	return &links[0], nil
}

// LinksInto returns everything linked into a project — what its file listing
// has to overlay.
func (s *Store) LinksInto(ctx context.Context, projectID uuid.UUID) ([]model.Link, error) {
	folders, err := s.linkQuery(ctx, "folder", `WHERE l.target_project=$1`, projectID)
	if err != nil {
		return nil, err
	}
	files, err := s.linkQuery(ctx, "file", `WHERE l.target_project=$1`, projectID)
	if err != nil {
		return nil, err
	}
	return append(folders, files...), nil
}

// LinksFrom returns the links other projects hold onto this one. The delete
// dialog names them before anything disappears.
func (s *Store) LinksFrom(ctx context.Context, projectID uuid.UUID) ([]model.Link, error) {
	folders, err := s.linkQuery(ctx, "folder", `WHERE l.source_project=$1`, projectID)
	if err != nil {
		return nil, err
	}
	files, err := s.linkQuery(ctx, "file", `WHERE l.source_project=$1`, projectID)
	if err != nil {
		return nil, err
	}
	return append(folders, files...), nil
}

func (s *Store) AllLinks(ctx context.Context) ([]model.Link, error) {
	folders, err := s.linkQuery(ctx, "folder", ``)
	if err != nil {
		return nil, err
	}
	files, err := s.linkQuery(ctx, "file", ``)
	if err != nil {
		return nil, err
	}
	return append(folders, files...), nil
}

func (s *Store) DeleteLink(ctx context.Context, kind string, id uuid.UUID) error {
	table := "folder_links"
	if kind == "file" {
		table = "file_links"
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM `+table+` WHERE id=$1`, id)
	if err != nil {
		return norm(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

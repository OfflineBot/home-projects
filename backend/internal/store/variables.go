package store

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/model"
)

// Variables are the one route from a project to the dashboard. Whoever writes
// one — a capability, a scheduler, an automation, project.yaml or a plain
// exports.json — goes through SetVariable.

const variableCols = `v.project_id, v.name, v.type, v.value, v.unit, v.source, v.error,
	v.ttl_seconds, v.updated_at`

const variableSelect = `SELECT ` + variableCols + `, COALESCE(p.slug,'')
	FROM variables v LEFT JOIN projects p ON p.id = v.project_id`

func scanVariable(r scanner) (*model.Variable, error) {
	var v model.Variable
	err := r.Scan(&v.ProjectID, &v.Name, &v.Type, &v.Value, &v.Unit, &v.Source, &v.Error,
		&v.TTLSeconds, &v.UpdatedAt, &v.ProjectSlug)
	if err != nil {
		return nil, norm(err)
	}
	return &v, nil
}

type VariableInput struct {
	Name       string
	Type       string
	Value      any
	Unit       string
	Source     string
	Error      string
	TTLSeconds int
	// History keeps a row in variable_history so the dashboard can draw a graph.
	History bool
}

func (s *Store) SetVariable(ctx context.Context, projectID uuid.UUID, in VariableInput) error {
	if in.Type == "" {
		in.Type = "text"
	}
	body, err := json.Marshal(in.Value)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO variables (project_id, name, type, value, unit, source, error, ttl_seconds, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8, now())
		ON CONFLICT (project_id, name) DO UPDATE SET
			type=EXCLUDED.type, value=EXCLUDED.value, unit=EXCLUDED.unit,
			source=EXCLUDED.source, error=EXCLUDED.error, ttl_seconds=EXCLUDED.ttl_seconds,
			updated_at=now()`,
		projectID, in.Name, in.Type, body, in.Unit, in.Source, in.Error, in.TTLSeconds)
	if err != nil {
		return norm(err)
	}
	if in.History {
		_, err = s.pool.Exec(ctx,
			`INSERT INTO variable_history (project_id, name, value) VALUES ($1,$2,$3)`,
			projectID, in.Name, body)
		if err != nil {
			return norm(err)
		}
	}
	return nil
}

// ReplaceVariables sets a project's variables from one source and drops the
// ones that source no longer reports.
func (s *Store) ReplaceVariables(ctx context.Context, projectID uuid.UUID, source string, in []VariableInput) error {
	keep := make([]string, 0, len(in))
	for _, v := range in {
		if err := s.SetVariable(ctx, projectID, v); err != nil {
			return err
		}
		keep = append(keep, v.Name)
	}
	_, err := s.pool.Exec(ctx,
		`DELETE FROM variables WHERE project_id=$1 AND source=$2 AND NOT (name = ANY($3))`,
		projectID, source, keep)
	return norm(err)
}

func (s *Store) DeleteVariable(ctx context.Context, projectID uuid.UUID, name string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM variables WHERE project_id=$1 AND name=$2`, projectID, name)
	return norm(err)
}

func (s *Store) queryVariables(ctx context.Context, q string, args ...any) ([]model.Variable, error) {
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, norm(err)
	}
	defer rows.Close()
	out := []model.Variable{}
	for rows.Next() {
		v, err := scanVariable(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, norm(rows.Err())
}

func (s *Store) VariablesForProject(ctx context.Context, projectID uuid.UUID) ([]model.Variable, error) {
	return s.queryVariables(ctx, variableSelect+` WHERE v.project_id=$1 ORDER BY v.name`, projectID)
}

// VariablesForGroup collects the variables of every project in a group.
func (s *Store) VariablesForGroup(ctx context.Context, groupID uuid.UUID) ([]model.Variable, error) {
	return s.queryVariables(ctx,
		variableSelect+` WHERE p.group_id=$1 ORDER BY p.slug, v.name`, groupID)
}

func (s *Store) AllVariables(ctx context.Context) ([]model.Variable, error) {
	return s.queryVariables(ctx, variableSelect+` ORDER BY p.slug, v.name`)
}

type HistoryPoint struct {
	At    time.Time       `json:"at"`
	Value json.RawMessage `json:"value"`
}

func (s *Store) VariableHistory(ctx context.Context, projectID uuid.UUID, name string, limit int) ([]HistoryPoint, error) {
	if limit <= 0 || limit > 2000 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT at, value FROM variable_history
		WHERE project_id=$1 AND name=$2 ORDER BY at DESC LIMIT $3`, projectID, name, limit)
	if err != nil {
		return nil, norm(err)
	}
	defer rows.Close()
	out := []HistoryPoint{}
	for rows.Next() {
		var p HistoryPoint
		if err := rows.Scan(&p.At, &p.Value); err != nil {
			return nil, norm(err)
		}
		out = append(out, p)
	}
	// oldest first — that is what a graph wants
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, norm(rows.Err())
}

// TrimHistory keeps the table from growing without bound.
func (s *Store) TrimHistory(ctx context.Context, keep time.Duration) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM variable_history WHERE at < now() - $1::interval`, keep.String())
	return norm(err)
}

// ------------------------------------------------------- derived group vars

func (s *Store) CreateGroupVariable(ctx context.Context, gv model.GroupVariable) (*model.GroupVariable, error) {
	inputs, err := json.Marshal(gv.Inputs)
	if err != nil {
		return nil, err
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO group_variables (group_id, name, op, inputs, expr, unit)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (group_id, name) DO UPDATE SET op=EXCLUDED.op, inputs=EXCLUDED.inputs,
			expr=EXCLUDED.expr, unit=EXCLUDED.unit
		RETURNING id, group_id, name, op, inputs, expr, unit`,
		gv.GroupID, gv.Name, gv.Op, inputs, gv.Expr, gv.Unit)
	return scanGroupVariable(row)
}

func scanGroupVariable(r scanner) (*model.GroupVariable, error) {
	var gv model.GroupVariable
	var inputs []byte
	if err := r.Scan(&gv.ID, &gv.GroupID, &gv.Name, &gv.Op, &inputs, &gv.Expr, &gv.Unit); err != nil {
		return nil, norm(err)
	}
	_ = json.Unmarshal(inputs, &gv.Inputs)
	if gv.Inputs == nil {
		gv.Inputs = []string{}
	}
	return &gv, nil
}

func (s *Store) ListGroupVariables(ctx context.Context, groupID uuid.UUID) ([]model.GroupVariable, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, group_id, name, op, inputs, expr, unit FROM group_variables
		 WHERE group_id=$1 ORDER BY name`, groupID)
	if err != nil {
		return nil, norm(err)
	}
	defer rows.Close()
	out := []model.GroupVariable{}
	for rows.Next() {
		gv, err := scanGroupVariable(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *gv)
	}
	return out, norm(rows.Err())
}

func (s *Store) DeleteGroupVariable(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM group_variables WHERE id=$1`, id)
	if err != nil {
		return norm(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------- dashboard

const tileCols = `t.id, COALESCE(t.group_id, '00000000-0000-0000-0000-000000000000'::uuid), t.project_id,
	t.variable, t.title, t.kind, t.options, t.section, t.visibility, t.x, t.y, t.w, t.h`

func scanTile(r scanner) (*model.DashboardTile, error) {
	var t model.DashboardTile
	if err := r.Scan(&t.ID, &t.GroupID, &t.ProjectID, &t.Variable, &t.Title, &t.Kind, &t.Options,
		&t.Section, &t.Visibility, &t.X, &t.Y, &t.W, &t.H, &t.GroupSlug, &t.ProjectSlug); err != nil {
		return nil, norm(err)
	}
	return &t, nil
}

func (s *Store) ListTiles(ctx context.Context, ownerID uuid.UUID) ([]model.DashboardTile, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+tileCols+`, COALESCE(g.slug,''), COALESCE(p.slug,'')
		FROM dashboard_tiles t
		LEFT JOIN groups g ON g.id=t.group_id
		LEFT JOIN projects p ON p.id=t.project_id
		WHERE t.owner_id=$1 ORDER BY t.section, t.y, t.x`, ownerID)
	if err != nil {
		return nil, norm(err)
	}
	defer rows.Close()
	out := []model.DashboardTile{}
	for rows.Next() {
		t, err := scanTile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, norm(rows.Err())
}

func (s *Store) CreateTile(ctx context.Context, ownerID uuid.UUID, t model.DashboardTile) (*model.DashboardTile, error) {
	if len(t.Options) == 0 {
		t.Options = json.RawMessage(`{}`)
	}
	if t.W <= 0 {
		t.W = 1
	}
	if t.H <= 0 {
		t.H = 1
	}
	// A project tile has no group of its own, and a number tile has no project.
	var group any = t.GroupID
	if t.GroupID == uuid.Nil {
		group = nil
	}
	var id uuid.UUID
	if t.Visibility == "" {
		t.Visibility = "private"
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO dashboard_tiles (owner_id, group_id, project_id, variable, title, kind, options,
			section, visibility, x, y, w, h)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id`,
		ownerID, group, t.ProjectID, t.Variable, t.Title, t.Kind, t.Options,
		t.Section, t.Visibility, t.X, t.Y, t.W, t.H).Scan(&id)
	if err != nil {
		return nil, norm(err)
	}
	return scanTile(s.pool.QueryRow(ctx, `SELECT `+tileCols+`, COALESCE(g.slug,''), COALESCE(p.slug,'')
		FROM dashboard_tiles t
		LEFT JOIN groups g ON g.id=t.group_id
		LEFT JOIN projects p ON p.id=t.project_id WHERE t.id=$1`, id))
}

type TilePatch struct {
	Variable   *string
	Title      *string
	Kind       *string
	Options    *json.RawMessage
	Section    *string
	Visibility *string
	X, Y       *int
	W, H       *int
}

func (s *Store) UpdateTile(ctx context.Context, ownerID, id uuid.UUID, p TilePatch) error {
	set := ""
	args := []any{id, ownerID}
	add := func(col string, val any) {
		args = append(args, val)
		if set != "" {
			set += ", "
		}
		set += col + " = $" + strconv.Itoa(len(args))
	}
	if p.Variable != nil {
		add("variable", *p.Variable)
	}
	if p.Title != nil {
		add("title", *p.Title)
	}
	if p.Kind != nil {
		add("kind", *p.Kind)
	}
	if p.Section != nil {
		add("section", *p.Section)
	}
	if p.Visibility != nil {
		add("visibility", *p.Visibility)
	}
	if p.Options != nil {
		add("options", *p.Options)
	}
	if p.X != nil {
		add("x", *p.X)
	}
	if p.Y != nil {
		add("y", *p.Y)
	}
	if p.W != nil {
		add("w", *p.W)
	}
	if p.H != nil {
		add("h", *p.H)
	}
	if set == "" {
		return nil
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE dashboard_tiles SET `+set+` WHERE id=$1 AND owner_id=$2`, args...)
	if err != nil {
		return norm(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteTile(ctx context.Context, ownerID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM dashboard_tiles WHERE id=$1 AND owner_id=$2`, id, ownerID)
	if err != nil {
		return norm(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ------------------------------------------------------- put away, per person

// Hidden is one thing somebody took off their dashboard: a project, or a single
// variable. The ref is "<uuid>:<name>" for a variable and the project's id for a
// project.
type Hidden struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}

func (s *Store) ListHidden(ctx context.Context, ownerID uuid.UUID) ([]Hidden, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT kind, ref FROM dashboard_hidden WHERE owner_id=$1 ORDER BY created_at`, ownerID)
	if err != nil {
		return nil, norm(err)
	}
	defer rows.Close()
	out := []Hidden{}
	for rows.Next() {
		var h Hidden
		if err := rows.Scan(&h.Kind, &h.Ref); err != nil {
			return nil, norm(err)
		}
		out = append(out, h)
	}
	return out, norm(rows.Err())
}

func (s *Store) Hide(ctx context.Context, ownerID uuid.UUID, kind, ref string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO dashboard_hidden (owner_id, kind, ref)
		VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, ownerID, kind, ref)
	return norm(err)
}

func (s *Store) Unhide(ctx context.Context, ownerID uuid.UUID, kind, ref string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM dashboard_hidden WHERE owner_id=$1 AND kind=$2 AND ref=$3`, ownerID, kind, ref)
	return norm(err)
}

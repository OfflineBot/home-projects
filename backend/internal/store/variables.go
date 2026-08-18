package store

import (
	"context"
	"encoding/json"
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

// SetVariable stores a value and says whether it is a different one than
// before. Most writes are a refresh saying the same thing again; the ones that
// are not are what a watching page wants to hear about.
func (s *Store) SetVariable(ctx context.Context, projectID uuid.UUID, in VariableInput) (bool, error) {
	if in.Type == "" {
		in.Type = "text"
	}
	body, err := json.Marshal(in.Value)
	if err != nil {
		return false, err
	}
	// The old value is read in the same statement, so nothing can slip in
	// between looking and writing.
	var changed bool
	err = s.pool.QueryRow(ctx, `
		WITH before AS (SELECT value FROM variables WHERE project_id=$1 AND name=$2)
		INSERT INTO variables (project_id, name, type, value, unit, source, error, ttl_seconds, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8, now())
		ON CONFLICT (project_id, name) DO UPDATE SET
			type=EXCLUDED.type, value=EXCLUDED.value, unit=EXCLUDED.unit,
			source=EXCLUDED.source, error=EXCLUDED.error, ttl_seconds=EXCLUDED.ttl_seconds,
			updated_at=now()
		RETURNING (SELECT value FROM before) IS DISTINCT FROM $4::jsonb`,
		projectID, in.Name, in.Type, body, in.Unit, in.Source, in.Error, in.TTLSeconds).Scan(&changed)
	if err != nil {
		return false, norm(err)
	}
	if in.History {
		_, err = s.pool.Exec(ctx,
			`INSERT INTO variable_history (project_id, name, value) VALUES ($1,$2,$3)`,
			projectID, in.Name, body)
		if err != nil {
			return changed, norm(err)
		}
	}
	return changed, nil
}

// ReplaceVariables sets a project's variables from one source and drops the
// ones that source no longer reports.
func (s *Store) ReplaceVariables(ctx context.Context, projectID uuid.UUID, source string, in []VariableInput) error {
	keep := make([]string, 0, len(in))
	for _, v := range in {
		if _, err := s.SetVariable(ctx, projectID, v); err != nil {
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

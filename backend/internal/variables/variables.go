// Package variables is the wire everything hangs on.
//
// Every project exports variables. The group collects them under
// <project-slug>.<name>. The dashboard adds groups and shows their variables.
// There is no other route to the dashboard — no special path into the data.
package variables

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/events"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/projectyaml"
	"github.com/offlinebot/home-projects/backend/internal/store"
)

// ExportsFile is the back door that keeps everything open: any tool that can
// write a JSON file can supply variables — by upload, by API, or by git push.
const ExportsFile = "exports.json"

type Collector struct {
	env *capability.Env
	// allowCommands gates `from: command` in project.yaml. A project that
	// visitors may write into never runs commands, whatever the flag says.
	allowCommands bool

	mu   sync.Mutex
	last map[string]time.Time // project|name -> last fetch, for `every:`
}

func New(env *capability.Env, allowCommands bool) *Collector {
	return &Collector{env: env, allowCommands: allowCommands, last: map[string]time.Time{}}
}

// Start refreshes everything once and then keeps going in the background.
func (c *Collector) Start(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = time.Minute
	}
	go func() {
		c.RefreshAll(ctx)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.RefreshAll(ctx)
			}
		}
	}()

	// A changed file may change what a project exports, so a write refreshes
	// that project instead of waiting for the next tick.
	c.env.Bus.Subscribe(func(e events.Event) {
		if e.Kind != events.FileChanged || e.ProjectID == uuid.Nil {
			return
		}
		p, err := c.env.Store.ProjectByID(context.Background(), e.ProjectID)
		if err != nil {
			return
		}
		c.Refresh(context.Background(), p)
	})
}

func (c *Collector) RefreshAll(ctx context.Context) {
	projects, err := c.env.Store.ListAllProjects(ctx, false)
	if err != nil {
		slog.Warn("variables: projects could not be read", "error", err)
		return
	}
	for i := range projects {
		c.Refresh(ctx, &projects[i])
	}
	_ = c.env.Store.TrimHistory(ctx, 90*24*time.Hour)
}

// Refresh recomputes one project's variables from all four sources.
func (c *Collector) Refresh(ctx context.Context, p *model.Project) {
	var collected []store.VariableInput

	// 1. what the switched-on capabilities export
	for _, name := range p.Capabilities {
		cap, ok := capability.Get(name)
		if !ok {
			continue
		}
		vars, err := cap.Exports(ctx, c.env, p)
		if err != nil {
			slog.Warn("variables: capability export failed", "project", p.Slug, "capability", name, "error", err)
			continue
		}
		collected = append(collected, vars...)
	}

	// 2. what the project declares about itself
	collected = append(collected, c.fromYAML(ctx, p)...)

	// 3. whatever some tool dropped in exports.json
	collected = append(collected, c.fromExportsFile(ctx, p)...)

	for _, v := range collected {
		if v.Name == "" {
			continue
		}
		if err := Set(ctx, c.env, p.ID, v); err != nil {
			// The project can be deleted while a refresh is still running. That
			// is not a fault worth logging.
			if strings.Contains(err.Error(), "violates foreign key constraint") {
				return
			}
			slog.Warn("variables: could not be stored", "project", p.Slug, "name", v.Name, "error", err)
		}
	}
}

// Spec returns the parsed project.yaml plus the error to show if it is broken.
func Spec(ctx context.Context, env *capability.Env, p *model.Project) (*projectyaml.Spec, error) {
	if !env.Files.Exists(p, projectyaml.Name) {
		return nil, nil
	}
	body, err := env.Files.ReadLocal(ctx, p, projectyaml.Name)
	if err != nil {
		return nil, err
	}
	return projectyaml.Parse(body)
}

func (c *Collector) fromYAML(ctx context.Context, p *model.Project) []store.VariableInput {
	spec, err := Spec(ctx, c.env, p)
	if err != nil {
		// A broken file is reported as a variable of its own, so it is visible
		// instead of silently doing nothing.
		return []store.VariableInput{{
			Name: "project_yaml", Type: "text", Value: "broken",
			Error: err.Error(), Source: "project.yaml",
		}}
	}
	if spec == nil {
		return nil
	}

	out := make([]store.VariableInput, 0, len(spec.Variables))
	for name, v := range spec.Variables {
		if d := v.Interval(); d > 0 && !c.due(p.ID, name, d) {
			continue
		}
		value, err := c.readVar(ctx, p, v)
		item := store.VariableInput{
			Name: name, Type: v.Type, Unit: v.Unit, Value: value,
			Source: "project.yaml", History: v.History,
		}
		if err != nil {
			item.Error = err.Error()
		}
		if item.Type == "" {
			item.Type = guessType(value)
		}
		out = append(out, item)
	}
	return out
}

func (c *Collector) due(projectID uuid.UUID, name string, every time.Duration) bool {
	key := projectID.String() + "|" + name
	c.mu.Lock()
	defer c.mu.Unlock()
	last, ok := c.last[key]
	if ok && time.Since(last) < every {
		return false
	}
	c.last[key] = time.Now()
	return true
}

func (c *Collector) readVar(ctx context.Context, p *model.Project, v projectyaml.VarSpec) (any, error) {
	switch strings.ToLower(v.From) {
	case "", "constant":
		return v.Value, nil

	case "http":
		method := v.Method
		if method == "" {
			method = http.MethodGet
		}
		var body io.Reader
		if v.Body != "" {
			body = bytes.NewReader([]byte(v.Body))
		}
		req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), v.URL, body)
		if err != nil {
			return nil, err
		}
		for k, val := range v.Headers {
			req.Header.Set(k, val)
		}
		resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
		if err != nil {
			return nil, fmt.Errorf("not reachable: %w", err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("answered %s", resp.Status)
		}
		if v.Pick == "" {
			return strings.TrimSpace(string(raw)), nil
		}
		var parsed any
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, fmt.Errorf("the answer is not JSON: %w", err)
		}
		return pick(parsed, v.Pick)

	case "ping":
		return reachable(v.Host, v.Port), nil

	case "file":
		raw, err := c.env.Files.ReadLocal(ctx, p, v.Path)
		if err != nil {
			return nil, err
		}
		if v.Pick != "" {
			var parsed any
			if err := json.Unmarshal(raw, &parsed); err != nil {
				return nil, fmt.Errorf("%s is not JSON: %w", v.Path, err)
			}
			return pick(parsed, v.Pick)
		}
		return strings.TrimSpace(string(raw)), nil

	case "command":
		if !c.allowCommands {
			return nil, fmt.Errorf("running commands from project.yaml is switched off (ALLOW_PROJECT_COMMANDS)")
		}
		if p.AnonWrite {
			return nil, fmt.Errorf("this project accepts writes from visitors, so it may not run commands")
		}
		cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		out, err := exec.CommandContext(cmdCtx, "/bin/sh", "-c", v.Command).Output()
		if err != nil {
			return nil, fmt.Errorf("the command failed: %w", err)
		}
		return strings.TrimSpace(string(out)), nil
	}
	return nil, fmt.Errorf("unknown source %q", v.From)
}

// reachable is the "is it online" check behind `from: ping`. Without a port it
// tries the usual ones, because a raw ICMP ping needs privileges a container
// normally does not have.
func reachable(host string, port int) bool {
	if host == "" {
		return false
	}
	ports := []int{port}
	if port == 0 {
		ports = []int{22, 80, 443, 445, 3389}
	}
	for _, p := range ports {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(p)), 2*time.Second)
		if err == nil {
			_ = conn.Close()
			return true
		}
	}
	return false
}

func (c *Collector) fromExportsFile(ctx context.Context, p *model.Project) []store.VariableInput {
	if !c.env.Files.Exists(p, ExportsFile) {
		return nil
	}
	raw, err := c.env.Files.ReadLocal(ctx, p, ExportsFile)
	if err != nil {
		return nil
	}
	var flat map[string]any
	if err := json.Unmarshal(raw, &flat); err != nil {
		return []store.VariableInput{{
			Name: "exports_json", Type: "text", Value: "broken",
			Error: "exports.json is not valid JSON: " + err.Error(), Source: ExportsFile,
		}}
	}
	out := make([]store.VariableInput, 0, len(flat))
	for name, value := range flat {
		item := store.VariableInput{Name: name, Value: value, Source: ExportsFile, Type: guessType(value)}
		// The long form lets a file give type and unit as well.
		if m, ok := value.(map[string]any); ok {
			if v, has := m["value"]; has {
				item.Value = v
				item.Type = guessType(v)
				if t, ok := m["type"].(string); ok && t != "" {
					item.Type = t
				}
				if u, ok := m["unit"].(string); ok {
					item.Unit = u
				}
			}
		}
		out = append(out, item)
	}
	return out
}

func guessType(v any) string {
	switch t := v.(type) {
	case nil:
		return "text"
	case bool:
		return "bool"
	case float64, float32, int, int64:
		return "number"
	case string:
		if _, err := time.Parse(time.RFC3339, t); err == nil {
			return "date"
		}
		if _, err := strconv.ParseFloat(t, 64); err == nil {
			return "number"
		}
		return "text"
	case []any:
		for _, item := range t {
			if _, ok := item.(map[string]any); ok {
				return "table"
			}
			break
		}
		return "list"
	case time.Time:
		return "date"
	}
	return "text"
}

// pick walks a small subset of JSONPath: $.a.b[0].c
func pick(doc any, path string) (any, error) {
	p := strings.TrimPrefix(strings.TrimSpace(path), "$")
	p = strings.TrimPrefix(p, ".")
	if p == "" {
		return doc, nil
	}
	current := doc
	for _, part := range strings.Split(p, ".") {
		name := part
		var indexes []int
		for strings.HasSuffix(name, "]") {
			open := strings.LastIndex(name, "[")
			if open < 0 {
				break
			}
			idx, err := strconv.Atoi(name[open+1 : len(name)-1])
			if err != nil {
				return nil, fmt.Errorf("%q is not a valid path", path)
			}
			indexes = append([]int{idx}, indexes...)
			name = name[:open]
		}
		if name != "" {
			m, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%q: there is no object at %q", path, name)
			}
			current, ok = m[name]
			if !ok {
				return nil, fmt.Errorf("%q: the answer has no %q", path, name)
			}
		}
		for _, idx := range indexes {
			list, ok := current.([]any)
			if !ok {
				return nil, fmt.Errorf("%q: %q is not a list", path, name)
			}
			if idx < 0 || idx >= len(list) {
				return nil, fmt.Errorf("%q: the list has only %d entries", path, len(list))
			}
			current = list[idx]
		}
	}
	return current, nil
}

// --------------------------------------------------------------- aggregation

// GroupView is what GET /api/groups/<slug>/variables answers with.
type GroupView struct {
	Variables []model.Variable `json:"variables"`
	Derived   []Derived        `json:"derived"`
}

type Derived struct {
	Name string `json:"name"`
	Op   string `json:"op"`
	Unit string `json:"unit,omitempty"`
	// Expr is set when this is a formula rather than an operation over a list.
	Expr  string `json:"expr,omitempty"`
	Value any    `json:"value"`
	Type  string `json:"type"`
	Error string `json:"error,omitempty"`
}

// ForGroup collects the variables of every project in a group, named
// <project-slug>.<name>, and computes the group's own derived ones.
func ForGroup(ctx context.Context, st *store.Store, groupID uuid.UUID, visible func(projectID uuid.UUID) bool) (*GroupView, error) {
	all, err := st.VariablesForGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	vars := make([]model.Variable, 0, len(all))
	byName := map[string]model.Variable{}
	for _, v := range all {
		if visible != nil && !visible(v.ProjectID) {
			continue
		}
		vars = append(vars, v)
		byName[v.QualifiedName()] = v
	}

	defs, err := st.ListGroupVariables(ctx, groupID)
	if err != nil {
		return nil, err
	}
	derived := make([]Derived, 0, len(defs))
	for _, d := range defs {
		if strings.TrimSpace(d.Expr) != "" {
			derived = append(derived, evaluate(ctx, st, d))
			continue
		}
		derived = append(derived, compute(d, byName))
	}
	return &GroupView{Variables: vars, Derived: derived}, nil
}

// evaluate works out a derived value that is written as an expression rather
// than an operation over a list. A reference reaches anywhere on the server —
// {group/project/variable} — because "the average of these two, halved" is not
// a question that respects a group boundary.
func evaluate(ctx context.Context, st *store.Store, d model.GroupVariable) Derived {
	out := Derived{Name: d.Name, Op: "expr", Unit: d.Unit, Type: "number", Expr: d.Expr}
	value, err := Eval(d.Expr, func(ref string) (float64, bool) {
		return Resolve(ctx, st, ref)
	})
	if err != nil {
		out.Error = err.Error()
		out.Value = nil
		return out
	}
	out.Value = value
	return out
}

// Resolve reads one reference: group/project/variable, or project/variable when
// the project's address is unambiguous.
func Resolve(ctx context.Context, st *store.Store, ref string) (float64, bool) {
	parts := strings.Split(strings.Trim(strings.TrimSpace(ref), "/"), "/")
	if len(parts) < 2 {
		return 0, false
	}
	name := parts[len(parts)-1]
	projectSlug := parts[len(parts)-2]
	groupSlug := ""
	if len(parts) >= 3 {
		groupSlug = parts[len(parts)-3]
	}

	projects, err := st.ListProjects(ctx, nil, false, true)
	if err != nil {
		return 0, false
	}
	for i := range projects {
		p := projects[i]
		if !strings.EqualFold(p.Slug, projectSlug) {
			continue
		}
		if groupSlug != "" && !strings.EqualFold(p.GroupSlug, groupSlug) {
			continue
		}
		vars, err := st.VariablesForProject(ctx, p.ID)
		if err != nil {
			continue
		}
		for _, v := range vars {
			if !strings.EqualFold(v.Name, name) {
				continue
			}
			var raw any
			if err := json.Unmarshal(v.Value, &raw); err != nil {
				return 0, false
			}
			switch t := raw.(type) {
			case float64:
				return t, true
			case bool:
				if t {
					return 1, true
				}
				return 0, true
			case string:
				if f, err := strconv.ParseFloat(t, 64); err == nil {
					return f, true
				}
			}
			return 0, false
		}
	}
	return 0, false
}

func compute(d model.GroupVariable, byName map[string]model.Variable) Derived {
	out := Derived{Name: d.Name, Op: d.Op, Unit: d.Unit, Type: "number"}

	numbers := []float64{}
	bools := []bool{}
	missing := []string{}
	for _, input := range d.Inputs {
		v, ok := byName[input]
		if !ok {
			missing = append(missing, input)
			continue
		}
		var raw any
		if err := json.Unmarshal(v.Value, &raw); err != nil {
			continue
		}
		switch t := raw.(type) {
		case float64:
			numbers = append(numbers, t)
		case bool:
			bools = append(bools, t)
		case string:
			if f, err := strconv.ParseFloat(t, 64); err == nil {
				numbers = append(numbers, f)
			}
		}
	}
	if len(missing) > 0 {
		out.Error = "not there (yet): " + strings.Join(missing, ", ")
	}

	switch d.Op {
	case "count":
		out.Value = len(d.Inputs) - len(missing)
	case "sum":
		out.Value = sum(numbers)
	case "avg":
		if len(numbers) == 0 {
			out.Value = nil
			break
		}
		out.Value = sum(numbers) / float64(len(numbers))
	case "min":
		if len(numbers) == 0 {
			out.Value = nil
			break
		}
		m := numbers[0]
		for _, n := range numbers {
			if n < m {
				m = n
			}
		}
		out.Value = m
	case "max":
		if len(numbers) == 0 {
			out.Value = nil
			break
		}
		m := numbers[0]
		for _, n := range numbers {
			if n > m {
				m = n
			}
		}
		out.Value = m
	case "any":
		out.Type = "bool"
		any := false
		for _, b := range bools {
			any = any || b
		}
		out.Value = any
	case "all":
		out.Type = "bool"
		all := len(bools) > 0
		for _, b := range bools {
			all = all && b
		}
		out.Value = all
	default:
		out.Error = "unknown operation " + d.Op
		out.Value = nil
	}
	return out
}

func sum(in []float64) float64 {
	var total float64
	for _, n := range in {
		total += n
	}
	return total
}

// Set stores a variable and, when the value is a different one than before,
// says so on the bus.
//
// Every place that writes a variable goes through here rather than through the
// store, so "the number changed" is published once, in one place, however it
// came about — a scheduler, a rule, a capability's export or a file somebody
// dropped in. A page listening on /api/events is then looking at the number as
// it is now instead of as it was when the tab was opened.
func Set(ctx context.Context, env *capability.Env, projectID uuid.UUID, in store.VariableInput) error {
	changed, err := env.Store.SetVariable(ctx, projectID, in)
	if err != nil {
		return err
	}
	if changed {
		env.Bus.Publish(events.Event{
			Kind:      events.VariableChanged,
			ProjectID: projectID,
			Detail:    map[string]any{"name": in.Name, "value": in.Value, "unit": in.Unit},
		})
	}
	return nil
}

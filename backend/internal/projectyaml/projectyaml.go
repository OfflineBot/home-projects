// Package projectyaml reads the `project.yaml` a project can describe itself
// with.
//
// This is what makes a project a tool instead of a folder: a new tool is a new
// project plus one file — no Go, no React, no deploy. The file lives in the
// project, so it is versioned, cloned and copied along with it.
//
// Broken YAML never breaks a project. It is reported, and the project stays
// usable as a file tree.
package projectyaml

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Name is the file this package reads.
const Name = "project.yaml"

type Spec struct {
	Title        string             `yaml:"title" json:"title,omitempty"`
	Icon         string             `yaml:"icon" json:"icon,omitempty"`
	Color        string             `yaml:"color" json:"color,omitempty"`
	Preset       string             `yaml:"preset" json:"preset,omitempty"`
	Capabilities []string           `yaml:"capabilities" json:"capabilities,omitempty"`
	Variables    map[string]VarSpec `yaml:"variables" json:"variables,omitempty"`
	Actions      []ActionSpec       `yaml:"actions" json:"actions,omitempty"`
	Extra        map[string]any     `yaml:",inline" json:"-"`
}

// VarSpec is one self-declared variable. `from` decides which of the other
// fields matter.
type VarSpec struct {
	Type    string            `yaml:"type" json:"type,omitempty"`
	Unit    string            `yaml:"unit" json:"unit,omitempty"`
	From    string            `yaml:"from" json:"from,omitempty"` // http | file | ping | command | constant
	URL     string            `yaml:"url" json:"url,omitempty"`
	Method  string            `yaml:"method" json:"method,omitempty"`
	Headers map[string]string `yaml:"headers" json:"headers,omitempty"`
	Body    string            `yaml:"body" json:"body,omitempty"`
	Pick    string            `yaml:"pick" json:"pick,omitempty"` // $.a.b[0] into the JSON answer
	Every   string            `yaml:"every" json:"every,omitempty"`
	Host    string            `yaml:"host" json:"host,omitempty"`
	Port    int               `yaml:"port" json:"port,omitempty"`
	Path    string            `yaml:"path" json:"path,omitempty"`
	Command string            `yaml:"command" json:"command,omitempty"`
	Value   any               `yaml:"value" json:"value,omitempty"`
	History bool              `yaml:"history" json:"history,omitempty"`
}

// ActionSpec is a button — in the project and on the dashboard.
type ActionSpec struct {
	Name    string            `yaml:"name" json:"name"`
	Run     string            `yaml:"run" json:"run"` // the registered automation action
	Method  string            `yaml:"method" json:"method,omitempty"`
	URL     string            `yaml:"url" json:"url,omitempty"`
	Headers map[string]string `yaml:"headers" json:"headers,omitempty"`
	Body    string            `yaml:"body" json:"body,omitempty"`
	Host    string            `yaml:"host" json:"host,omitempty"`
	Port    int               `yaml:"port" json:"port,omitempty"`
	MAC     string            `yaml:"mac" json:"mac,omitempty"`
	User    string            `yaml:"user" json:"user,omitempty"`
	Command string            `yaml:"command" json:"command,omitempty"`
	Account string            `yaml:"account" json:"account,omitempty"`
	Path    string            `yaml:"path" json:"path,omitempty"`
	Content string            `yaml:"content" json:"content,omitempty"`
	Project string            `yaml:"project" json:"project,omitempty"`
}

// Params turns an action into the map the automation registry expects.
func (a ActionSpec) Params() map[string]any {
	m := map[string]any{}
	put := func(k string, v any) {
		switch t := v.(type) {
		case string:
			if t == "" {
				return
			}
		case int:
			if t == 0 {
				return
			}
		}
		m[k] = v
	}
	put("method", a.Method)
	put("url", a.URL)
	put("body", a.Body)
	put("host", a.Host)
	put("port", a.Port)
	put("mac", a.MAC)
	put("user", a.User)
	put("command", a.Command)
	put("account", a.Account)
	put("path", a.Path)
	put("content", a.Content)
	put("project", a.Project)
	if len(a.Headers) > 0 {
		headers := map[string]any{}
		for k, v := range a.Headers {
			headers[k] = v
		}
		m["headers"] = headers
	}
	return m
}

// Interval is how often a variable should be refreshed; zero means "with the
// normal refresh".
func (v VarSpec) Interval() time.Duration {
	if v.Every == "" {
		return 0
	}
	d, err := time.ParseDuration(v.Every)
	if err != nil || d < 10*time.Second {
		return 0
	}
	return d
}

// Parse reads a project.yaml. The returned error is meant to be shown to the
// user, not swallowed.
func Parse(data []byte) (*Spec, error) {
	var s Spec
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("project.yaml cannot be read: %w", err)
	}
	if err := s.Validate(); err != nil {
		return &s, err
	}
	return &s, nil
}

func (s *Spec) Validate() error {
	var problems []string
	for name, v := range s.Variables {
		switch strings.ToLower(v.From) {
		case "", "constant":
			if v.Value == nil {
				problems = append(problems, fmt.Sprintf("%s: needs a value", name))
			}
		case "http":
			if v.URL == "" {
				problems = append(problems, fmt.Sprintf("%s: from: http needs a url", name))
			}
		case "ping":
			if v.Host == "" {
				problems = append(problems, fmt.Sprintf("%s: from: ping needs a host", name))
			}
		case "file":
			if v.Path == "" {
				problems = append(problems, fmt.Sprintf("%s: from: file needs a path", name))
			}
		case "command":
			if v.Command == "" {
				problems = append(problems, fmt.Sprintf("%s: from: command needs a command", name))
			}
		default:
			problems = append(problems, fmt.Sprintf("%s: unknown source %q", name, v.From))
		}
	}
	for i, a := range s.Actions {
		if a.Name == "" {
			problems = append(problems, fmt.Sprintf("action %d: needs a name", i+1))
		}
		if a.Run == "" {
			problems = append(problems, fmt.Sprintf("action %q: needs a run", a.Name))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("project.yaml has problems: %s", strings.Join(problems, "; "))
	}
	return nil
}

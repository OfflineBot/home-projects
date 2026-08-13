// Package ics reads and writes iCalendar (RFC 5545).
//
// The file is the truth, so parsing is lossless: every property and every
// component that comes in also goes out again, including the ones this server
// has no opinion about. Editing an event touches the properties it knows and
// leaves the rest of the file exactly as it was.
package ics

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
)

type Param struct {
	Name   string
	Values []string
}

type Prop struct {
	Name   string
	Params []Param
	Value  string
}

// Param returns the first value of a parameter, or "".
func (p *Prop) Param(name string) string {
	for _, pa := range p.Params {
		if strings.EqualFold(pa.Name, name) && len(pa.Values) > 0 {
			return pa.Values[0]
		}
	}
	return ""
}

func (p *Prop) SetParam(name, value string) {
	for i, pa := range p.Params {
		if strings.EqualFold(pa.Name, name) {
			if value == "" {
				p.Params = append(p.Params[:i], p.Params[i+1:]...)
			} else {
				p.Params[i].Values = []string{value}
			}
			return
		}
	}
	if value != "" {
		p.Params = append(p.Params, Param{Name: name, Values: []string{value}})
	}
}

type Component struct {
	Name     string
	Props    []Prop
	Children []*Component
}

func NewComponent(name string) *Component { return &Component{Name: strings.ToUpper(name)} }

// Get returns the first property with that name.
func (c *Component) Get(name string) *Prop {
	for i := range c.Props {
		if strings.EqualFold(c.Props[i].Name, name) {
			return &c.Props[i]
		}
	}
	return nil
}

func (c *Component) All(name string) []*Prop {
	var out []*Prop
	for i := range c.Props {
		if strings.EqualFold(c.Props[i].Name, name) {
			out = append(out, &c.Props[i])
		}
	}
	return out
}

func (c *Component) Value(name string) string {
	if p := c.Get(name); p != nil {
		return p.Value
	}
	return ""
}

// Set replaces a property's value, keeping its position and parameters.
func (c *Component) Set(name, value string) *Prop {
	if p := c.Get(name); p != nil {
		p.Value = value
		return p
	}
	c.Props = append(c.Props, Prop{Name: strings.ToUpper(name), Value: value})
	return &c.Props[len(c.Props)-1]
}

func (c *Component) Remove(name string) {
	out := c.Props[:0]
	for _, p := range c.Props {
		if !strings.EqualFold(p.Name, name) {
			out = append(out, p)
		}
	}
	c.Props = out
}

func (c *Component) RemoveChildren(name string) {
	out := c.Children[:0]
	for _, ch := range c.Children {
		if !strings.EqualFold(ch.Name, name) {
			out = append(out, ch)
		}
	}
	c.Children = out
}

// Kids returns the child components with that name.
func (c *Component) Kids(name string) []*Component {
	var out []*Component
	for _, ch := range c.Children {
		if strings.EqualFold(ch.Name, name) {
			out = append(out, ch)
		}
	}
	return out
}

// ------------------------------------------------------------------ parsing

// Parse reads one or more calendars. Content lines are unfolded first, as RFC
// 5545 requires.
func Parse(data []byte) (*Component, error) {
	root := &Component{Name: "ROOT"}
	stack := []*Component{root}

	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var current strings.Builder
	flush := func() error {
		line := current.String()
		current.Reset()
		if strings.TrimSpace(line) == "" {
			return nil
		}
		prop, err := parseLine(line)
		if err != nil {
			return err
		}
		switch {
		case strings.EqualFold(prop.Name, "BEGIN"):
			child := &Component{Name: strings.ToUpper(prop.Value)}
			parent := stack[len(stack)-1]
			parent.Children = append(parent.Children, child)
			stack = append(stack, child)
		case strings.EqualFold(prop.Name, "END"):
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		default:
			top := stack[len(stack)-1]
			top.Props = append(top.Props, prop)
		}
		return nil
	}

	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			current.WriteString(line[1:]) // folded continuation
			continue
		}
		if err := flush(); err != nil {
			return nil, err
		}
		current.WriteString(line)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if len(root.Children) == 0 {
		return nil, fmt.Errorf("this is not an iCalendar file: no BEGIN:VCALENDAR found")
	}
	return root, nil
}

// ParseCalendar returns the first VCALENDAR in the data.
func ParseCalendar(data []byte) (*Component, error) {
	root, err := Parse(data)
	if err != nil {
		return nil, err
	}
	for _, c := range root.Children {
		if strings.EqualFold(c.Name, "VCALENDAR") {
			return c, nil
		}
	}
	return nil, fmt.Errorf("this is not an iCalendar file: no VCALENDAR found")
}

func parseLine(line string) (Prop, error) {
	// name[;param=value[,value]]*:value — colons inside quoted parameters do
	// not end the name part.
	inQuotes := false
	colon := -1
	semi := -1
	for i, r := range line {
		switch r {
		case '"':
			inQuotes = !inQuotes
		case ':':
			if !inQuotes {
				colon = i
			}
		case ';':
			if !inQuotes && semi < 0 {
				semi = i
			}
		}
		if colon >= 0 {
			break
		}
	}
	if colon < 0 {
		return Prop{}, fmt.Errorf("malformed line (no colon): %.60s", line)
	}
	head := line[:colon]
	value := line[colon+1:]

	p := Prop{Value: value}
	if semi >= 0 && semi < colon {
		p.Name = strings.ToUpper(strings.TrimSpace(head[:semi]))
		for _, part := range splitUnquoted(head[semi+1:], ';') {
			if part == "" {
				continue
			}
			name, val, ok := strings.Cut(part, "=")
			if !ok {
				p.Params = append(p.Params, Param{Name: strings.ToUpper(name)})
				continue
			}
			var values []string
			for _, v := range splitUnquoted(val, ',') {
				values = append(values, strings.Trim(v, `"`))
			}
			p.Params = append(p.Params, Param{Name: strings.ToUpper(name), Values: values})
		}
	} else {
		p.Name = strings.ToUpper(strings.TrimSpace(head))
	}
	return p, nil
}

func splitUnquoted(s string, sep rune) []string {
	var out []string
	var b strings.Builder
	inQuotes := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuotes = !inQuotes
			b.WriteRune(r)
		case r == sep && !inQuotes:
			out = append(out, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	out = append(out, b.String())
	return out
}

// ------------------------------------------------------------- serialisation

// Write renders a component tree with CRLF line endings and 75-octet folding.
func (c *Component) Write(w io.Writer) error {
	if c.Name == "ROOT" {
		for _, ch := range c.Children {
			if err := ch.Write(w); err != nil {
				return err
			}
		}
		return nil
	}
	if err := writeFolded(w, "BEGIN:"+c.Name); err != nil {
		return err
	}
	for _, p := range c.Props {
		if err := writeFolded(w, p.String()); err != nil {
			return err
		}
	}
	for _, ch := range c.Children {
		if err := ch.Write(w); err != nil {
			return err
		}
	}
	return writeFolded(w, "END:"+c.Name)
}

func (c *Component) Bytes() []byte {
	var buf bytes.Buffer
	_ = c.Write(&buf)
	return buf.Bytes()
}

func (p Prop) String() string {
	var b strings.Builder
	b.WriteString(p.Name)
	for _, pa := range p.Params {
		b.WriteString(";")
		b.WriteString(pa.Name)
		if len(pa.Values) > 0 {
			b.WriteString("=")
			for i, v := range pa.Values {
				if i > 0 {
					b.WriteString(",")
				}
				if strings.ContainsAny(v, ";:,\"") {
					b.WriteString(`"` + strings.ReplaceAll(v, `"`, "") + `"`)
				} else {
					b.WriteString(v)
				}
			}
		}
	}
	b.WriteString(":")
	b.WriteString(p.Value)
	return b.String()
}

// writeFolded splits a content line at 75 octets, continuing with a space.
func writeFolded(w io.Writer, line string) error {
	const limit = 75
	b := []byte(line)
	for len(b) > limit {
		cut := limit
		// never split a UTF-8 sequence
		for cut > 0 && b[cut]&0xC0 == 0x80 {
			cut--
		}
		if _, err := w.Write(append(append([]byte{}, b[:cut]...), '\r', '\n', ' ')); err != nil {
			return err
		}
		b = b[cut:]
	}
	_, err := w.Write(append(append([]byte{}, b...), '\r', '\n'))
	return err
}

// ---------------------------------------------------------------- TEXT value

// EscapeText encodes a TEXT value: backslash, semicolon, comma and newline.
func EscapeText(s string) string {
	r := strings.NewReplacer(
		"\\", "\\\\",
		";", "\\;",
		",", "\\,",
		"\r\n", "\\n",
		"\n", "\\n",
		"\r", "\\n",
	)
	return r.Replace(s)
}

// UnescapeText decodes a TEXT value.
func UnescapeText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n', 'N':
				b.WriteByte('\n')
			case '\\', ';', ',':
				b.WriteByte(s[i])
			default:
				b.WriteByte('\\')
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

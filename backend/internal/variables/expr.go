package variables

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// An expression is how one number is made out of others.
//
//	({studies/noten/durchschnitt} + {studies/noten/durchschnitt2}) / 2
//
// A reference is written in braces, exactly as a project is in a filter rule:
// group/project/variable. Everything else is arithmetic — the four operations,
// parentheses, and numbers.
//
// It is deliberately not a language. There are no variables to declare, no
// functions to look up and nothing to loop over, because the question it
// answers is "what number should the tile show?" and that question has never
// needed a loop. What it does have is a readable error: an expression that
// cannot be worked out says which reference was missing, rather than showing a
// zero that looks like an answer.

// Lookup answers what a reference is worth. Missing means "not there (yet)",
// which is different from zero.
type Lookup func(ref string) (float64, bool)

// Eval works out an expression. The error names what was wrong in words.
func Eval(expr string, lookup Lookup) (float64, error) {
	p := &parser{src: []rune(expr), lookup: lookup}
	p.skip()
	if p.done() {
		return 0, fmt.Errorf("there is nothing to work out")
	}
	value, err := p.expression()
	if err != nil {
		return 0, err
	}
	p.skip()
	if !p.done() {
		return 0, fmt.Errorf("cannot read %q — check the brackets", string(p.src[p.at:]))
	}
	return value, nil
}

// References lists what an expression depends on, so the UI can say what it is
// waiting for and the dashboard can refresh when one of them changes.
func References(expr string) []string {
	var out []string
	runes := []rune(expr)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '{' {
			continue
		}
		end := i + 1
		for end < len(runes) && runes[end] != '}' {
			end++
		}
		if end < len(runes) {
			out = append(out, strings.TrimSpace(string(runes[i+1:end])))
			i = end
		}
	}
	return out
}

type parser struct {
	src    []rune
	at     int
	lookup Lookup
}

func (p *parser) done() bool { return p.at >= len(p.src) }

func (p *parser) skip() {
	for !p.done() && unicode.IsSpace(p.src[p.at]) {
		p.at++
	}
}

// expression is addition and subtraction: the loosest binding.
func (p *parser) expression() (float64, error) {
	left, err := p.term()
	if err != nil {
		return 0, err
	}
	for {
		p.skip()
		if p.done() || (p.src[p.at] != '+' && p.src[p.at] != '-') {
			return left, nil
		}
		op := p.src[p.at]
		p.at++
		right, err := p.term()
		if err != nil {
			return 0, err
		}
		if op == '+' {
			left += right
		} else {
			left -= right
		}
	}
}

// term is multiplication and division, which bind tighter.
func (p *parser) term() (float64, error) {
	left, err := p.factor()
	if err != nil {
		return 0, err
	}
	for {
		p.skip()
		if p.done() || (p.src[p.at] != '*' && p.src[p.at] != '/') {
			return left, nil
		}
		op := p.src[p.at]
		p.at++
		right, err := p.factor()
		if err != nil {
			return 0, err
		}
		if op == '*' {
			left *= right
			continue
		}
		if right == 0 {
			return 0, fmt.Errorf("dividing by zero")
		}
		left /= right
	}
}

// factor is a number, a reference, a bracketed expression, or a minus in front
// of any of them.
func (p *parser) factor() (float64, error) {
	p.skip()
	if p.done() {
		return 0, fmt.Errorf("something is missing at the end")
	}
	switch c := p.src[p.at]; {
	case c == '-':
		p.at++
		value, err := p.factor()
		return -value, err
	case c == '+':
		p.at++
		return p.factor()
	case c == '(':
		p.at++
		value, err := p.expression()
		if err != nil {
			return 0, err
		}
		p.skip()
		if p.done() || p.src[p.at] != ')' {
			return 0, fmt.Errorf("a bracket was opened and not closed")
		}
		p.at++
		return value, nil
	case c == '{':
		end := p.at + 1
		for end < len(p.src) && p.src[end] != '}' {
			end++
		}
		if end >= len(p.src) {
			return 0, fmt.Errorf("a reference was opened with { and not closed")
		}
		ref := strings.TrimSpace(string(p.src[p.at+1 : end]))
		p.at = end + 1
		if p.lookup == nil {
			return 0, fmt.Errorf("nothing to look %s up in", ref)
		}
		value, ok := p.lookup(ref)
		if !ok {
			return 0, fmt.Errorf("%s is not there (yet)", ref)
		}
		return value, nil
	case unicode.IsDigit(c) || c == '.':
		start := p.at
		for !p.done() && (unicode.IsDigit(p.src[p.at]) || p.src[p.at] == '.') {
			p.at++
		}
		return strconv.ParseFloat(string(p.src[start:p.at]), 64)
	default:
		return 0, fmt.Errorf("cannot read %q — a reference is written {group/project/variable}",
			string(p.src[p.at:]))
	}
}

// Package slug makes URL and branch names out of titles.
//
// A slug is both the URL segment and the git branch (or repository) name, so
// it has to survive both: lowercase, ASCII, no spaces, and none of the
// characters git refuses in a ref.
package slug

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var (
	multiDash = regexp.MustCompile(`-{2,}`)
	// git refuses refs with these, and URLs are happier without them too.
	allowed = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
)

// Reserved names would collide with the server's own routes.
var reserved = map[string]bool{
	"api": true, "git": true, "s": true, "assets": true, "static": true,
	"login": true, "logout": true, "health": true, "new": true, "ungrouped": true,
	"head": true, "refs": true, "objects": true,
}

// Make turns arbitrary text into a slug. It never fails; an empty result is
// reported by the caller through Validate.
func Make(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = replaceUmlauts(s)

	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		case unicode.IsSpace(r) || r == '/' || r == '\\':
			b.WriteRune('-')
		default:
			// drop everything else
		}
	}
	out := multiDash.ReplaceAllString(b.String(), "-")
	out = strings.Trim(out, "-._")
	if len(out) > 60 {
		out = strings.Trim(out[:60], "-._")
	}
	return out
}

func replaceUmlauts(s string) string {
	r := strings.NewReplacer(
		"ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss",
		"á", "a", "à", "a", "â", "a", "å", "a", "ã", "a",
		"é", "e", "è", "e", "ê", "e", "ë", "e",
		"í", "i", "ì", "i", "î", "i", "ï", "i",
		"ó", "o", "ò", "o", "ô", "o", "õ", "o", "ø", "o",
		"ú", "u", "ù", "u", "û", "u",
		"ñ", "n", "ç", "c",
	)
	return r.Replace(s)
}

// Validate checks a slug a human typed.
func Validate(s string) error {
	if s == "" {
		return fmt.Errorf("the address may not be empty")
	}
	if len(s) > 60 {
		return fmt.Errorf("the address may be at most 60 characters long")
	}
	if !allowed.MatchString(s) {
		return fmt.Errorf("the address may only contain a-z, 0-9, dot, dash and underscore, and has to start with a letter or a digit")
	}
	if strings.HasSuffix(s, ".lock") || strings.HasSuffix(s, ".git") {
		return fmt.Errorf("the address may not end in .lock or .git — git refuses that as a branch name")
	}
	if reserved[s] {
		return fmt.Errorf("%q is reserved by the server", s)
	}
	return nil
}

// Unique appends -2, -3 … until taken says the slug is free.
func Unique(base string, taken func(string) (bool, error)) (string, error) {
	if base == "" {
		base = "untitled"
	}
	candidate := base
	for i := 2; i < 500; i++ {
		if err := Validate(candidate); err == nil {
			used, err := taken(candidate)
			if err != nil {
				return "", err
			}
			if !used {
				return candidate, nil
			}
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
	return "", fmt.Errorf("no free address found for %q", base)
}

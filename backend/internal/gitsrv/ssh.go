package gitsrv

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Git over SSH: `git clone git@<host>:<group-slug>.git`.
//
// A push over SSH reaches the bare repository without passing through this
// server's HTTP handler, so on its own it would ignore everything the HTTPS
// path enforces — hidden branches, read-only projects, and the working tree
// that has to follow a push.
//
// It does not, because every key sshd hands out carries a forced command. The
// wrapper that command names asks this server the same questions the HTTP
// handler asks itself, sets the same environment for the same pre-receive hook,
// and reports back afterwards so the working trees follow. SSH and HTTPS end up
// in exactly the same place.

// AuthorizedKey is one key sshd is told about.
type AuthorizedKey struct {
	ID        string
	Name      string
	PublicKey string
}

// The options make a key useless for anything but talking to the wrapper: no
// shell, no forwarding, no pty.
const keyOptions = "no-agent-forwarding,no-port-forwarding,no-pty,no-user-rc,no-X11-forwarding"

// AuthorizedKeys renders what sshd asks for through AuthorizedKeysCommand:
// one line per registered key, each with a forced command carrying the key's
// id.
//
// There is no file. sshd asks this server every time someone connects, so a
// key that was just removed stops working at once, and there is no second copy
// of the truth to drift from the database.
func AuthorizedKeys(wrapper string, keys []AuthorizedKey) string {
	if wrapper == "" {
		wrapper = "/usr/local/bin/hp-git-shell"
	}
	sorted := append([]AuthorizedKey(nil), keys...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	var b strings.Builder
	for _, k := range sorted {
		key := strings.TrimSpace(k.PublicKey)
		// A key carrying a newline could smuggle a second line with a forced
		// command of its own. It is dropped, not written.
		if key == "" || strings.ContainsAny(key, "\n\r") || strings.ContainsAny(k.ID, "\n\r\"") {
			continue
		}
		fmt.Fprintf(&b, "command=%q,%s %s\n", wrapper+" "+k.ID, keyOptions, key)
	}
	return b.String()
}

// ParsePublicKey checks an OpenSSH public key and returns its fingerprint in
// the form ssh-keygen prints, so it can be compared by eye.
func ParsePublicKey(input string) (normalised string, fingerprint string, err error) {
	line := strings.TrimSpace(strings.ReplaceAll(input, "\r", ""))
	if line == "" {
		return "", "", fmt.Errorf("the key is empty")
	}
	if strings.Contains(line, "\n") {
		return "", "", fmt.Errorf("this looks like several keys — add them one at a time")
	}
	if strings.Contains(line, "PRIVATE KEY") {
		return "", "", fmt.Errorf("this is a private key. Only the public one belongs here — the file ending in .pub")
	}

	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", "", fmt.Errorf("this is not an OpenSSH public key")
	}
	algorithm, blob := fields[0], fields[1]
	switch algorithm {
	case "ssh-ed25519", "ssh-rsa", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384",
		"ecdsa-sha2-nistp521", "sk-ssh-ed25519@openssh.com", "sk-ecdsa-sha2-nistp256@openssh.com":
	default:
		return "", "", fmt.Errorf("%q is not a key type this server accepts", algorithm)
	}
	raw, derr := base64.StdEncoding.DecodeString(blob)
	if derr != nil || len(raw) < 4 {
		return "", "", fmt.Errorf("the key body cannot be read")
	}

	sum := sha256.Sum256(raw)
	fingerprint = "SHA256:" + strings.TrimRight(base64.StdEncoding.EncodeToString(sum[:]), "=")
	normalised = algorithm + " " + blob
	if len(fields) > 2 {
		normalised += " " + strings.Join(fields[2:], " ")
	}
	return normalised, fingerprint, nil
}

// SSHCloneURL is the address shown next to the HTTPS one.
func SSHCloneURL(host, groupSlug string) string {
	if host == "" {
		return ""
	}
	if groupSlug == "" {
		groupSlug = UngroupedRepo
	}
	return fmt.Sprintf("%s:%s.git", host, groupSlug)
}

// repoName is the shape a group's address has: the same one slug.Validate
// enforces when the group is created.
var repoName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// RepoFromSSHCommand turns what sshd hands the wrapper — `git-upload-pack
// '/srv/git/personal.git'` — into a plain group slug.
func RepoFromSSHCommand(argument string) (string, error) {
	name := strings.TrimSpace(argument)
	name = strings.Trim(name, "'\"")
	name = strings.ReplaceAll(name, "\\", "/")
	// A path that walks upwards is refused, not quietly reduced to its last
	// segment: `../../etc/passwd` is not a request for the repository `passwd`,
	// it is a request that should be answered with a no.
	for _, segment := range strings.Split(name, "/") {
		if segment == ".." {
			return "", fmt.Errorf("%q is not a repository name", argument)
		}
	}
	name = strings.TrimPrefix(name, "/")
	name = filepath.Base(name)
	name = strings.TrimSuffix(name, ".git")
	// What is left has to look like a group's address — the same shape the rest
	// of the server uses for a slug. Anything else is refused rather than
	// interpreted.
	if !repoName.MatchString(name) || strings.Contains(name, "..") {
		return "", fmt.Errorf("%q is not a repository name", argument)
	}
	return name, nil
}

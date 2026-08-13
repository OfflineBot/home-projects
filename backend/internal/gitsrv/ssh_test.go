package gitsrv

import (
	"strings"
	"testing"
)

func TestParsePublicKey(t *testing.T) {
	const ed25519 = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ4vQwzZ0mHnQ0FhZq0lC0mAWmMSMFRoZP0nqvGGKk0P someone@laptop"

	normalised, fingerprint, err := ParsePublicKey("  " + ed25519 + "\n")
	if err != nil {
		t.Fatalf("a valid key was refused: %v", err)
	}
	if !strings.HasPrefix(normalised, "ssh-ed25519 AAAA") {
		t.Errorf("normalised = %q", normalised)
	}
	if !strings.HasPrefix(fingerprint, "SHA256:") {
		t.Errorf("fingerprint = %q — it should read like the one ssh-keygen prints", fingerprint)
	}
	// The same key twice has to give the same fingerprint, or the duplicate
	// check in the database is worthless.
	_, again, _ := ParsePublicKey(ed25519)
	if again != fingerprint {
		t.Error("the fingerprint is not stable")
	}
}

func TestParsePublicKeyRefusesTheWrongThings(t *testing.T) {
	cases := map[string]string{
		"a private key":    "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaA==\n-----END OPENSSH PRIVATE KEY-----",
		"empty":            "   ",
		"nonsense":         "hello there",
		"an unknown type":  "ssh-dss AAAAB3NzaC1kc3M=",
		"two keys at once": "ssh-ed25519 AAAAC3Nza\nssh-ed25519 AAAAC3Nzb",
		"a broken body":    "ssh-ed25519 not-base64!!",
	}
	for what, input := range cases {
		if _, _, err := ParsePublicKey(input); err == nil {
			t.Errorf("%s was accepted", what)
		}
	}
}

func TestRepoFromSSHCommand(t *testing.T) {
	ok := map[string]string{
		"'/srv/git/personal.git'": "personal",
		"'personal.git'":          "personal",
		"/personal.git":           "personal",
		"personal":                "personal",
		"'~/studies.git'":         "studies",
	}
	for in, want := range ok {
		got, err := RepoFromSSHCommand(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}

	// A path that tries to leave, or a name with room for a second argument,
	// is refused rather than cleaned up.
	for _, bad := range []string{
		"", "'..'", "'a b.git'", "'/'", "'.'",
		"'../../etc/passwd'", "'/srv/git/../../etc/shadow'", "'..%2f..%2fx'",
	} {
		if got, err := RepoFromSSHCommand(bad); err == nil {
			t.Errorf("%q was accepted as %q", bad, got)
		}
	}
}

func TestAuthorizedKeys(t *testing.T) {
	text := AuthorizedKeys("/usr/local/bin/hp-git-shell", []AuthorizedKey{
		{ID: "11111111-1111-1111-1111-111111111111", Name: "laptop", PublicKey: "ssh-ed25519 AAAA laptop"},
		{ID: "22222222-2222-2222-2222-222222222222", Name: "phone", PublicKey: "ssh-ed25519 BBBB phone"},
		{ID: "33333333-3333-3333-3333-333333333333", Name: "broken", PublicKey: "ssh-ed25519 CCCC\ncommand=\"/bin/sh\" ssh-ed25519 DDDD"},
	})

	if strings.Count(text, "ssh-ed25519") != 2 {
		t.Errorf("expected two keys, got:\n%s", text)
	}
	// A key carrying a newline could smuggle a second line with a forced
	// command of its own. It is dropped, not handed to sshd.
	if strings.Contains(text, "/bin/sh") {
		t.Errorf("a key with a newline came through:\n%s", text)
	}
	for _, want := range []string{
		`command="/usr/local/bin/hp-git-shell 11111111-1111-1111-1111-111111111111"`,
		"no-pty", "no-port-forwarding", "no-agent-forwarding",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	// Every line has to end in one, or sshd reads two keys as one.
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if !strings.HasPrefix(line, "command=") {
			t.Errorf("stray line: %q", line)
		}
	}
}

func TestAuthorizedKeysWithNoKeys(t *testing.T) {
	if got := AuthorizedKeys("", nil); got != "" {
		t.Errorf("expected nothing, got %q", got)
	}
}

func TestSSHCloneURL(t *testing.T) {
	cases := map[string]string{
		"git@offlinebot.xyz":  "git@offlinebot.xyz:studies.git",
		" git@offlinebot.xyz": "git@offlinebot.xyz:studies.git",
		"git@offlinebot.xyz:": "git@offlinebot.xyz:studies.git",
		// Written without a user, git would read it as scp syntax with whoever
		// you happen to be. The user is filled in rather than handed out broken.
		"offlinebot.xyz": "git@offlinebot.xyz:studies.git",
		// A port needs the URL form.
		"ssh://git@offlinebot.xyz:2222":  "ssh://git@offlinebot.xyz:2222/studies.git",
		"ssh://git@offlinebot.xyz:2222/": "ssh://git@offlinebot.xyz:2222/studies.git",
	}
	for host, want := range cases {
		if got := SSHCloneURL(host, "studies"); got != want {
			t.Errorf("%q → %q, want %q", host, got, want)
		}
	}
	if got := SSHCloneURL("git@offlinebot.xyz", ""); got != "git@offlinebot.xyz:ungrouped.git" {
		t.Errorf("ungrouped: got %q", got)
	}
	if got := SSHCloneURL("", "studies"); got != "" {
		t.Errorf("without a host there is no address, got %q", got)
	}
}

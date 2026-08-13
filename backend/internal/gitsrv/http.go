package gitsrv

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Smart HTTP is served by running git's own upload-pack and receive-pack.
//
// The brief suggests git-http-backend as CGI; this does the same protocol one
// layer lower and without the CGI binary. That removes the trap noted in the
// brief (Alpine's git package does not contain git-http-backend, it needs
// git-daemon) and gives us something CGI cannot: the push guard below runs
// inside our own process, with the environment we set for it.

// PreReceiveHook refuses every ref that the server did not explicitly allow.
// It is installed into every repository and reads its allow-list from the
// environment the server sets when it starts receive-pack. Nothing else on the
// machine starts receive-pack, so nothing else can push.
const PreReceiveHook = `#!/bin/sh
# Installed by home-projects. Do not edit.
# HP_ALLOWED_REFS holds the refs this push may touch; anything else is refused.
while read -r old new ref; do
	case " ${HP_ALLOWED_REFS} " in
		*" ${ref} "*) ;;
		*)
			echo "${HP_DENY_MESSAGE:-This ref may not be pushed.}" >&2
			echo "refused: ${ref}" >&2
			exit 1
			;;
	esac
done
exit 0
`

// InstallHooks writes the push guard into a repository.
func (s *Service) InstallHooks(groupSlug string) error {
	repo := s.repoFor(groupSlug)
	dir := filepath.Join(repo, "hooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "pre-receive"), []byte(PreReceiveHook), 0o755)
}

type RPC struct {
	GroupSlug string
	// Service is "upload-pack" (clone, fetch) or "receive-pack" (push).
	Service string
	// Advertise is the GET info/refs half of the conversation.
	Advertise bool
	// Protocol carries the client's Git-Protocol header (protocol v2).
	Protocol string
	// HiddenRefs are branches the client may not see — projects whose
	// visibility is stricter than their group's.
	HiddenRefs []string
	// AllowedRefs are the refs a push may write. Empty means: no push at all.
	AllowedRefs []string
	DenyMessage string

	Stdin  io.Reader
	Stdout io.Writer
}

func ValidService(name string) bool {
	return name == "upload-pack" || name == "receive-pack"
}

// Run performs one leg of the smart HTTP conversation.
func (s *Service) Run(ctx context.Context, rpc RPC) error {
	if !ValidService(rpc.Service) {
		return fmt.Errorf("unknown git service %q", rpc.Service)
	}
	repo := s.repoFor(rpc.GroupSlug)
	if _, err := os.Stat(filepath.Join(repo, "HEAD")); err != nil {
		return fmt.Errorf("no repository at %s", rpc.GroupSlug)
	}

	args := []string{}
	for _, ref := range rpc.HiddenRefs {
		args = append(args, "-c", "transfer.hideRefs=refs/heads/"+ref)
	}
	args = append(args, rpc.Service, "--stateless-rpc")
	if rpc.Advertise {
		args = append(args, "--advertise-refs")
	}
	args = append(args, repo)

	cmd := exec.CommandContext(ctx, s.cfg.GitBinary, args...)
	cmd.Dir = repo
	env := append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME="+os.TempDir(),
		"HP_ALLOWED_REFS="+strings.Join(rpc.AllowedRefs, " "),
		"HP_DENY_MESSAGE="+rpc.DenyMessage,
	)
	if rpc.Protocol != "" {
		env = append(env, "GIT_PROTOCOL="+rpc.Protocol)
	}
	cmd.Env = env
	if rpc.Stdin != nil {
		cmd.Stdin = rpc.Stdin
	}
	cmd.Stdout = rpc.Stdout
	stderr := &strings.Builder{}
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %v: %s", rpc.Service, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// PktLine wraps a string in git's length-prefixed packet format.
func PktLine(s string) string {
	return fmt.Sprintf("%04x%s", len(s)+4, s)
}

// PktFlush ends a packet sequence.
const PktFlush = "0000"

// AdvertisementPrefix is what a smart HTTP info/refs response starts with.
func AdvertisementPrefix(service string) string {
	return PktLine("# service=git-"+service+"\n") + PktFlush
}

// ContentType returns the media type for one leg of the conversation.
func ContentType(service string, advertise bool) string {
	if advertise {
		return "application/x-git-" + service + "-advertisement"
	}
	return "application/x-git-" + service + "-result"
}

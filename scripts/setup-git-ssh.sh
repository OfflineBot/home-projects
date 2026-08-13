#!/usr/bin/env bash
# home-projects: set up `git clone git@<host>:<group>.git` on this machine.
#
#   sudo ./scripts/setup-git-ssh.sh
#
# It creates the `git` user, hands it the repository directory, installs the
# forced command, and prints the three environment variables the server needs.
# Run it once on the host, then redeploy the stack with those variables set.
#
# Nothing here decides permissions. The wrapper asks the server for those, every
# time — see scripts/hp-git-shell.

set -euo pipefail

GIT_HOME=${GIT_HOME:-/home/git}
GIT_DIR=${GIT_DIR:-/srv/home-projects/git}
SECRET_FILE=${SECRET_FILE:-/etc/home-projects/git-ssh-secret}
WRAPPER=${WRAPPER:-/usr/local/bin/hp-git-shell}
KEYS_COMMAND=${KEYS_COMMAND:-/usr/local/bin/hp-authorized-keys}
SSHD_DROPIN=${SSHD_DROPIN:-/etc/ssh/sshd_config.d/home-projects.conf}
API=${API:-http://127.0.0.1:5000}
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

if [ "$(id -u)" -ne 0 ]; then
	echo "This has to run as root — it creates a user and writes into /usr/local/bin." >&2
	exit 1
fi

echo "==> the git user"
if id git >/dev/null 2>&1; then
	echo "    already there"
else
	useradd --create-home --home-dir "$GIT_HOME" --shell /usr/sbin/nologin git
	echo "    created, with no shell of its own"
fi

echo "==> the repository directory ($GIT_DIR)"
mkdir -p "$GIT_DIR"
# The container writes here as its own user and the git user has to read and
# write the same files, so they share a group and new files inherit it.
chgrp -R git "$GIT_DIR"
chmod -R g+rwX "$GIT_DIR"
find "$GIT_DIR" -type d -exec chmod g+s {} +
echo "    handed to the git group, setgid so new files keep it"

echo "==> the shared secret ($SECRET_FILE)"
mkdir -p "$(dirname "$SECRET_FILE")"
if [ -s "$SECRET_FILE" ]; then
	echo "    already there, keeping it"
else
	head -c 32 /dev/urandom | base64 | tr -d '\n=' > "$SECRET_FILE"
	echo "    generated"
fi
chown root:git "$SECRET_FILE"
chmod 0640 "$SECRET_FILE"
SECRET=$(cat "$SECRET_FILE")

echo "==> the two scripts"
# sshd insists that both are owned by root and writable by nobody else.
install -o root -g root -m 0755 "$HERE/hp-git-shell" "$WRAPPER"
install -o root -g root -m 0755 "$HERE/hp-authorized-keys" "$KEYS_COMMAND"
echo "    $WRAPPER and $KEYS_COMMAND"

echo "==> sshd ($SSHD_DROPIN)"
mkdir -p "$(dirname "$SSHD_DROPIN")"
cat > "$SSHD_DROPIN" <<CONF
# home-projects. The git user has no authorized_keys file: sshd asks the server
# for the keys on every connection, so removing one in the UI takes effect at
# once.
Match User git
    AuthorizedKeysCommand $KEYS_COMMAND
    AuthorizedKeysCommandUser nobody
    AuthorizedKeysFile none
    PasswordAuthentication no
    PermitTTY no
    X11Forwarding no
    AllowTcpForwarding no
CONF
chmod 0644 "$SSHD_DROPIN"
if sshd -t 2>/dev/null; then
	echo "    written, config checks out"
	systemctl reload ssh 2>/dev/null || systemctl reload sshd 2>/dev/null || \
		echo "    reload sshd yourself: systemctl reload ssh"
else
	echo "    written, but 'sshd -t' complains — look at it before reloading" >&2
	sshd -t || true
fi

cat <<EOF

Done. Two things left.

1. Give the backend these:

     GIT_SSH_HOST=git@$(hostname -f 2>/dev/null || hostname)
     GIT_SSH_SECRET=$SECRET
     GIT_SSH_WRAPPER=$WRAPPER

   mount the repositories into it:

     volumes:
       - $GIT_DIR:/srv/git

   and publish it on the host, so sshd and the wrapper can ask it:

     ports:
       - "127.0.0.1:5000:5000"      # local only — the wrapper runs on this machine

   If the backend answers somewhere else, set HP_API in the wrapper's
   environment or edit the default at the top of $WRAPPER.

2. Add your public key under Security → "Keys for git over SSH", then:

     git clone git@$(hostname -f 2>/dev/null || hostname):<group-slug>.git

A key can do nothing on this machine except talk to the server about
repositories: no shell, no forwarding, no pty. And a push over SSH goes through
the same checks as one over HTTPS — a read-only project refuses it, and a
project you may not see is not even advertised.
EOF

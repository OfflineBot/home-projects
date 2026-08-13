#!/usr/bin/env python3
"""Git over SSH, checked without a real sshd.

Nothing on the host decides anything itself: sshd asks the server which keys
exist, and the wrapper asks it what a key may see and write. This walks both
conversations — register a key, read what sshd would be handed, and ask the
authorize endpoint the questions the wrapper asks.

    python3 scripts/ssh_access.py --url http://127.0.0.1:8099 --secret <GIT_SSH_SECRET>
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from sweep import Client  # noqa: E402

ap = argparse.ArgumentParser()
ap.add_argument("--url", default="http://127.0.0.1:5000")
ap.add_argument("--user", default="offlinebot")
ap.add_argument("--password", default=os.environ.get("HP_PASSWORD", "set-HP_PASSWORD"))
ap.add_argument("--secret", default=os.environ.get("GIT_SSH_SECRET", ""))
args = ap.parse_args()

ok = True


def check(cond: bool, what: str) -> None:
    global ok
    print(("  ok  " if cond else "  FAIL") + "  " + what)
    if not cond:
        ok = False


def wrapper_asks(payload: dict, path: str = "/api/git/ssh/authorize", secret: str | None = None):
    """What scripts/hp-git-shell does, in one call."""
    req = urllib.request.Request(
        args.url + path,
        data=json.dumps(payload).encode(),
        headers={
            "Content-Type": "application/json",
            "X-Git-Ssh-Secret": args.secret if secret is None else secret,
        },
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            return r.status, json.loads(r.read() or b"{}")
    except urllib.error.HTTPError as e:
        body = e.read()
        try:
            return e.code, json.loads(body or b"{}")
        except json.JSONDecodeError:
            return e.code, {}


c = Client(args.url)
login = c.call("POST", "/api/auth/login", {"username": args.user, "password": args.password})
if not login:
    print("cannot sign in")
    raise SystemExit(1)
c.token = login["accessToken"]
c.call("POST", "/api/auth/step-up", {"password": args.password})

state = c.call("GET", "/api/ssh-keys")
if not state or not state.get("enabled"):
    print("git over SSH is not configured on this server:", (state or {}).get("note", ""))
    raise SystemExit(1)
print(f"host: {state['host']}\n")

# A group with two projects: one anybody may see, one private.
group = c.call("POST", "/api/groups", {"title": "ssh-check", "visibility": "public"})
open_project = c.call("POST", "/api/projects", {"title": "open", "groupId": group["slug"], "preset": "data"})
c.call("PATCH", f"/api/projects/{open_project['id']}", {"visibility": "public"})
secret_project = c.call("POST", "/api/projects", {"title": "hidden", "groupId": group["slug"], "preset": "data"})

# --- the key -----------------------------------------------------------------
# A run that was interrupted may have left this key behind; the check has to be
# repeatable.
for existing in (state.get("keys") or []):
    if existing["name"] in ("check", "again", "private"):
        c.call("DELETE", f"/api/ssh-keys/{existing['id']}")

public_key = (
    "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ4vQwzZ0mHnQ0FhZq0lC0mAWmMSMFRoZP0nqvGGKk0P check@home-projects"
)
key = c.call("POST", "/api/ssh-keys", {"name": "check", "key": public_key}, expect=201)
check(bool(key) and key["fingerprint"].startswith("SHA256:"), "a public key is accepted and fingerprinted")
c.call("POST", "/api/ssh-keys", {"name": "again", "key": public_key}, expect=409)
check(True, "the same key twice is refused")
c.call("POST", "/api/ssh-keys", {"name": "private", "key": "-----BEGIN OPENSSH PRIVATE KEY-----"}, expect=400)
check(True, "a private key pasted by mistake is refused")

# What sshd is handed on a connection.
def keys_sshd_sees(secret: str) -> str:
    request = urllib.request.Request(args.url + "/api/git/ssh/keys",
                                     headers={"X-Git-Ssh-Secret": secret})
    with urllib.request.urlopen(request, timeout=30) as r:
        return r.read().decode()


try:
    lines = keys_sshd_sees(args.secret)
except urllib.error.HTTPError as e:
    print(f"\nthe server refused the shared secret ({e.code}). Pass --secret, or set "
          f"GIT_SSH_SECRET to the same value the server has.")
    raise SystemExit(1)
check(key["id"] in lines, "sshd is handed the key with its id in the forced command")
check("no-pty" in lines and "no-port-forwarding" in lines,
      "and with no shell, no pty, no forwarding")
try:
    urllib.request.urlopen(urllib.request.Request(args.url + "/api/git/ssh/keys"))
    check(False, "the key list is readable without the secret")
except urllib.error.HTTPError as e:
    check(e.code == 401, "the key list needs the shared secret")

# --- what the wrapper is told ------------------------------------------------
status, answer = wrapper_asks({"keyId": key["id"], "repo": f"'{group['slug']}.git'", "service": "upload-pack"})
check(status == 200 and answer.get("allowed"), f"a fetch is allowed ({answer.get('message','')})")
check(answer.get("repoPath", "").endswith(f"{group['slug']}.git"), "and points at the group's repository")
check(answer.get("hiddenRefs") == [], "the owner's own key sees every branch")

status, answer = wrapper_asks({"keyId": key["id"], "repo": f"'{group['slug']}.git'", "service": "receive-pack"})
refs = answer.get("allowedRefs") or []
check(status == 200 and answer.get("allowed"), "a push is allowed")
check(f"refs/heads/{open_project['slug']}" in refs and f"refs/heads/{secret_project['slug']}" in refs,
      "and names exactly the branches that may be written")

# A frozen project drops out of that list — the same rule as over HTTPS.
c.call("PATCH", f"/api/projects/{open_project['id']}", {"readOnly": True})
_, answer = wrapper_asks({"keyId": key["id"], "repo": f"'{group['slug']}.git'", "service": "receive-pack"})
refs = answer.get("allowedRefs") or []
check(f"refs/heads/{open_project['slug']}" not in refs,
      "a read-only project is not among the writable branches")
check(f"refs/heads/{secret_project['slug']}" in refs, "the others still are")
c.call("PATCH", f"/api/projects/{open_project['id']}", {"readOnly": False})

# Freeze the whole group: nothing may be pushed at all.
c.call("PATCH", f"/api/groups/{group['slug']}", {"readOnly": True})
_, answer = wrapper_asks({"keyId": key["id"], "repo": f"'{group['slug']}.git'", "service": "receive-pack"})
check(not answer.get("allowed"), f"a frozen group refuses every push ({answer.get('message','')})")
c.call("PATCH", f"/api/groups/{group['slug']}", {"readOnly": False})

# --- the ways in that must not work -----------------------------------------
status, _ = wrapper_asks({"keyId": key["id"], "repo": "x.git", "service": "upload-pack"}, secret="wrong")
check(status == 401, "a wrapper with the wrong secret is turned away")

status, _ = wrapper_asks({"keyId": key["id"], "repo": "'../../etc/passwd'", "service": "upload-pack"})
check(status == 400, "a repository name that tries to leave is refused")

status, _ = wrapper_asks({"keyId": key["id"], "repo": f"'{group['slug']}.git'", "service": "shell"})
check(status == 400, "anything that is not upload-pack or receive-pack is refused")

status, answer = wrapper_asks(
    {"keyId": "11111111-1111-1111-1111-111111111111", "repo": f"'{group['slug']}.git'", "service": "upload-pack"}
)
check(status == 401, "a key that is not registered gets nothing")

# A removed key stops working immediately.
c.call("DELETE", f"/api/ssh-keys/{key['id']}")
status, _ = wrapper_asks({"keyId": key["id"], "repo": f"'{group['slug']}.git'", "service": "upload-pack"})
check(status == 401, "a removed key stops working at once")
after = keys_sshd_sees(args.secret)
check(key["id"] not in after, "and sshd is no longer handed it")

for p in (open_project, secret_project):
    c.call("DELETE", f"/api/projects/{p['id']}?confirm={p['slug']}")
c.call("DELETE", f"/api/groups/{group['slug']}?confirm={group['slug']}")

import sweep  # noqa: E402

if sweep.failures:
    ok = False
    print("\nrequests that failed:")
    for f in sweep.failures:
        print("  x " + f)

print("\nssh access:", "green" if ok else "RED")
sys.exit(0 if ok else 1)

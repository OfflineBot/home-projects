#!/usr/bin/env python3
"""Pushing with the repository's password, without an account.

A group can be set to accept its own password as a licence to write, not only
to read. This checks both halves of that: that it works when it is switched on,
and that it does not when it is off — and that read-only still wins over it.

    python3 scripts/password_push.py --url http://127.0.0.1:8099
"""

from __future__ import annotations

import argparse
import base64
import os
import subprocess
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from sweep import Client  # noqa: E402

ap = argparse.ArgumentParser()
ap.add_argument("--url", default="http://127.0.0.1:5000")
ap.add_argument("--user", default="offlinebot")
ap.add_argument("--password", default=os.environ.get("HP_PASSWORD", "set-HP_PASSWORD"))
args = ap.parse_args()

REPO_PASSWORD = "the-repository-password"
ok = True

# A shell that has the server's configuration in its environment would hand
# GIT_DIR to every git we run here.
CLEAN_ENV = {
    k: v
    for k, v in os.environ.items()
    if k not in ("GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY")
}


def check(cond: bool, what: str) -> None:
    global ok
    print(("  ok  " if cond else "  FAIL") + "  " + what)
    if not cond:
        ok = False


def git_as(password: str, *cmd, cwd=None, user="anyone"):
    """git, authenticating with nothing but a password — no account anywhere."""
    header = "Authorization: Basic " + base64.b64encode(f"{user}:{password}".encode()).decode()
    return subprocess.run(
        ["git", "-c", f"http.extraHeader={header}", *cmd],
        cwd=cwd, capture_output=True, text=True, env=CLEAN_ENV,
    )


c = Client(args.url)
login = c.call("POST", "/api/auth/login", {"username": args.user, "password": args.password})
if not login:
    print("cannot sign in")
    raise SystemExit(1)
c.token = login["accessToken"]
c.call("POST", "/api/auth/step-up", {"password": args.password})

group = c.call("POST", "/api/groups", {
    "title": "password-push", "visibility": "password", "password": REPO_PASSWORD,
})
project = c.call("POST", "/api/projects", {"title": "shared", "groupId": group["slug"]})
check(project["visibility"] == "password",
      "a project in a password-protected group takes that visibility")

url = f"{args.url}/git/{group['slug']}.git"

with tempfile.TemporaryDirectory() as tmp:
    # --- with the switch off ---------------------------------------------
    r = git_as(REPO_PASSWORD, "clone", "--quiet", "-b", project["slug"], "--single-branch", url, tmp + "/w")
    check(r.returncode == 0, f"the password alone can clone ({r.stderr.strip()[:110]})")

    w = tmp + "/w"
    subprocess.run(["git", "config", "user.email", "someone@example.com"], cwd=w, env=CLEAN_ENV)
    subprocess.run(["git", "config", "user.name", "someone"], cwd=w, env=CLEAN_ENV)
    open(w + "/from-a-stranger.txt", "w").write("no account was involved\n")
    subprocess.run(["git", "add", "-A"], cwd=w, env=CLEAN_ENV)
    subprocess.run(["git", "commit", "-qm", "with the password only"], cwd=w, env=CLEAN_ENV)

    r = git_as(REPO_PASSWORD, "push", "origin", project["slug"], cwd=w)
    check(r.returncode != 0, "and cannot push while the group has not allowed it")

    # --- with the switch on ----------------------------------------------
    c.call("PATCH", f"/api/groups/{group['slug']}", {"pushWithPassword": True})

    r = git_as(REPO_PASSWORD, "push", "origin", project["slug"], cwd=w)
    check(r.returncode == 0, f"once allowed, the password pushes ({r.stderr.strip()[:140]})")

    listing = c.call("GET", f"/api/projects/{project['id']}/files")
    names = [e["name"] for e in (listing or {}).get("entries", [])]
    check("from-a-stranger.txt" in names, "and the working tree on the server follows it")

    # A wrong password is still a wrong password.
    r = git_as("not-the-password", "ls-remote", url)
    check(r.returncode != 0, "a wrong password gets nothing")

    # Read-only outranks the switch.
    c.call("PATCH", f"/api/projects/{project['id']}", {"readOnly": True})
    open(w + "/second.txt", "w").write("no\n")
    subprocess.run(["git", "add", "-A"], cwd=w, env=CLEAN_ENV)
    subprocess.run(["git", "commit", "-qm", "second"], cwd=w, env=CLEAN_ENV)
    r = git_as(REPO_PASSWORD, "push", "origin", project["slug"], cwd=w)
    check(r.returncode != 0, "a read-only project refuses it anyway")
    check("read-only" in (r.stderr + r.stdout).lower() or "refused" in (r.stderr + r.stdout).lower(),
          "and says why")
    c.call("PATCH", f"/api/projects/{project['id']}", {"readOnly": False})

    # A private project in the same group stays out of reach of the password.
    hidden = c.call("POST", "/api/projects", {
        "title": "not shared", "groupId": group["slug"], "visibility": "private",
    })
    r = git_as(REPO_PASSWORD, "ls-remote", url)
    check(r.returncode == 0 and hidden["slug"] not in r.stdout,
          "a private project in the group is not even advertised")
    check(project["slug"] in r.stdout, "the shared one is")
    c.call("DELETE", f"/api/projects/{hidden['id']}?confirm={hidden['slug']}")

c.call("DELETE", f"/api/projects/{project['id']}?confirm={project['slug']}")
c.call("DELETE", f"/api/groups/{group['slug']}?confirm={group['slug']}&withProjects=true")

import sweep  # noqa: E402

if sweep.failures:
    ok = False
    print("\nrequests that failed:")
    for f in sweep.failures:
        print("  x " + f)

print("\npassword push:", "green" if ok else "RED")
sys.exit(0 if ok else 1)

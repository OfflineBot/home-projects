#!/usr/bin/env python3
"""One project pulls material in, another is where it gets arranged.

That is the shape the work has: a scheduler writes into one project, and the
semester folders you actually use live in another. This walks it — link, copy
and move between two projects — and checks the thing that makes a link worth
having: when the source changes, what you arranged changes with it, while a
copy stays as it was.

    python3 scripts/two_projects.py --url http://127.0.0.1:8099
"""

from __future__ import annotations

import argparse
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from sweep import Client  # noqa: E402

ap = argparse.ArgumentParser()
ap.add_argument("--url", default="http://127.0.0.1:5000")
ap.add_argument("--user", default="offlinebot")
ap.add_argument("--password", default=os.environ.get("HP_PASSWORD", "set-HP_PASSWORD"))
args = ap.parse_args()

ok = True


def check(cond: bool, what: str) -> None:
    global ok
    print(("  ok  " if cond else "  FAIL") + "  " + what)
    if not cond:
        ok = False


c = Client(args.url)
login = c.call("POST", "/api/auth/login", {"username": args.user, "password": args.password})
if not login:
    print("cannot sign in")
    raise SystemExit(1)
c.token = login["accessToken"]
c.call("POST", "/api/auth/step-up", {"password": args.password})

group = c.call("POST", "/api/groups", {"title": "two-projects"})
# The one a scheduler writes into.
source = c.call("POST", "/api/projects", {"title": "pulled", "groupId": group["slug"], "preset": "data"})
# The one you actually work in.
work = c.call("POST", "/api/projects", {"title": "semester 1", "groupId": group["slug"], "preset": "notes"})


def write(project, path, content):
    return c.call("PUT", f"/api/projects/{project['id']}/files/content", {"path": path, "content": content})


def read(project, path):
    got = c.call("GET", f"/api/projects/{project['id']}/files/content?path={path}")
    return (got or {}).get("content")


def send(path, target, target_path, mode):
    return c.call("POST", f"/api/projects/{source['id']}/files/send", {
        "path": path, "targetProject": target["id"], "targetPath": target_path, "mode": mode,
    }, expect=(200, 201))


# What a Moodle run would have left behind.
write(source, "analysis/script.md", "# Analysis\n\nversion one\n")
write(source, "analysis/exercise-1.md", "task one\n")
write(source, "databases/script.md", "# Databases\n\n")

# --- link ------------------------------------------------------------------
send("analysis", work, "semester-1/analysis", "link")
listing = c.call("GET", f"/api/projects/{work['id']}/files?path=semester-1")
names = [(e["name"], bool(e.get("linkId"))) for e in (listing or {}).get("entries", [])]
check(("analysis", True) in names, "a linked folder appears where it was put, marked as a link")

inside = c.call("GET", f"/api/projects/{work['id']}/files?path=semester-1/analysis")
check(bool(inside) and {e["name"] for e in inside["entries"]} == {"script.md", "exercise-1.md"},
      "and shows what the source holds")

check(read(work, "semester-1/analysis/script.md") == "# Analysis\n\nversion one\n",
      "a file inside it reads through to the source")

# The point of a link: the next run reaches what you arranged.
write(source, "analysis/script.md", "# Analysis\n\nversion two\n")
check(read(work, "semester-1/analysis/script.md") == "# Analysis\n\nversion two\n",
      "when the source changes, the link changes with it")

# Editing through the link acts on the source, not on a copy.
c.call("PUT", f"/api/projects/{work['id']}/files/content",
       {"path": "semester-1/analysis/notes.md", "content": "mine\n"})
check(read(source, "analysis/notes.md") == "mine\n", "and writing through it lands in the source")

# --- copy ------------------------------------------------------------------
send("databases/script.md", work, "semester-1/databases-as-it-was.md", "copy")
check(read(work, "semester-1/databases-as-it-was.md") == "# Databases\n\n", "a copy arrives")
write(source, "databases/script.md", "# Databases\n\nrewritten\n")
check(read(work, "semester-1/databases-as-it-was.md") == "# Databases\n\n",
      "and stays as it was when the source moves on")

# --- move ------------------------------------------------------------------
write(source, "loose-note.md", "belongs elsewhere\n")
send("loose-note.md", work, "semester-1/moved.md", "move")
check(read(work, "semester-1/moved.md") == "belongs elsewhere\n", "a move arrives")
gone = c.call("GET", f"/api/projects/{source['id']}/files")
check("loose-note.md" not in [e["name"] for e in gone["entries"]], "and is gone from where it came from")

# --- removing the link leaves the original alone ---------------------------
c.call("DELETE", f"/api/projects/{work['id']}/files?path=semester-1/analysis")
still = c.call("GET", f"/api/projects/{source['id']}/files?path=analysis")
check(bool(still) and len(still["entries"]) == 3, "removing the link leaves the source untouched")

for p in (work, source):
    c.call("DELETE", f"/api/projects/{p['id']}?confirm={p['slug']}")
c.call("DELETE", f"/api/groups/{group['slug']}?confirm={group['slug']}&withProjects=true")

import sweep  # noqa: E402

if sweep.failures:
    ok = False
    print("\nrequests that failed:")
    for f in sweep.failures:
        print("  x " + f)

print("\ntwo projects:", "green" if ok else "RED")
sys.exit(0 if ok else 1)

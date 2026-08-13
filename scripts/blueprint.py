#!/usr/bin/env python3
"""The arrangement as one JSON document, out and back in.

Export the shape of the server, and check that importing it says what it would
do before it does anything, that it creates what is missing, that a second run
changes nothing, and — the part that matters — that no password ever travels
inside it.

    python3 scripts/blueprint.py --url http://127.0.0.1:8099
"""

from __future__ import annotations

import argparse
import json
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

# An arrangement worth describing: a group with a puller and two semesters, a
# link between them, and a password on the group.
group = c.call("POST", "/api/groups", {
    "title": "blueprint", "visibility": "password", "password": "the-group-password",
    "color": "teal", "icon": "graduation", "pinned": True,
})
puller = c.call("POST", "/api/projects", {"title": "material", "groupId": group["slug"], "preset": "data"})
sem1 = c.call("POST", "/api/projects", {"title": "semester 1", "groupId": group["slug"], "preset": "notes"})
c.call("PUT", f"/api/projects/{puller['id']}/files/content", {"path": "analysis/script.md", "content": "# a\n"})
c.call("POST", f"/api/projects/{puller['id']}/files/send", {
    "path": "analysis", "targetProject": sem1["id"], "targetPath": "linked/analysis", "mode": "link",
}, expect=(200, 201))
c.call("POST", "/api/schedulers", {
    "projectId": puller["id"], "kind": "ics", "title": "a subscription",
    "schedule": "0 */6 * * *", "options": {"url": "https://example.com/x.ics"},
}, expect=201)

# --- export ------------------------------------------------------------------
doc = c.call("GET", f"/api/export?group={group['slug']}")
check(bool(doc) and doc["version"] >= 1, "the export names its own version")

raw = json.dumps(doc)
check("the-group-password" not in raw, "no password travels in the document")
check("password_hash" not in raw and "argon2" not in raw, "and no hash of one either")

exported = doc["groups"][0]
check(exported["slug"] == group["slug"] and exported["pinned"] is True, "the group is described")
check(exported.get("needsPassword") is True, "and says it was password-protected")
slugs = {p["slug"] for p in exported["projects"]}
check(slugs == {puller["slug"], sem1["slug"]}, "both projects are in it")
check(any(p.get("schedulers") for p in exported["projects"]), "so is the scheduler")
check(len(doc.get("links", [])) == 1, "and the link between them")

notes = next(p for p in exported["projects"] if p["slug"] == sem1["slug"])
check("markdown" in notes["capabilities"], "a project's capabilities travel with it")

# --- import into a second arrangement ----------------------------------------
# Rewrite the addresses so it lands next to the original instead of on top.
copy = json.loads(raw)
copy["groups"][0]["slug"] = "blueprint-copy"
copy["groups"][0]["title"] = "blueprint (copy)"
for p in copy["groups"][0]["projects"]:
    p["slug"] = p["slug"] + "-copy"
for l in copy.get("links", []):
    l["from"] += "-copy"
    l["to"] += "-copy"

plan = c.call("POST", "/api/import", copy)
check(bool(plan) and plan["dryRun"] is True, "an import says what it would do, and does nothing yet")
kinds = {(s["action"], s["what"]) for s in plan["steps"]}
check(("create", "group") in kinds and ("create", "project") in kinds and ("create", "link") in kinds,
      "and names the group, the projects and the link it would create")
check(any("password" in w for w in plan.get("warnings", [])),
      "it warns that the password cannot come with it")

after = c.call("GET", "/api/groups")
check("blueprint-copy" not in [g["slug"] for g in after["groups"]], "nothing was created by the dry run")

applied = c.call("POST", "/api/import?apply=true", copy)
check(bool(applied) and applied["dryRun"] is False, "applying it says so")

groups = {g["slug"] for g in c.call("GET", "/api/groups")["groups"]}
check("blueprint-copy" in groups, "the group arrived")
made = c.call("GET", "/api/groups/blueprint-copy")
check({p["slug"] for p in made["projects"]} == {puller["slug"] + "-copy", sem1["slug"] + "-copy"},
      "with both projects")
check(made["group"]["visibility"] == "private",
      "and private rather than password-protected, since no password travelled")

listing = c.call("GET", f"/api/projects/{sem1['slug']}-copy/files?path=linked")
check(bool(listing) and any(e.get("linkId") for e in listing["entries"]), "the link came too")

schedulers = c.call("GET", "/api/schedulers")
copied = [s for s in schedulers["schedulers"] if s["projectSlug"] == puller["slug"] + "-copy"]
check(len(copied) == 1, "and the scheduler")

# --- a second run changes nothing --------------------------------------------
again = c.call("POST", "/api/import?apply=true", copy)
created_again = [s for s in again["steps"] if s["action"] == "create" and s["what"] in ("project", "group")]
check(created_again == [], "running it a second time creates nothing twice")

# --- cleanup -----------------------------------------------------------------
for slug in ("blueprint-copy", group["slug"]):
    c.call("DELETE", f"/api/groups/{slug}?confirm={slug}&withProjects=true")

import sweep  # noqa: E402

if sweep.failures:
    ok = False
    print("\nrequests that failed:")
    for f in sweep.failures:
        print("  x " + f)

print("\nblueprint:", "green" if ok else "RED")
sys.exit(0 if ok else 1)

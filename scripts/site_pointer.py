#!/usr/bin/env python3
"""A site is an address, not a folder of its own.

One project holds the files. A second one says where they are reachable, which
folder is served, and whether a password stands in front of it. This walks that
arrangement — including the part that is easy to get wrong: a protected address
must hand out nothing at all until the password is right.

    python3 scripts/site_pointer.py --url http://127.0.0.1:8099
"""

from __future__ import annotations

import argparse
import os
import sys
import urllib.parse
import urllib.request

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

group = c.call("POST", "/api/groups", {"title": "site-pointer"})
files = c.call("POST", "/api/projects",
               {"title": "the material", "groupId": group["slug"], "preset": "data"})
site = c.call("POST", "/api/projects",
              {"title": "the address", "groupId": group["slug"], "preset": "address"})

c.call("PUT", f"/api/projects/{files['id']}/files/content",
       {"path": "web/index.html", "content": "<h1>from the other project</h1>"})
c.call("PUT", f"/api/projects/{files['id']}/files/content",
       {"path": "web/style.css", "content": "body { color: red }"})
c.call("PUT", f"/api/projects/{files['id']}/files/content",
       {"path": "private/secret.txt", "content": "not part of the site"})


def visit(path: str, jar: bool = True) -> tuple[int, str]:
    """A visitor with no account and no token — what the address really serves."""
    opener = c.opener if jar else urllib.request.build_opener()
    req = urllib.request.Request(args.url + path)
    try:
        with opener.open(req, timeout=30) as resp:
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")


base = f"/s/{group['slug']}/{site['slug']}/"

# --- nothing is published yet ----------------------------------------------
status, _ = visit(base, jar=False)
check(status == 404, "an address with nothing behind it says so")

# --- point it at the other project's folder --------------------------------
c.call("PATCH", f"/api/projects/{site['id']}",
       {"siteSource": files["id"], "siteRoot": "web", "visibility": "public"})

st = c.call("GET", f"/api/projects/{site['id']}/site/status")
check(bool(st) and st["sourceSlug"] == files["slug"] and st["siteRoot"] == "web",
      "the site says which project holds its files and which folder")
check(bool(st) and st["hasIndex"], "and finds the index.html over there")

status, body = visit(base, jar=False)
check(status == 200 and "from the other project" in body,
      "a visitor gets the page out of the other project")
status, body = visit(base + "style.css", jar=False)
check(status == 200 and "color: red" in body, "and everything else in that folder")

# What is not in the served folder is not part of the site. The escape is sent
# encoded, because a client library would otherwise tidy it away before the
# server ever sees it.
status, body = visit(base + "%2e%2e/%2e%2e/private/secret.txt", jar=False)
check(status in (400, 404) and "not part of the site" not in body, "and nothing above it")

# --- the folder can move without the address moving ------------------------
c.call("POST", f"/api/projects/{files['id']}/files/move", {"from": "web", "to": "public"})
c.call("PATCH", f"/api/projects/{site['id']}", {"siteRoot": "public"})
status, body = visit(base, jar=False)
check(status == 200 and "from the other project" in body,
      "the material can be rearranged; the address stays what it was")

# --- a password in front of it ---------------------------------------------
c.call("PATCH", f"/api/projects/{site['id']}", {"visibility": "password", "password": "open-sesame"})
fresh = urllib.request.build_opener()  # a stranger, no cookies
req = urllib.request.Request(args.url + base)
try:
    with fresh.open(req, timeout=30) as resp:
        status, body = resp.status, resp.read().decode()
except urllib.error.HTTPError as e:
    status, body = e.code, e.read().decode()
check(status == 401 and "<form" in body and "from the other project" not in body,
      "a protected address asks for the password and shows nothing else")

data = urllib.parse.urlencode({"password": "wrong"}).encode()
req = urllib.request.Request(args.url + base, data=data)
try:
    with fresh.open(req, timeout=30) as resp:
        status, body = resp.status, resp.read().decode()
except urllib.error.HTTPError as e:
    status, body = e.code, e.read().decode()
check(status == 401 and "does not open" in body, "a wrong password is told so")

# The right one opens it, and the cookie keeps it open.
import http.cookiejar  # noqa: E402

jar = http.cookiejar.CookieJar()
visitor = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
data = urllib.parse.urlencode({"password": "open-sesame"}).encode()
req = urllib.request.Request(args.url + base, data=data)
try:
    with visitor.open(req, timeout=30) as resp:
        status, body = resp.status, resp.read().decode()
except urllib.error.HTTPError as e:
    status, body = e.code, e.read().decode()
check(status == 200 and "from the other project" in body,
      "the right password opens it, and the page comes straight back")

req = urllib.request.Request(args.url + base + "style.css")
try:
    with visitor.open(req, timeout=30) as resp:
        status, body = resp.status, resp.read().decode()
except urllib.error.HTTPError as e:
    status, body = e.code, e.read().decode()
check(status == 200 and "color: red" in body, "and stays open for the rest of the site")

# --- what a document says about it -----------------------------------------
doc = c.call("GET", f"/api/export?group={group['slug']}")
projects = {p["slug"]: p for p in (doc or {}).get("groups", [{}])[0].get("projects", [])}
check(projects.get(site["slug"], {}).get("siteSource", "").endswith(files["slug"]),
      "the arrangement writes down which project the address serves")

# --- pointing at a pointer is refused --------------------------------------
c.call("PATCH", f"/api/projects/{files['id']}", {"siteSource": site["id"]}, expect=(400,))
check(True, "an address that serves another address is refused rather than looped")

for p in (site, files):
    c.call("DELETE", f"/api/projects/{p['id']}?confirm={p['slug']}")
c.call("DELETE", f"/api/groups/{group['slug']}?confirm={group['slug']}&withProjects=true")

import sweep  # noqa: E402

if sweep.failures:
    ok = False
    print("\nrequests that failed:")
    for f in sweep.failures:
        print("  x " + f)

print("\nsite as a pointer:", "green" if ok else "RED")
sys.exit(0 if ok else 1)

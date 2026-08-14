#!/usr/bin/env python3
"""Endpoint sweep.

Walks the whole API the way the UI does and complains at any non-2xx. An empty
list in the UI is a suspected 500 until measured otherwise — this is the
measurement, and it runs before every deploy.

    python3 scripts/sweep.py [--url http://127.0.0.1:5000] [--user …] [--password …]
"""

from __future__ import annotations

import argparse
import io
import json
import os
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile
from http.cookiejar import CookieJar
from typing import Any

ok_count = 0
failures: list[str] = []


class Client:
    def __init__(self, base: str) -> None:
        self.base = base.rstrip("/")
        self.jar = CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar))
        self.token: str | None = None

    def head(self, path: str, token_in_query: bool = False) -> tuple[int, dict[str, str]]:
        """The status and headers of a GET — for the checks that are about how
        something is served, not about what it says."""
        url = self.base + path
        headers = {}
        if self.token and not token_in_query:
            headers["Authorization"] = "Bearer " + self.token
        if token_in_query and self.token:
            url += ("&" if "?" in url else "?") + "token=" + urllib.parse.quote(self.token)
        req = urllib.request.Request(url, headers=headers, method="GET")
        try:
            with self.opener.open(req, timeout=60) as resp:
                return resp.status, {k.lower(): v for k, v in resp.headers.items()}
        except urllib.error.HTTPError as e:
            return e.code, {k.lower(): v for k, v in e.headers.items()}
        except Exception as e:
            failures.append(f"GET {path} → {e}")
            return 0, {}

    def raw_get(self, path: str) -> bytes | None:
        """The body as bytes — for a zip, which is not a JSON answer."""
        req = urllib.request.Request(self.base + path, method="GET")
        if self.token:
            req.add_header("Authorization", "Bearer " + self.token)
        try:
            with self.opener.open(req, timeout=300) as resp:
                global ok_count
                ok_count += 1
                return resp.read()
        except Exception as e:
            failures.append(f"GET {path} → {e}")
            return None

    def call(
        self,
        method: str,
        path: str,
        body: Any = None,
        expect: int | tuple[int, ...] = (200, 201),
        raw: bytes | None = None,
        content_type: str | None = None,
    ) -> Any:
        global ok_count
        url = path if path.startswith("http") else self.base + path
        data = raw
        headers = {"Accept": "application/json"}
        if body is not None:
            data = json.dumps(body).encode()
            headers["Content-Type"] = "application/json"
        if content_type:
            headers["Content-Type"] = content_type
        if self.token:
            headers["Authorization"] = "Bearer " + self.token

        req = urllib.request.Request(url, data=data, headers=headers, method=method)
        wanted = (expect,) if isinstance(expect, int) else expect
        try:
            with self.opener.open(req, timeout=60) as resp:
                payload = resp.read()
                status = resp.status
        except urllib.error.HTTPError as e:
            payload = e.read()
            status = e.code
        except Exception as e:  # connection refused, timeout …
            failures.append(f"{method} {path} → {e}")
            return None

        text = payload.decode("utf-8", "replace")
        if status not in wanted:
            failures.append(f"{method} {path} → {status} (expected {wanted}): {text[:400]}")
            return None
        ok_count += 1
        if text.startswith("{") or text.startswith("["):
            try:
                return json.loads(text)
            except json.JSONDecodeError:
                return text
        return text


def check(condition: bool, message: str) -> None:
    global ok_count
    if condition:
        ok_count += 1
    else:
        failures.append("check failed: " + message)


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--url", default="http://127.0.0.1:5000")
    ap.add_argument("--user", default="offlinebot")
    ap.add_argument("--password", default=os.environ.get("HP_PASSWORD", "set-HP_PASSWORD"))
    args = ap.parse_args()

    c = Client(args.url)
    stamp = str(int(time.time()))

    # ---------------------------------------------------------------- basics
    health = c.call("GET", "/health")
    check(bool(health and health.get("ok")), "/health says ok")

    meta = c.call("GET", "/api/meta")
    check(bool(meta and meta.get("presets")), "/api/meta lists presets")
    check(bool(meta and meta.get("capabilities")), "/api/meta lists capabilities")

    # A visitor sees the public things and no more.
    anon = Client(args.url)
    anon.call("GET", "/api/groups")
    anon.call("GET", "/api/dashboard")
    anon.call("GET", "/api/structure")

    login = c.call("POST", "/api/auth/login", {"username": args.user, "password": args.password})
    if not login:
        print("cannot sign in — the rest of the sweep would be meaningless")
        report()
        return 1
    c.token = login["accessToken"]
    c.call("GET", "/api/auth/me")
    c.call("POST", "/api/auth/step-up", {"password": args.password})
    c.call("GET", "/api/auth/sessions")
    c.call("GET", "/api/auth/audit")
    c.call("GET", "/api/tokens")

    # ---------------------------------------------------------------- groups
    group = c.call("POST", "/api/groups", {
        "title": f"Sweep {stamp}",
        "description": "created by the endpoint sweep",
        "color": "teal",
        "icon": "box",
    })
    if not group:
        report()
        return 1
    gslug = group["slug"]
    c.call("GET", f"/api/groups/{gslug}")
    c.call("PATCH", f"/api/groups/{gslug}", {"description": "changed"})
    c.call("GET", f"/api/groups/{gslug}/variables")
    c.call("GET", f"/api/groups/{gslug}/git")
    c.call("GET", f"/api/groups/{gslug}/deletion-preview")

    # -------------------------------------------------------------- projects
    presets = {p["key"]: p for p in meta["presets"]}
    made: dict[str, dict] = {}
    for key in presets:
        p = c.call("POST", "/api/projects", {
            "title": f"{key}-{stamp}",
            "groupId": gslug,
            "preset": key,
        })
        if p:
            made[key] = p
    check(len(made) == len(presets), "every preset can create a project")

    c.call("GET", "/api/projects")
    c.call("GET", "/api/projects?capability=calendar")

    for key, p in made.items():
        pid = p["id"]
        c.call("GET", f"/api/projects/{pid}")
        c.call("GET", f"/api/projects/{pid}/files")
        c.call("GET", f"/api/projects/{pid}/variables?refresh=true")
        c.call("GET", f"/api/projects/{pid}/git")
        c.call("GET", f"/api/projects/{pid}/tool")
        c.call("GET", f"/api/projects/{pid}/deletion-preview")
        # Each capability's own routes.
        for cap in p["capabilities"]:
            if cap == "calendar":
                c.call("GET", f"/api/projects/{pid}/calendar/events")
                c.call("GET", f"/api/projects/{pid}/calendar/settings")
                c.call("GET", f"/api/projects/{pid}/calendar/subscription")
                c.call("GET", f"/api/projects/{pid}/calendar/export.ics")
            elif cap == "markdown":
                c.call("GET", f"/api/projects/{pid}/markdown/notes")
                c.call("GET", f"/api/projects/{pid}/markdown/graph")
            elif cap == "grades":
                c.call("GET", f"/api/projects/{pid}/grades/")
            elif cap == "site":
                c.call("GET", f"/api/projects/{pid}/site/status")
                c.call("GET", f"/api/projects/{pid}/site/candidates")
            elif cap == "mail":
                c.call("GET", f"/api/projects/{pid}/mail/messages")
            elif cap == "feed":
                c.call("GET", f"/api/projects/{pid}/feed/entries")
            elif cap == "automation":
                c.call("GET", f"/api/projects/{pid}/automation/rules")
                c.call("GET", f"/api/projects/{pid}/automation/runs")

    # ------------------------------------------------------------- calendar
    cal = made["calendar"]["id"]
    created = c.call("POST", f"/api/projects/{cal}/calendar/events", {
        "summary": "Sweep event",
        "start": "2026-03-02T09:00:00Z",
        "end": "2026-03-02T10:30:00Z",
        "rrule": "FREQ=WEEKLY;COUNT=3",
        "alarms": [15],
    }, expect=201)
    if created:
        uid = created["uid"]
        events = c.call("GET", f"/api/projects/{cal}/calendar/events?from=2026-01-01&to=2026-12-31")
        check(bool(events) and len(events["events"]) == 3, "a weekly series with COUNT=3 shows three times")
        c.call("PATCH", f"/api/projects/{cal}/calendar/events/{uid}", {"summary": "Renamed", "start": "2026-03-02T11:00:00Z", "end": "2026-03-02T12:00:00Z", "rrule": "FREQ=WEEKLY;COUNT=3"})
        ics = c.call("GET", f"/api/projects/{cal}/calendar/export.ics")
        check(isinstance(ics, str) and "BEGIN:VCALENDAR" in ics, "the export is a real VCALENDAR")
        check(isinstance(ics, str) and "SUMMARY:Renamed" in ics, "the edit reached the file")
        # the subscription URL works without a session
        sub = c.call("GET", f"/api/projects/{cal}/calendar/subscription")
        if sub:
            anon_ics = anon.call("GET", sub["url"])
            check(isinstance(anon_ics, str) and "BEGIN:VCALENDAR" in anon_ics,
                  "the subscription URL works without an account")
        c.call("DELETE", f"/api/projects/{cal}/calendar/events/{uid}")
        after = c.call("GET", f"/api/projects/{cal}/calendar/events?from=2026-01-01&to=2026-12-31")
        check(bool(after) and after["events"] == [], "the deleted series is gone")

    # -------------------------------------------------------------- markdown
    notes = made["notes"]["id"]
    c.call("PUT", f"/api/projects/{notes}/markdown/note",
           {"path": "second.md", "content": "# Second\n\nSee [[README]] for more."})
    back = c.call("GET", f"/api/projects/{notes}/markdown/backlinks?path=README.md")
    check(bool(back) and "second.md" in back["backlinks"], "a [[wiki link]] produces a backlink")

    # ---------------------------------------------------------------- grades
    grades = made["grades"]["id"]
    put = c.call("PUT", f"/api/projects/{grades}/grades/", {"modules": [
        {"name": "Mathematik I", "grade": 2.0, "credits": 5, "status": "passed"},
        {"name": "Programmieren", "grade": 1.0, "credits": 5, "status": "passed"},
    ]})
    check(bool(put) and abs(put["average"] - 1.5) < 0.001, "the average is weighted by credits")

    # ----------------------------------------------------------------- files
    data = made["data"]["id"]
    c.call("POST", f"/api/projects/{data}/files/folder", {"path": "docs"}, expect=201)
    c.call("PUT", f"/api/projects/{data}/files/content", {"path": "docs/notes.txt", "content": "hello"})
    listing = c.call("GET", f"/api/projects/{data}/files?path=docs")
    check(bool(listing) and any(e["name"] == "notes.txt" for e in listing["entries"]), "the written file is listed")
    content = c.call("GET", f"/api/projects/{data}/files/content?path=docs/notes.txt")
    check(bool(content) and content["content"] == "hello", "the file reads back")
    c.call("POST", f"/api/projects/{data}/files/move", {"from": "docs/notes.txt", "to": "docs/renamed.txt"})
    c.call("GET", f"/api/projects/{data}/files/download?path=docs/renamed.txt")
    c.call("GET", f"/api/projects/{data}/download")

    # A download link is an <a href> and a picture is an <img src>: neither can
    # set an Authorization header, so the token has to work in the query. This
    # is what made every download in the browser answer "no project here".
    status, _ = c.head(f"/api/projects/{data}/files/download?path=docs/renamed.txt", token_in_query=True)
    check(status == 200, "a download link works with the token in the address, without a header")

    # Not everything is text, and a file that is not text still has to be
    # *lookable at* rather than only downloadable.
    png = bytes.fromhex("89504e470d0a1a0a0000000d494844520000000100000001080600000"
                        "01f15c4890000000a49444154789c6360000002000100ffff03000006000557bfabd4"
                        "0000000049454e44ae426082")
    boundary2 = "----sweepbin"
    binparts = (
        f"--{boundary2}\r\nContent-Disposition: form-data; name=\"path\"\r\n\r\nmedia\r\n"
        f"--{boundary2}\r\nContent-Disposition: form-data; name=\"files\"; filename=\"dot.png\"\r\n"
        f"Content-Type: image/png\r\n\r\n"
    ).encode() + png + f"\r\n--{boundary2}--\r\n".encode()
    c.call("POST", f"/api/projects/{data}/files/upload", raw=binparts,
           content_type=f"multipart/form-data; boundary={boundary2}")
    status, head = c.head(f"/api/projects/{data}/files/raw?path=media/dot.png")
    check(status == 200 and head.get("content-type", "").startswith("image/png")
          and head.get("content-disposition", "").startswith("inline"),
          "a picture is served to be shown, not to be saved")
    check(head.get("x-content-type-options") == "nosniff" and "sandbox" in head.get("content-security-policy", ""),
          "and it is served so it cannot become something executable")

    # The editor still refuses it, and says what to do instead.
    c.call("GET", f"/api/projects/{data}/files/content?path=media/dot.png", expect=400)

    # Anything a browser would run is handed over instead of shown.
    c.call("PUT", f"/api/projects/{data}/files/content",
           {"path": "media/page.html", "content": "<b>hi</b>"})
    _, head = c.head(f"/api/projects/{data}/files/raw?path=media/page.html")
    check(head.get("content-disposition", "").startswith("attachment"),
          "html is handed over as a download, never rendered in the origin")

    boundary = "----sweep"
    parts = (
        f"--{boundary}\r\nContent-Disposition: form-data; name=\"path\"\r\n\r\nuploads\r\n"
        f"--{boundary}\r\nContent-Disposition: form-data; name=\"paths\"\r\n\r\nsub/one.txt\r\n"
        f"--{boundary}\r\nContent-Disposition: form-data; name=\"files\"; filename=\"one.txt\"\r\n"
        f"Content-Type: text/plain\r\n\r\nuploaded\r\n--{boundary}--\r\n"
    ).encode()
    up = c.call("POST", f"/api/projects/{data}/files/upload", raw=parts,
                content_type=f"multipart/form-data; boundary={boundary}")
    check(bool(up) and up["uploaded"] == ["uploads/sub/one.txt"],
          "a folder upload keeps the relative path")

    # An invalid path must be refused, not silently cleaned.
    c.call("GET", f"/api/projects/{data}/files?path=../../etc", expect=400)

    # ------------------------------------------------------- a project from a zip
    import io as _io
    import zipfile as _zipfile

    buf = _io.BytesIO()
    with _zipfile.ZipFile(buf, "w") as zf:
        zf.writestr("wrapper/index.html", "<h1>from a zip</h1>")
        zf.writestr("wrapper/assets/style.css", "body{}")
        zf.writestr("wrapper/../escape.txt", "should be refused")
    archive = buf.getvalue()

    zip_project = c.call("POST", "/api/projects", {
        "title": f"from-zip-{stamp}", "groupId": gslug, "preset": "data",
    })
    if zip_project:
        boundary2 = "----sweepzip"
        parts2 = (
            f"--{boundary2}\r\nContent-Disposition: form-data; name=\"clear\"\r\n\r\ntrue\r\n"
            f"--{boundary2}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"start.zip\"\r\n"
            f"Content-Type: application/zip\r\n\r\n"
        ).encode() + archive + f"\r\n--{boundary2}--\r\n".encode()
        imported = c.call("POST", f"/api/projects/{zip_project['id']}/files/import-zip", raw=parts2,
                          content_type=f"multipart/form-data; boundary={boundary2}")
        check(bool(imported) and imported["files"] >= 2, "a project can be filled from a zip")
        listing_zip = c.call("GET", f"/api/projects/{zip_project['id']}/files")
        names = [e["name"] for e in (listing_zip or {}).get("entries", [])]
        check("index.html" in names, "the wrapping folder in the archive was dropped")
        check("escape.txt" not in names, "an entry that would leave the project was refused")
        made["fromzip"] = zip_project

    # ----------------------------------------------------------------- links
    link = c.call("POST", "/api/links", {
        "kind": "folder",
        "sourceProject": data,
        "sourcePath": "docs",
        "targetProject": notes,
        "targetPath": "linked-docs",
    }, expect=201)
    if link:
        linked = c.call("GET", f"/api/projects/{notes}/files")
        check(bool(linked) and any(e.get("linkId") for e in linked["entries"]),
              "the link shows up in the target's listing")
        inside = c.call("GET", f"/api/projects/{notes}/files?path=linked-docs")
        check(bool(inside) and any(e["name"] == "renamed.txt" for e in inside["entries"]),
              "the linked folder shows the source's files")
        c.call("DELETE", f"/api/links/folder/{link['id']}")
        still = c.call("GET", f"/api/projects/{data}/files?path=docs")
        check(bool(still) and any(e["name"] == "renamed.txt" for e in still["entries"]),
              "removing a link leaves the original alone")

    # ------------------------------------------------------------------ site
    website = made["website"]
    site_url = f"{args.url}/s/{gslug}/{website['slug']}/"
    page = anon.call("GET", site_url)
    check(isinstance(page, str) and "<!doctype html>" in page.lower(), "the published site is served")

    # ------------------------------------------------------------------- git
    commit = c.call("POST", f"/api/projects/{data}/git/commit", {"message": "sweep"})
    check(bool(commit) and commit.get("changed"), "a commit captures the current state")
    again = c.call("POST", f"/api/projects/{data}/git/commit", {"message": "sweep"})
    check(bool(again) and not again.get("changed"), "a second commit without changes says so")
    log = c.call("GET", f"/api/projects/{data}/git")
    check(bool(log) and len(log["commits"]) >= 1, "the history lists the commit")

    # A real clone, with the real git binary.
    with tempfile.TemporaryDirectory() as tmp:
        url = f"{args.url}/git/{gslug}.git"
        proc = subprocess.run(
            ["git", "-c", f"http.extraHeader=Authorization: Basic "
             + __import__("base64").b64encode(f"{args.user}:{args.password}".encode()).decode(),
             "clone", "--quiet", "-b", made["data"]["slug"], "--single-branch", url, tmp + "/clone"],
            capture_output=True, text=True)
        if proc.returncode != 0:
            failures.append("git clone failed: " + proc.stderr.strip()[:400])
        else:
            global ok_count
            ok_count += 1
            check(os.path.exists(tmp + "/clone/docs/renamed.txt"), "the clone contains the project's files")

    # -------------------------------------------------------------- accounts
    # Pointed at the server's own port: it accepts the connection and is not a
    # mail server, so the attempt gets as far as failing — which is what has to
    # cost the password. An address that answers nothing never gets that far
    # any more; that is the precheck, and it is checked further down.
    account = c.call("POST", "/api/accounts", {
        "kind": "mail",
        "title": f"sweep-mail-{stamp}",
        "config": {"host": "127.0.0.1", "port": 5000, "user": "nobody"},
        "secret": "this-will-fail",
    }, expect=201)
    if account:
        aid = account["id"]
        # The test must fail (there is no mail server), and that must consume
        # the credential — this is the rule the whole design turns on.
        c.call("POST", f"/api/accounts/{aid}/test", expect=(401, 400))
        after = c.call("GET", "/api/accounts")
        entry = next((a for a in after["accounts"] if a["id"] == aid), None) if after else None
        check(bool(entry) and entry["needsSecret"] and not entry["hasSecret"],
              "a failed attempt deletes the stored password")
        check(bool(entry) and entry["state"] == "needs_password",
              "the account then asks for the password again")
        # No second attempt with the old password is even possible.
        c.call("POST", f"/api/accounts/{aid}/test", expect=409)
        # An account can be changed — its address, its name — without the
        # password being touched.
        c.call("PATCH", f"/api/accounts/{aid}",
               {"title": "sweep-mail-renamed", "config": {"host": "127.0.0.2", "port": 2, "user": "someone"}})
        changed = c.call("GET", "/api/accounts")
        row = next((a for a in (changed or {}).get("accounts", []) if a["id"] == aid), None)
        check(bool(row) and row["title"] == "sweep-mail-renamed" and row["config"]["host"] == "127.0.0.2",
              "an account can be edited after it exists")
        c.call("DELETE", f"/api/accounts/{aid}")

    # --------------------------------------------------------------- filters
    # Rules that answer "where does this belong?" — a menu of their own, asked
    # by a scheduler about a course and by a project about a file.
    f = c.call("POST", "/api/filters", {
        "title": f"sweep-filter-{stamp}",
        "text": "2 -> semester2\nGrundlagen In -> semester1\nAlt -> archiv/2024\n* -> rest",
    }, expect=201)
    if f:
        fid = f["id"]
        check(len(f["rules"]) == 4, "a filter keeps its rules in order")
        # A filter with nothing in it is an empty list, never null: null is what
        # takes a page down on the other side.
        blank = c.call("POST", "/api/filters", {"title": f"sweep-blank-{stamp}", "text": ""}, expect=201)
        check(bool(blank) and blank["rules"] == [], "a filter with no rules says so with a list, not with null")
        if blank:
            listed = next((x for x in c.call("GET", "/api/filters")["filters"] if x["id"] == blank["id"]), None)
            check(bool(listed) and listed["rules"] == [], "and still does when it is listed")
            c.call("DELETE", f"/api/filters/{blank['id']}")
        tried = c.call("POST", "/api/filters/try", {
            "text": "Grundlagen In -> {%s/semester1}\n* -> {%s/rest}" % (gslug, gslug),
            "names": ["WDS125 - Grundlagen Informatik (INA)", "Etwas anderes"],
        })
        by = {r["name"]: r for r in (tried or {}).get("results", [])}
        check(by.get("WDS125 - Grundlagen Informatik (INA)", {}).get("project", "").endswith("semester1")
              and by.get("Etwas anderes", {}).get("project", "").endswith("rest"),
              "a filter can be tried before it is used")
        # And against a real project, which is what stops a folder name being
        # typed from memory.
        against = c.call("POST", "/api/filters/try",
                         {"text": "docs* ->", "projects": [f"{gslug}/{made['data']['slug']}"]})
        names = {r["name"]: r for r in (against or {}).get("results", [])}
        check("docs" in names and names["docs"]["matched"],
              "a filter can be tried against what is really in a project")
        c.call("PATCH", f"/api/filters/{fid}", {"text": "* -> rest"})
        c.call("GET", "/api/filters")
        # A line that is not a rule is refused rather than silently dropped.
        c.call("POST", "/api/filters", {"title": "broken", "text": "this is not a rule"}, expect=400)

        # A project picks up the filters it wants; the filter itself belongs to
        # nobody.
        c.call("POST", f"/api/projects/{data}/filters",
               {"filter": fid, "automatic": True, "target": f"{gslug}/{made['notes']['slug']}"}, expect=201)
        with_target = c.call("GET", f"/api/projects/{data}/filters")
        check(bool(with_target) and with_target["filters"][0]["targetProject"].endswith(made["notes"]["slug"]),
              "where a filter sends things is set on the project that uses it")
        attached = c.call("GET", f"/api/projects/{data}/filters")
        check(bool(attached) and any(x["id"] == fid and x["automatic"] for x in attached["filters"]),
              "a project adds a filter, rather than being assigned one")
        c.call("DELETE", f"/api/projects/{data}/filters/{fid}")
        after = c.call("GET", f"/api/projects/{data}/filters")
        check(bool(after) and not after["filters"], "and can drop it again")
        c.call("POST", f"/api/projects/{data}/filters", {"filter": fid}, expect=201)

        # And a project can be run through one, saying what it would do first.
        c.call("PUT", f"/api/projects/{data}/files/content",
               {"path": "loose/Übung 3.txt", "content": "x"})
        plan = c.call("POST", f"/api/projects/{data}/filter",
                      {"filter": fid, "path": "loose", "apply": False})
        check(bool(plan) and plan["applied"] is False, "applying a filter to a project asks first")

    # ------------------------------------------------------- a number out of others
    # A value can be worked out of variables anywhere on the server, which is
    # what makes the dashboard more than a list of what each project reports.
    c.call("PUT", f"/api/projects/{data}/files/content", {"path": "project.yaml", "content":
           "variables:\n  eins:\n    from: constant\n    value: 2.3\n"
           "  zwei:\n    from: constant\n    value: 1.7\n"})
    c.call("GET", f"/api/projects/{data}/variables?refresh=true")
    formula = c.call("POST", f"/api/groups/{gslug}/variables", {
        "name": "schnitt", "op": "expr",
        "expr": f"({{{gslug}/{made['data']['slug']}/eins}} + {{{gslug}/{made['data']['slug']}/zwei}}) / 2",
    }, expect=201)
    view = c.call("GET", f"/api/groups/{gslug}/variables")
    schnitt = next((d for d in (view or {}).get("derived", []) if d["name"] == "schnitt"), None)
    check(bool(schnitt) and abs((schnitt.get("value") or 0) - 2.0) < 1e-9,
          "a value can be worked out of variables from anywhere")
    # A formula that cannot be read is refused when it is written, not shown
    # broken on the dashboard.
    c.call("POST", f"/api/groups/{gslug}/variables", {"name": "kaputt", "expr": "(1 +"}, expect=400)
    if formula:
        c.call("DELETE", f"/api/groups/{gslug}/variables/{formula['id']}")

    # ------------------------------------------------------- sorting the mail
    # Categories without a model at all, and the same shape when a classifier
    # is pointed at.
    box = c.call("POST", "/api/projects",
                 {"title": f"sweep-mail-{stamp}", "groupId": gslug, "preset": "mail"}, expect=201)
    if box:
        def eml(subject, sender, body):
            return f"From: {sender}\r\nTo: me@example.org\r\nSubject: {subject}\r\n\r\n{body}\r\n"
        c.call("PUT", f"/api/projects/{box['id']}/files/content",
               {"path": "inbox/1.eml", "content": eml("Ihre Rechnung 2026", "billing@shop.example", "Betrag 12 EUR")})
        c.call("PUT", f"/api/projects/{box['id']}/files/content",
               {"path": "inbox/2.eml", "content": eml("Vorlesung faellt aus", "prof@dhbw-ravensburg.de", "Klausur")})
        c.call("PUT", f"/api/projects/{box['id']}/files/content",
               {"path": "inbox/3.eml", "content": eml("Hallo", "freund@example.org", "wie gehts")})
        sorted_out = c.call("POST", f"/api/projects/{box['id']}/mail/classify")
        check(bool(sorted_out) and sorted_out["sorted"] == 3 and sorted_out["by"] == "rules",
              "mail is sorted into categories without anything configured")
        listed = c.call("GET", f"/api/projects/{box['id']}/mail/messages")
        by_path = {m["path"]: m.get("category") for m in (listed or {}).get("messages", [])}
        check(by_path.get("inbox/1.eml") == "invoice" and by_path.get("inbox/2.eml") == "university",
              "and the plain rules get the obvious ones right")

        # A correction is a correction: sorting again leaves it alone.
        c.call("POST", f"/api/projects/{box['id']}/mail/label",
               {"path": "inbox/3.eml", "label": "personal"})
        c.call("POST", f"/api/projects/{box['id']}/mail/classify?all=true")
        again = c.call("GET", f"/api/projects/{box['id']}/mail/messages")
        fixed = next((m for m in (again or {}).get("messages", []) if m["path"] == "inbox/3.eml"), None)
        check(bool(fixed) and fixed.get("category") == "personal" and fixed.get("fixed"),
              "a label set by hand survives the next run")

        # And a classifier that cannot be reached says so rather than pretending.
        c.call("PUT", f"/api/projects/{box['id']}/mail/classifier",
               {"endpoint": "http://127.0.0.1:9/none"})
        c.call("POST", f"/api/projects/{box['id']}/mail/classify?all=true", expect=502)
        c.call("DELETE", f"/api/projects/{box['id']}?confirm={box['slug']}")

    # ------------------------------------------------------------------ grades
    # Three exams that are one subject, and semesters in the order they were
    # sat. Counted as three the average would be wrong.
    marks = c.call("POST", "/api/projects",
                   {"title": f"sweep-grades-{stamp}", "groupId": gslug, "preset": "grades"}, expect=201)
    if marks:
        sheet = {"modules": [
            {"id": "M1", "name": "Grundlagen Informatik", "credits": 8, "semester": "WiSe 2025/2026"},
            {"name": "Betriebssysteme", "grade": 2.0, "credits": 3, "partOf": "M1", "semester": "WiSe 2025/2026"},
            {"name": "Kommunikation", "grade": 3.0, "credits": 3, "partOf": "M1", "semester": "WiSe 2025/2026"},
            {"name": "Rechnerarchitektur", "grade": 1.0, "credits": 2, "partOf": "M1", "semester": "WiSe 2025/2026"},
            {"name": "Analysis", "grade": 1.7, "credits": 5, "semester": "SoSe 2025", "status": "passed"},
            {"name": "Fortgeschrittene", "grade": 2.5, "credits": 5, "semester": "SoSe 2026", "status": "passed"},
        ]}
        c.call("PUT", f"/api/projects/{marks['id']}/files/content",
               {"path": "grades.json", "content": json.dumps(sheet)})
        view = c.call("GET", f"/api/projects/{marks['id']}/grades")
        terms = [t["name"] for t in (view or {}).get("terms", [])]
        check(terms == ["SoSe 2026", "WiSe 2025/2026", "SoSe 2025"],
              "semesters come back newest first")
        folded = next((m for t in view["terms"] for m in t["modules"] if m["name"] == "Grundlagen Informatik"), None)
        check(bool(folded) and len(folded.get("parts", [])) == 3 and folded.get("computed"),
              "three exams that are one subject are one row with its parts under it")
        check(bool(folded) and abs(folded["grade"] - 2.13) < 0.01,
              "and the subject's grade is what the parts weigh out to")
        check(view["counted"] == 3, "the average counts the subject once, not the exams")
        c.call("DELETE", f"/api/projects/{marks['id']}?confirm={marks['slug']}")

    # ------------------------------------------------ what the files already are
    # A project full of .ics files with no calendar switched on is a thing
    # nobody said out loud. Now something does.
    c.call("PUT", f"/api/projects/{data}/files/content",
           {"path": "notes/thought.md", "content": "# hello"})
    found = c.call("GET", f"/api/projects/{data}/detect")
    names = {x["name"]: x for x in (found or {}).get("capabilities", [])}
    check("markdown" in names and not names["markdown"]["on"],
          "a project says which capabilities its files already call for")
    check(bool(names.get("markdown", {}).get("matched")),
          "and which files said so")
    group_wide = c.call("GET", f"/api/groups/{gslug}/detect")
    check(bool(group_wide) and any(row["projectId"] == data for row in group_wide["projects"]),
          "and a group answers for all of its projects at once")

    # ------------------------------------------- a project may defer, or lock
    # "As the group" is the default, and it stays deferred: a group that opens
    # later takes its projects with it.
    deferring = c.call("POST", "/api/projects",
                       {"title": f"sweep-defer-{stamp}", "groupId": gslug, "preset": "data"}, expect=201)
    check(bool(deferring) and deferring["visibility"] == "group",
          "a new project defers to its group")
    c.call("PATCH", f"/api/groups/{gslug}", {"visibility": "public"})
    seen = c.call("GET", f"/api/projects?group={gslug}")
    mine = next((p for p in (seen or {}).get("projects", []) if p["id"] == deferring["id"]), None)
    check(bool(mine) and mine["effectiveVisibility"] == "public",
          "and follows it when the group opens")

    # A password-protected project is listed with a lock and nothing else.
    c.call("PATCH", f"/api/projects/{deferring['id']}",
           {"visibility": "password", "password": "knock-knock"})
    anon = Client(args.url)
    listed = anon.call("GET", f"/api/projects?group={gslug}")
    locked = next((p for p in (listed or {}).get("projects", []) if p["id"] == deferring["id"]), None)
    check(bool(locked) and locked.get("locked") is True and locked["title"] == deferring["title"],
          "a locked project is still named, so there is a door to knock on")
    check(bool(locked) and not locked.get("description") and not locked.get("capabilities"),
          "and nothing behind the door is handed out with it")
    anon.call("GET", f"/api/projects/{deferring['id']}/files", expect=401)
    opened = anon.call("POST", f"/api/projects/{deferring['id']}/unlock", {"password": "knock-knock"})
    check(opened is not None, "the password opens it")
    after = anon.call("GET", f"/api/projects/{deferring['id']}/files")
    check(after is not None, "and then the contents are there")
    c.call("PATCH", f"/api/groups/{gslug}", {"visibility": "private"})
    c.call("DELETE", f"/api/projects/{deferring['id']}?confirm={deferring['slug']}")

    # --------------------------------------------- who may clone, on its own
    # A group can be public while its repository is not, and the other way
    # round: two different questions.
    c.call("PATCH", f"/api/groups/{gslug}", {"gitVisibility": "private"})
    after = c.call("GET", f"/api/groups/{gslug}")
    check(bool(after) and after["group"]["gitVisibility"] == "private",
          "a repository can be closed while the group is open")
    c.call("PATCH", f"/api/groups/{gslug}", {"gitVisibility": "nonsense"}, expect=400)
    c.call("PATCH", f"/api/groups/{gslug}", {"gitVisibility": ""})
    back = c.call("GET", f"/api/groups/{gslug}")
    check(bool(back) and not back["group"].get("gitVisibility"),
          "and can go back to following the group")

    # --------------------------------------------------------------- one-way
    # Someone hands their material over without having an account here: a link,
    # a form, one sign-in, nothing stored.
    kinds = c.call("GET", f"/api/projects/{data}/oneway")
    check(bool(kinds) and any(k["name"] == "moodle" for k in kinds["kinds"]),
          "a project can say what may be dropped off into it")
    drop = c.call("POST", f"/api/projects/{data}/oneway/link", {"kind": "moodle", "days": 1})
    check(bool(drop) and "/oneway/" in drop.get("url", ""), "and hand out a link for it")
    if drop:
        path = "/oneway/" + drop["url"].split("/oneway/")[1]
        page = c.call("GET", path)
        check(isinstance(page, str) and 'name="secret"' in page and "<form" in page,
              "the link shows a form and nothing else")
        # A made-up token opens nothing.
        c.call("GET", "/oneway/not-a-real-token", expect=404)
        # And the form refuses an empty password rather than trying one.
        c.call("POST", path, raw=b"url=https://example.com&user=x&secret=",
               content_type="application/x-www-form-urlencoded", expect=400)
    c.call("POST", f"/api/projects/{data}/oneway/link", {"kind": "nonsense"}, expect=400)

    # ---------------------------------------------------------- emptying a project
    # A project can be emptied without being deleted — with the password, and
    # with its name typed.
    empty = c.call("POST", "/api/projects", {
        "title": f"sweep-empty-{stamp}", "groupId": gslug, "preset": "data"}, expect=201)
    if empty:
        c.call("PUT", f"/api/projects/{empty['id']}/files/content",
               {"path": "a/b/c.txt", "content": "gone soon"})
        c.call("POST", f"/api/projects/{empty['id']}/clear", {"confirm": "not the name"}, expect=400)
        cleared = c.call("POST", f"/api/projects/{empty['id']}/clear", {"confirm": empty["title"]})
        check(bool(cleared) and cleared["filesNow"] == 0, "a project can be emptied without being deleted")
        still = c.call("GET", f"/api/projects/{empty['id']}")
        check(bool(still), "and it is still there afterwards")
        c.call("DELETE", f"/api/projects/{empty['id']}?confirm={empty['slug']}")

    # ------------------------------------------------------- moving, and what breaks
    impact = c.call("GET", f"/api/projects/{data}/move-impact?group=ungrouped")
    levels = {n["level"] for n in (impact or {}).get("notes", [])}
    check("changes" in levels, "a move says what changes before it is made")
    graph = c.call("GET", f"/api/groups/{gslug}/graph")
    check(bool(graph) and "nodes" in graph and "edges" in graph,
          "a group can say what depends on what inside it")

    # ------------------------------------------------------------ schedulers
    sched = c.call("POST", "/api/schedulers", {
        "projectId": cal,
        "kind": "ics",
        "title": "sweep-ics",
        "schedule": "manual",
        "options": {"url": f"{args.url}/api/projects/{cal}/calendar/export.ics"},
    }, expect=201)
    if sched:
        sid = sched["id"]
        c.call("GET", "/api/schedulers")
        # A scheduler is not carved in stone: what it is told, when it runs and
        # where it writes can all be changed afterwards.
        c.call("PATCH", f"/api/schedulers/{sid}", {
            "title": "renamed", "schedule": "0 5 * * 1", "targetPath": "elsewhere",
            "options": {"url": "https://example.com/other.ics", "name": "other"},
        })
        after = c.call("GET", "/api/schedulers")
        mine = [x for x in (after or {}).get("schedulers", []) if x["id"] == sid]
        check(bool(mine) and mine[0].get("running") is False,
              "a scheduler says whether it is running, so the button can be dark before it is pressed")
        check(bool(mine) and mine[0]["title"] == "renamed"
              and mine[0]["schedule"] == "0 5 * * 1"
              and mine[0]["targetPath"] == "elsewhere"
              and mine[0]["options"].get("name") == "other",
              "a scheduler can be edited after the fact")
        c.call("POST", f"/api/schedulers/{sid}/run", expect=(200, 502))
        runs = c.call("GET", f"/api/schedulers/{sid}/runs")
        check(bool(runs) and len(runs["runs"]) >= 1, "the run is in the log")
        c.call("GET", "/api/runs")
        c.call("DELETE", f"/api/schedulers/{sid}")
    if f:
        c.call("DELETE", f"/api/filters/{f['id']}")

    # A scheduler runs several filters, in the order they are given: one for the
    # first semester, one for the second.
    first = c.call("POST", "/api/filters", {"title": f"sweep-first-{stamp}", "text": "Grundlagen -> ./semester1"})
    second = c.call("POST", "/api/filters", {"title": f"sweep-second-{stamp}", "text": "Vertiefung -> ./semester2"})
    many = c.call("POST", "/api/schedulers", {
        "projectId": cal, "kind": "ics", "title": "sweep-many-filters", "schedule": "manual",
        "options": {"url": "https://example.com/x.ics"},
        "filterIds": [first["id"], second["id"]] if first and second else [],
    }, expect=201)

    def scheduler_now(sid):
        listed = c.call("GET", "/api/schedulers") or {}
        return next((x for x in listed.get("schedulers", []) if x["id"] == sid), None)

    if many and first and second:
        with_filters = scheduler_now(many["id"])
        check(bool(with_filters) and with_filters.get("filterIds") == [first["id"], second["id"]],
              "a scheduler is created with several filters")
        check(bool(with_filters) and len(with_filters.get("filterNames") or []) == 2,
              "and says which ones")
        c.call("PATCH", f"/api/schedulers/{many['id']}", {"filterIds": [second["id"], first["id"]]})
        swapped = scheduler_now(many["id"])
        check(bool(swapped) and swapped.get("filterIds") == [second["id"], first["id"]],
              "their order is theirs to set")
        c.call("PATCH", f"/api/schedulers/{many['id']}", {"filterIds": []})
        cleared = scheduler_now(many["id"])
        check(bool(cleared) and not cleared.get("filterIds"), "and they can all be taken off again")
        c.call("DELETE", f"/api/schedulers/{many['id']}")
    for one in (first, second):
        if one:
            c.call("DELETE", f"/api/filters/{one['id']}")

    # ------------------------------------------------------------ automation
    system = made["system"]["id"]
    c.call("PUT", f"/api/projects/{system}/automation/rules", {"rules": [{
        "name": "check-self",
        "trigger": {"type": "button"},
        "actions": [{"run": "ping", "host": "127.0.0.1", "port": int(args.url.rsplit(":", 1)[1].strip("/"))}],
    }]})
    run = c.call("POST", f"/api/projects/{system}/automation/rules/check-self/run", expect=(200, 502))
    # A rule whose name has a space in it is addressed the way every other one
    # is. "There is no rule called \"Start%20PC\"" was a real answer once.
    c.call("PUT", f"/api/projects/{system}/automation/rules", {"rules": [{
        "name": "check-self", "trigger": {"type": "button"},
        "actions": [{"run": "ping", "host": "127.0.0.1", "port": 5000, "variable": "online"}],
    }, {
        "name": "Start PC", "trigger": {"type": "button"},
        "actions": [{"run": "ping", "host": "127.0.0.1", "port": 5000, "variable": "online"}],
    }]})
    c.call("POST", f"/api/projects/{system}/automation/rules/Start%20PC/run", expect=(200, 502))
    check(bool(run) and run.get("run", {}).get("status") == "ok", "the automation rule ran")
    vars_after = c.call("GET", f"/api/projects/{system}/variables")
    check(bool(vars_after) and any(v["name"] == "online" for v in vars_after["variables"]),
          "the ping action produced a variable")

    # ------------------------------------------------------------- dashboard
    dash = c.call("GET", "/api/dashboard")
    check(bool(dash) and any(b["group"]["slug"] == gslug for b in dash["groups"]),
          "the new group is on the dashboard")
    board_now = c.call("GET", "/api/boards") or {}
    first_tab = board_now["tabs"][0]["id"] if board_now.get("tabs") else None
    if first_tab:
        card = c.call("POST", "/api/boards/cards", {
            "tabId": first_tab, "kind": "number",
            "options": {"groupId": group["id"], "variable": f"{made['grades']['slug']}.average",
                        "title": "Average"},
        }, expect=201)
        if card:
            c.call("PATCH", f"/api/boards/cards/{card['id']}", {"options": {"title": "Ø"}})
            c.call("DELETE", f"/api/boards/cards/{card['id']}")
    c.call("GET", "/api/structure")

    # ------------------------------------------------- visibility for anyone
    c.call("PATCH", f"/api/projects/{data}", {"visibility": "public"})
    check(bool(anon.call("GET", f"/api/projects/{data}")), "a public project is readable without an account")
    c.call("PATCH", f"/api/projects/{data}", {"visibility": "private"})
    anon.call("GET", f"/api/projects/{data}", expect=404)

    # read-only really is read-only
    c.call("PATCH", f"/api/projects/{data}", {"readOnly": True})
    c.call("PUT", f"/api/projects/{data}/files/content",
           {"path": "docs/blocked.txt", "content": "no"}, expect=403)
    c.call("PATCH", f"/api/projects/{data}", {"readOnly": False})

    # ------------------------------------------------------- more than one person
    # Someone asks for an account; until it is let in it opens nothing. Once in,
    # what they make is theirs and nobody else's — and the owner can shut the
    # door again.
    guest_name, guest_pw = f"guest{stamp[-6:]}", "a-long-enough-password"
    guest = Client(args.url)
    guest.call("POST", "/api/auth/register",
               {"username": guest_name, "password": guest_pw, "note": "the sweep"}, expect=201)
    guest.call("POST", "/api/auth/register", {"username": guest_name, "password": guest_pw}, expect=409)
    guest.call("POST", "/api/auth/register", {"username": guest_name + "x", "password": "short"}, expect=400)
    guest.call("POST", "/api/auth/register", {"username": "ab", "password": guest_pw}, expect=400)
    guest.call("POST", "/api/auth/login", {"username": guest_name, "password": guest_pw}, expect=403)

    people = c.call("GET", "/api/users")
    waiting = {u["username"]: u for u in (people or {}).get("users", [])}
    check(guest_name in waiting, "a new account is listed for the owner")
    check(waiting.get(guest_name, {}).get("note") == "the sweep", "with what they wrote about themselves")
    check(waiting.get(guest_name, {}).get("approved") is False, "and marked as waiting")
    guest_id = waiting.get(guest_name, {}).get("id")

    if guest_id:
        c.call("POST", f"/api/users/{guest_id}/approve", {})
        signed_in = guest.call("POST", "/api/auth/login", {"username": guest_name, "password": guest_pw})
        check(bool(signed_in), "after being let in the account opens")
        if signed_in:
            guest.token = signed_in["accessToken"]
            check(signed_in.get("isOwner") is not True, "and it is not the owner")
            own = guest.call("POST", "/api/groups",
                             {"title": f"Guest {stamp}", "visibility": "private"}, expect=201)
            mine = guest.call("GET", "/api/groups") or {}
            titles = [g["title"] for g in mine.get("groups", [])]
            check(f"Guest {stamp}" in titles, "they see what they made")
            check(f"Sweep {stamp}" not in titles, "and not what the owner made")
            everything = c.call("GET", "/api/groups") or {}
            check(any(g["title"] == f"Guest {stamp}" for g in everything.get("groups", [])),
                  "the owner sees everything")
            guest.call("GET", "/api/users", expect=403)
            if own:
                c.call("DELETE", f"/api/groups/{own['slug']}?confirm={own['slug']}")
        c.call("POST", f"/api/users/{guest_id}/approve?undo=true", {})
        guest.call("POST", "/api/auth/login", {"username": guest_name, "password": guest_pw}, expect=403)
        c.call("DELETE", f"/api/users/{guest_id}")
        guest.call("POST", "/api/auth/login", {"username": guest_name, "password": guest_pw}, expect=401)

    # ------------------------------------------------- a mailbox that is not there
    # The password is single-use, so a wrong server must be caught before it is
    # spent — that is what the precheck is for.
    dead = c.call("POST", "/api/accounts", {
        "kind": "mail", "title": f"nowhere {stamp}",
        "config": {"protocol": "imap", "host": "127.0.0.1", "port": 9, "user": "nobody"},
        "secret": "not-a-real-password",
    }, expect=201)
    if dead:
        c.call("POST", f"/api/accounts/{dead['id']}/test", expect=400)
        listed = c.call("GET", "/api/accounts") or {}
        row = next((a for a in listed.get("accounts", []) if a["id"] == dead["id"]), None)
        check(bool(row) and row["hasSecret"], "an unreachable server does not cost the password")
        check(bool(row) and row["state"] != "needs_password", "and the account is still ready")
        c.call("DELETE", f"/api/accounts/{dead['id']}")

    # ------------------------------------------------------------------ boards
    # A board is a page somebody arranged: tabs, cards, and who may see each
    # card. The front page and a group's page are the same thing twice.
    home = c.call("GET", "/api/boards")
    check(bool(home) and home.get("scope") == "home", "everybody has a board of their own")
    check(bool(home) and len(home.get("tabs", [])) >= 1, "and it starts with one tab")
    kinds = c.call("GET", "/api/boards/cards") or {}
    names = [k["name"] for k in kinds.get("cards", [])]
    check("text" in names and "project" in names, "the core's cards are offered")
    check("machine" in names and "terminal" in names and "agenda" in names,
          "and every capability's, without the board knowing what they are")

    board = c.call("GET", f"/api/boards?group={gslug}")
    check(bool(board) and board.get("scope") == "group", "a group has a board too")
    tab = board["tabs"][0]["id"] if board and board.get("tabs") else None
    if tab:
        note = c.call("POST", "/api/boards/cards", {
            "tabId": tab, "kind": "text", "options": {"text": "a note"}, "x": 0, "y": 0, "w": 4, "h": 2,
        }, expect=201)
        number = c.call("POST", "/api/boards/cards", {
            "tabId": tab, "kind": "number",
            "options": {"variable": f"data-{stamp}.eins", "groupId": group["id"]},
            "x": 4, "y": 0, "w": 2, "h": 2,
        }, expect=201)
        c.call("POST", "/api/boards/cards", {"tabId": tab, "kind": "nonsense"}, expect=400)
        c.call("POST", "/api/boards/cards", {"kind": "text"}, expect=400)

        # One drag moves several cards; one request saves the lot.
        if note and number:
            c.call("PUT", f"/api/boards/{board['id']}/layout", {"cards": [
                {"id": note["id"], "x": 0, "y": 2, "w": 6, "h": 3},
                {"id": number["id"], "x": 6, "y": 2, "w": 3, "h": 1},
            ]})
            moved = c.call("GET", f"/api/boards?group={gslug}") or {}
            placed = {x["id"]: x for x in moved["tabs"][0]["cards"]}
            check(placed.get(note["id"], {}).get("w") == 6, "an arrangement is saved as one")
            check(placed.get(number["id"], {}).get("y") == 2, "for every card that moved")

        # Another tab, and the cards that sit on it.
        second = c.call("POST", f"/api/boards/{board['id']}/tabs", {"title": "Terminals"}, expect=201)
        if second:
            with_tabs = c.call("GET", f"/api/boards?group={gslug}") or {}
            check(len(with_tabs.get("tabs", [])) == 2, "a board takes more than one tab")
            c.call("PATCH", f"/api/boards/tabs/{second['id']}", {"title": "Machines"})
            renamed = c.call("GET", f"/api/boards?group={gslug}") or {}
            check(any(t["title"] == "Machines" for t in renamed.get("tabs", [])), "a tab can be renamed")
            c.call("DELETE", f"/api/boards/tabs/{second['id']}")

        # Who may see a card: what it says, and what it shows, whichever is
        # stricter. This runs on the home board — a visitor cannot see a
        # private group at all, which is a different rule and is checked
        # elsewhere.
        home_tab = home["tabs"][0]["id"] if home and home.get("tabs") else None
        number = c.call("POST", "/api/boards/cards", {
            "tabId": home_tab, "kind": "number",
            "options": {"variable": f"data-{stamp}.eins", "groupId": group["id"]},
        }, expect=201) if home_tab else None
        note = c.call("POST", "/api/boards/cards", {
            "tabId": home_tab, "kind": "text", "options": {"text": "a note"},
        }, expect=201) if home_tab else None
        if number:
            c.call("PATCH", f"/api/boards/cards/{number['id']}", {"visibility": "public"})
            c.call("PATCH", f"/api/boards/cards/{number['id']}", {"visibility": "sideways"}, expect=400)
            c.call("PATCH", f"/api/projects/{data}", {"visibility": "private"})
            seen = anon.call("GET", "/api/boards") or {}
            cards = [x["id"] for t in seen.get("tabs", []) for x in t.get("cards", [])]
            check(number["id"] not in cards, "a public card on a private project stays private")
            c.call("PATCH", f"/api/projects/{data}", {"visibility": "public"})
            seen = anon.call("GET", "/api/boards") or {}
            cards = [x["id"] for t in seen.get("tabs", []) for x in t.get("cards", [])]
            check(number["id"] in cards, "and is seen once that project is public")
            c.call("PATCH", f"/api/projects/{data}", {"visibility": "private"})
        if note:
            # A card that shows nothing of anybody's is as open as it says.
            c.call("PATCH", f"/api/boards/cards/{note['id']}", {"visibility": "public"})
            seen = anon.call("GET", "/api/boards") or {}
            cards = [x["id"] for t in seen.get("tabs", []) for x in t.get("cards", [])]
            check(note["id"] in cards, "a piece of text can simply be public")
            c.call("PATCH", f"/api/boards/cards/{note['id']}", {"visibility": "private"})
            gone = anon.call("GET", "/api/boards") or {}
            cards = [x["id"] for t in gone.get("tabs", []) for x in t.get("cards", [])]
            check(note["id"] not in cards, "and private means private")
            c.call("DELETE", f"/api/boards/cards/{note['id']}")
        if number:
            c.call("DELETE", f"/api/boards/cards/{number['id']}")
        anon.call("POST", "/api/boards/cards", {"tabId": tab, "kind": "text"}, expect=401)
        anon.call("GET", f"/api/boards?group={gslug}", expect=404)

    # What a project has to offer a board: pick the project, then the thing
    # itself — not a kind of card and a form.
    offers = c.call("GET", f"/api/projects/{system}/offers") or {}
    kinds_offered = [o["card"] for o in offers.get("offers", [])]
    check("project" in kinds_offered, "a project offers itself")
    check("rule" in kinds_offered, "and the rules it already has, as buttons")
    check(any(o["card"] in ("number", "status") for o in offers.get("offers", [])),
          "and every number it reports")
    for o in offers.get("offers", []):
        if o["card"] == "rule":
            check(o["options"].get("rule") and o["options"].get("projectId"),
                  "an offer arrives ready to place, with its options filled in")
            break
    anon.call("GET", f"/api/projects/{system}/offers", expect=404)

    # A group's board can have an address of its own.
    c.call("PATCH", f"/api/groups/{gslug}", {"boardHost": f"{gslug}.example.com"})
    c.call("PATCH", f"/api/groups/{gslug}", {"boardHost": "nonsense"}, expect=400)
    here = c.call("GET", "/api/here")
    check(bool(here) and here.get("kind") == "app", "an ordinary address is the app")
    # The Host header decides, which is what a browser sends.
    status, headers = c.head("/api/here")
    check(status == 200, "and the question can be asked without an account")
    c.call("PATCH", f"/api/groups/{gslug}", {"boardHost": ""})

    # Projects can be tidied into folders inside their group.
    c.call("PATCH", f"/api/projects/{data}", {"folder": "Semester 1"})
    filed = c.call("GET", f"/api/projects?group={gslug}") or {}
    check(any(p["id"] == data and p.get("folder") == "Semester 1" for p in filed.get("projects", [])),
          "a project can be put in a folder")
    c.call("PATCH", f"/api/projects/{data}", {"folder": ""})

    # A tab is a grid or a page, and a card can be dressed.
    if tab:
        c.call("PATCH", f"/api/boards/tabs/{tab}", {"layout": "flow"})
        flowing = c.call("GET", f"/api/boards?group={gslug}") or {}
        check(flowing["tabs"][0].get("layout") == "flow", "a tab can be a page instead of a grid")
        c.call("PATCH", f"/api/boards/tabs/{tab}", {"layout": "diagonal"}, expect=400)
        c.call("PATCH", f"/api/boards/tabs/{tab}", {"layout": "grid"})
        dressed = c.call("POST", "/api/boards/cards", {
            "tabId": tab, "kind": "text", "options": {"text": "look"},
            "style": {"color": "mauve", "background": "tinted", "align": "center"},
        }, expect=201)
        if dressed:
            back = c.call("GET", f"/api/boards?group={gslug}") or {}
            mine_card = next((x for t in back["tabs"] for x in t["cards"] if x["id"] == dressed["id"]), None)
            check(bool(mine_card) and mine_card["style"].get("color") == "mauve",
                  "a card keeps the look it was given")
            c.call("DELETE", f"/api/boards/cards/{dressed['id']}")

    # Several addresses at once, and the lights.
    c.call("PUT", f"/api/projects/{system}/automation/rules", {"rules": [{
        "name": "many", "trigger": {"type": "button"},
        "actions": [{"run": "http", "url": "http://127.0.0.1:5000/health\nhttp://127.0.0.1:5000/api/meta"}],
    }, {
        "name": "lights", "trigger": {"type": "button"},
        "actions": [{"run": "wled", "host": "127.0.0.1:9", "power": "on", "color": "#ff8800"}],
    }]})
    many = c.call("POST", f"/api/projects/{system}/automation/rules/many/run", expect=(200, 502))
    check(bool(many) and (many.get("run", {}).get("status") == "ok"),
          "one rule can call several addresses at once")
    lights = c.call("POST", f"/api/projects/{system}/automation/rules/lights/run", expect=(200, 502))
    # Nothing is listening on port 9, so this has to fail — and say which lamp.
    check(bool(lights) and "127.0.0.1:9" in json.dumps(lights), "a WLED that is not there says which one")

    # A group is a project in its own right: a README on the main branch, and a
    # history you can read.
    readme = c.call("GET", f"/api/groups/{gslug}/readme")
    check(readme is not None and "text" in readme, "a group has a README")
    c.call("PUT", f"/api/groups/{gslug}/readme", {"text": "# Sweep\n\nWritten by the sweep."})
    written = c.call("GET", f"/api/groups/{gslug}/readme") or {}
    check("Written by the sweep" in written.get("text", ""), "and it can be written")
    git = c.call("GET", f"/api/groups/{gslug}/git") or {}
    check(any("README" in x.get("message", "") or x.get("message") for x in git.get("commits", [])),
          "the write is a commit on main")
    if git.get("commits"):
        one = git["commits"][0]
        patch = c.call("GET", f"/api/groups/{gslug}/git/commit/{one['short']}") or {}
        check("diff --git" in patch.get("patch", "") or "README" in patch.get("patch", ""),
              "and one commit can be read as its patch")
    c.call("GET", f"/api/groups/{gslug}/git/commit/nonsense", expect=404)
    # Every project is a branch, and its history is the same question.
    branchy = c.call("GET", f"/api/groups/{gslug}/git?branch={made['data']['slug']}") or {}
    check(branchy.get("branch") == made["data"]["slug"], "a project's own history is asked for by branch")

    # ------------------------------------------------------------------ links
    # A place to put an address so it can be found again — and a public one is a
    # list two people keep together.
    keep = c.call("POST", "/api/projects", {
        "title": f"Sweep links {stamp}", "groupId": gslug, "preset": "links",
    }, expect=201)
    if keep:
        empty = c.call("GET", f"/api/projects/{keep['id']}/links")
        check(bool(empty) and empty.get("links") == [], "a links project starts empty")
        one = c.call("POST", f"/api/projects/{keep['id']}/links",
                     {"url": "example.com/kettle", "note": "the one with the timer", "tags": ["buy"]},
                     expect=201)
        check(bool(one) and one["url"].startswith("https://"), "an address without a scheme gets one")
        check(bool(one) and one["title"] == "example.com", "and a name when none was given")
        c.call("POST", f"/api/projects/{keep['id']}/links", {"url": ""}, expect=400)
        c.call("POST", f"/api/projects/{keep['id']}/links", {"url": "not an address"}, expect=400)
        if one:
            c.call("PATCH", f"/api/projects/{keep['id']}/links/{one['id']}", {"done": True, "title": "Kettle"})
            after = c.call("GET", f"/api/projects/{keep['id']}/links") or {}
            saved = after.get("links", [{}])[0]
            check(saved.get("done") is True and saved.get("title") == "Kettle", "a link can be changed")
            # It is a file like everything else, so it is in the project.
            listing = c.call("GET", f"/api/projects/{keep['id']}/files") or {}
            check(any(e["name"] == "links.json" for e in listing.get("entries", [])),
                  "and it lies in the project as links.json")
            c.call("DELETE", f"/api/projects/{keep['id']}/links/{one['id']}")
            gone = c.call("GET", f"/api/projects/{keep['id']}/links") or {}
            check(gone.get("links") == [], "and dropped again")
            c.call("DELETE", f"/api/projects/{keep['id']}/links/{one['id']}", expect=404)

        # A public project with anonWrite is the shared list.
        c.call("PATCH", f"/api/projects/{keep['id']}", {"visibility": "public", "anonWrite": True})
        anon.call("POST", f"/api/projects/{keep['id']}/links", {"url": "https://example.com/from-a-visitor"},
                  expect=201)
        c.call("PATCH", f"/api/projects/{keep['id']}", {"anonWrite": False})
        anon.call("POST", f"/api/projects/{keep['id']}/links", {"url": "https://example.com/no"}, expect=403)
        c.call("DELETE", f"/api/projects/{keep['id']}?confirm={keep['slug']}")

    # ------------------------------------------------- one project gathering others
    # A main calendar is not a kind of calendar: it is a project that gathers
    # the others, and a view that draws them beside its own.
    hero = c.call("POST", "/api/projects", {
        "title": f"Sweep main calendar {stamp}", "groupId": gslug, "preset": "calendar",
    }, expect=201)
    if hero:
        none_yet = c.call("GET", f"/api/projects/{hero['id']}/sources")
        check(bool(none_yet) and none_yet.get("sources") == [], "a project gathers nothing to begin with")
        c.call("PUT", f"/api/projects/{hero['id']}/sources", {"sources": [cal]})
        gathered = c.call("GET", f"/api/projects/{hero['id']}/sources") or {}
        check([p["id"] for p in gathered.get("sources", [])] == [cal], "it can be told to gather another")
        # The entries of both come back from one request, which is what the
        # view asks for.
        both = c.call("GET",
                      f"/api/capabilities/calendar/events?from=2025-01-01&to=2026-12-31&projects={hero['id']},{cal}")
        check(both is not None and "sources" in both, "and both are asked for at once")
        c.call("PUT", f"/api/projects/{hero['id']}/sources", {"sources": [hero["id"]]}, expect=400)
        c.call("PUT", f"/api/projects/{hero['id']}/sources", {"sources": ["not-an-id"]}, expect=400)
        c.call("PUT", f"/api/projects/{hero['id']}/sources", {"sources": []})
        emptied = c.call("GET", f"/api/projects/{hero['id']}/sources") or {}
        check(emptied.get("sources") == [], "and it can gather nothing again")
        c.call("DELETE", f"/api/projects/{hero['id']}?confirm={hero['slug']}")

    # ------------------------------------------------- another machine, over ssh
    # Wake, power, and the tmux sessions. The connection itself needs a machine
    # to connect to, so the parts that need one run only when HP_SSH is set:
    #   HP_SSH=host:port:user:password python3 scripts/sweep.py
    pcs = c.call("POST", "/api/projects", {
        "title": f"Sweep machines {stamp}", "groupId": gslug, "preset": "machines",
    }, expect=201)
    if pcs:
        empty = c.call("GET", f"/api/projects/{pcs['id']}/machines")
        check(bool(empty) and empty.get("machines") == [], "a machines project starts with none")
        ssh_env = os.environ.get("HP_SSH", "")
        host, port, user, password = (ssh_env.split(":") + ["", "", "", ""])[:4] if ssh_env else ("", "", "", "")
        c.call("PUT", f"/api/projects/{pcs['id']}/machines", {"machines": [{
            "name": "probe", "host": host or "127.0.0.1", "port": int(port or 22),
            "user": user or "nobody", "mac": "aa:bb:cc:dd:ee:ff", "note": "the sweep's",
        }]})
        listed = c.call("GET", f"/api/projects/{pcs['id']}/machines") or {}
        check(len(listed.get("machines", [])) == 1, "a machine can be written down")
        check("up" in (listed.get("machines") or [{}])[0], "and it says whether it is up")
        # A magic packet needs no credential and answers nothing — but it must
        # not fail, and a machine without a MAC must say so rather than pretend.
        c.call("POST", f"/api/projects/{pcs['id']}/machines/probe/wake", {})
        c.call("POST", f"/api/projects/{pcs['id']}/machines/nothing-here/wake", {}, expect=404)
        c.call("PUT", f"/api/projects/{pcs['id']}/machines", {"machines": [{"name": "no mac", "host": "127.0.0.1"}]})
        c.call("POST", f"/api/projects/{pcs['id']}/machines/no%20mac/wake", {}, expect=400)
        c.call("PUT", f"/api/projects/{pcs['id']}/machines", {"machines": [{"name": "", "host": "x"}]}, expect=400)

        # A machine can be an account: added once, and never asked again.
        machine_account = c.call("POST", "/api/accounts", {
            "kind": "machine", "title": f"sweep-machine-{stamp}",
            "config": {"user": user or "nobody", "host": host or "127.0.0.1", "port": port or "22"},
            "secret": "whatever-it-is",
        }, expect=201)
        if machine_account:
            # This kind does not lock, so a failed attempt says so and leaves
            # the stored secret alone — losing it on every typo would be the
            # worse failure.
            c.call("POST", f"/api/accounts/{machine_account['id']}/test", expect=(200, 502, 400))
            kept = next((a for a in (c.call("GET", "/api/accounts") or {}).get("accounts", [])
                         if a["id"] == machine_account["id"]), None)
            check(bool(kept) and kept["hasSecret"],
                  "a machine account keeps its password after a failed attempt")

        if ssh_env:
            c.call("PUT", f"/api/projects/{pcs['id']}/machines", {"machines": [{
                "name": "probe", "host": host, "port": int(port), "user": user,
            }]})
            base = f"/api/projects/{pcs['id']}/machines/probe"
            c.call("POST", f"{base}/tmux", {"password": "not-the-password"}, expect=502)
            sessions = c.call("POST", f"{base}/tmux", {"password": password})
            check(sessions is not None and "sessions" in sessions, "the tmux sessions can be listed")
            name = f"sweep-{stamp}"
            c.call("POST", f"{base}/tmux-new", {"password": password, "session": name})
            after = c.call("POST", f"{base}/tmux", {"password": password}) or {}
            check(any(x["name"] == name for x in after.get("sessions", [])), "a session can be started")
            typed = c.call("POST", f"{base}/tmux/{name}/keys",
                           {"password": password, "keys": "echo swept-through", "enter": True})
            check(bool(typed) and "swept-through" in typed.get("screen", ""),
                  "something typed into it lands on its screen")
            seen = c.call("POST", f"{base}/tmux/{name}", {"password": password, "lines": 20})
            check(bool(seen) and "swept-through" in seen.get("screen", ""), "and is there when looked at again")
            # The same, through an account: no password in the request at all.
            if machine_account:
                c.call("POST", f"/api/accounts/{machine_account['id']}/secret", {"secret": password})
                c.call("PUT", f"/api/projects/{pcs['id']}/machines", {"machines": [{
                    "name": "probe", "host": host, "port": int(port), "user": user,
                    "account": machine_account["title"],
                }]})
                by_account = c.call("POST", f"{base}/tmux", {})
                check(by_account is not None and "sessions" in by_account,
                      "a machine with an account needs no password typed in")
            c.call("POST", f"{base}/tmux/{name}/kill", {"password": password})
            gone = c.call("POST", f"{base}/tmux", {"password": password}) or {}
            check(not any(x["name"] == name for x in gone.get("sessions", [])), "and it can be closed again")
        else:
            print("  (no HP_SSH — the parts that need a real machine were not measured)")
        if machine_account:
            c.call("DELETE", f"/api/accounts/{machine_account['id']}")
        c.call("DELETE", f"/api/projects/{pcs['id']}?confirm={pcs['slug']}")

    # ------------------------------------------------- packed up and carried over
    # The whole point: a group set up here, taken somewhere else, whole. It is
    # proved the only way that proves anything — export it, delete it, bring it
    # back out of the bundle and look for the files again.
    bundle = c.raw_get(f"/api/export/bundle?group={gslug}")
    check(bool(bundle) and bundle[:2] == b"PK", "a group exports as a bundle")
    if bundle:
        archive = zipfile.ZipFile(io.BytesIO(bundle))
        names = archive.namelist()
        check("blueprint.json" in names, "the bundle holds the arrangement")
        inside = json.loads(archive.read("blueprint.json"))
        check(any(g["slug"] == gslug for g in inside["groups"]), "and the group is in it")
        packed = [n for n in names if n.startswith("files/")]
        check(len(packed) > 0, "and the files are in it")
        check(not any("secret" in json.dumps(a).lower() for a in inside.get("accounts", [])),
              "and no password travels with it")

        # Gone, and then back.
        c.call("DELETE", f"/api/groups/{gslug}?confirm={gslug}")
        c.call("GET", f"/api/groups/{gslug}", expect=404)

        boundary = "----hpbundle"
        parts = (f"--{boundary}\r\n".encode()
                 + b'Content-Disposition: form-data; name="file"; filename="bundle.zip"\r\n'
                 + b"Content-Type: application/zip\r\n\r\n" + bundle
                 + f"\r\n--{boundary}--\r\n".encode())
        plan = c.call("POST", "/api/import/bundle", raw=parts,
                      content_type=f"multipart/form-data; boundary={boundary}")
        check(bool(plan) and any(s["what"] == "group" for s in plan.get("steps", [])),
              "the bundle says what it would do")
        check(bool(plan) and plan.get("dryRun") is True, "and does none of it yet")
        done = c.call("POST", "/api/import/bundle?apply=true", raw=parts,
                      content_type=f"multipart/form-data; boundary={boundary}")
        check(bool(done) and not done.get("dryRun"), "and then does it")

        back = c.call("GET", f"/api/groups/{gslug}")
        check(bool(back), "the group is here again")
        again = c.call("GET", f"/api/projects?group={gslug}") or {}
        slugs = [p["slug"] for p in again.get("projects", [])]
        check(len(slugs) > 0, f"with its projects ({len(slugs)})")
        # One file that was written during this sweep, back where it belongs.
        restored = next((p for p in again.get("projects", []) if p["slug"] == made["data"]["slug"]), None)
        if restored:
            listing = c.call("GET", f"/api/projects/{restored['id']}/files?path=docs") or {}
            names_back = [e["name"] for e in listing.get("entries", [])]
            check("renamed.txt" in names_back, "and the files that were in them")
            made["data"] = restored
        for key, project in list(made.items()):
            found = next((p for p in again.get("projects", []) if p["slug"] == project["slug"]), None)
            if found:
                made[key] = found

    # ---------------------------------------------------------------- delete
    for key, p in made.items():
        c.call("DELETE", f"/api/projects/{p['id']}?confirm={p['slug']}")
    c.call("DELETE", f"/api/groups/{gslug}?confirm={gslug}")
    c.call("POST", "/api/auth/logout")

    report()
    return 1 if failures else 0


def report() -> None:
    print(f"\n{ok_count} checks passed")
    if failures:
        print(f"{len(failures)} FAILED:\n")
        for f in failures:
            print("  ✗ " + f)
    else:
        print("sweep green")


if __name__ == "__main__":
    sys.exit(main())

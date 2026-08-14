#!/usr/bin/env python3
"""Endpoint sweep.

Walks the whole API the way the UI does and complains at any non-2xx. An empty
list in the UI is a suspected 500 until measured otherwise — this is the
measurement, and it runs before every deploy.

    python3 scripts/sweep.py [--url http://127.0.0.1:5000] [--user …] [--password …]
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
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
    account = c.call("POST", "/api/accounts", {
        "kind": "mail",
        "title": f"sweep-mail-{stamp}",
        "config": {"host": "127.0.0.1", "port": 1, "user": "nobody"},
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

    # ------------------------------------------------------------ automation
    system = made["system"]["id"]
    c.call("PUT", f"/api/projects/{system}/automation/rules", {"rules": [{
        "name": "check-self",
        "trigger": {"type": "button"},
        "actions": [{"run": "ping", "host": "127.0.0.1", "port": int(args.url.rsplit(":", 1)[1].strip("/"))}],
    }]})
    run = c.call("POST", f"/api/projects/{system}/automation/rules/check-self/run", expect=(200, 502))
    check(bool(run) and run.get("run", {}).get("status") == "ok", "the automation rule ran")
    vars_after = c.call("GET", f"/api/projects/{system}/variables")
    check(bool(vars_after) and any(v["name"] == "online" for v in vars_after["variables"]),
          "the ping action produced a variable")

    # ------------------------------------------------------------- dashboard
    dash = c.call("GET", "/api/dashboard")
    check(bool(dash) and any(b["group"]["slug"] == gslug for b in dash["groups"]),
          "the new group is on the dashboard")
    tile = c.call("POST", "/api/dashboard/tiles", {
        "groupId": group["id"], "variable": f"{made['grades']['slug']}.average",
        "title": "Average", "kind": "number", "w": 1, "h": 1,
    }, expect=201)
    if tile:
        c.call("PATCH", f"/api/dashboard/tiles/{tile['id']}", {"title": "Ø", "w": 2})
        c.call("DELETE", f"/api/dashboard/tiles/{tile['id']}")
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

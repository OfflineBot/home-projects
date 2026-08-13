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
            import os
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
        c.call("DELETE", f"/api/accounts/{aid}")

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
        c.call("POST", f"/api/schedulers/{sid}/run", expect=(200, 502))
        runs = c.call("GET", f"/api/schedulers/{sid}/runs")
        check(bool(runs) and len(runs["runs"]) >= 1, "the run is in the log")
        c.call("GET", "/api/runs")
        c.call("DELETE", f"/api/schedulers/{sid}")

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

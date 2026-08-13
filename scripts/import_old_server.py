#!/usr/bin/env python3
"""Bring the old home_server's contents into home-projects.

It reads the old database and its MinIO bucket, and writes everything through
the new server's ordinary API — never into its schema. That is the point: an
import that goes around the API is an import that can create states the API
would have refused.

It says what it would do and does nothing until told to:

    python3 scripts/import_old_server.py --url http://192.168.178.54:8091
    python3 scripts/import_old_server.py --url … --apply

Run it where docker can reach the old stack. Nothing is deleted, on either
side: the old server keeps everything it has.

What it carries over, and what it decides on the way:

  groups        one for one.
  projects      the old model let a project sit in several groups; this one
                does not. A project lands in its first group, and the others
                are reported so you can decide whether a link belongs there.
  capabilities  store→(files, always there), edit→markdown, events→calendar,
                host→site. `git` and `export` are not capabilities any more:
                every project has a branch and a download.
  files         the tree, from MinIO, as files.
  calendar      the timetable, the DHBW entries and the private ones become
                real events in a calendar project.
  grades        the Dualis rows become grades.json.
  sites         index_path becomes the project's published folder.
  whiteboards   their data lands as files, since that is what they are.

  mail caches   left behind on purpose. They are caches; a mail scheduler
                fetches them again.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import urllib.error
import urllib.request
from collections import defaultdict

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from sweep import Client  # noqa: E402

ap = argparse.ArgumentParser()
ap.add_argument("--url", default="http://127.0.0.1:5000", help="the new server")
ap.add_argument("--user", default="offlinebot")
ap.add_argument("--password", default=os.environ.get("HP_PASSWORD", "set-HP_PASSWORD"))
ap.add_argument("--old", default="cxdx5ryf1mqttbhwj5d40e6v", help="the old stack's prefix in docker")
ap.add_argument("--ssh", default="", help="run docker over ssh on this host, e.g. 192.168.178.54")
ap.add_argument("--apply", action="store_true", help="actually write; without it, only say what would happen")
ap.add_argument("--only", default="", help="restrict to these old project slugs, comma separated")
args = ap.parse_args()

APPLY = args.apply
notes: list[str] = []
counts: defaultdict[str, int] = defaultdict(int)


def say(line: str) -> None:
    print(line, flush=True)


def docker(*command: str, binary: bool = False):
    """Run something in a container of the old stack."""
    prefix = ["ssh", "-o", "BatchMode=yes", args.ssh] if args.ssh else []
    full = prefix + ["docker", *command]
    done = subprocess.run(full, capture_output=True)
    if done.returncode != 0:
        raise RuntimeError(" ".join(command[:3]) + ": " + done.stderr.decode()[:300])
    return done.stdout if binary else done.stdout.decode()


def container(kind: str) -> str:
    names = docker("ps", "--format", "{{.Names}}").splitlines()
    for name in names:
        if name.startswith(f"{kind}-{args.old}"):
            return name
    raise SystemExit(f"no {kind} container of the old stack is running")


DB = container("db")
MINIO = container("minio")


def query(sql: str) -> list[dict]:
    """Ask the old database, and get JSON back rather than parsed columns."""
    wrapped = f"select coalesce(json_agg(t), '[]'::json)::text from ({sql}) t;"
    out = docker("exec", DB, "sh", "-lc",
                 f'psql -U $POSTGRES_USER -d $POSTGRES_DB -tAc {json.dumps(wrapped)}')
    return json.loads(out.strip() or "[]")


def object_bytes(key: str) -> bytes:
    return docker("exec", MINIO, "sh", "-lc",
                  f'mc alias set old http://127.0.0.1:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" >/dev/null 2>&1; '
                  f'mc cat old/home/{key}', binary=True)


# ---------------------------------------------------------------- the new side

new = Client(args.url)
login = new.call("POST", "/api/auth/login", {"username": args.user, "password": args.password})
if not login:
    raise SystemExit("cannot sign in to the new server")
new.token = login["accessToken"]
new.call("POST", "/api/auth/step-up", {"password": args.password})

CAPABILITIES = {"edit": "markdown", "events": "calendar", "host": "site"}
PRESET = {"calendar": "calendar", "files": "data", "mixed": "data", "notes": "notes"}


def upload(project_id: str, path: str, content: bytes) -> None:
    boundary = "----import"
    head = (
        f"--{boundary}\r\nContent-Disposition: form-data; name=\"path\"\r\n\r\n\r\n"
        f"--{boundary}\r\nContent-Disposition: form-data; name=\"paths\"\r\n\r\n{path}\r\n"
        f"--{boundary}\r\nContent-Disposition: form-data; name=\"files\"; filename=\"{os.path.basename(path)}\"\r\n"
        f"Content-Type: application/octet-stream\r\n\r\n"
    ).encode()
    body = head + content + f"\r\n--{boundary}--\r\n".encode()
    new.call("POST", f"/api/projects/{project_id}/files/upload", raw=body,
             content_type=f"multipart/form-data; boundary={boundary}")


# ------------------------------------------------------------------- read it

old_groups = query("select id, slug, title, description, color, area, position from project_groups order by position")
old_projects = query("select id, slug, title, description, color, visibility, archived, type, "
                     "array_to_string(capabilities, ',') as caps from projects order by slug")
members = query("select group_id, project_id, position from project_group_members order by position")
folders = query("select id, name, parent_id, project_id from folders")
files = query("select id, name, folder_id, project_id, storage_key, size, content_type from files "
              "where storage_key is not null")
sites = query("select project_id, index_path, slug, title from sites where project_id is not null")
lectures = query("select title, room, lecturer, start_time, end_time from timetable_lectures order by start_time")
dhbw_events = query("select title, room, lecturer, description, start_time, end_time, project_id from manual_dhbw_events")
private_events = query("select title, location, description, start_time, end_time, project_id from private_events")
grades = query("select semester_name, modules, average_grade, total_ects from dualis_grades")
whiteboards = query("select title, data, folder from whiteboards where archived = false")

wanted = {s.strip() for s in args.only.split(",") if s.strip()}
if wanted:
    old_projects = [p for p in old_projects if p["slug"] in wanted]

say(f"the old server holds: {len(old_groups)} groups, {len(old_projects)} projects, {len(files)} files, "
    f"{len(lectures)} lectures, {len(dhbw_events) + len(private_events)} events, "
    f"{len(grades)} grade rows, {len(sites)} sites, {len(whiteboards)} whiteboards")
say("")

# A project belonged to several groups; here it belongs to one.
group_of: dict[str, str] = {}
extra_groups: defaultdict[str, list[str]] = defaultdict(list)
group_by_id = {g["id"]: g for g in old_groups}
for m in members:
    project, group = m["project_id"], m["group_id"]
    if project not in group_of:
        group_of[project] = group
    else:
        extra_groups[project].append(group)

# ------------------------------------------------------------------ write it

new_group_id: dict[str, str] = {}
for g in old_groups:
    existing = new.call("GET", f"/api/groups/{g['slug']}", expect=(200, 404, 401))
    if existing and existing.get("group"):
        new_group_id[g["id"]] = existing["group"]["id"]
        say(f"  group {g['slug']}: already there")
        counts["group kept"] += 1
        continue
    say(f"  group {g['slug']}: create")
    counts["group"] += 1
    if APPLY:
        created = new.call("POST", "/api/groups", {
            "title": g["title"], "slug": g["slug"], "description": g["description"] or "",
            "color": g["color"] or "mauve",
        }, expect=201)
        if created:
            new_group_id[g["id"]] = created["id"]

new_project_id: dict[str, str] = {}
for p in old_projects:
    caps = [CAPABILITIES[c] for c in (p["caps"] or "").split(",") if c in CAPABILITIES]
    preset = PRESET.get(p["type"] or "", "data")
    if "markdown" in caps and preset == "data":
        preset = "notes"
    group_id = new_group_id.get(group_of.get(p["id"], ""), "")

    for other in extra_groups.get(p["id"], []):
        name = group_by_id.get(other, {}).get("slug", other)
        notes.append(f"{p['slug']} was also in {name} — a project lives in one group now. "
                     f"Link what you need from it instead.")

    say(f"  project {p['slug']}: create as {preset}" + (f" with {', '.join(caps)}" if caps else "") +
        (f" in {group_by_id.get(group_of.get(p['id'],''), {}).get('slug', '—')}" if group_id else " (ungrouped)"))
    counts["project"] += 1
    if not APPLY:
        continue
    created = new.call("POST", "/api/projects", {
        "title": p["title"], "slug": p["slug"], "description": p["description"] or "",
        "groupId": group_id, "preset": preset, "capabilities": caps,
        "color": p["color"] or "",
    }, expect=201)
    if created:
        new_project_id[p["id"]] = created["id"]

# --- the file tree ------------------------------------------------------------
folder_by_id = {f["id"]: f for f in folders}


def folder_path(folder_id: str | None) -> str:
    parts: list[str] = []
    seen = set()
    while folder_id and folder_id in folder_by_id and folder_id not in seen:
        seen.add(folder_id)
        node = folder_by_id[folder_id]
        parts.append(node["name"])
        folder_id = node["parent_id"]
    return "/".join(reversed(parts))


by_project: defaultdict[str, list[dict]] = defaultdict(list)
for f in files:
    by_project[f["project_id"]].append(f)

for old_id, group in by_project.items():
    if old_id not in {p["id"] for p in old_projects}:
        continue
    slug = next(p["slug"] for p in old_projects if p["id"] == old_id)
    total = sum(f["size"] or 0 for f in group)
    say(f"  files for {slug}: {len(group)} ({total // 1024 // 1024} MB)")
    counts["file"] += len(group)
    if not APPLY:
        continue
    target = new_project_id.get(old_id)
    if not target:
        continue
    for f in group:
        path = "/".join(x for x in (folder_path(f["folder_id"]), f["name"]) if x)
        try:
            upload(target, path, object_bytes(f["storage_key"]))
        except Exception as e:  # one unreadable object must not stop the rest
            notes.append(f"{slug}/{path}: {e}")

# --- calendars ----------------------------------------------------------------
def calendar_target(old_project_id: str | None, fallback_slug: str) -> str | None:
    if old_project_id and old_project_id in new_project_id:
        return new_project_id[old_project_id]
    for p in old_projects:
        if p["slug"] == fallback_slug:
            return new_project_id.get(p["id"])
    return None


def add_events(rows: list[dict], fallback: str, what: str) -> None:
    if not rows:
        return
    say(f"  {what}: {len(rows)} events")
    counts["event"] += len(rows)
    if not APPLY:
        return
    for e in rows:
        target = calendar_target(e.get("project_id"), fallback)
        if not target or not e.get("start_time"):
            continue
        new.call("POST", f"/api/projects/{target}/calendar/events", {
            "summary": e.get("title") or "(no title)",
            "location": e.get("room") or e.get("location") or "",
            "description": "\n".join(x for x in [e.get("description"), e.get("lecturer")] if x),
            "start": e["start_time"], "end": e.get("end_time") or e["start_time"],
        }, expect=(201, 400, 409))


add_events(lectures, "dhbw-kalender", "the timetable")
add_events(dhbw_events, "dhbw-kalender", "DHBW entries")
add_events(private_events, "privat-kalender", "private entries")

# --- grades -------------------------------------------------------------------
if grades:
    say(f"  grades: {len(grades)} semesters")
    counts["grades"] += len(grades)
    if APPLY:
        target = None
        for p in old_projects:
            if p["slug"] in ("noten", "dhbw"):
                target = new_project_id.get(p["id"])
                if target:
                    break
        if target:
            new.call("PATCH", f"/api/projects/{target}", {"capabilities": ["grades"]})
            modules = []
            for row in grades:
                for m in (row["modules"] or []):
                    modules.append({
                        "name": m.get("name", ""),
                        "grade": float(str(m.get("grade", "0")).replace(",", ".") or 0) if str(m.get("grade", "")).replace(",", ".").replace(".", "").isdigit() else 0,
                        "credits": float(m.get("ects") or 0),
                        "semester": row["semester_name"],
                        "status": m.get("status") or "pending",
                    })
            new.call("PUT", f"/api/projects/{target}/grades/", {"modules": modules})

# --- sites --------------------------------------------------------------------
for s in sites:
    target = new_project_id.get(s["project_id"])
    say(f"  site {s['slug']}: publish {s['index_path'] or '/'}")
    counts["site"] += 1
    if APPLY and target:
        root = os.path.dirname(s["index_path"] or "") or ""
        new.call("PATCH", f"/api/projects/{target}", {"siteRoot": root})

# --- whiteboards --------------------------------------------------------------
if whiteboards:
    say(f"  whiteboards: {len(whiteboards)} — written as files")
    counts["whiteboard"] += len(whiteboards)
    if APPLY:
        target = next((new_project_id.get(p["id"]) for p in old_projects if p["slug"] == "allgemein"), None)
        if target:
            for w in whiteboards:
                name = (w["title"] or "whiteboard").replace("/", "-")
                new.call("PUT", f"/api/projects/{target}/files/content", {
                    "path": f"whiteboards/{name}.json", "content": w["data"] or "{}",
                })

# ------------------------------------------------------------------- the tally

say("")
say("what " + ("happened" if APPLY else "would happen") + ":")
for what, n in sorted(counts.items()):
    say(f"  {n:5}  {what}")

if notes:
    say("")
    say("worth reading:")
    for n in notes[:40]:
        say("  · " + n)
    if len(notes) > 40:
        say(f"  … and {len(notes) - 40} more")

import sweep  # noqa: E402

if sweep.failures:
    say("")
    say("requests that failed:")
    for f in sweep.failures[:20]:
        say("  x " + f[:200])

if not APPLY:
    say("")
    say("nothing was written. Run it again with --apply to do it.")
sys.exit(1 if sweep.failures else 0)

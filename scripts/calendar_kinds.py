#!/usr/bin/env python3
"""The five kinds of entry — and the promise that the file stays iCalendar.

A slot, an all-day, a deadline, a phase and a milestone go in through the API.
Then the check that matters: what lies on disk is still something Thunderbird
would open. A deadline is a VTODO with a DUE, a phase is an ordinary VEVENT
carrying one X- property, and a plain appointment gains nothing at all.

    python3 scripts/calendar_kinds.py --url http://127.0.0.1:8099
"""

from __future__ import annotations

import argparse
import os
import sys
from datetime import datetime, timedelta, timezone

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

group = c.call("POST", "/api/groups", {"title": "calendar-kinds"})
project = c.call("POST", "/api/projects",
                 {"title": "semester plan", "groupId": group["slug"], "preset": "calendar"})

now = datetime.now(timezone.utc).replace(microsecond=0)


def iso(t: datetime) -> str:
    return t.isoformat().replace("+00:00", "Z")


def add(body):
    return c.call("POST", f"/api/projects/{project['id']}/calendar/events", body, expect=(200, 201))


# --- one of each -----------------------------------------------------------
slot = add({
    "kind": "slot", "summary": "Analysis lecture", "location": "A2.14",
    "start": iso(now + timedelta(days=1, hours=2)), "end": iso(now + timedelta(days=1, hours=5)),
    "rrule": "FREQ=WEEKLY", "person": "Prof. Beispiel", "categories": ["Studium"],
})
allday = add({
    "kind": "all-day", "summary": "Excursion", "allDay": True,
    "start": iso(now + timedelta(days=3)), "end": iso(now + timedelta(days=4)),
})
deadline = add({
    "kind": "deadline", "summary": "Hand in the assignment", "priority": 1,
    "start": iso(now + timedelta(days=5)), "end": iso(now + timedelta(days=5)),
    "categories": ["Abgabe"],
})
late = add({
    "kind": "deadline", "summary": "Was due last week",
    "start": iso(now - timedelta(days=7)), "end": iso(now - timedelta(days=7)),
})
phase = add({
    "kind": "phase", "summary": "Semester 3", "allDay": True,
    "start": iso(now - timedelta(days=20)), "end": iso(now + timedelta(days=60)),
})
milestone = add({
    "kind": "milestone", "summary": "Semester start", "allDay": True,
    "start": iso(now - timedelta(days=20)), "end": iso(now - timedelta(days=20)),
})
check(all([slot, allday, deadline, late, phase, milestone]), "all five kinds go in")

# --- what comes back -------------------------------------------------------
frm = (now - timedelta(days=30)).strftime("%Y-%m-%d")
to = (now + timedelta(days=90)).strftime("%Y-%m-%d")
events = c.call("GET", f"/api/projects/{project['id']}/calendar/events?from={frm}&to={to}")["events"]
kinds = {}
for e in events:
    kinds.setdefault(e["kind"], []).append(e)

check(sorted(kinds) == ["all-day", "deadline", "milestone", "phase", "slot"],
      "every entry comes back as the kind it went in as")
check(len(kinds.get("slot", [])) > 3, "a weekly lecture is one entry that appears many times")
overdue = [e for e in kinds.get("deadline", []) if e.get("overdue")]
check(len(overdue) == 1 and overdue[0]["summary"] == "Was due last week",
      "a deadline in the past is marked overdue, an appointment is not")
check(kinds["deadline"][0].get("isTodo") is True, "a deadline is a todo")
check(kinds["phase"][0]["end"] > kinds["phase"][0]["start"], "a phase has a span")

# --- ticking one off -------------------------------------------------------
done = c.call("POST", f"/api/projects/{project['id']}/calendar/events/{late['uid']}/done", {"done": True})
check(bool(done) and done.get("done") is True, "a deadline can be ticked off")
events = c.call("GET", f"/api/projects/{project['id']}/calendar/events?from={frm}&to={to}")["events"]
ticked = [e for e in events if e["uid"] == late["uid"]][0]
check(ticked.get("done") is True and not ticked.get("overdue"),
      "and is then done rather than overdue")

# --- the file is still a calendar -----------------------------------------
body = (c.call("GET", f"/api/projects/{project['id']}/files/content?path=calendar.ics") or {}).get("content", "")
check("BEGIN:VTODO" in body and "DUE:" in body, "a deadline lies on disk as a VTODO with a DUE")
check("X-HOME-KIND:phase" in body, "a phase is an event carrying one custom property")
check("X-HOME-KIND:milestone" in body, "and so is a milestone")
check("PRIORITY:1" in body and "CATEGORIES:Abgabe" in body, "importance and tags are RFC 5545 properties")
check("STATUS:COMPLETED" in body and "PERCENT-COMPLETE:100" in body, "done is written the way iCalendar writes it")
check(body.count("X-HOME-KIND") == 2,
      "an ordinary appointment gains nothing — only the two kinds that need it are marked")
check("BEGIN:VCALENDAR" in body and "VERSION:2.0" in body and body.rstrip().endswith("END:VCALENDAR"),
      "the file is one complete VCALENDAR")

# --- what the subscription hands out --------------------------------------
as_events = c.call("GET", f"/api/projects/{project['id']}/calendar/export.ics?deadlines=events")
check("BEGIN:VTODO" not in as_events and as_events.count("BEGIN:VEVENT") >= 6,
      "the export can hand deadlines out as events, for calendars that ignore todos")
as_todos = c.call("GET", f"/api/projects/{project['id']}/calendar/export.ics?deadlines=todos")
check(as_todos.count("BEGIN:VTODO") == 2, "and as todos for the calendars that do not")

# --- what the dashboard is told -------------------------------------------
variables = c.call("GET", f"/api/projects/{project['id']}/variables?refresh=true")
by_name = {v["name"]: v.get("value") for v in (variables or {}).get("variables", [])}
check(by_name.get("current_phase") == "Semester 3", "the dashboard is told which phase today falls into")
check(by_name.get("next_deadline") == "Hand in the assignment", "and what is owed next")
check(isinstance(by_name.get("phase_progress"), (int, float)), "and how far through the phase it is")

# --- a change of mind ------------------------------------------------------
c.call("PATCH", f"/api/projects/{project['id']}/calendar/events/{deadline['uid']}",
       {"kind": "slot", "summary": "Hand in the assignment",
        "start": iso(now + timedelta(days=5)), "end": iso(now + timedelta(days=5, hours=1))})
body = (c.call("GET", f"/api/projects/{project['id']}/files/content?path=calendar.ics") or {}).get("content", "")
check(body.count("BEGIN:VTODO") == 1, "turning a deadline into an appointment changes its shape on disk")
check("Hand in the assignment" in body, "without losing it")

c.call("DELETE", f"/api/projects/{project['id']}?confirm={project['slug']}")
c.call("DELETE", f"/api/groups/{group['slug']}?confirm={group['slug']}&withProjects=true")

import sweep  # noqa: E402

if sweep.failures:
    ok = False
    print("\nrequests that failed:")
    for f in sweep.failures:
        print("  x " + f)

print("\ncalendar kinds:", "green" if ok else "RED")
sys.exit(0 if ok else 1)

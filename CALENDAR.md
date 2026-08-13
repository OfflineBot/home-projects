# Calendar — what belongs in it

> **State.** Sections 1, 3, 4, 5 and 7 are built, and so is most of 2 and 6:
> the five kinds, deadlines as `VTODO` with the export switch, phases as bands
> in their own lane, milestones as pins, overlap markers, gaps in the day view,
> the deadline panel, filters in the URL, and every variable in section 7.
> Still open: attaching your own note to a *pulled* occurrence has the storage
> (`X-HOME-ATTACHED-TO`) but no dialog yet, and "repeat until the end of this
> phase" still wants a date typed in. See `DECISIONS.md` §13 for why a deadline
> is a todo.

Companion to `PROMPT.md` section 3. That document says the calendar is a project whose
storage is valid iCalendar. This one says **what kinds of things go into it** and how each
is stored, shown and used.

Everything here is still one `calendar.ics` per project (RFC 5545). No side tables, no
hidden storage.

---

## 1. The five kinds of entry

| Kind | Question it answers | Has a duration | Example |
|---|---|---|---|
| **Slot** | when am I where | start → end, same day | Lecture 08:00–11:15, room A2.14 |
| **All-day** | what is today | a whole day or several | Holiday, trip, sick day |
| **Deadline** | when is it due | a point in time | Assignment due 23:59, exam registration |
| **Phase** | what period am I in | days to months | Semester 3, exam period, internship |
| **Milestone** | when does it start | a point, no duration | Semester start, project kickoff |

A `kind` field marks each entry. It is written into the ICS as `X-HOME-KIND` — a custom
property, allowed by RFC 5545 and preserved by every serious client. Anything without it
counts as `slot` or `all-day`, depending on whether it has a time.

**Why kinds at all, when they are all events?** Because they want to be *drawn*
differently. A deadline is a moment with weight, not a block from 23:00 to 23:59. A phase
runs behind everything else instead of filling ten week-cells. Without kinds you get a
calendar where a six-week internship covers everything else.

---

## 2. Slot — the normal timetable

The everyday case: a lecture, an appointment, a shift.

| Field | ICS |
|---|---|
| Start / end | `DTSTART` / `DTEND` |
| Title | `SUMMARY` |
| Room | `LOCATION` |
| Lecturer, notes | `DESCRIPTION`, plus `X-HOME-PERSON` if it should be filterable |
| Repetition | `RRULE`, e.g. `FREQ=WEEKLY;BYDAY=MO;UNTIL=20260320T000000Z` |
| Cancelled occurrence | `EXDATE` |
| Moved occurrence | separate `VEVENT` with `RECURRENCE-ID` |

**Timetables are recurring slots with an end.** A weekly lecture is one entry with an
`RRULE` that stops at the end of term — not sixteen copies. Changing the series changes
all of them; changing one week asks: *this occurrence, or all following?*

**Rules for the grid view:**

- Week starts Monday.
- Default visible range 06:00–22:00, scrollable beyond, and it widens automatically if
  entries fall outside.
- Overlapping slots sit side by side, not stacked on top of each other.
- **Overlaps are flagged.** Two slots at the same time in the same project or group get a
  visible marker — with a timetable that is almost always a mistake worth seeing.
- Gaps between slots on the same day may be shown as "2h 15m gap" — useful when the
  question is really "when do I have time".

### Pulled timetables

A DHBW timetable comes in through an `ics` scheduler (PROMPT.md section 5) and is
**read-only**: the next run overwrites it, and the UI says so instead of letting you edit
something that will silently revert.

But you can still attach your own things to it, kept **separately** so no pull destroys
them:

- your own note on an occurrence,
- your own color,
- your own reminder,
- a link to a file or note in another project (the folder for that lecture).

They are stored in the project's own `calendar.ics` as entries carrying
`X-HOME-ATTACHED-TO` with the foreign `UID`, never inside the pulled file.

---

## 3. Deadline — a point with weight

A deadline has no duration. It is due, and then it is either done or overdue.

| Field | ICS |
|---|---|
| Due at | `VTODO` with `DUE` |
| Title | `SUMMARY` |
| Done | `STATUS:COMPLETED` + `COMPLETED` timestamp |
| Importance | `PRIORITY` (1–9), shown as normal / important / critical |
| Belongs to | `RELATED-TO` — a phase, or another entry |

**Why `VTODO` and not a zero-length event:** because that is what iCalendar has for it,
and it keeps "done" meaningful. An event cannot be completed; a todo can.

**One catch, stated plainly:** Google Calendar ignores `VTODO` on subscribed feeds. So
the subscription URL takes an option **"deadlines as events"**, which converts them to
short `VEVENT`s on the way out. Internally they stay todos. Without that option your
phone would simply not show them, and you would find out at the wrong moment.

**How they are shown:**

- In month and week view as a **marker on the day**, not as a block — a flag on the edge
  of the cell with the time next to it.
- In the day and list view as a line at its time, visually distinct from slots.
- **Overdue and not done** stands out (Catppuccin red), and stays visible instead of
  scrolling into the past.
- Done deadlines fade out, they do not disappear — you want to see what you finished.
- An **"Upcoming deadlines"** panel next to the grid, sorted by time, with the distance
  in plain words: "in 3 days", "tomorrow 23:59", "2 days overdue".

**Default reminders** for deadlines: one day before, and one hour before. Changeable, but
those are the defaults, because a deadline you learn about when it hits is worthless.

---

## 4. Phase — the period you are in

A semester, an exam period, an internship, a project stage, a holiday. Days to months.

| Field | ICS |
|---|---|
| Start / end | `DTSTART` / `DTEND` as full days |
| Kind | `X-HOME-KIND:phase` |
| Color | `COLOR` or the project color |
| Title | `SUMMARY` |

**Phases are drawn as bands, not as blocks.** A horizontal bar in a lane above the grid,
spanning its days, in month and week view alike. Several phases stack into several lanes.
They never occupy cells in the grid, because otherwise six weeks of internship would bury
every lecture underneath.

What phases are good for:

- **Context**: the header says "Semester 3 · week 7 of 20" — where you are, without
  counting.
- **Grouping**: deadlines and milestones can point at a phase via `RELATED-TO`. The phase
  then has its own view: everything belonging to it, in order.
- **Boundaries**: a timetable series can be created "until the end of this phase" instead
  of typing a date.
- **Variables**: `current_phase`, `phase_progress`, `days_left_in_phase` (section 7).

Phases may overlap — "Semester 3" and "exam period" run at the same time, and that is not
an error.

---

## 5. Milestone — a point that starts something

A single point in time with no duration: semester start, project kickoff, move-in date,
the day something goes live.

- Stored as a full-day `VEVENT` with `X-HOME-KIND:milestone`.
- Shown as a pin on the day, in its own lane below the phases.
- Optional countdown on the dashboard: "Semester start in 12 days".

The difference from a deadline: a milestone is not owed and cannot be completed. It
simply arrives. Mixing the two makes the deadline list useless, which is why they are
separate.

---

## 6. Across all kinds

**Every entry can carry:**

- title, description, color, reminders (several, each with its own lead time),
- a **link into a project** — a folder, a file, a note. Clicking the entry gets you to
  the material for that lecture or the folder for that assignment. Stored as `URL` or
  `X-HOME-LINK` with a project path.
- tags via `CATEGORIES`, which the filter bar picks up.

**Views:**

| View | Shows |
|---|---|
| Month | phases as bands, all-days and deadline markers, slots compact |
| Week | the timetable grid, phase lane on top |
| Day | one column, wide, with gaps |
| List / agenda | everything upcoming, grouped by day, deadlines emphasized |
| Phase | one phase with everything related to it |

**Filtering** by kind, by project, by tag — and the filter lives in the URL, so the back
button and a shared link both work.

**Creating** is one dialog with the kind chosen first, because that decides which fields
appear: a slot wants start and end, a deadline only a due time, a phase two dates. The
last-used kind is preselected.

**Overlaying several projects:** a group's calendar view shows all its calendar projects
at once, each in its color, individually toggleable. A private calendar and the DHBW
timetable side by side is the normal case, not a special one.

---

## 7. What the calendar reports outward

Variables for the group and the dashboard (PROMPT.md section 8):

| Variable | Type | Meaning |
|---|---|---|
| `next_event` | text | the next slot, with its time |
| `today_count` | number | entries today |
| `next_deadline` | text | the nearest open deadline |
| `deadlines_open` | number | open, not yet overdue |
| `deadlines_overdue` | number | overdue — the one worth a red tile |
| `current_phase` | text | which phase today falls into |
| `phase_progress` | number | 0–100, how far through it |
| `free_today` | text | the largest gap today |

These make the dashboard useful without a calendar widget: a number tile for
`deadlines_overdue`, a text tile for `next_deadline`, a progress tile for the semester.

And automations can hook onto them — "when `deadlines_overdue` goes above 0, send a
mail", "30 minutes before a slot starts, turn on the light".

---

## 8. How timetable data gets pulled

A scheduler (PROMPT.md section 5) writes into the calendar project. Two source kinds:

- **`ics`** — a foreign ICS URL, the simple case. Fetch, parse, reconcile.
- **`dhbw`** — the scraper for `api.dhbw.app`, with Rapla as the source for the
  module → lecturer mapping. This already exists and works (section 9).

**Cadence:** every 30 minutes by default, and manually at any time. A timetable changes
rarely but at short notice — a cancellation the evening before is exactly the case that
matters.

**Window:** 4 weeks back, 24 weeks ahead. Backwards because rooms and lecturers still get
corrected after the fact; forwards because that covers the rest of the term.

**Reconciliation** happens per week, not as a wipe-and-refill:

1. Fetch the week from the source.
2. Match against what is already there, by a stable key — `UID` where the source gives
   one, otherwise a hash of module + start + room.
3. New entries are added, changed ones updated, entries that vanished upstream are
   removed.
4. Unchanged entries keep their identity, so your notes, colors and reminders stay
   attached.

**Three rules that protect your data — all of them learned the hard way:**

- **The past is never touched.** Anything before today stays, even when the source stops
  returning it. `api.dhbw.app` prunes past lessons from its response every day; without
  this rule, history would erase itself.
- **A failed fetch deletes nothing.** Error, timeout, empty response, half a response —
  the previous state stays and the run is logged as failed. Deletion only ever happens
  from a fetch that succeeded in full.
- **A suspicious result is not applied.** A week that had lessons and suddenly returns
  zero is not "cleared", it is flagged. Upstream being briefly broken must not look like
  a free week.

**What lands where:** the events go into the project's `calendar.ics`, marked
`X-HOME-SOURCE` with the scheduler that brought them. Your own additions live beside them
via `X-HOME-ATTACHED-TO` and are never part of the reconciliation. If the project has
`git_tracked` on, a run with changes produces a commit — so the timetable's history is
readable: what changed, and when.

**Every run is logged** with numbers, not just "ok": weeks fetched, lessons seen, changes
applied. `weeks=29 lessons=19 changes=0` tells you at a glance that it ran and that
nothing moved.

---

## 9. This already runs today

The pull is not a new idea to be invented — it exists in the old server and works. Worth
carrying over rather than rewriting:

- `backend/backend/dhbw/timetable/sync.go` — the weekly reconciliation, window −4/+24,
  with the "never touch the past" guard already in it.
- `backend/backend/dhbw/timetable/rapla_scrape.go` — the Rapla scrape for lecturer names.
- Registered in `main.go` as `timetable_sync`, running every 30 minutes.

Measured in production on 2026-08-13: last run 20:48, `weeks=29 lessons=19 changes=0`,
198 rows in `timetable_lectures` covering 2026-04-06 to 2026-11-06.

The old schema also already separates pulled from manual entries — `timetable_lectures`
versus `manual_dhbw_events` and `private_events`. That is the same idea as
`X-HOME-ATTACHED-TO` here, just expressed in tables instead of files. The logic carries
over; only the storage target changes.

---

## 10. Deliberate decisions

**Deadlines are `VTODO`, not events.** Costs an export option for Google, buys a
meaningful "done". Worth it.

**Phases are bands, not blocks.** Without this rule a long phase makes the calendar
unreadable, and that is exactly when you need it.

**Custom kinds ride in `X-` properties.** They survive a round trip through foreign
clients, and a client that ignores them still shows a correct calendar — just less
prettily.

**Pulled entries are never edited in place.** Your notes live beside them, so a pull
cannot delete your work. This is the same rule as everywhere else in the system: what a
scheduler fetched, a scheduler owns.

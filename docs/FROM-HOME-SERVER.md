# What the old server did, and where it lives now

`home_server` (branch `full`) was the predecessor: one Go server with a route
file per subject and a page per route. This one is built the other way round —
a small core that knows nothing about lights or lectures, and capabilities that
do. Same functionality, kept apart on purpose.

This file is the checklist that says so. It is written from the old server's
own route table, not from memory.

## Carried over

| The old server | Here | Where |
| --- | --- | --- |
| `/auth` login, sessions, step-up | same | `internal/api/auth_routes.go` |
| `/auth/2fa` TOTP | same | `internal/auth/totp.go`, Security page |
| `/files` upload, folders, zip, download | a project's files | `internal/api/files.go` |
| `/public/files` | a group's own address, exposed boards | `internal/api/sites.go` |
| `/container` storage with folders | the same thing, per project | `internal/files` |
| `/git` repos, branches | every group is a repository | `internal/api/git.go` |
| `/sites` install from git, publish, pull | the `site` capability | `capabilities/site` |
| `/calendar` events, sources | the `calendar` capability | `capabilities/calendar` |
| `/dhbw/grades` Dualis | the `grades` capability | `capabilities/grades` |
| `/dhbw/mail` Exchange | the `mail` capability, EWS included | `capabilities/mail/ews.go` |
| `/private/mail` IMAP, send, attachments | the same capability | `capabilities/mail` |
| mail classification | kept | `capabilities/mail/classify.go` |
| Moodle import | the `moodle` capability | `capabilities/moodle` |
| `/notes` | the `markdown` capability, with backlinks | `capabilities/markdown` |
| `/pc` wake, state, ssh, tmux | the `machines` capability | `capabilities/machines` |
| `/dhbw/timetable` | a `timetable` scheduler on a calendar project | `capabilities/calendar/timetable.go` |
| `/lights` on, off, colour | the `automation` capability and its light card | `capabilities/automation` |
| `/admin` schedulers, locks | schedulers, accounts | `internal/scheduler`, `internal/accounts` |
| client error reporting | same | `internal/api/clienterrors.go` |
| the dashboard | a board that can be arranged, per group and per project | `internal/api/boards.go` |

## Not carried over yet

Each of these is a real thing the old server did that this one does not. They
are worked through in this order.

| Missing | What it was | Status |
| --- | --- | --- |
| live updates | `/lights/events`, `/mail/stream` — the browser was told, not asked | done: `GET /api/events` |
| lights: colour | the old page set a colour, not only on and off | done: on the light card |
| web push | VAPID, subscribe, a notification on the phone | open |
| timetable | the Rapla scrape that filled the DHBW calendar | done: a `timetable` scheduler |
| whiteboards | a drawing per note | open |
| mensa | the canteen menu of the day | open |
| lecturers | who teaches what, from the DHBW pages | open |
| server logs | the log page under `/admin/logs` | open |

## How to check

`scripts/sweep.py` measures the server end to end, `frontend/src/*.test.tsx`
draws every screen against it, and `make test` runs the Go tests. Nothing in
this table is ticked off without one of the three saying so.

The terminal is the one thing that cannot be measured against nothing: it needs
a machine to attach to. Give the sweep a key and it goes the whole way — through
the proxy's upgrade, ssh, tmux — and asks tmux itself how wide it thinks its
client is:

    HP_SSH_KEY=~/.ssh/id_ed25519 HP_SSH_WHO=you@machine python3 scripts/sweep.py

Both faults that made the terminal unusable — a proxy that dropped the upgrade,
and a size message that was sent too early to be heard — would have been caught
there and nowhere else.

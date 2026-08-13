# home-projects

Everything is a project. Projects live in groups. A group is the virtual
environment, a project is the container inside it.

Your own GitHub, taken further: groups are repositories, projects are branches,
and on top of that sit calendars, connections to the outside, automations and a
dashboard fed by variables. There are no hard-wired areas — "Calendar",
"Notes", "Sites" are groups with projects in them, created on the first start,
and you can delete every one of them without the server noticing.

This was built from a written brief that stays out of the repository. The
decisions that deviate from it, and why, are in [`DECISIONS.md`](DECISIONS.md) —
each one quotes the sentence it is answering, so they read on their own.

---

## Running it

```bash
cp .env.example .env      # fill in the secrets and the owner account
make up                   # docker compose up -d --build
```

Then open <http://localhost:8080>. The first start creates the owner account
from `OWNER_USERNAME` / `OWNER_PASSWORD` and four groups to begin with.

### On Coolify

Deploy it as **one** "Docker Compose" resource, not as two applications:

1. New resource → Docker Compose → this repository, compose file
   `docker-compose.coolify.yml`.
2. Set the domain on the **frontend** service only. The backend and the database
   stay inside the stack; the frontend's nginx forwards `/api`, `/git` and `/s`
   to the backend by its service name.
3. Set these environment variables. All of them are required, and a missing one
   stops the deploy with a message naming it:

   | | |
   |---|---|
   | `POSTGRES_PASSWORD` | anything long and random |
   | `JWT_SECRET` | signs the access tokens |
   | `SECRET_KEY` | encrypts the credentials in the accounts menu |
   | `PUBLIC_URL` | `https://` plus the domain you gave the frontend |
   | `OWNER_USERNAME`, `OWNER_PASSWORD` | only for the very first start |

4. Under **Storages**, point the two directory mounts at real host paths:
   `/srv/home-projects/git` and `/srv/home-projects/data`, or wherever you want
   them. **This is the one that bites:** in the image they would be gone after
   every deploy, and with them every repository and every project's files.

Why one resource and not two: the frontend reaches the backend over the stack's
own network, everything sits under one domain, so the app calls relative paths
and there is no API address to bake into the build — and one deploy is one
consistent version of both halves.

`PUBLIC_URL` must be the real domain. Clone URLs, calendar subscription links
and webhook addresses are built from it; the compose file takes it from the
domain you set on the frontend.

### Locally, without Docker

```bash
make dev-db     # a throwaway postgres on 127.0.0.1:5544
make backend    # :5000
make frontend   # :5173, proxying /api, /git and /s to the backend
```

### Before a deploy

```bash
make check      # go vet, go test, tsc, and the endpoint sweep
```

`make check` runs the tests **and** a sweep across every endpoint that complains
at any non-2xx. The single-use credential test runs with
them. If it goes red, nothing gets deployed.

---

## What lives where

```
backend/
  cmd/server/            the one binary
  internal/
    api/                 every route; the only place that knows HTTP
    auth/  access/       who is asking, and what they may do
    store/               the only place that speaks SQL
    workspace/  files/   a project is a directory; this is how it is touched
    gitsrv/              bare repos, branches, smart HTTP
    capability/          the contract and the registry
    capabilities/        one folder per capability
      all/all.go         ← the registry: one line each
    scheduler/           the jobs, and the single-use credential rule
    accounts/            the one place a stored credential is ever used
    variables/           what projects report, and how a group adds it up
    automation/…         (inside capabilities/) triggers and actions
frontend/
  src/caps/index.ts      ← the frontend registry: one line each
  src/pages/             dashboard, groups, project, structure, accounts, …
scripts/
  sweep.py               the endpoint sweep
  git_roundtrip.py       clone, push, and check the working tree followed
  ssh_access.py          what a key over SSH may see and write
  password_push.py       pushing with the repository password, and its limits
  setup-git-ssh.sh       the one-time host setup for git@<host>
  hp-git-shell           the forced command every registered key carries
  hp-authorized-keys     what sshd asks for the keys
```

## Storage

- **A project is a directory.** `DATA_DIR/projects/<id>/tree` is exactly what
  the file tree shows. There is no hidden storage: an event is an `.ics` file, a
  mail is an `.eml` file, grades are a `grades.json`.
- **The database keeps indexes over those files** for fast queries. Every index
  is derived and is rebuilt from the files whenever they change — including
  changes that arrive by upload or by `git push`.
- **A group is a bare repository** at `GIT_DIR/<group-slug>.git`; every project
  is a branch named after its slug. The working tree has no `.git` directory of
  its own, so the folder the user sees stays a plain folder.

Both directories must be bind mounts. In the image they would be gone after
every deploy.

## Git

```bash
git clone https://<host>/git/<group-slug>.git                     # the whole group
git clone -b <project-slug> --single-branch https://<host>/git/<group>.git
```

Access is checked at the group, because the repository belongs to the group.
Projects with a stricter visibility have their branches left out of the
advertisement. `read_only` is enforced for `git push` as well — a protected
branch is refused with a message that says why.

Basic auth takes either your account password or the group's own password; a
machine token works too (any user name, the token as the password).

### Cloning and pushing with nothing but a password

A group set to **password** hands out its repository to anyone who knows that
password — no account involved:

```bash
git clone https://<host>/git/<group-slug>.git      # it asks; the group's password
```

Pushing that way is a separate switch, off by default: **Settings → the
password may push, too**. With it on, `git push` needs no account either. What
it does not switch off:

- a read-only project or group still refuses the push, with a reason;
- a project whose visibility is stricter than its group's is not advertised, so
  the password cannot reach it;
- failed attempts are counted and throttled — per user name *and* per
  repository, since the name in a basic-auth header is whatever the client
  chose to send.

Projects created in a password-protected group take that visibility, so the
group behaves as one thing instead of cloning empty.

### Over SSH

```bash
git clone git@<host>:<group-slug>.git
git clone -b <project-slug> --single-branch git@<host>:<group-slug>.git
```

Set it up once on the host:

```bash
sudo ./scripts/setup-git-ssh.sh     # creates the git user, installs two scripts,
                                    # configures sshd, prints the variables
```

Then add your public key under **Security → Keys for git over SSH**.

A key can do nothing on that machine except talk to this server about
repositories: no shell, no pty, no forwarding. There is no `authorized_keys`
file — sshd asks the server for the keys on every connection, so a key you
remove stops working at once and nothing can drift from what the database says.

And a push over SSH goes through exactly the checks a push over HTTPS does: a
read-only project refuses it, a project you may not see is not advertised, and
the working tree on the server follows the push. That is not a second
implementation — the wrapper asks this server the same questions the HTTP
handler asks itself, and the same `pre-receive` hook refuses everything the
answer did not name.

## The rule that outranks everything else

A stored credential is used **once**.

Before the credentials are sent, the account is marked "attempt in flight",
persistently. Only a confirmed sign-in clears that mark. Every other outcome —
a wrong password, a timeout, an abort, an answer we cannot interpret, or the
server stopping mid-attempt — deletes the secret, pauses the schedulers that
used it, and the account then reads *"enter the password again"*. There is no
automatic second attempt anywhere: not in the same run, not on the next tick,
not after a restart, and "Test connection" is the same attempt with the same
consequences.

Dualis locks an account after a few failed attempts. That is what this is for.

It is enforced in SQL (`internal/store/accounts.go`) and covered by six tests in
`internal/scheduler/credentials_test.go`.

## Capabilities

| Capability | View | on disk as |
|---|---|---|
| `calendar` | calendar grid, month/week/day/list | `calendar.ics` (RFC 5545) |
| `markdown` | editor with backlinks, Obsidian over git | `*.md` |
| `grades` | table and average | `grades.json` |
| `mail` | mailbox: read, classify, send | `*.eml` |
| `feed` | entries from a source | `feed.json` + articles |
| `site` | static serving of `site_root` | the files themselves |
| `automation` | rules and runs | `automation.yaml` + log |
| `moodle` | no view of its own — course material lands as files | the files themselves |

Adding one is a folder plus one line in `backend/internal/capabilities/all/all.go`
and one in `frontend/src/caps/index.ts`. Deleting one is deleting exactly that —
and the server still builds and runs; projects that used it then show their
files. A test (`internal/capability/isolation_test.go`) keeps the core from ever
naming a capability.

## A project as a tool

A `project.yaml` in a project's own folder turns it into a tool without a line
of server code:

```yaml
title: Living room heater
icon: flame
preset: system
capabilities: [automation]

variables:
  temperature:
    type: number
    unit: "°C"
    from: http
    url: http://192.168.178.30/status
    pick: $.temp
    every: 5m
  online:
    type: bool
    from: ping
    host: 192.168.178.30

actions:
  - name: Warmer
    run: http
    method: POST
    url: http://192.168.178.30/set?t=22
```

Broken YAML never breaks a project: it is reported and the file tree stays
usable. `from: command` is off unless `ALLOW_PROJECT_COMMANDS=true`, and never
runs in a project that visitors may write to.

Anything that can write a JSON file can also supply variables: an `exports.json`
in a project becomes variables, whether it arrived by upload, by API or by push.

## Configuration

| Variable | Meaning |
|---|---|
| `DATABASE_URL` | postgres connection string |
| `JWT_SECRET` | signs the access tokens |
| `SECRET_KEY` | encrypts the credentials in the accounts menu |
| `DATA_DIR` | the project working trees (bind mount) |
| `GIT_DIR` | the group repositories (bind mount) |
| `PUBLIC_URL` | the address used in clone, subscription and webhook URLs |
| `OWNER_USERNAME`, `OWNER_PASSWORD` | only for the very first start |
| `COOKIE_SECURE` | `true` behind HTTPS |
| `ALLOW_PROJECT_COMMANDS` | lets `project.yaml` run shell commands |
| `GIT_SSH_HOST` | `git@your-host` — switches git over SSH on |
| `GIT_SSH_SECRET` | what sshd and the wrapper authenticate to the server with |
| `MAX_UPLOAD_MB` | upload limit, default 512 |

Changing `SECRET_KEY` makes every stored credential unreadable — they then have
to be entered again, which is exactly what the single-use rule says anyway.

## Theme

Catppuccin, all four flavours (Mocha, Macchiato, Frappé, Latte) with fourteen
accents. Dark stays the default even when the device says otherwise. Every
colour comes from a CSS variable of the palette; there is no hex value anywhere
outside `src/app.css`, so switching flavour swaps the variable set and nothing
else.

## Not built

- The Android app. The API it needs is here — tokens, an ICS subscription URL
  per project, and every view behind a plain REST call — but the app itself is
  its own piece of work.
- WebAuthn / passkeys. TOTP is in; passwordless login is not.
- **Gmail over OAuth.** The accounts menu has IMAP/SMTP, ICS, generic HTTP,
  Dualis, Moodle and SSH. Gmail through OAuth needs a registered client and a
  consent flow, which is a piece of setup rather than a piece of code.
- An import of the old server's data. That is meant to happen through the normal
  API as its own step, never as a direct write into this schema.

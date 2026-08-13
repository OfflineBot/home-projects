# Decisions

Where this build deviates from the brief it was written to, and why. Each entry
quotes the sentence it is answering, so it reads without the brief at hand.
Everything not listed here follows it.

---

## 1. The filesystem is the file store, not MinIO

**The brief says:** *"Backend Go with Fiber, PostgreSQL via pgx, MinIO for file
contents"*, and in the same breath *"deviate only with a good reason"*.

**What was built:** files live on disk. `DATA_DIR/projects/<id>/tree` is the
project, one directory per project.

**Why:** the brief's own model rules MinIO out. Two of its load-bearing
sentences are

> *"There is no hidden storage. An event is an `.ics` file, a mail is an `.eml`
> file. That is why git works for everything"*

and

> *"A push from outside into the branch — the working tree on the server
> follows."*

Git needs a real working tree on disk. With MinIO as the store, every project
would exist twice — once as objects, once as the tree git operates on — and the
two would have to be kept in step on every write, every push, every checkout.
That is precisely the doubling section 12 of the brief blames for the first
attempt failing.

On disk there is one copy. `git`, the static site server, the zip download and
the capabilities all read the same bytes.

Nothing in the API depends on this: `internal/workspace` is a small interface
(list, read, write, move, remove, zip) with a filesystem implementation. If
object storage is ever wanted for large blobs, it goes behind that interface.

## 2. Smart HTTP is served directly, not through `git-http-backend` as CGI

**The brief says:** *"Served through `git http-backend` as CGI behind
`/git/...`"*, and warns that *"Alpine's `git` package does not contain
`git-http-backend`. It needs `git-daemon`."*

**What was built:** the server speaks git's smart HTTP itself. It runs
`git upload-pack` and `git receive-pack` with `--stateless-rpc` and streams
their input and output. Same protocol, one layer lower.

**Why:** it removes the trap the brief names, and it buys something CGI cannot
give. A push has to be refused for a read-only project *before* it lands. Since
this server starts `receive-pack` itself, it sets the environment for it, and a
`pre-receive` hook installed in every repository allows exactly the refs the
server listed. Nothing else on the machine starts `receive-pack`, so nothing
else can push.

`git-daemon` is still installed in the image, so the usual tooling is there.

## 3. Project addresses are unique per group; the API also accepts the id

**The brief says:** capability routes sit under `/api/projects/:slug/<name>/…`.

**What was built:** a project's slug is unique inside its group (that is what
makes it a branch name), so `/api/projects/:project/…` accepts either the id or
a slug that is unambiguous across the whole server. An ambiguous slug is
answered with a 409 that names the groups it could mean — never with a guess.

**Why:** two groups may each want a project called `docs`. Forcing global
uniqueness would make the address of one project depend on another group's
contents.

## 4. Group visibility governs the group and its repository, not its projects

The brief lists `visibility` on the group as *"applies to the repo (section
4)"*, and separately on the project. They are therefore independent: a public
project stays reachable by its own address even inside a private group, and what
a private group hides is its own listing. A clone of a group leaves out the
branches of projects whose visibility is stricter, which is exactly what section
4 asks for.

## 5. Where the presets and the account kinds come from

The brief's preset table and account table both name things by capability
("Calendar", "Mail", "Dualis"). Putting those lists in the core would mean the
core knows capability names, which section 11 forbids.

So the capabilities contribute them: a capability declares its presets, its
scheduler kinds, its account kinds and its automation actions, and the core only
ever assembles the lists. Deleting a capability's folder removes its preset and
its account kind with it. `internal/capability/isolation_test.go` fails the build
if a core file ever mentions a capability by name.

## 6. `from: command` in `project.yaml` is off by default

The brief lists `command` as a variable source. A `project.yaml` is a file in a
project — and a project can be public and can allow visitor writes. Running
shell commands from it unconditionally would hand anyone who may write a file a
shell.

It therefore needs `ALLOW_PROJECT_COMMANDS=true`, and it never runs in a project
with `anon_write` on. The same guard stops automation rules from running in such
a project at all.

## 7. The ICS subscription token is derived, not stored

Section 6 says a token is *"shown once"*. A calendar subscription URL has to be
shown as often as it is asked for, which is the opposite.

So it is not one of those tokens: it is an HMAC over the project id and a
rotation counter, computed on demand. It can be displayed at any time, it is
never stored, and "renew" changes the counter so every old link stops working at
once.

## 8. Mail is fetched with a small IMAP client written here

The mail capability needs an unambiguous answer to *"did the sign-in
succeed?"* — the single-use rule stands or falls with it. A minimal IMAP client
(`internal/capabilities/mail/imap.go`) makes that the tagged `OK` to `LOGIN` and
nothing else. It does what a fetch needs — sign in, select, fetch the newest
messages whole, sign out — and no more.

Dualis and Moodle use the libraries the brief points at
(`OfflineBot/nicht-libs`), as section 14 suggests.

## 9. Extra: a project can be started from a zip

Not in the brief, asked for during the build. Every project already offers a zip
download; `POST /api/projects/:project/files/import-zip` is its counterpart, and
the create dialog offers it as "start from a zip". Entries that would leave the
project, and symlinks, are refused rather than quietly cleaned up.

## 10. Git over SSH asks the server, and keeps no key file

Not in the brief, asked for during the build: `git clone git@offlinebot.xyz:…`.

A push over SSH never touches this server's HTTP handler, so on its own it would
ignore everything that handler enforces — hidden branches, read-only projects,
and the working tree that has to follow a push. Two ways in with two sets of
rules is the doubling section 12 warns about, so there is only one set:

- Every key sshd hands out carries a forced command. That wrapper asks this
  server what the key may see and write, runs git with exactly that answer, and
  reports back afterwards so the working trees follow.
- The `pre-receive` hook that already guards HTTPS refuses every ref the answer
  did not name. It is the same hook, unchanged.
- There is **no `authorized_keys` file**. sshd asks the server for the keys on
  every connection (`AuthorizedKeysCommand`). The first version did write that
  file, and it was wrong twice over: the container could not own a file in the
  git user's home, and a key could end up in the database while the write
  failed — two truths that can drift apart.

The cost is that SSH access needs the server to be up. It needed that anyway:
the wrapper cannot ask anyone else what a key may do.

## 11. A password may push, if the group says so

**The brief says:** in the table in section 4, `push` is *"login required"* for
every visibility, including `password`.

**What was built:** a switch on the group, off by default. With it on, the
repository's own password is enough to push — no account anywhere.

**Why:** asked for during the build, and it is a coherent thing to want: a
repository that is simply password-protected, the way a small private git server
is. It is a switch rather than the new behaviour because a password that may
read is a different thing from one that may write, and the brief is right that
the second one should not happen by accident.

What the switch deliberately does not touch: read-only still refuses the push,
branches the password may not see are not advertised, and the attempt counter
now counts per repository as well as per user name — a basic-auth user name is
chosen by the client, so counting only that could be walked around by varying
it.

While building it, one thing turned out to be wrong on its own account: a
project created in a password-protected group was `private`, so such a group
cloned empty. A new project now takes its group's visibility unless it is given
one, and a project that carries no password of its own is opened by its group's.

## 12. CSRF

The brief asks for *"CSRF protection for everything that writes via cookie"*.
Nothing writes via cookie: every write carries the access token in an
`Authorization` header, which a cross-site form cannot set. The two cookies that
do exist — the refresh token and the binding cookie — are `httpOnly`, `Secure`
and `SameSite=Strict`, so a cross-site request never carries them either.

## 13. Five kinds of entry, and none of them a new file format

`CALENDAR.md` asks for five things in a calendar — slot, all-day, deadline,
phase, milestone — because they want to be *drawn* differently. That could have
been a `kind` column and a private format. It is neither.

- A **deadline is a `VTODO`** with a `DUE`, `PRIORITY`, and `STATUS:COMPLETED`
  when it is ticked off. That is what iCalendar has for something that can be
  finished; an event cannot be completed, a todo can. The cost is real and is
  stated in the UI: Google Calendar ignores `VTODO` on a subscribed feed, so
  the export converts them to short events by default. `?deadlines=todos`
  turns that off for the clients that do understand them. The file is never
  what leaves — the conversion happens on the way out.
- A **phase and a milestone are ordinary `VEVENT`s** carrying `X-HOME-KIND`,
  which RFC 5545 explicitly allows. A client that ignores it shows a correct
  calendar, just less prettily.
- **A slot and an all-day carry nothing at all.** The kind is worked out from
  what the entry is — timed or whole-day — so an appointment written by
  Thunderbird comes back out byte-for-byte the same shape it went in as. The
  check script asserts exactly this.
- Everything else uses properties that already exist: `CATEGORIES` for tags,
  `RELATED-TO` for "belongs to this phase", `PRIORITY` for importance.
  `X-HOME-LINK` and `X-HOME-ATTACHED-TO` are the only two additions, and both
  are decoration: losing them loses a jump to a folder, never an appointment.

## 14. What is not built

- **The Android app.** Everything it needs exists on the server side.
- **WebAuthn / passkeys.** TOTP is in; the brief lists passkeys as optional.
- **Gmail over OAuth.** Every other account kind in the brief's table is here.
  Gmail needs a registered OAuth client and a consent round trip; the account
  kind is a handful of lines once those exist, and it fits the same registry.
- **Automatic import of the old server's data.** The brief asks for that to be
  its own step through the normal API, and it is not that step.

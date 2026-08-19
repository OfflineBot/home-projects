# Writing a page here, from a program

This describes everything an assistant needs to build a page on this server:
one token, two calls. Nothing else has to be understood — no cards, no
coordinates, no ids to keep track of.

## 1. The token

Made in the running server under **Security → Tokens**:

- **Scope** `write`
- **Group** the group whose page is to be written

The secret is shown once. A token is *not* an account: it inherits nothing from
the person who made it. A token for the group `dhbw` can read and write that
group's page and reach that group's projects — and nothing else on the server.
Everything private elsewhere stays out of reach, and a token with scope `read`
cannot write at all.

Send it as a bearer token:

    Authorization: Bearer <the secret>

## 2. Read the page

    GET /api/page?group=<group-slug>

```json
{
  "board": "…", "tab": "…", "title": "Page",
  "html": "<h1>DHBW</h1>…"
}
```

`404` means nobody has made a page there yet — write one and it exists.

## 3. Replace the page

    PUT /api/page?group=<group-slug>
    Content-Type: application/json

```json
{ "html": "<h1>DHBW</h1><p>Whatever you like.</p>", "title": "Front" }
```

The HTML replaces the page whole; `title` is optional and names the tab. The
limit is two megabytes.

    curl -X PUT "https://home.example.com/api/page?group=dhbw" \
      -H "Authorization: Bearer $TOKEN" \
      -H "Content-Type: application/json" \
      -d '{"html":"<h1>Hallo</h1><p>Von einem Programm geschrieben.</p>"}'

## 4. A page is not a picture: live values and real cards

Two things inside the HTML are replaced when the page is drawn, and they are
what make a hand-written page worth writing.

**A value, in double braces.** The name is `project-slug.variable`, optionally
with its group in front:

    <p>Schnitt: <strong>{{noten.average}}</strong></p>
    <p>Ungelesen: {{mail.inbox}}</p>

Whatever the project reports is put there when somebody looks. A name that does
not exist is left as it stands, so a typo is visible rather than silent.

Which names exist: `GET /api/projects/<id>/offers` lists everything a project
has to offer, ready to use — that is the same list the interface offers a
person.

**A card, as a tag.** Anything the board can draw can stand in the text:

    <hp-card kind="machine"  project="<project-id>" machine="pc"></hp-card>
    <hp-card kind="terminal" project="<project-id>" machine="pc" as="button"></hp-card>
    <hp-card kind="agenda"   project="<project-id>" days="14"></hp-card>
    <hp-card kind="rule"     project="<project-id>" rule="Start PC"></hp-card>
    <hp-card kind="light"    project="<project-id>" host="192.168.178.60" title="Desk"></hp-card>
    <hp-card kind="links-list" project="<project-id>"></hp-card>

`kind` is the card's name from `GET /api/boards/cards`; every other attribute is
that card's option, with `project` standing for the project id. The tag is
replaced by the working card — buttons press, terminals open, numbers update.

**How wide, how tall.** On a board a card's size is set in its own settings —
columns and rows on a grid, pixels on a free surface — or by dragging its
corner. In a written page it is the tag that says so: `width` and `height` do
exactly what they say, and a bare number is pixels while anything else (`50%`,
`20rem`) is taken as written:

    <hp-card kind="rule" project="<id>" rule="Start PC" width="220"></hp-card>
    <hp-card kind="terminal" project="<id>" machine="pc" width="60%" height="320"></hp-card>

For rows there are two classes, so no hand-written flexbox is needed:

    <div class="row">…</div>                          side by side, wrapping
    <div class="cols" style="--cols:3">…</div>        three equal columns

**Where things sit.** A top strip, a left side, the middle, a right side, a
bottom strip — any of the five may be left out, and one that is not there takes
no room:

    <div class="sides">
      <div class="top">…</div>
      <div class="left">…</div>
      <div class="main">…</div>
      <div class="right">…</div>
      <div class="bottom">…</div>
    </div>

The sides are 260 pixels wide; `style="--side:180px"` on it changes
that. On a narrow screen the whole thing becomes one column, in the order it is
written. `POST /api/boards/tabs/<id>/as-html` writes the same vocabulary, so a
tab of cards turned into a page comes out as something you would have written.

A terminal is worth giving a height: without one it is as tall as its box, and
`as="button"` makes it a button that opens the terminal over the page instead —
usually the better card on a page that is mostly text.

## 5. The board itself, not only its page

The same token reads and builds the board it belongs to, so an assistant can
work in cards as well as in HTML.

    GET  /api/boards?group=<slug>        the whole board: tabs, cards, sizes
    GET  /api/boards/cards               every kind of card this server has
    GET  /api/projects/<id>/offers       what one project has, ready to place
    POST /api/boards/cards               { tabId, kind, options, x, y, w, h }
    PATCH /api/boards/cards/<id>         { options, style, visibility, w, h }
    DELETE /api/boards/cards/<id>
    PUT  /api/boards/<board>/layout      { cards: [{ id, x, y, w, h }] }
    POST /api/boards/<board>/tabs        { title, icon }
    PATCH /api/boards/tabs/<id>          { title, icon, layout, style }
    POST /api/boards/<board>/fill        put what the projects report on it
    POST /api/boards/tabs/<id>/as-html   turn its cards into one page of HTML

The last one is worth knowing: cards and HTML are not two systems. A tab full of
cards becomes one document in which the cards stand as `<hp-card>` tags and the
numbers as `{{…}}` — after that it is written like any other page, by hand or by
you, and it still works.

A card sits on a twelve-column grid: `x` 0…11, `w` up to 12, `h` in rows of
about 92 pixels. A tab whose `layout` is `free` takes pixels instead, and one
whose layout is `page` is the HTML above.

Reading first is worth it: `GET /api/boards?group=…` says what is already
there, and `/offers` says what could be. Then place, or rearrange, or fill.

## 6. What may be in that HTML

The page is shown as part of the board, so it is cleaned before it is drawn:
markup, tables, images, links and inline styles stay; `<script>`, `<iframe>`,
`<form>` and every `on…` attribute are removed. That is a property of the
viewer, not of the storage — what is sent is kept as it was sent.

A card that needs scripts of its own is made in the browser instead ("Your own
HTML" → *in a frame of its own*), where it runs sandboxed.

Useful classes, if the page should look like the rest of the server:
`card`, `btn`, `btn primary`, `badge`, `meta`, `mono`, `stat`, `list`,
`list-row`, `prose`. They are optional; plain HTML works.

## 7. Where the page appears

- On the group's page, as a tab of its board.
- Under the group's own address, when one is set (**Group settings → Own
  address**): `dhbw.example.com` then shows that board, read-only.

Cards on that board are private unless they say otherwise; a page written
through this route is public, because a page is the thing that gets handed out.

## 8. What a token cannot do

- It cannot read a private project of another group, or that group's board.
- It cannot make, change or delete accounts, users, tokens or schedulers.
- It cannot write anywhere without scope `write`.
- It stops working the moment it is revoked, and it is listed with the date it
  was last used.

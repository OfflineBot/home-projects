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

**Lights as accounts.** A light account is a name and its lamps — the bed, the
desk, or every one in the house. It belongs to the house rather than to a
project, needs no password, and is switched as one:

    POST /api/accounts  {"kind": "wled", "title": "All the lights",
                         "config": {"hosts": "192.168.178.49, 192.168.178.53"}}
    GET  /api/capabilities/automation/lights           every light account
    GET  /api/capabilities/automation/lights/<id>      what it is doing
    POST /api/capabilities/automation/lights/<id>      {"power": "toggle"} | {"color": "#ff8800"}

A rule reaches one by name: `{"run": "wled", "account": "All the lights",
"power": "on"}`. And anything WLED can do that this does not is one `http`
action away — its whole JSON API is a POST to `http://<lamp>/json/state`.

**Lights in a project.** A project's `automation.yaml` may also hold lamps, and
a name there may carry a whole room:

    lights:
      - name: Desk
        host: 192.168.178.60
      - name: Living room
        hosts: [192.168.178.49, 192.168.178.50, 192.168.178.51]

`GET`/`PUT /api/projects/<id>/automation/lights` reads and replaces that list.
Everything else uses the name — the `light` card, the `wled` action in a rule,
and `POST /api/projects/<id>/automation/light` with
`{"host": "Living room", "power": "toggle"}`, which switches all of them at
once. Anything that is not a known name is taken to be an address.

**Something later.** A rule can be asked for with a delay rather than put on a
schedule — "everything on in five minutes" is asked for by hand, happens once,
and has to be callable off:

    POST   /api/projects/<id>/automation/later   {"rule": "Alles an", "minutes": 5}
    GET    /api/projects/<id>/automation/later   what is waiting, and when
    DELETE /api/projects/<id>/automation/later/<id>   one of them
    DELETE /api/projects/<id>/automation/later        the lot

The `timer` card is that with a box to type into: a number of minutes, one
button, what is waiting underneath and a stop beside it.

**A page out of parts.** A tab whose `layout` is `panes` is built the way a
page builder builds one: sections down the page, columns across a section,
cards in a column. The arrangement is in the tab's style and the cards are
ordinary cards, so one page can hold a light from one project, a machine from
another and a calendar from a third:

    PATCH /api/boards/tabs/<id>
    {"layout": "panes",
     "style": {"sections": [
       {"shape": "one",   "columns": [["<card-id>"]]},
       {"shape": "three", "columns": [["<id>"], ["<id>", "<id>"], ["<id>"]]}
     ]}}

`shape` is one of `one`, `two`, `three`, `left` (narrow left), `right` (narrow
right) or `quarters`. A section may also carry `"title"` for a heading above it
and `"look": "band"` for a tinted strip across the page. On a telephone the
columns stand underneath each other in the order they are written — there is
nothing to set for that. A card that no section mentions appears at the top of
the first one, so nothing is lost by rearranging.

The plain parts of a page are cards too: `image` (an address, cover or
contain), `clock`, `spacer` and `embed` (somebody else's page in a sandboxed
frame), besides `text`, `heading`, `link`, `number`, `status`, `list` and the
capability cards.

**A tab that fills the screen.** `PATCH /api/boards/tabs/<id>` with
`{"style": {"fill": true}}` makes the tab exactly as tall as the window, and
the cards divide that height between them instead of counting rows of 92
pixels: a card four rows tall where the deepest is eight is half the screen, on
any screen. That is the mode for something left open — a terminal, a wall
display. Without it the tab is as tall as it needs to be and the page scrolls.

**How wide, how tall.** On a board a card's size is set in its own settings —
columns and rows on a grid, pixels on a free surface — or by dragging its
corner. In a written page it is the tag that says so: `width` and `height` do
exactly what they say, and a bare number is pixels while anything else (`50%`,
`20rem`) is taken as written:

    <hp-card kind="rule" project="<id>" rule="Start PC" width="220"></hp-card>
    <hp-card kind="terminal" project="<id>" machine="pc" width="60%" height="320"></hp-card>

**What it looks like.** A card in a written page wears what the tag says, and
only that — anything left out keeps the theme's answer:

    <hp-card kind="rule" project="<id>" rule="Alles an"
             background="#101018" color="#cdd6f4" radius="20"
             border="1px solid #45475a" padding="18" shadow="0 2px 12px #0008">
    </hp-card>

The same knobs are in a card's settings for the cards that are not written by
hand: corners, a colour of its own, a background, a border.

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

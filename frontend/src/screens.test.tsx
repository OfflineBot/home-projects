/**
 * The pages, actually drawn.
 *
 * `tsc` proves the types line up and says nothing about a page that renders
 * blank — which is exactly the failure that keeps happening, because a hook
 * that throws unmounts the tree and leaves nothing behind. So this renders each
 * screen in a real DOM against a real server, and fails on an empty page or a
 * caught error.
 *
 * It talks to the API the way the browser does: a session, a token in memory,
 * nothing added to the server for testing's sake. Point it at a throwaway
 * instance with HP_URL and HP_PASSWORD.
 */
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import GroupSettings from "./components/GroupSettings";
import ProjectSettings from "./components/ProjectSettings";
import { api, login, type Group, type Project } from "./lib/api";
import { loadMeta, setUser } from "./lib/store";
import { Route, Routes } from "react-router-dom";
import FilesView from "./caps/FilesView";
import ProjectPage from "./pages/ProjectPage";
import Accounts from "./pages/Accounts";
import Dashboard from "./pages/Dashboard";
import FiltersPage from "./pages/Filters";
import Groups from "./pages/Groups";
import Schedulers from "./pages/Schedulers";
import Structure from "./pages/Structure";
import Users from "./pages/Users";
import MailView from "./caps/MailView";
import MachinesView from "./caps/MachinesView";
import Login from "./pages/Login";

declare const process: { env: Record<string, string | undefined> };

const url = process.env.HP_URL ?? "http://127.0.0.1:8099";
const password = process.env.HP_PASSWORD ?? "";

let owner: Awaited<ReturnType<typeof login>>;
let group: Group;
let project: Project;

// The client builds relative addresses; in a DOM without a server behind it,
// they have to be pointed somewhere real.
beforeAll(async () => {
  // A browser keeps cookies; this environment does not. The session here rests
  // on the same httpOnly binding cookie the real client uses, so the test has
  // to carry it — rather than the server being asked to do without it.
  const realFetch = globalThis.fetch;
  const jar = new Map<string, string>();
  globalThis.fetch = (async (input: any, init: any = {}) => {
    const address = typeof input === "string" && input.startsWith("/") ? url + input : input;
    const headers = new Headers(init.headers ?? {});
    if (jar.size > 0) {
      headers.set("cookie", [...jar].map(([k, v]) => `${k}=${v}`).join("; "));
    }
    const response = await realFetch(address, { ...init, headers });
    for (const raw of response.headers.getSetCookie?.() ?? []) {
      const [pair] = raw.split(";");
      const [name, ...rest] = pair.split("=");
      jar.set(name.trim(), rest.join("="));
    }
    return response;
  }) as typeof fetch;

  // Signed in *in the store*, not only in the client: half the pages have a
  // "sign in first" branch, and a test that renders that branch tests nothing.
  // "Sign in to see the filters." matched /filters/i for four runs.
  owner = await login("offlinebot", password);
  setUser(owner);
  // The app loads this once at startup, and half the dialogs are drawn from
  // it. Without it the test renders the empty half of every branch — which is
  // how a capability list that crashes on real data passed nine times.
  await loadMeta();
  group = await api<Group>("/api/groups", { body: { title: "ui-test-" + Date.now() } });
  project = await api<Project>("/api/projects", {
    body: { title: "ui probe", groupId: group.slug, preset: "data" },
  });
});

/** Rendered, and not empty — the two things a blank page fails. */
async function draw(node: React.ReactElement, expected: RegExp) {
  const { container } = render(<MemoryRouter>{node}</MemoryRouter>);
  await waitFor(() => expect(container.textContent ?? "").toMatch(expected), { timeout: 8000 });
  expect(container.textContent).not.toMatch(/could not be drawn/i);
  return container;
}

describe("every screen draws something", () => {
  it("the dashboard", async () => {
    await draw(<Dashboard />, /Dashboard|Nothing pinned/i);
  });

  // The front page is a board: a card is put on it, drawn, and arranged.
  it("the dashboard is a board that can be arranged", async () => {
    const board = await api<{ id: string; tabs: { id: string }[] }>("/api/boards");
    const card = await api<{ id: string }>("/api/boards/cards", {
      body: {
        tabId: board.tabs[0].id,
        kind: "text",
        options: { text: "# Guten Morgen\n\nEine **Notiz**." },
        x: 0, y: 0, w: 4, h: 2,
      },
    });
    try {
      const c = await draw(<Dashboard />, /Guten Morgen/i);
      // Drawn as markdown, not as its source.
      expect(c.querySelector(".card-text strong")?.textContent).toBe("Notiz");
      expect(c.textContent).not.toMatch(/\*\*/);
      // Reading is quiet: no handles until edit mode.
      expect(c.querySelector(".grid-handle")).toBeNull();

      fireEvent.click([...c.querySelectorAll("button")].find((b) => b.textContent?.trim() === "Edit")!);
      await waitFor(() => expect(c.querySelector(".grid-handle")).not.toBeNull());
      expect(c.querySelector(".grid-resize")).not.toBeNull();

      // And the settings of that card say who may see it.
      fireEvent.click(
        [...c.querySelectorAll("button")].find(
          (b) => b.getAttribute("aria-label") === "Settings for this card",
        )!,
      );
      const dialog = await waitFor(() => screen.getByRole("dialog"));
      expect(dialog.textContent).toMatch(/Who may see it/i);
      expect(dialog.textContent).toMatch(/Who may see it/i);
      cleanup();
    } finally {
      await api(`/api/boards/cards/${card.id}`, { method: "DELETE" });
    }
  });


  it("groups", async () => {
    await draw(<Groups />, new RegExp(group.title, "i"));
  });

  it("structure, as a tree", async () => {
    const c = await draw(<Structure />, new RegExp(group.slug, "i"));
    expect(c.querySelector(".tree-root")).not.toBeNull();
    expect(c.textContent).toContain("└──");
  });

  it("accounts", async () => {
    await draw(<Accounts />, /New account/i);
  });

  // Every mailbox, not one of them. The dialog is drawn from the server's own
  // answer, so this fails if a provider goes missing or the picker stops
  // filling the fields in.
  it("a mail account can be any provider", async () => {
    const c = await draw(<Accounts />, /New account/i);
    fireEvent.click([...c.querySelectorAll("button")].find((b) => /New account/i.test(b.textContent ?? ""))!);
    const dialog = await waitFor(() => screen.getByRole("dialog"));
    const selects = [...dialog.querySelectorAll("select")];
    const kind = selects[0];
    fireEvent.change(kind, { target: { value: "mail" } });

    const picker = [...dialog.querySelectorAll("select")][1];
    const offered = [...picker.querySelectorAll("option")].map((o) => o.textContent);
    for (const name of ["Gmail", "Outlook / Microsoft 365", "GMX", "WEB.DE", "Posteo", "iCloud", "DHBW Ravensburg"]) {
      expect(offered.join("|")).toContain(name);
    }
    expect(offered.length).toBeGreaterThan(7);

    // Picking one has to fill the servers in — that is the whole point of it.
    fireEvent.change(picker, { target: { value: "gmail" } });
    await waitFor(() =>
      expect([...dialog.querySelectorAll("input")].some((i) => i.value === "imap.gmail.com")).toBe(true),
    );
    // A dialog left standing would be found by the next test looking for one.
    cleanup();
  });


  // A real message, read the way the page reads it: the umlauts decoded, the
  // HTML shown in its frame, the attachment offered. Every one of those three
  // was broken at once, and none of it shows up in a type check.
  it("a mail is readable", async () => {
    const mail = await api<Project>("/api/projects", {
      body: { title: "ui mail " + Date.now(), groupId: group.slug, preset: "mail" },
    });
    const eml = [
      "From: Studienbuero <buero@dhbw-ravensburg.de>",
      "To: someone@example.com",
      "Subject: =?UTF-8?Q?Pr=C3=BCfungsanmeldung?=",
      "Date: Mon, 4 Aug 2025 09:12:00 +0200",
      "MIME-Version: 1.0",
      'Content-Type: text/html; charset=UTF-8',
      "Content-Transfer-Encoding: quoted-printable",
      "",
      "<p>Die Pr=C3=BCfung f=C3=A4llt aus.</p>",
      "",
    ].join("\r\n");
    try {
      await api(`/api/projects/${mail.id}/files/content`, {
        method: "PUT",
        body: { path: "inbox/2025-08-04-pruefung.eml", content: eml },
      });
      const c = await draw(<MailView project={mail} reload={() => {}} />, /Studienbuero/i);
      // The list shows the sender's name and the decoded subject.
      expect(c.textContent).toContain("Prüfungsanmeldung");
      expect(c.textContent).not.toMatch(/=C3|=BC/);

      fireEvent.click(c.querySelector(".mail-row")!);
      await waitFor(() => expect(c.querySelector(".mail-body-frame")).not.toBeNull());
      const frame = c.querySelector("iframe") as HTMLIFrameElement;
      expect(frame.getAttribute("sandbox") ?? "").not.toContain("allow-scripts");
      expect(frame.getAttribute("srcdoc") ?? "").toContain("Die Prüfung fällt aus.");
    } finally {
      await api(`/api/projects/${mail.id}?confirm=${mail.slug}`, { method: "DELETE" });
      cleanup();
    }
  });

  // A log is there when it is wanted and out of the way when it is not.
  it("a run's log folds away", async () => {
    const c = await draw(<Schedulers />, /New scheduler/i);
    const runs = c.querySelectorAll(".list-row");
    if (runs.length === 0) return; // nothing has run on this instance
    fireEvent.click(runs[runs.length - 1]);
    const dialog = await waitFor(() => screen.getByRole("dialog"));
    const fold = dialog.querySelector("details.fold");
    if (fold) {
      expect(dialog.textContent).toMatch(/lines/);
      // The summary is visible; what is inside is not, until it is opened.
      expect(fold.querySelector("summary")).not.toBeNull();
    }
    cleanup();
  });

  // A board can be a page: HTML written by hand, drawn as part of the board —
  // and what would run in it does not.
  it("a card can be your own HTML", async () => {
    const board = await api<{ id: string; tabs: { id: string }[] }>("/api/boards");
    const card = await api<{ id: string }>("/api/boards/cards", {
      body: {
        tabId: board.tabs[0].id,
        kind: "html",
        options: {
          html: '<h2 id="mine">Meine Seite</h2><p>Mit <b>Auszeichnung</b>.</p><script>window.pwned=1</script>',
          mode: "inline",
        },
        x: 0, y: 0, w: 6, h: 3,
      },
    });
    try {
      const c = await draw(<Dashboard />, /Meine Seite/i);
      expect(c.querySelector("#mine")?.textContent).toBe("Meine Seite");
      expect(c.querySelector(".html-inline b")?.textContent).toBe("Auszeichnung");
      // The script is gone, and it never ran.
      expect(c.querySelector("script")).toBeNull();
      expect((window as unknown as { pwned?: number }).pwned).toBeUndefined();
    } finally {
      await api(`/api/boards/cards/${card.id}`, { method: "DELETE" });
      cleanup();
    }
  });

  // A button that runs a rule: it has to be drawn, and pressing it has to say
  // what happened. This is the card that was reported as not showing at all.
  it("a rule is a button on the board", async () => {
    const group = await api<{ slug: string }>("/api/groups", { body: { title: "btn " + Date.now() } });
    const pc = await api<Project>("/api/projects", {
      body: { title: "pc", groupId: group.slug, preset: "machines" },
    });
    await api(`/api/projects/${pc.id}/automation/rules`, {
      method: "PUT",
      body: {
        rules: [{ name: "Start PC", trigger: { type: "button" },
                  actions: [{ run: "wol", mac: "aa:bb:cc:dd:ee:ff" }] }],
      },
    });
    const board = await api<{ id: string; tabs: { id: string }[] }>("/api/boards");
    const card = await api<{ id: string }>("/api/boards/cards", {
      body: {
        tabId: board.tabs[0].id, kind: "rule",
        options: { projectId: pc.id, rule: "Start PC", title: "Start PC" },
        x: 0, y: 0, w: 2, h: 1,
      },
    });
    try {
      const c = await draw(<Dashboard />, /Start PC/i);
      const button = [...c.querySelectorAll("button")].find((b) => /Start PC/.test(b.textContent ?? ""));
      expect(button).toBeTruthy();
      fireEvent.click(button!);
      // It says what it did — a button that reports nothing is not trusted twice.
      await waitFor(() => expect(c.querySelector(".card-rule .meta")?.textContent ?? "").not.toBe(""), {
        timeout: 8000,
      });
    } finally {
      await api(`/api/boards/cards/${card.id}`, { method: "DELETE" });
      await api(`/api/groups/${group.slug}?confirm=${group.slug}`, { method: "DELETE" });
      cleanup();
    }
  });

  // Exactly the page that was reported as not showing its button: a rule tag
  // written by hand, in a tab that is one page.
  it("a page shows the cards it asks for", async () => {
    const group = await api<{ slug: string }>("/api/groups", { body: { title: "tagpage " + Date.now() } });
    const pc = await api<Project>("/api/projects", {
      body: { title: "pc", groupId: group.slug, preset: "machines" },
    });
    await api(`/api/projects/${pc.id}/automation/rules`, {
      method: "PUT",
      body: { rules: [{ name: "Start PC", trigger: { type: "button" },
                        actions: [{ run: "wol", mac: "aa:bb:cc:dd:ee:ff" }] }] },
    });
    const board = await api<{ id: string; tabs: { id: string }[] }>(`/api/boards?group=${group.slug}`);
    const tab = board.tabs[0].id;
    await api(`/api/boards/tabs/${tab}`, { method: "PATCH", body: { layout: "page" } });
    await api(`/api/page?group=${group.slug}&tab=${tab}`, {
      method: "PUT",
      body: {
        html:
          `<hp-card kind="project" project="${pc.id}"></hp-card>\n` +
          `<h1>Home Main Page</h1>\n` +
          `<hp-card kind="rule" project="${pc.id}" rule="Start PC">Start PC</hp-card>`,
      },
    });
    try {
      const GroupBoard = (await import("./components/board/Board")).default;
      const c = await draw(<GroupBoard group={group.slug} />, /Home Main Page/i);
      // The tags have to become the cards, not stay as empty elements.
      await waitFor(() => expect(c.querySelector(".card-rule")).not.toBeNull(), { timeout: 8000 });
      expect([...c.querySelectorAll("button")].some((b) => /Start PC/.test(b.textContent ?? ""))).toBe(true);
      expect(c.querySelector(".card-project")).not.toBeNull();
      expect(c.textContent).not.toMatch(/That project is gone/);
    } finally {
      await api(`/api/groups/${group.slug}?confirm=${group.slug}`, { method: "DELETE" });
      cleanup();
    }
  });

  // A page is not a picture: a value in braces is the number as it is now.
  it("a page fills in live values", async () => {
    const board = await api<{ id: string; tabs: { id: string }[] }>("/api/boards");
    const card = await api<{ id: string }>("/api/boards/cards", {
      body: {
        tabId: board.tabs[0].id,
        kind: "html",
        options: { html: "<p>Files: <b>{{" + project.slug + ".files}}</b> · {{nothing.here}}</p>", mode: "inline" },
        x: 0, y: 0, w: 6, h: 2,
      },
    });
    try {
      const c = await draw(<Dashboard />, /Files:/i);
      // The name that exists is replaced; the one that does not stays visible,
      // so a typo shows itself instead of vanishing.
      await waitFor(() => expect(c.querySelector(".html-inline b")?.textContent).not.toBe(""));
      expect(c.textContent).toContain("{{nothing.here}}");
    } finally {
      await api(`/api/boards/cards/${card.id}`, { method: "DELETE" });
      cleanup();
    }
  });

  // A tab that is one page: read as the page, edited as its source beside a
  // preview — the same document an assistant writes through /api/page.
  it("a tab can be one page, written by hand", async () => {
    const board = await api<{ id: string; tabs: { id: string }[] }>("/api/boards");
    const tab = await api<{ id: string }>(`/api/boards/${board.id}/tabs`, {
      body: { title: "Seite", icon: "code" },
    });
    try {
      await api(`/api/boards/tabs/${tab.id}`, { method: "PATCH", body: { layout: "page" } });
      await api(`/api/page?tab=${tab.id}`, {
        method: "PUT",
        body: { html: "<h1>Geschrieben</h1><p>Von Hand.</p>" },
      });
      const c = await draw(<Dashboard />, /Seite/i);
      fireEvent.click([...c.querySelectorAll("button")].find((b) => /Seite/.test(b.textContent ?? ""))!);
      await waitFor(() => expect(c.textContent).toMatch(/Geschrieben/));
      expect(c.querySelector(".page-tab h1")?.textContent).toBe("Geschrieben");
      cleanup();
    } finally {
      await api(`/api/boards/tabs/${tab.id}`, { method: "DELETE" });
    }
  });

  // A tab is set up in one place: its name, its icon, how its cards lie and
  // how wide the page is.
  it("a tab can be set up", async () => {
    const c = await draw(<Dashboard />, /Dashboard/i);
    fireEvent.click([...c.querySelectorAll("button")].find((b) => b.textContent?.trim() === "Edit")!);
    await waitFor(() =>
      expect([...c.querySelectorAll("button")].some((b) => /This tab/i.test(b.textContent ?? ""))).toBe(true),
    );
    fireEvent.click([...c.querySelectorAll("button")].find((b) => /This tab/i.test(b.textContent ?? ""))!);
    const dialog = await waitFor(() => screen.getByRole("dialog"));
    for (const label of ["Name", "Icon", "Cards are", "Page width"]) {
      expect(dialog.textContent).toContain(label);
    }
    expect(dialog.textContent).toMatch(/one after another/i);
    cleanup();
  });

  // Every kind of card the server offers can be put on a board — including the
  // ones a capability brought, which is the whole point of the registry.
  it("a board offers the capabilities' cards too", async () => {
    const kinds = await api<{ cards: { name: string }[] }>("/api/boards/cards");
    const names = kinds.cards.map((k) => k.name);
    for (const wanted of ["text", "link", "project", "machine", "terminal", "agenda", "rule"]) {
      expect(names).toContain(wanted);
    }
  });

  // A calendar that gathers another calendar draws both, each switchable.
  it("a calendar gathers other calendars", async () => {
    const main = await api<Project>("/api/projects", {
      body: { title: "ui main calendar " + Date.now(), groupId: group.slug, preset: "calendar" },
    });
    const other = await api<Project>("/api/projects", {
      body: { title: "ui second calendar " + Date.now(), groupId: group.slug, preset: "calendar" },
    });
    try {
      await api(`/api/projects/${main.id}/sources`, { method: "PUT", body: { sources: [other.id] } });
      const CalendarView = (await import("./caps/CalendarView")).default;
      const c = await draw(<CalendarView project={main} reload={() => {}} />, new RegExp(other.title, "i"));
      // Both names are there, which is the whole point of gathering.
      expect(c.textContent).toContain(main.title);
      expect(c.textContent).toContain(other.title);
      expect(c.textContent).not.toMatch(/could not be drawn/i);
    } finally {
      await api(`/api/projects/${main.id}?confirm=${main.slug}`, { method: "DELETE" });
      await api(`/api/projects/${other.id}?confirm=${other.slug}`, { method: "DELETE" });
      cleanup();
    }
  });

  // Machines: the state of one, and the buttons that belong to that state.
  // Nothing here signs in — that needs a password and a real machine, which
  // the sweep does — but the page has to draw what it knows.
  it("machines, with what is known without signing in", async () => {
    const pcs = await api<Project>("/api/projects", {
      body: { title: "ui machines " + Date.now(), groupId: group.slug, preset: "machines" },
    });
    try {
      await api(`/api/projects/${pcs.id}/machines`, {
        method: "PUT",
        body: { machines: [{ name: "cellar pc", host: "127.0.0.1", port: 22, user: "someone", mac: "aa:bb:cc:dd:ee:ff" }] },
      });
      const c = await draw(<MachinesView project={pcs} reload={() => {}} />, /cellar pc/i);
      expect(c.textContent).toMatch(/up|not answering/i);
      expect(c.querySelector(".dot-status")).not.toBeNull();
      // The password lives in the page, and the page says so.
      expect(c.textContent).toMatch(/Keep the SSH password/i);
      expect(c.textContent).not.toMatch(/could not be drawn/i);
    } finally {
      await api(`/api/projects/${pcs.id}?confirm=${pcs.slug}`, { method: "DELETE" });
      cleanup();
    }
  });

  // The people page is drawn from the server's own list, so it fails if the
  // owner is not really the owner or the list comes back as null.
  it("people, with the owner in it", async () => {
    const c = await draw(<Users />, /offlinebot/i);
    expect(c.textContent).toContain("owner");
    expect(c.textContent).not.toMatch(/could not be drawn/i);
  });

  // Asking for an account is the one form a stranger sees, so it has to be
  // reachable without a session.
  it("the sign-in page can ask for an account", async () => {
    // A stranger, which is who that form is for: the session is put back
    // afterwards so the rest of the run keeps its owner.
    setUser(null);
    try {
      const c = await draw(<Login />, /Sign in/i);
      const ask = [...c.querySelectorAll("button")].find((b) => /need an account/i.test(b.textContent ?? ""));
      expect(ask).toBeTruthy();
      fireEvent.click(ask!);
      expect(c.textContent).toMatch(/Ask for an account/i);
      expect(c.textContent).toMatch(/About you/i);
    } finally {
      setUser(owner);
    }
  });

  it("schedulers", async () => {
    await draw(<Schedulers />, /New scheduler/i);
  });

  // The account for a scheduler can be made where the need for it comes up.
  // Before this, a mail scheduler could only pick what already existed, which
  // read as "only this one mailbox is possible".
  it("a scheduler can make its own account", async () => {
    const c = await draw(<Schedulers />, /New scheduler/i);
    fireEvent.click([...c.querySelectorAll("button")].find((b) => /New scheduler/i.test(b.textContent ?? ""))!);
    const dialog = await waitFor(() => screen.getByRole("dialog"));
    const account = [...dialog.querySelectorAll("select")].find((sel) =>
      [...sel.querySelectorAll("option")].some((o) => /a new account/i.test(o.textContent ?? "")),
    );
    expect(account).toBeTruthy();
    fireEvent.change(account!, { target: { value: "__new" } });
    await waitFor(() => expect(screen.getAllByRole("dialog").length).toBeGreaterThan(1));
    cleanup();
  });

  // Including one with no rules at all — the shape that broke the page, because
  // an empty list marshals to null unless something stops it.
  it("filters, including an empty one", async () => {
    const title = "ui-test-empty-" + Date.now();
    const made = await api<{ id: string }>("/api/filters", { body: { title, text: "" } });
    try {
      // Wait for the filter itself, not merely the heading: the heading is
      // there before the list arrives, and asserting against that tests nothing.
      const c = await draw(<FiltersPage />, new RegExp(title));
      expect(c.textContent).toContain("(empty)");
      expect(c.textContent).not.toMatch(/could not be drawn/i);
    } finally {
      await api(`/api/filters/${made.id}`, { method: "DELETE" });
    }
  });

  // The one that was reported blank. A settings dialog is a modal, so it has to
  // be found inside the document rather than in the container.
  it("a project's settings", async () => {
    render(
      <MemoryRouter>
        <ProjectSettings project={project} onClose={() => {}} onChanged={() => {}} />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByRole("dialog")).toBeTruthy(), { timeout: 8000 });
    const dialog = screen.getByRole("dialog");
    for (const label of ["Name", "Address", "Group", "Visibility", "Capabilities", "Filters"]) {
      expect(dialog.textContent).toContain(label);
    }
    // The capability list has to be *drawn*, not merely present as an empty
    // branch: that list is built from the server's own answer, and the empty
    // branch is what let a null in it go unnoticed.
    expect(dialog.textContent).toContain("Calendar");
    expect(dialog.textContent).not.toMatch(/could not be drawn/i);
  });

  // The file browser is what a Moodle project is looked at through, so it is
  // checked the way it is used: folders first, a file that opens, and a search
  // that reaches into folders you are not standing in.
  it("the files of a project", async () => {
    for (const [path, content] of [
      ["Vorlesung/Kap 3 Differentialrechnung.md", "# Kapitel 3\n\nEin **Satz** und `Code`.\n\n- eins\n- zwei"],
      ["Vorlesung/Übung 1.txt", "aufgaben"],
      ["liesmich.md", "# Oben"],
    ]) {
      await api(`/api/projects/${project.id}/files/content`, { method: "PUT", body: { path, content } });
    }

    const c = await draw(<FilesView project={project} reload={() => {}} />, /Vorlesung/);
    const rows = [...c.querySelectorAll(".list-row")].map((r) => r.textContent ?? "");
    expect(rows[0]).toContain("Vorlesung");           // folders first
    expect(rows.some((r) => r.includes("liesmich.md"))).toBe(true);

    // Searching reaches into the folder without opening it.
    const hits = await api<{ entries: { path: string }[] }>(
      `/api/projects/${project.id}/files/search?q=${encodeURIComponent("kap 3")}`,
    );
    expect(hits.entries.map((e) => e.path)).toContain("Vorlesung/Kap 3 Differentialrechnung.md");

    // And a markdown file is rendered rather than shown as asterisks.
    const view = render(
      <MemoryRouter initialEntries={["/?file=liesmich.md"]}>
        <FilesView project={project} reload={() => {}} />
      </MemoryRouter>,
    );
    await waitFor(() => expect(view.container.querySelector(".prose h1")).not.toBeNull(), { timeout: 8000 });
    expect(view.container.querySelector(".prose h1")?.textContent).toBe("Oben");
  });

  // Not a project made for the test: every project that actually exists on the
  // server, because the one that breaks is always the real one.
  it("the settings of every project that exists", async () => {
    const { projects } = await api<{ projects: Project[] }>("/api/projects");
    for (const p of projects) {
      const view = render(
        <MemoryRouter>
          <ProjectSettings project={p} onClose={() => {}} onChanged={() => {}} onDeleted={() => {}} />
        </MemoryRouter>,
      );
      await waitFor(() => expect(view.container.textContent ?? "").toMatch(/Visibility/i), { timeout: 8000 });
      const text = view.container.textContent ?? "";
      if (/could not be drawn/i.test(text)) {
        throw new Error(`settings for ${p.slug} failed: ${text.slice(0, 300)}`);
      }
      view.unmount();
    }
  });

  // The project page itself, for every project and every one of its tabs —
  // this is where an error box would show up, and a test that only renders the
  // dialog would never see it.
  it("every project page, and every tab it offers", { timeout: 90_000 }, async () => {
    const { projects } = await api<{ projects: Project[] }>("/api/projects");
    for (const p of projects) {
      for (const tab of ["files", "git", ...p.capabilities]) {
        const view = render(
          <MemoryRouter initialEntries={[`/p/${p.id}?tab=${tab}`]}>
            <Routes>
              <Route path="/p/:project/*" element={<ProjectPage />} />
            </Routes>
          </MemoryRouter>,
        );
        await waitFor(() => expect(view.container.textContent ?? "").toMatch(new RegExp(p.title, "i")), {
          timeout: 10000,
        });
        const text = view.container.textContent ?? "";
        // The words an error box uses — not every sentence with "failed" in
        // it, which is how "Failed runs show up here too" became a failure.
        const complaint = /could not be drawn|Something went wrong|no endpoint|is not right|could not be|cannot be/i.exec(text);
        if (complaint) {
          throw new Error(`${p.slug} · tab ${tab}: ${text.slice(0, 400)}`);
        }
        view.unmount();
      }
    }
  });

  // The rule builder writes the line, so nothing has to be typed or spelt from
  // memory. This clicks it the way a person would.
  it("builds a rule out of what is really there", async () => {
    await api(`/api/projects/${project.id}/files/content`, {
      method: "PUT",
      body: { path: "Vorlesung/x.txt", content: "x" },
    });
    const c = await draw(<FiltersPage />, /New filter/i);
    // Several earlier screens are still mounted in this document; the button is
    // the one in the page just drawn.
    fireEvent.click([...c.querySelectorAll("button")].find((b) => /New filter/i.test(b.textContent ?? ""))!);

    const address = `${group.slug}/${project.slug}`;
    const dialog = await waitFor(() => {
      const found = screen.getAllByRole("dialog");
      return found[found.length - 1];
    });
    const selects = () => [...dialog.querySelectorAll("select")];

    // Try it against the project, so its folders become the choices. The list
    // of projects has to have arrived first, or the change lands on nothing.
    await waitFor(() => expect(selects()[0].textContent).toContain(address), { timeout: 8000 });
    fireEvent.change(selects()[0], { target: { value: address } });
    await waitFor(() => expect(selects()[1].textContent).toContain("Vorlesung"), { timeout: 8000 });

    // what → how → where → Add
    fireEvent.change(selects()[1], { target: { value: "Vorlesung" } });
    fireEvent.change(selects()[2], { target: { value: "starts" } });
    fireEvent.change(selects()[3], { target: { value: `{${address}}` } });
    fireEvent.click([...dialog.querySelectorAll("button")].find((b) => b.textContent === "Add")!);

    const rules = dialog.querySelector("textarea") as HTMLTextAreaElement;
    expect(rules.value).toContain(`Vorlesung* -> {${address}}`);
    expect(c.textContent).not.toMatch(/could not be drawn/i);
  });

  // A scheduler can be moved to another project — the dialog has to offer it,
  // not merely the API.
  it("offers to move a scheduler to another project", async () => {
    const made = await api<{ id: string }>("/api/schedulers", {
      body: {
        kind: "ics", projectId: project.id, title: "ui-test-move",
        schedule: "manual", targetPath: "", options: { url: "https://example.com/x.ics" },
      },
    });
    try {
      const c = await draw(<Schedulers />, /ui-test-move/);
      fireEvent.click([...c.querySelectorAll("button")].find((b) => b.textContent?.includes("Edit"))!);
      const dialogs = await waitFor(() => screen.getAllByRole("dialog"));
      const dialog = dialogs[dialogs.length - 1];
      expect(dialog.textContent).toContain("Project");
      // The list of projects arrives on its own; the choice is only real once
      // it is in the select.
      await waitFor(
        () => {
          const options = [...dialog.querySelectorAll("select")].flatMap((sel) =>
            [...sel.querySelectorAll("option")].map((o) => o.textContent ?? ""),
          );
          expect(options.some((o) => o.includes(project.title))).toBe(true);
        },
        { timeout: 8000 },
      );
    } finally {
      await api(`/api/schedulers/${made.id}`, { method: "DELETE" });
    }
  });

  // An account can be edited: the dialog has to exist and be filled with what
  // is stored, without ever showing the password.
  it("edits an account", async () => {
    const made = await api<{ id: string }>("/api/accounts", {
      body: {
        kind: "mail", title: "ui-test-account",
        config: { host: "mail.example.org", port: 993, user: "someone" },
        secret: "never-shown",
      },
    });
    try {
      const c = await draw(<Accounts />, /ui-test-account/);
      fireEvent.click([...c.querySelectorAll("button")].find((b) => b.textContent?.includes("Edit"))!);
      const dialogs = await waitFor(() => screen.getAllByRole("dialog"));
      const dialog = dialogs[dialogs.length - 1];
      const values = [...dialog.querySelectorAll("input")].map((i) => i.value);
      expect(values).toContain("ui-test-account");
      expect(values).toContain("mail.example.org");
      expect(values).not.toContain("never-shown");
    } finally {
      await api(`/api/accounts/${made.id}`, { method: "DELETE" });
    }
  });

  it("a group's settings", async () => {
    render(
      <MemoryRouter>
        <GroupSettings group={group} projects={[project]} onClose={() => {}} onChanged={() => {}} />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getAllByRole("dialog").length).toBeGreaterThan(0), { timeout: 8000 });
    const dialogs = screen.getAllByRole("dialog");
    const text = dialogs.map((d) => d.textContent).join(" ");
    expect(text).toContain("Visibility");
    expect(text).not.toMatch(/could not be drawn/i);
  });
});

afterAll(async () => {
  try {
    await api(`/api/projects/${project.id}?confirm=${encodeURIComponent(project.slug)}`, { method: "DELETE" });
    await api(`/api/groups/${group.slug}?confirm=${group.slug}&withProjects=true`, { method: "DELETE" });
  } catch {
    /* a leftover test group is not worth failing a run over */
  }
});

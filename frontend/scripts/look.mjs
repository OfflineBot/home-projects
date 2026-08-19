/**
 * The app in a real browser, photographed.
 *
 * The sweep measures the server and the screen tests draw the pages in jsdom;
 * neither of them can see. This opens the app in a real browser, builds a
 * board with the awkward cards on it — a machine, a light, a rule, a terminal
 * attached to a real host — and takes pictures.
 *
 * It found four things nothing else did: a terminal that asked for a password
 * a machine had an account for, two cards on one account colliding, every bar
 * in the app huddled on the left because a class had no rule behind it, and
 * ten icons that drew nothing because their path was cut in the wrong place.
 *
 *   npm i playwright && npx playwright install chromium
 *   HP_PASSWORD=… SHOTS=/tmp/shots HP_SSH_KEY=~/.ssh/id_ed25519 \
 *     HP_SSH_WHO=you@machine node scripts/look.mjs
 *
 * Without a key it does everything except attach the terminal. It cleans up
 * after itself: the group, the projects and the key it was given.
 */
import { chromium } from "playwright";
import { readFileSync } from "node:fs";
import { mkdir } from "node:fs/promises";

const URL = process.env.HP_URL ?? "http://127.0.0.1:8099";
const password = process.env.HP_PASSWORD;
const out = process.env.SHOTS ?? "/tmp/home-projects-shots";
await mkdir(out, { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({
  viewport: { width: Number(process.env.WIDE ?? 1600), height: Number(process.env.TALL ?? 1000) },
});
const shot = async (name, target) => {
  await (target ?? page).screenshot({ path: `${out}/${name}.png` });
  console.log("shot:", name);
};

// ---------------------------------------------------------------- signing in
await page.goto(URL, { waitUntil: "networkidle" });
await shot("01-visitor");
// Signing in is a step of its own: the app is usable as a visitor, and the
// form is behind the button at the bottom of the sidebar — which on a phone is
// behind the menu.
const menu = page.locator('button[aria-label="Menu"]');
if (await menu.isVisible().catch(() => false)) {
  await menu.click();
  await page.waitForTimeout(500);
}
await page.getByText("Sign in", { exact: true }).first().click();
await page.waitForTimeout(1200);
await shot("01b-login");
const boxes = page.locator("input");
await boxes.nth(0).fill("offlinebot");
await boxes.nth(1).fill(password);
await page.keyboard.press("Enter");
await page.waitForTimeout(2500);
await shot("02-dashboard");

// ------------------------------------------------------- a board to look at
// The fixture is made through the API from here, with a session of its own:
// the app keeps its access token in memory, so a bare fetch inside the page is
// nobody. Same server, same user — the browser sees it after a reload.
let jar = "";
let bearer = "";
const api = async (path, init = {}) => {
  const res = await fetch(URL + path, {
    method: init.method ?? (init.body ? "POST" : "GET"),
    headers: {
      "Content-Type": "application/json",
      ...(jar ? { Cookie: jar } : {}),
      ...(bearer ? { Authorization: "Bearer " + bearer } : {}),
    },
    body: init.body ? JSON.stringify(init.body) : undefined,
  });
  const kept = res.headers.getSetCookie?.() ?? [];
  if (kept.length) {
    jar = [...new Set([...jar.split("; ").filter(Boolean), ...kept.map((c) => c.split(";")[0])])].join("; ");
  }
  const text = await res.text();
  const body = text ? JSON.parse(text) : {};
  if (res.status >= 400) throw new Error(`${path} → ${res.status} ${text.slice(0, 200)}`);
  return body;
};

bearer = (await api("/api/auth/login", { body: { username: "offlinebot", password } })).accessToken;
await api("/api/auth/step-up", { body: { password } });
const keyFile = process.env.HP_SSH_KEY?.replace("~", process.env.HOME ?? "");
const who = process.env.HP_SSH_WHO ?? "";
const [sshUser, sshHost] = who.includes("@") ? who.split("@") : ["", ""];
const key = keyFile ? readFileSync(keyFile, "utf8") : "";
const account = key && sshHost
  ? await api("/api/accounts", {
      body: {
        kind: "machine", title: "look-key",
        config: { user: sshUser, host: sshHost, port: "22" },
        secret: key,
      },
    })
  : null;
if (!account) console.log("(no HP_SSH_KEY — the terminal will not be attached)");
const group = await api("/api/groups", { body: { title: "Schaufenster" } });
const pc = await api("/api/projects", { body: { title: "server", groupId: group.slug, preset: "machines" } });
await api(`/api/projects/${pc.id}/machines`, {
  method: "PUT",
  body: { machines: [{
    name: "srv", host: sshHost || "127.0.0.1", port: 22,
    user: sshUser || "nobody", ...(account ? { account: "look-key" } : {}),
  }] },
});
await api(`/api/projects/${pc.id}/automation/rules`, {
  method: "PUT",
  body: { rules: [
    { name: "Licht an", trigger: { type: "button" }, actions: [{ run: "wled", host: "192.168.178.60", power: "on" }] },
  ] },
});

const board = await api(`/api/boards?group=${group.slug}`);
const tab = board.tabs[0].id;
const card = async (kind, options, x, y, w, h) =>
  api("/api/boards/cards", { body: { tabId: tab, kind, options, x, y, w, h } });
await card("heading", { title: "Schaufenster" }, 0, 0, 12, 1);
await card("machine", { projectId: pc.id, machine: "srv", title: "Server" }, 0, 1, 4, 2);
await card("light", { projectId: pc.id, host: "192.168.178.60", title: "Schreibtisch" }, 4, 1, 4, 2);
await card("rule", { projectId: pc.id, rule: "Licht an", title: "Licht an" }, 8, 1, 4, 2);
await card("terminal", { projectId: pc.id, machine: "srv", session: "schaufenster" }, 0, 3, 8, 4);
await card("terminal", { projectId: pc.id, machine: "srv", as: "button", title: "Terminal öffnen" }, 8, 3, 4, 1);

await page.goto(`${URL}/groups/${group.slug}`, { waitUntil: "networkidle" });
await page.waitForTimeout(6000);
await shot("03-board");

// The terminal card, close up — is it drawn, how wide did it come out?
const terminal = page.locator(".terminal").first();
if (await terminal.count()) {
  await shot("04-terminal-card", terminal);
  const cell = await page.evaluate(() => {
    const t = document.querySelector(".terminal");
    const c = t?.closest(".grid-cell");
    const box = (el) => (el ? `${Math.round(el.getBoundingClientRect().width)}x${Math.round(el.getBoundingClientRect().height)}` : "?");
    return `cell ${box(c)} (min ${c ? getComputedStyle(c).minHeight : "?"}, --rows ${c ? getComputedStyle(c).getPropertyValue("--rows") : "?"})  card ${box(t?.closest(".card"))}  terminal ${box(t)}`;
  });
  console.log("card boxes:", cell);
  console.log("terminal says:", (await terminal.innerText()).split("\n").slice(0, 3).join(" | "));
}

// Full screen, the way the button opens it.
const full = page.locator('button[aria-label="Full screen"]').first();
if (await full.count()) {
  await full.click();
  await page.waitForTimeout(4000);
  await shot("05-terminal-full");
  const filled = await page.evaluate(() => {
    const box = (sel) => {
      const el = document.querySelector(sel);
      if (!el) return "?";
      const r = el.getBoundingClientRect();
      return `${Math.round(r.width)}x${Math.round(r.height)}@${Math.round(r.x)},${Math.round(r.y)}`;
    };
    return `viewport ${window.innerWidth}x${window.innerHeight}  backdrop ${box(".terminal-backdrop")}  terminal ${box(".terminal.full")}  screen ${box(".terminal.full .terminal-screen")}  xterm ${box(".terminal.full .xterm-screen")}`;
  });
  console.log("full screen fills:", filled);
  const bar = await page.locator(".terminal.full .terminal-bar").innerText();
  console.log("bar in full screen:", bar.replace(/\n/g, " "));
  const buttons = await page.locator(".terminal.full .terminal-bar button").evaluateAll((bs) =>
    bs.map((b) => `${b.getAttribute("aria-label")}@${Math.round(b.getBoundingClientRect().x)}`));
  console.log("bar buttons:", buttons.join("  "));
  await page.locator('button[aria-label="Close"]').first().click().catch(() => {});
  await page.waitForTimeout(800);
  await shot("06-after-closing");
}

// ------------------------------------------------- a page built out of parts
// Sections down, columns across: the arrangement lives in the tab, the cards
// are the same cards.
const built = await api(`/api/boards/${(await api(`/api/boards?group=${group.slug}`)).id}/tabs`, {
  body: { title: "Zusammen" },
});
const sectionTab = built.id ?? built.tabs?.slice(-1)[0]?.id;
if (sectionTab) {
  const put = async (kind, options) =>
    (await api("/api/boards/cards", { body: { tabId: sectionTab, kind, options, x: 0, y: 0, w: 4, h: 2 } })).id;
  const machine = await put("machine", { projectId: pc.id, machine: "srv", title: "Server" });
  const light = await put("light", { projectId: pc.id, host: "192.168.178.60", title: "Licht" });
  const rule = await put("rule", { projectId: pc.id, rule: "Licht an", title: "Licht an" });
  const text = await put("text", { text: "## Zuhause\n\nEine Seite aus mehreren Projekten." });
  const term = await put("terminal", { projectId: pc.id, machine: "srv", as: "button", title: "Terminal" });
  const clock = await put("clock", { title: "Zuhause", seconds: "yes" });
  const picture = await put("image", { url: "https://placehold.co/600x300/1e1e2e/cdd6f4/png?text=Zuhause", fit: "cover" });
  await api(`/api/boards/tabs/${sectionTab}`, {
    method: "PATCH",
    body: {
      layout: "panes",
      style: {
        width: "wide",
        sections: [
          { shape: "left", columns: [[clock], [text]], look: "band" },
          { shape: "three", columns: [[machine], [light, rule], [term]], title: "Zuhause" },
          { shape: "one", columns: [[picture]] },
        ],
      },
    },
  });
  await page.goto(`${URL}/groups/${group.slug}`, { waitUntil: "networkidle" });
  await page.waitForTimeout(1500);
  const tabButton = page.getByRole("button", { name: /Zusammen/ }).first();
  if (await tabButton.count()) await tabButton.click();
  await page.waitForTimeout(3500);
  await shot("14-sections");

  // Dragging: the card in the first column is carried to the third, with a
  // pointer, the way a person does it.
  const before = await api(`/api/boards?group=${group.slug}`);
  const beforeCols = before.tabs.find((t) => t.id === sectionTab)?.style?.sections?.[1]?.columns;
  await page.getByRole("button", { name: "Edit" }).first().click();
  await page.waitForTimeout(4000);
  await shot("17-building");
  console.log(
    "panel:",
    await page.evaluate(() => {
      const b = document.querySelector(".page-builder");
      if (!b) return "none";
      const r = b.getBoundingClientRect();
      const box = (sel) => {
        const el = document.querySelector(sel);
        if (!el) return `${sel}: none`;
        const q = el.getBoundingClientRect();
        const cs = getComputedStyle(el);
        return `${sel} ${Math.round(q.width)}x${Math.round(q.height)}@${Math.round(q.x)},${Math.round(q.y)} ${cs.display}/${cs.visibility}/${cs.opacity}`;
      };
      return `${Math.round(r.width)}x${Math.round(r.height)}@${Math.round(r.x)},${Math.round(r.y)}  ${box(".page-builder-body")}  ${box(".page-builder-block")}  blocks ${document.querySelectorAll(".page-builder-block").length}`;
    }),
  );
  const grip = page.locator(".sections-section").nth(1).locator(".card-grip").first();
  const target = page.locator(".sections-section").nth(1).locator(".sections-col").nth(2);
  if ((await grip.count()) && (await target.count())) {
    const from = await grip.boundingBox();
    const to = await target.boundingBox();
    await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2);
    await page.mouse.down();
    await page.mouse.move(to.x + to.width / 2, to.y + 20, { steps: 12 });
    await page.waitForTimeout(300);
    await shot("16-dragging");
    await page.mouse.up();
    await page.waitForTimeout(2500);
    const after = await api(`/api/boards?group=${group.slug}`);
    const afterCols = after.tabs.find((t) => t.id === sectionTab)?.style?.sections?.[1]?.columns;
    console.log("dragged:", JSON.stringify(beforeCols), "→", JSON.stringify(afterCols));
  }
  await page.getByRole("button", { name: "Done" }).first().click().catch(() => {});
  await page.waitForTimeout(800);
  console.log(
    "sections:",
    await page.evaluate(() => {
      const cols = [...document.querySelectorAll(".sections-col")];
      return `${document.querySelectorAll(".sections-section").length} sections, columns ${cols
        .map((c) => Math.round(c.getBoundingClientRect().width))
        .join("/")}`;
    }),
  );
}

// ------------------------------------------------ a tab that fills the screen
await api(`/api/boards/tabs/${tab}`, {
  method: "PATCH",
  body: { style: { width: "wide", fill: true } },
});
await page.goto(`${URL}/groups/${group.slug}`, { waitUntil: "networkidle" });
await page.waitForTimeout(6000);
await shot("13-fills-the-screen");
console.log(
  "filling:",
  await page.evaluate(() => {
    const b = document.querySelector(".board");
    const t = document.querySelector(".terminal");
    const r = (el) => (el ? `${Math.round(el.getBoundingClientRect().height)}` : "?");
    return `window ${window.innerHeight}  board ${r(b)} (top ${Math.round(b.getBoundingClientRect().top)})  terminal ${r(t)}  bottom ${Math.round(b.getBoundingClientRect().bottom)}`;
  }),
);
await api(`/api/boards/tabs/${tab}`, { method: "PATCH", body: { style: { width: "wide" } } });

// ---------------------------------------------- the machines page of a project
await page.goto(`${URL}/p/${pc.id}?tab=machines`, { waitUntil: "networkidle" });
await page.waitForTimeout(2500);
await shot("11-machines");
const opener = page.getByRole("button", { name: /sessions|terminal/i }).first();
if (await opener.count()) {
  await opener.click();
  await page.waitForTimeout(5000);
  await shot("12-machines-terminal");
  const sizes = await page.evaluate(() => {
    const box = (sel) => {
      const el = document.querySelector(sel);
      return el ? Math.round(el.getBoundingClientRect().width) : "?";
    };
    return `main ${box("main.main")}  tiles ${box(".tiles")}  tile ${box(".tile.machine")}  terminal ${box(".machine-terminal")}`;
  });
  console.log("machines widths:", sizes);
}

// ------------------------------------------------- what can be set on a card
// Edit mode, then one card's settings: this is where how wide and how high a
// card is are typed rather than dragged.
await page.goto(`${URL}/groups/${group.slug}`, { waitUntil: "networkidle" });
await page.waitForTimeout(2500);
const edit = page.getByRole("button", { name: "Edit" }).first();
if (await edit.count()) {
  await edit.click();
  await page.waitForTimeout(1200);
  await shot("09-editing");
  // The way over from a grid: what is arranged stays arranged.
  const over = page.getByRole("button", { name: /Turn into sections/i }).first();
  if (await over.count()) {
    await over.click();
    await page.waitForTimeout(3000);
    await shot("15-turned-into-sections");
    console.log(
      "turned:",
      await page.evaluate(() => {
        const rows = [...document.querySelectorAll(".sections-section")];
        return rows
          .map((r) => [...r.querySelectorAll(".sections-col")].map((c) => Math.round(c.getBoundingClientRect().width)).join("+"))
          .join("  |  ");
      }),
    );
  }
  const settings = page.locator('button[aria-label="Settings for this card"]').first();
  if (await settings.count()) {
    await settings.click();
    await page.waitForTimeout(1200);
    const dialog = page.getByRole("dialog").first();
    await shot("10-card-settings", dialog);
    console.log("card settings:", (await dialog.innerText()).replace(/\n+/g, " | ").slice(0, 400));
  }
}

// ------------------------------------------------------------- the calendar
const cal = await api("/api/projects", { body: { title: "kalender", groupId: group.slug, preset: "calendar" } });
await page.goto(`${URL}/p/${cal.id}?tab=calendar`, { waitUntil: "networkidle" });
await page.waitForTimeout(2500);
await shot("07-calendar");
console.log("calendar toolbar:", await page.locator(".cal-head, .page-head").first().innerText().catch(() => "?"));

// ------------------------------------------------------------ a page layout
await api(`/api/boards/tabs/${tab}`, { method: "PATCH", body: { layout: "page" } });
await api(`/api/page?group=${group.slug}&tab=${tab}`, {
  method: "PUT",
  body: {
    html:
      `<div class="sides">` +
      `<div class="top"><h1>Zuhause</h1></div>` +
      `<div class="left"><hp-card kind="light" project="${pc.id}" host="192.168.178.60" title="Licht"></hp-card>` +
      `<hp-card kind="rule" project="${pc.id}" rule="Licht an"></hp-card></div>` +
      `<div class="main"><hp-card kind="terminal" project="${pc.id}" machine="srv" as="button" title="Terminal"></hp-card>` +
      `<p>Der mittlere Teil, mit allem was hier stehen soll.</p></div>` +
      `<div class="right"><hp-card kind="machine" project="${pc.id}" machine="srv"></hp-card></div>` +
      `<div class="bottom"><p>Streifen unten.</p></div>` +
      `</div>`,
  },
});
await page.goto(`${URL}/groups/${group.slug}`, { waitUntil: "networkidle" });
await page.waitForTimeout(4000);
await shot("08-page-layout");
const measured = await page.evaluate(() => {
  const of = (sel) => {
    const el = document.querySelector(sel);
    if (!el) return null;
    const cs = getComputedStyle(el);
    return `${sel}=${Math.round(el.getBoundingClientRect().width)} (max ${cs.maxWidth}, ${cs.display})`;
  };
  return [".board", ".board-page", ".html-inline", ".html-inline .sides", ".html-inline .sides > .main"].map(of).filter(Boolean).join("  ");
});
console.log("page widths:", measured);

// ------------------------------------------------------------------ tidy up
await api(`/api/groups/${group.slug}?confirm=${group.slug}&withProjects=true`, { method: "DELETE" });
if (account) await api(`/api/accounts/${account.id}`, { method: "DELETE" });
console.log("cleaned up");
await browser.close();

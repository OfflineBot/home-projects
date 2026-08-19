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
const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } });
const shot = async (name, target) => {
  await (target ?? page).screenshot({ path: `${out}/${name}.png` });
  console.log("shot:", name);
};

// ---------------------------------------------------------------- signing in
await page.goto(URL, { waitUntil: "networkidle" });
await shot("01-visitor");
// Signing in is a step of its own: the app is usable as a visitor, and the
// form is behind the button at the bottom of the sidebar.
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
  console.log("terminal says:", (await terminal.innerText()).split("\n").slice(0, 3).join(" | "));
}

// Full screen, the way the button opens it.
const full = page.locator('button[aria-label="Full screen"]').first();
if (await full.count()) {
  await full.click();
  await page.waitForTimeout(4000);
  await shot("05-terminal-full");
  const bar = await page.locator(".terminal.full .terminal-bar").innerText();
  console.log("bar in full screen:", bar.replace(/\n/g, " "));
  const buttons = await page.locator(".terminal.full .terminal-bar button").evaluateAll((bs) =>
    bs.map((b) => `${b.getAttribute("aria-label")}@${Math.round(b.getBoundingClientRect().x)}`));
  console.log("bar buttons:", buttons.join("  "));
  await page.locator('button[aria-label="Close"]').first().click().catch(() => {});
  await page.waitForTimeout(800);
  await shot("06-after-closing");
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
      `<div class="layout">` +
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

// ------------------------------------------------------------------ tidy up
await api(`/api/groups/${group.slug}?confirm=${group.slug}&withProjects=true`, { method: "DELETE" });
if (account) await api(`/api/accounts/${account.id}`, { method: "DELETE" });
console.log("cleaned up");
await browser.close();

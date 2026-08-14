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
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
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

  // A tile that is a project: the way straight back in, which is what the page
  // is opened for. It is drawn from two answers at once — the tiles and the
  // projects — so it fails if either is missing.
  it("a project on the dashboard", async () => {
    const tile = await api<{ id: string }>("/api/dashboard/tiles", {
      body: { kind: "project", projectId: project.id, title: "straight in" },
    });
    try {
      const c = await draw(<Dashboard />, /straight in/i);
      expect(c.querySelector(".project-tile")).not.toBeNull();
      expect(c.textContent).toContain("Open");
      expect(c.textContent).not.toMatch(/could not be drawn|That project is gone/i);
    } finally {
      await api(`/api/dashboard/tiles/${tile.id}`, { method: "DELETE" });
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
      expect(c.textContent).toMatch(/Who are you/i);
    } finally {
      setUser(owner);
    }
  });

  it("schedulers", async () => {
    await draw(<Schedulers />, /New scheduler/i);
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
  it("every project page, and every tab it offers", async () => {
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
        const complaint = /could not be drawn|Something went wrong|no endpoint|not right|cannot|failed/i.exec(text);
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

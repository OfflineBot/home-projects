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
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import GroupSettings from "./components/GroupSettings";
import ProjectSettings from "./components/ProjectSettings";
import { api, login, type Group, type Project } from "./lib/api";
import FilesView from "./caps/FilesView";
import Accounts from "./pages/Accounts";
import Dashboard from "./pages/Dashboard";
import FiltersPage from "./pages/Filters";
import Groups from "./pages/Groups";
import Schedulers from "./pages/Schedulers";
import Structure from "./pages/Structure";

declare const process: { env: Record<string, string | undefined> };

const url = process.env.HP_URL ?? "http://127.0.0.1:8099";
const password = process.env.HP_PASSWORD ?? "";

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

  await login("offlinebot", password);
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

  it("groups", async () => {
    await draw(<Groups />, new RegExp(group.title, "i"));
  });

  it("structure, as a tree", async () => {
    const c = await draw(<Structure />, new RegExp(group.slug, "i"));
    expect(c.querySelector(".tree-root")).not.toBeNull();
    expect(c.textContent).toContain("└──");
  });

  it("accounts", async () => {
    await draw(<Accounts />, /Accounts/i);
  });

  it("schedulers", async () => {
    await draw(<Schedulers />, /Schedulers|Nothing scheduled/i);
  });

  it("filters", async () => {
    await draw(<FiltersPage />, /Filters|No filters/i);
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
    for (const label of ["Name", "Address", "Group", "Visibility", "Capabilities"]) {
      expect(dialog.textContent).toContain(label);
    }
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

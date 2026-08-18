import { describe, expect, it } from "vitest";

/**
 * One terminal, and only one.
 *
 * The point of the terminal living in `components/terminal/` is that fixing it
 * there fixes it in every place a terminal appears — the machines page, a card
 * on a board, a tag in a written page. That only stays true as long as nothing
 * else opens a socket to a pty or draws its own emulator, which is exactly the
 * kind of thing that grows back quietly. So it is checked, against the source
 * itself rather than against a rendered page.
 */

const sources = import.meta.glob("./**/*.{ts,tsx}", {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

const mine = (path: string) => path.includes("components/terminal/");

describe("there is one terminal", () => {
  const files = Object.entries(sources).filter(([path]) => !/\.test\.tsx?$/.test(path));

  it("only the terminal module draws a terminal", () => {
    const others = files.filter(([path, text]) => !mine(path) && text.includes("@xterm/"));
    expect(others.map(([path]) => path)).toEqual([]);
  });

  it("only the terminal module opens a pty", () => {
    const others = files.filter(([path, text]) => !mine(path) && /\/pty|new WebSocket\(/.test(text));
    expect(others.map(([path]) => path)).toEqual([]);
  });

  it("everything that shows a terminal asks the module for it", () => {
    const users = files.filter(([path, text]) => !mine(path) && /<Terminal[\s/>]/.test(text));
    expect(users.length).toBeGreaterThan(0);
    for (const [path, text] of users) {
      expect(`${path}: ${/from "[./]*components\/terminal"/.test(text)}`).toBe(`${path}: true`);
    }
  });
});

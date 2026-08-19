import { describe, expect, it } from "vitest";
import { arrange, fromGrid, reshape, widthsOf } from "./components/board/Sections";
import type { Card } from "./components/board/Board";

/**
 * The arranging itself, without a browser.
 *
 * Everything else about sections is measured against a real page; this is the
 * part that decides where a card ends up, and it is worth being able to check
 * it in a millisecond. Two properties matter more than the rest: nothing is
 * ever lost, and a board that was arranged on a grid comes over looking the
 * way it looked.
 */

const card = (id: string, x: number, y: number, w = 4, h = 2): Card =>
  ({ id, kind: "heading", options: {}, x, y, w, h, visibility: "private" }) as Card;

describe("sections", () => {
  it("keeps every card, placed or not", () => {
    const cards = [card("a", 0, 0), card("b", 4, 0), card("stray", 8, 0)];
    const sections = arrange([{ shape: "two", columns: [["a"], ["b"]] }], cards);
    const all = sections.flatMap((s) => s.columns.flat());
    expect([...all].sort()).toEqual(["a", "b", "stray"]);
    // The one nobody placed is at the top of the first column, not appended
    // somewhere nobody looks.
    expect(sections[0].columns[0][0]).toBe("stray");
  });

  it("forgets a card that no longer exists, and never keeps one twice", () => {
    const sections = arrange([{ shape: "two", columns: [["a", "gone"], ["a"]] }], [card("a", 0, 0)]);
    expect(sections.flatMap((s) => s.columns.flat())).toEqual(["a"]);
  });

  it("loses nothing when a section gets fewer columns", () => {
    const after = reshape({ shape: "three", columns: [["a"], ["b"], ["c"]] }, "two");
    expect(after.columns.length).toBe(2);
    expect([...after.columns.flat()].sort()).toEqual(["a", "b", "c"]);
  });

  it("brings a grid over the way it was arranged", () => {
    // A heading across the top, three side by side, then a wide one beside a
    // narrow one — which is what a board looks like.
    const sections = fromGrid([
      card("head", 0, 0, 12, 1),
      card("one", 0, 1, 4, 2),
      card("two", 4, 1, 4, 2),
      card("three", 8, 1, 4, 2),
      card("wide", 0, 3, 8, 4),
      card("slim", 8, 3, 4, 1),
    ]);
    expect(sections.map((s) => s.shape)).toEqual(["one", "three", "right"]);
    expect(sections[1].columns).toEqual([["one"], ["two"], ["three"]]);
    expect(sections[2].columns).toEqual([["wide"], ["slim"]]);
    expect(widthsOf("right")).toEqual([8, 4]);
  });

  it("reads a narrow card beside a wide one as a sidebar", () => {
    const sections = fromGrid([card("side", 0, 0, 3, 4), card("main", 3, 0, 9, 4)]);
    expect(sections[0].shape).toBe("left");
  });
});

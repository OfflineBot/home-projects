import { useEffect, useRef, useState } from "react";
import { Icon } from "../Icon";
import { CardBody } from "./cards-body";
import type { Project, Variable } from "../../lib/api";
import type { Card } from "./Board";

/**
 * A tab built the way a page is built: sections down the page, columns across
 * a section, cards in a column.
 *
 * This is the layout for making a page out of several projects — a light from
 * one, a machine from another, a calendar from a third — rather than for
 * arranging boxes on a grid. It is what a page builder does, and it has one
 * property a grid cannot have: it needs no rules for a phone. A section is a
 * row of columns on a screen and the same columns underneath each other on a
 * telephone, because that is what a column does when there is no room beside
 * it.
 *
 * The arrangement lives in the tab's style, as a list of sections holding card
 * ids. The cards themselves are ordinary cards; only where they sit is
 * different. Anything not placed — a card added somewhere else, a section
 * removed with cards still in it — appears at the top of the first section, so
 * nothing is ever lost by rearranging.
 */

export type Shape = "one" | "two" | "three" | "left" | "right" | "quarters";

export interface Section {
  shape: Shape;
  /** One list of card ids per column. */
  columns: string[][];
  /** A heading above it, if it wants one. */
  title?: string;
  /** plain — nothing; band — a tinted strip the width of the page. */
  look?: "plain" | "band";
}

export const SHAPES: { key: Shape; title: string; widths: number[] }[] = [
  { key: "one", title: "one column", widths: [12] },
  { key: "two", title: "two", widths: [6, 6] },
  { key: "three", title: "three", widths: [4, 4, 4] },
  { key: "left", title: "narrow left", widths: [4, 8] },
  { key: "right", title: "narrow right", widths: [8, 4] },
  { key: "quarters", title: "four", widths: [3, 3, 3, 3] },
];

export function widthsOf(shape: Shape): number[] {
  return (SHAPES.find((s) => s.key === shape) ?? SHAPES[0]).widths;
}

/** A section with the right number of columns, whatever it held before. */
export function reshape(section: Section, shape: Shape): Section {
  const wanted = widthsOf(shape).length;
  const columns: string[][] = [];
  for (let i = 0; i < wanted; i++) columns.push([...(section.columns[i] ?? [])]);
  // Nothing is thrown away: what was in the columns that no longer exist goes
  // into the last one that does.
  for (let i = wanted; i < section.columns.length; i++) {
    columns[wanted - 1].push(...section.columns[i]);
  }
  return { shape, columns, title: section.title, look: section.look };
}

/**
 * The sections as they will be drawn: every card of the tab appears exactly
 * once, whether or not the saved arrangement knows about it.
 */
export function arrange(saved: Section[] | undefined, cards: Card[]): Section[] {
  const sections: Section[] = (saved ?? []).map((s) =>
    reshape({ shape: s.shape, columns: s.columns ?? [], title: s.title, look: s.look }, s.shape),
  );
  if (sections.length === 0) sections.push({ shape: "one", columns: [[]] });

  const known = new Set<string>();
  for (const section of sections) {
    section.columns = section.columns.map((column) =>
      column.filter((id) => {
        if (known.has(id) || !cards.some((c) => c.id === id)) return false;
        known.add(id);
        return true;
      }),
    );
  }
  const loose = cards.filter((c) => !known.has(c.id)).map((c) => c.id);
  if (loose.length) sections[0].columns[0] = [...loose, ...sections[0].columns[0]];
  return sections;
}

/**
 * The same cards, as sections.
 *
 * A board that was arranged on a grid is not thrown away to become a page: the
 * rows it already has are the sections, and what stood side by side in a row
 * stands side by side in a column. The shape of each section is read from how
 * wide the cards were — two equal cards are two columns, a narrow one beside a
 * wide one is a sidebar.
 */
export function fromGrid(cards: Card[]): Section[] {
  const placed = [...cards].sort((a, b) => a.y - b.y || a.x - b.x);
  const bands: Card[][] = [];
  let bottom = -1;
  for (const card of placed) {
    if (bands.length === 0 || card.y >= bottom) {
      bands.push([card]);
      bottom = card.y + Math.max(1, card.h);
      continue;
    }
    bands[bands.length - 1].push(card);
    bottom = Math.max(bottom, card.y + Math.max(1, card.h));
  }
  return bands.map((band) => {
    const row = [...band].sort((a, b) => a.x - b.x);
    const shape: Shape =
      row.length <= 1
        ? "one"
        : row.length === 2
          ? row[0].w === row[1].w
            ? "two"
            : row[0].w < row[1].w
              ? "left"
              : "right"
          : row.length === 3
            ? "three"
            : "quarters";
    const columns: string[][] = widthsOf(shape).map(() => []);
    row.forEach((card, i) => columns[Math.min(i, columns.length - 1)].push(card.id));
    return { shape, columns };
  });
}

export function Sections({
  sections,
  cards,
  editing,
  value,
  projects,
  onChange,
  onAdd,
  onSettings,
  onRemove,
  chosen,
  onChoose,
  quiet,
}: {
  sections: Section[];
  cards: Card[];
  editing: boolean;
  value: (variable: string, groupId?: string) => Variable | undefined;
  projects: Project[];
  onChange: (next: Section[]) => void;
  onAdd: (section: number, column: number) => void;
  onSettings: (card: Card) => void;
  onRemove: (card: Card) => void;
  /** Which card the panel is showing, when a page is being built. */
  chosen?: string | null;
  onChoose?: (card: Card) => void;
  /** The bars and buttons live in the panel instead. */
  quiet?: boolean;
}) {
  const byID = new Map(cards.map((c) => [c.id, c]));

  // Dragging, with a pointer rather than with the HTML5 drag events: those do
  // not exist on a telephone, and a page builder that can only be arranged
  // with a mouse is half a page builder.
  const [drag, setDrag] = useState<{ id: string; from: [number, number]; x: number; y: number } | null>(null);
  const [over, setOver] = useState<{ section: number; column: number; index: number } | null>(null);
  const columns = useRef(new Map<string, HTMLDivElement>());

  const change = (fn: (draft: Section[]) => void) => {
    const draft = sections.map((s) => ({ shape: s.shape, columns: s.columns.map((c) => [...c]) }));
    fn(draft);
    onChange(draft);
  };

  useEffect(() => {
    if (!drag) return;
    const move = (event: PointerEvent) => {
      setDrag((d) => (d ? { ...d, x: event.clientX, y: event.clientY } : d));
      // Which column is under the pointer, and where in it.
      let found: { section: number; column: number; index: number } | null = null;
      for (const [key, box] of columns.current) {
        const rect = box.getBoundingClientRect();
        if (event.clientX < rect.left || event.clientX > rect.right) continue;
        if (event.clientY < rect.top - 40 || event.clientY > rect.bottom + 40) continue;
        const [si, ci] = key.split(":").map(Number);
        const kids = [...box.querySelectorAll<HTMLElement>(".sections-card")];
        let index = kids.length;
        for (let i = 0; i < kids.length; i++) {
          const k = kids[i].getBoundingClientRect();
          if (event.clientY < k.top + k.height / 2) {
            index = i;
            break;
          }
        }
        found = { section: si, column: ci, index };
        break;
      }
      setOver(found);
    };
    const up = () => {
      setDrag(null);
      setOver((target) => {
        if (target && drag) {
          change((draft) => {
            const [fs, fc] = drag.from;
            const from = draft[fs]?.columns[fc];
            if (!from) return;
            const at = from.indexOf(drag.id);
            if (at < 0) return;
            from.splice(at, 1);
            const into = draft[target.section]?.columns[target.column];
            if (!into) {
              from.splice(at, 0, drag.id);
              return;
            }
            // Moving down inside the same column: the list is one shorter now.
            const index =
              fs === target.section && fc === target.column && target.index > at
                ? target.index - 1
                : target.index;
            into.splice(Math.max(0, Math.min(into.length, index)), 0, drag.id);
          });
        }
        return null;
      });
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    window.addEventListener("pointercancel", up);
    return () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", up);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [drag?.id]);

  return (
    <div className="sections">
      {sections.map((section, si) => (
        <div key={si} className={section.look === "band" ? "sections-section band" : "sections-section"}>
          {editing && !quiet ? (
            <div className="sections-bar">
              <select
                className="sections-shape"
                aria-label="Columns"
                value={section.shape}
                onChange={(e) => change((draft) => (draft[si] = reshape(draft[si], e.target.value as Shape)))}
              >
                {SHAPES.map((shape) => (
                  <option key={shape.key} value={shape.key}>
                    {shape.title}
                  </option>
                ))}
              </select>
              <input
                className="sections-name"
                value={section.title ?? ""}
                placeholder="a heading, if it wants one"
                onChange={(e) => change((draft) => (draft[si].title = e.target.value))}
              />
              <button
                className={section.look === "band" ? "btn small primary" : "btn small ghost"}
                title="A tinted strip across the page"
                onClick={() =>
                  change((draft) => (draft[si].look = draft[si].look === "band" ? "plain" : "band"))
                }
              >
                band
              </button>
              <span className="grow" />
              <button
                className="btn ghost icon"
                aria-label="Section up"
                disabled={si === 0}
                onClick={() => change((draft) => draft.splice(si - 1, 0, draft.splice(si, 1)[0]))}
              >
                <Icon name="chevronUp" size={13} />
              </button>
              <button
                className="btn ghost icon"
                aria-label="Section down"
                disabled={si === sections.length - 1}
                onClick={() => change((draft) => draft.splice(si + 1, 0, draft.splice(si, 1)[0]))}
              >
                <Icon name="chevronDown" size={13} />
              </button>
              <button
                className="btn ghost icon"
                aria-label="Remove this section"
                title="Its cards move to the section above"
                disabled={sections.length === 1}
                onClick={() =>
                  change((draft) => {
                    const [gone] = draft.splice(si, 1);
                    const into = draft[Math.max(0, si - 1)];
                    into.columns[0].push(...gone.columns.flat());
                  })
                }
              >
                <Icon name="trash" size={13} />
              </button>
            </div>
          ) : null}

          {section.title ? <h2 className="sections-title">{section.title}</h2> : null}

          <div className="sections-row">
            {section.columns.map((column, ci) => (
              <div
                key={ci}
                ref={(box) => {
                  if (box) columns.current.set(`${si}:${ci}`, box);
                  else columns.current.delete(`${si}:${ci}`);
                }}
                className={
                  over && over.section === si && over.column === ci ? "sections-col over" : "sections-col"
                }
                style={{ flexGrow: widthsOf(section.shape)[ci], flexBasis: 0 }}
              >
                {column.map((id, index) => {
                  const card = byID.get(id);
                  if (!card) return null;
                  const landing = over && over.section === si && over.column === ci && over.index === index;
                  return (
                    <div
                      key={id}
                      className={[
                        "sections-card",
                        drag?.id === id ? "lifted" : "",
                        landing ? "landing" : "",
                        chosen === id ? "chosen" : "",
                      ]
                        .filter(Boolean)
                        .join(" ")}
                      onClickCapture={(e) => {
                        if (!editing || !onChoose) return;
                        // Choosing a card should not press what is on it.
                        e.stopPropagation();
                        e.preventDefault();
                        onChoose(card);
                      }}
                    >
                      {editing ? (
                        <div className="card-tools">
                          <button
                            className="card-grip"
                            aria-label={`Move ${card.options?.title ?? card.kind}`}
                            title="Drag me"
                            onPointerDown={(e) => {
                              e.preventDefault();
                              (e.target as HTMLElement).setPointerCapture?.(e.pointerId);
                              onChoose?.(card);
                              setDrag({ id, from: [si, ci], x: e.clientX, y: e.clientY });
                            }}
                          >
                            ⠿
                          </button>
                          {quiet ? null : (
                            <>
                              <button
                                className="btn ghost icon"
                                aria-label="Settings for this card"
                                onClick={() => onSettings(card)}
                              >
                                <Icon name="settings" size={13} />
                              </button>
                              <button
                                className="btn ghost icon"
                                aria-label="Remove this card"
                                onClick={() => onRemove(card)}
                              >
                                <Icon name="x" size={14} />
                              </button>
                            </>
                          )}
                        </div>
                      ) : null}
                      <CardBody card={card} value={value} projects={projects} editing={editing} />
                    </div>
                  );
                })}
                {editing ? (
                  <button className="add-here in-column" onClick={() => onAdd(si, ci)}>
                    <Icon name="plus" size={14} /> {quiet ? "Here" : "Add here"}
                  </button>
                ) : null}
              </div>
            ))}
          </div>
        </div>
      ))}

      {editing && !quiet ? (
        <button
          className="add-here"
          onClick={() => onChange([...sections, { shape: "two", columns: [[], []] }])}
        >
          <Icon name="plus" size={16} /> Another section
        </button>
      ) : null}
    </div>
  );
}

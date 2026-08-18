import { useEffect, useRef, useState, type ReactNode } from "react";

/**
 * The twelve-column grid a board is arranged on.
 *
 * Written here rather than pulled in: a grid that drags and resizes is about
 * two hundred lines, and a dependency for it would be larger than the rest of
 * this page put together. What it does is small and exact — cards snap to
 * whole columns and whole rows, they never overlap, and what is under the
 * pointer is what moves.
 *
 * In view mode none of this runs: the cards are laid out by the same maths and
 * nothing listens for a pointer, so a board that is only being read costs
 * nothing.
 */

export const COLUMNS = 12;
export const ROW_HEIGHT = 92;
export const GAP = 12;

export interface Placed {
  id: string;
  x: number;
  y: number;
  w: number;
  h: number;
}

type Drag =
  | { kind: "move"; id: string; startX: number; startY: number; from: Placed }
  | { kind: "size"; id: string; startX: number; startY: number; from: Placed }
  | null;

export function Grid({
  cards,
  editing,
  free,
  onChange,
  children,
}: {
  cards: Placed[];
  editing: boolean;
  /**
   * A free surface: the card goes where it is put, in pixels, and nothing
   * snaps it anywhere. This is the mode for building a page rather than
   * filling a grid.
   */
  free?: boolean;
  /** Called once a drag ends, with everything that moved. */
  onChange: (next: Placed[]) => void;
  children: (card: Placed, editing: boolean) => ReactNode;
}) {
  const area = useRef<HTMLDivElement>(null);
  const [drag, setDrag] = useState<Drag>(null);
  const [live, setLive] = useState<Placed[] | null>(null);

  const shown = live ?? cards;
  const rows = Math.max(4, ...shown.map((c) => c.y + c.h));
  const tallest = Math.max(360, ...shown.map((c) => c.y + c.h + 40));

  useEffect(() => {
    if (!drag) return;

    const columnWidth = () => {
      const width = area.current?.clientWidth ?? 1200;
      return (width - GAP * (COLUMNS - 1)) / COLUMNS;
    };

    const move = (event: PointerEvent) => {
      if (free) {
        // Pixels, exactly where the pointer went. Nothing else moves.
        const dx = event.clientX - drag.startX;
        const dy = event.clientY - drag.startY;
        setLive(
          cards.map((c) => {
            if (c.id !== drag.id) return { ...c };
            if (drag.kind === "move") {
              return { ...c, x: Math.max(0, drag.from.x + dx), y: Math.max(0, drag.from.y + dy) };
            }
            return {
              ...c,
              w: Math.max(120, drag.from.w + dx),
              h: Math.max(60, drag.from.h + dy),
            };
          }),
        );
        return;
      }
      const dx = Math.round((event.clientX - drag.startX) / (columnWidth() + GAP));
      const dy = Math.round((event.clientY - drag.startY) / (ROW_HEIGHT + GAP));
      const next = cards.map((c) => {
        if (c.id !== drag.id) return { ...c };
        if (drag.kind === "move") {
          return {
            ...c,
            x: clamp(drag.from.x + dx, 0, COLUMNS - c.w),
            y: Math.max(0, drag.from.y + dy),
          };
        }
        return {
          ...c,
          w: clamp(drag.from.w + dx, 1, COLUMNS - c.x),
          h: Math.max(1, drag.from.h + dy),
        };
      });
      setLive(settle(next, drag.id));
    };

    const up = () => {
      setDrag(null);
      setLive((current) => {
        if (current) onChange(current);
        return null;
      });
    };

    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    return () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
    };
  }, [drag, cards, onChange]);

  return (
    <div
      ref={area}
      className={[free ? "free-area" : "grid-area", editing ? "editing" : ""].join(" ").trim()}
      style={
        free
          ? { minHeight: tallest }
          : {
              gridTemplateColumns: `repeat(${COLUMNS}, 1fr)`,
              gridAutoRows: `${ROW_HEIGHT}px`,
              gap: GAP,
              minHeight: rows * (ROW_HEIGHT + GAP),
            }
      }
    >
      {shown.map((card) => (
        <div
          key={card.id}
          className={drag?.id === card.id ? "grid-cell dragging" : "grid-cell"}
          style={
            free
              ? { position: "absolute", left: card.x, top: card.y, width: card.w, height: card.h }
              : {
                  gridColumn: `${card.x + 1} / span ${card.w}`,
                  gridRow: `${card.y + 1} / span ${card.h}`,
                }
          }
        >
          {editing ? (
            <>
              <button
                className="grid-handle"
                aria-label="Move this card"
                onPointerDown={(e) => {
                  e.preventDefault();
                  setDrag({ kind: "move", id: card.id, startX: e.clientX, startY: e.clientY, from: card });
                }}
              >
                ⠿
              </button>
              <button
                className="grid-resize"
                aria-label="Resize this card"
                onPointerDown={(e) => {
                  e.preventDefault();
                  setDrag({ kind: "size", id: card.id, startX: e.clientX, startY: e.clientY, from: card });
                }}
              >
                ⤡
              </button>
            </>
          ) : null}
          {children(card, editing)}
        </div>
      ))}
    </div>
  );
}

function clamp(value: number, low: number, high: number) {
  return Math.max(low, Math.min(high, value));
}

/**
 * Push whatever the moved card now overlaps downwards, then close the gaps.
 *
 * This is the whole of "cards never overlap": the one being dragged wins its
 * place, everything it lands on moves down, and afterwards every card falls as
 * far up as it can. Simple, and it behaves the way a person expects — nothing
 * teleports sideways.
 */
function settle(cards: Placed[], moved: string): Placed[] {
  const out = cards.map((c) => ({ ...c }));
  const order = [...out].sort((a, b) => (a.id === moved ? -1 : b.id === moved ? 1 : a.y - b.y || a.x - b.x));

  for (let i = 0; i < order.length; i++) {
    for (let j = i + 1; j < order.length; j++) {
      if (overlaps(order[i], order[j])) {
        order[j].y = order[i].y + order[i].h;
      }
    }
  }
  // Gravity: everything falls until it rests on something or on the top.
  for (const card of [...order].sort((a, b) => a.y - b.y)) {
    while (card.y > 0) {
      const lifted = { ...card, y: card.y - 1 };
      if (order.some((other) => other.id !== card.id && overlaps(lifted, other))) break;
      card.y -= 1;
    }
  }
  return out;
}

function overlaps(a: Placed, b: Placed) {
  return a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
}

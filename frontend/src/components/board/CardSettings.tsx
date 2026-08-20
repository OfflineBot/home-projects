import { useState } from "react";
import { ErrorBox, Field, Modal, Section } from "../ui";
import { Fields } from "../Fields";
import { colorVar } from "../../lib/theme";
import { api } from "../../lib/api";
import { useMeta } from "../../lib/store";
import HtmlCard from "./HtmlCard";
import type { Card, CardKind, CardStyle, Tab } from "./Board";

/**
 * What a card shows, how big it is, how it looks and who may see it.
 *
 * The fields are their own component because they are wanted in two places: in
 * a dialog over a grid, and in the panel beside a page that is being built.
 * The difference between those is where the Save is — here there is none, the
 * caller decides when to write.
 */
export interface CardDraft {
  options: Record<string, any>;
  style: CardStyle;
  visibility: Card["visibility"];
  w: number;
  h: number;
}

export function CardFields({
  card,
  kinds,
  layout,
  draft,
  onChange,
  part = "all",
}: {
  card: Card;
  kinds: CardKind[];
  /** What the tab is: a grid counts in columns and rows, a free surface in pixels. */
  layout: Tab["layout"];
  draft: CardDraft;
  onChange: (next: CardDraft) => void;
  /**
   * Which part of it. A dialog shows everything at once; a panel beside the
   * page shows one thing at a time, folded, or it is a mile long.
   */
  part?: "all" | "options" | "size" | "look" | "who";
}) {
  const meta = useMeta();
  const kind = kinds.find((k) => k.name === card.kind);
  const options = draft.options;
  const style = draft.style;
  const visibility = draft.visibility;
  const size = { w: draft.w, h: draft.h };
  const setOptions = (next: Record<string, any>) => onChange({ ...draft, options: next });
  const setStyle = (next: CardStyle) => onChange({ ...draft, style: next });
  const setVisibility = (next: Card["visibility"]) => onChange({ ...draft, visibility: next });
  const setSize = (next: { w: number; h: number }) => onChange({ ...draft, w: next.w, h: next.h });
  const here = (which: string) => part === "all" || part === which;

  return (
    <>
      {/* eslint-disable-next-line @typescript-eslint/no-explicit-any */}
      {/* The card's own settings, drawn by the one renderer. */}
      {here("options") ? (
        <>
          <Fields specs={kind?.options ?? []} values={options} onChange={setOptions} />
          {/* What it says on the page. It sat outside the folds before, which
              drew it once per fold and called it "Title", which is not what
              anybody is looking for when they want to rename a button. */}
          {card.kind === "html" ? (
            <Field label="How it looks">
              <div className="html-preview">
                <HtmlCard options={options} value={() => undefined} projects={[]} editing={false} />
              </div>
            </Field>
          ) : null}
          <Field label="What it says" hint="The name shown on the card itself.">
            <input
              value={options.title ?? ""}
              placeholder={kind?.title}
              onChange={(e) => setOptions({ ...options, title: e.target.value })}
            />
          </Field>
        </>
      ) : null}

      {here("size") ? (
        <>
      <Section title="How big" />
      {layout === "free" ? (
        <div className="row">
          <Field label="Wide" hint="In pixels, on this free surface.">
            <input
              type="number"
              min={40}
              value={size.w}
              onChange={(e) => setSize({ ...size, w: Number(e.target.value) })}
            />
          </Field>
          <Field label="High" hint="In pixels.">
            <input
              type="number"
              min={40}
              value={size.h}
              onChange={(e) => setSize({ ...size, h: Number(e.target.value) })}
            />
          </Field>
        </div>
      ) : (
        <>
          <div className="row">
            <Field label="Wide" hint="Columns, out of twelve.">
              <input
                type="number"
                min={1}
                max={12}
                value={size.w}
                onChange={(e) => setSize({ ...size, w: clampTo(Number(e.target.value), 1, 12) })}
              />
            </Field>
            <Field label="High" hint="Rows of about 92 pixels — and on a phone it keeps this height.">
              <input
                type="number"
                min={1}
                max={40}
                value={size.h}
                onChange={(e) => setSize({ ...size, h: clampTo(Number(e.target.value), 1, 40) })}
              />
            </Field>
          </div>
          <div className="row-buttons">
            {[
              { label: "a third", w: 4 },
              { label: "half", w: 6 },
              { label: "two thirds", w: 8 },
              { label: "the whole width", w: 12 },
            ].map((piece) => (
              <button
                key={piece.w}
                className={size.w === piece.w ? "btn small primary" : "btn small ghost"}
                onClick={() => setSize({ ...size, w: piece.w })}
              >
                {piece.label}
              </button>
            ))}
          </div>
        </>
      )}

        </>
      ) : null}
      {here("look") ? (
        <>
      <Section title="Look" />
      <div className="row">
        <Field label="Colour" optional>
          <div className="swatches">
            <button
              className={!style.color ? "swatch selected" : "swatch"}
              title="none"
              style={{ background: "var(--ctp-surface1)" }}
              onClick={() => setStyle({ ...style, color: undefined })}
            />
            {(meta?.colors ?? []).map((name) => (
              <button
                key={name}
                className={style.color === name ? "swatch selected" : "swatch"}
                style={{ background: colorVar(name) }}
                title={name}
                onClick={() => setStyle({ ...style, color: name })}
              />
            ))}
          </div>
        </Field>
      </div>
      <div className="row">
        <Field label="Corners" hint="Pixels. 0 is square, 24 is a pill.">
          <input
            type="number"
            min={0}
            max={40}
            value={style.radius ?? 12}
            onChange={(e) => setStyle({ ...style, radius: clampTo(Number(e.target.value), 0, 40) })}
          />
        </Field>
        <Field label="Its own colour" optional hint="Overrides the swatch above.">
          <input
            type="color"
            value={style.tint ?? "#89b4fa"}
            onChange={(e) => setStyle({ ...style, tint: e.target.value })}
          />
        </Field>
      </div>
      <div className="row">
        <Field label="Background">
          <select
            value={style.background ?? "plain"}
            onChange={(e) => setStyle({ ...style, background: e.target.value as CardStyle["background"] })}
          >
            <option value="plain">plain</option>
            <option value="tinted">tinted</option>
            <option value="bare">none</option>
          </select>
        </Field>
        <Field label="Text">
          <select
            value={style.size ?? "normal"}
            onChange={(e) => setStyle({ ...style, size: e.target.value as CardStyle["size"] })}
          >
            <option value="normal">normal</option>
            <option value="large">large</option>
          </select>
        </Field>
        <Field label="Aligned">
          <select
            value={style.align ?? "left"}
            onChange={(e) => setStyle({ ...style, align: e.target.value as CardStyle["align"] })}
          >
            <option value="left">left</option>
            <option value="center">centred</option>
          </select>
        </Field>
      </div>
      <label className="check">
        <input
          type="checkbox"
          checked={Boolean(style.boxed)}
          onChange={(e) => setStyle({ ...style, boxed: e.target.checked })}
        />
        <span>A box around it — on a page the cards have none by default</span>
      </label>

        </>
      ) : null}
      {here("who") ? (
        <>
      <Section title="Who may see it" />
      <Field label="" hint="Never wider than what it shows.">
        <select
          value={visibility}
          onChange={(e) => setVisibility(e.target.value as Card["visibility"])}
        >
          <option value="private">Private — only signed in</option>
          <option value="public">Public — anyone who opens the page</option>
          <option value="password">Password — once its project has been unlocked</option>
        </select>
      </Field>
        </>
      ) : null}
    </>
  );
}

/** The same fields in a dialog, for the layouts that are arranged rather than built. */
export function CardSettings({
  card,
  kinds,
  layout,
  onClose,
  onSaved,
}: {
  card: Card;
  kinds: CardKind[];
  layout: Tab["layout"];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [draft, setDraft] = useState<CardDraft>({
    options: card.options ?? {},
    style: card.style ?? {},
    visibility: card.visibility,
    w: card.w,
    h: card.h,
  });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const kind = kinds.find((k) => k.name === card.kind);

  return (
    <Modal
      title={kind?.title ?? card.kind}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn primary"
            disabled={busy}
            onClick={async () => {
              setBusy(true);
              setError(null);
              try {
                await api(`/api/boards/cards/${card.id}`, {
                  method: "PATCH",
                  body: {
                    options: draft.options,
                    style: draft.style,
                    visibility: draft.visibility,
                    w: draft.w,
                    h: draft.h,
                  },
                });
                onSaved();
              } catch (err) {
                setError(err as Error);
              } finally {
                setBusy(false);
              }
            }}
          >
            Save
          </button>
        </>
      }
    >
      <ErrorBox error={error} />
      <CardFields card={card} kinds={kinds} layout={layout} draft={draft} onChange={setDraft} />
    </Modal>
  );
}

function clampTo(value: number, low: number, high: number) {
  if (Number.isNaN(value)) return low;
  return Math.max(low, Math.min(high, value));
}

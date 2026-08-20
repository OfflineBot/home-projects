import { useEffect, useState } from "react";
import { Icon } from "../Icon";
import { ErrorBox, Field } from "../ui";
import { api, type Project } from "../../lib/api";
import { useQuery } from "../../lib/store";
import { CardFields, type CardDraft } from "./CardSettings";
import { SHAPES, type Section, type Shape } from "./Sections";
import type { Card, CardKind, Tab, TabStyle } from "./Board";

/**
 * Building a page, without a workshop around it.
 *
 * The panel beside the page was better than tools on every card, and still
 * wrong: something permanent at the edge is something permanently in the way,
 * and a page being built next to a column of thirty rows is a page you cannot
 * see. This is the third attempt and the one that holds.
 *
 * Nothing is permanent except a bar the height of a button, at the bottom,
 * where hands are. Everything else appears when it is asked for and goes away
 * when it is done with:
 *
 *   Add      a search over everything that could go on the page, in the middle
 *            of the screen, keyboard first — type three letters, press Enter.
 *   Selected the settings of the one thing you clicked, in a sheet that slides
 *            in from the side and closes again.
 *   Page     the sections and the page's own settings, in the same sheet.
 *
 * The page itself is never covered by any of it while you are not using it,
 * which is the whole point: what you are making is what you are looking at.
 */

export function BuildBar({
  tab,
  tabs,
  kinds,
  projects,
  group,
  project,
  chosen,
  sections,
  onChoose,
  onTab,
  onSections,
  onAdd,
  onChanged,
  onDone,
}: {
  tab: Tab;
  tabs: Tab[];
  kinds: CardKind[];
  projects: Project[];
  group?: string;
  project?: string;
  chosen: Card | null;
  sections: Section[];
  onChoose: (card: Card | null) => void;
  onTab: (index: number) => void;
  onSections: (next: Section[]) => void;
  onAdd: (kind: string, options: Record<string, any>) => Promise<void>;
  onChanged: () => void;
  onDone: () => void;
}) {
  const [open, setOpen] = useState<"" | "add" | "card" | "page">("");
  const [error, setError] = useState<Error | null>(null);
  const [saved, setSaved] = useState("");

  // Escape closes whatever is open, then leaves building. Nothing else on the
  // page listens for it, so it always means "back one step".
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        if (open) setOpen("");
        else onDone();
      }
      if (e.key.toLowerCase() === "a" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        setOpen("add");
      }
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [open, onDone]);

  const said = (what: string) => {
    setSaved(what);
    window.setTimeout(() => setSaved(""), 1500);
  };

  return (
    <>
      <div className="build-bar">
        <button className="btn small primary" onClick={() => setOpen(open === "add" ? "" : "add")}>
          <Icon name="plus" size={14} /> Add
        </button>

        {chosen ? (
          <button className="build-chip" onClick={() => setOpen(open === "card" ? "" : "card")}>
            <Icon name="settings" size={13} />
            {String(chosen.options?.title ?? "") || kinds.find((k) => k.name === chosen.kind)?.title || chosen.kind}
          </button>
        ) : (
          <span className="meta build-hint">click something on the page to change it</span>
        )}

        <span className="grow" />
        {saved ? <span className="meta">{saved}</span> : null}

        {tabs.length > 1 ? (
          <select
            className="build-tabs"
            aria-label="Which tab"
            value={tabs.findIndex((t) => t.id === tab.id)}
            onChange={(e) => onTab(Number(e.target.value))}
          >
            {tabs.map((t, i) => (
              <option key={t.id} value={i}>
                {t.title}
              </option>
            ))}
          </select>
        ) : null}

        <button className={open === "page" ? "btn small primary" : "btn small ghost"} onClick={() => setOpen(open === "page" ? "" : "page")}>
          <Icon name="grid" size={13} /> Page
        </button>
        <button className="btn small" onClick={onDone}>
          <Icon name="check" size={13} /> Done
        </button>
      </div>

      {open === "add" ? (
        <AddOverlay
          kinds={kinds}
          projects={projects}
          group={group}
          project={project}
          onClose={() => setOpen("")}
          onAdd={async (kind, options) => {
            try {
              await onAdd(kind, options);
              said("added");
              setOpen("");
            } catch (err) {
              setError(err as Error);
            }
          }}
        />
      ) : null}

      {open === "card" && chosen ? (
        <Sheet title="This card" onClose={() => setOpen("")}>
          <ErrorBox error={error} />
          <CardSheet
            card={chosen}
            kinds={kinds}
            layout={tab.layout}
            onSaved={() => {
              said("saved");
              onChanged();
            }}
            onFailed={setError}
            onRemoved={() => {
              onChoose(null);
              setOpen("");
              onChanged();
            }}
          />
        </Sheet>
      ) : null}

      {open === "page" ? (
        <Sheet title="This page" onClose={() => setOpen("")}>
          <ErrorBox error={error} />
          <PageSheet
            tab={tab}
            sections={sections}
            onSections={(next) => {
              onSections(next);
              said("saved");
            }}
            onChanged={onChanged}
            onFailed={setError}
          />
        </Sheet>
      ) : null}
    </>
  );
}

/** A sheet from the side: it covers a third of the page and closes again. */
function Sheet({
  title,
  onClose,
  children,
}: {
  title: string;
  onClose: () => void;
  children: React.ReactNode;
}) {
  return (
    <div className="sheet-backdrop" onClick={(e) => e.target === e.currentTarget && onClose()}>
      <aside className="sheet">
        <div className="sheet-head">
          <strong>{title}</strong>
          <span className="grow" />
          <button className="btn ghost icon" aria-label="Close" onClick={onClose}>
            <Icon name="x" size={15} />
          </button>
        </div>
        <div className="sheet-body">{children}</div>
      </aside>
    </div>
  );
}

/**
 * What can go on the page, as a search.
 *
 * A list of everything is a list nobody reads; a search is the same list with
 * the answer at the top. Yours first — the rules you wrote, the machines you
 * wrote down, the lamps you named — then what the server works out by itself,
 * folded away, because "automation_runs" is not what anybody came here for.
 */
function AddOverlay({
  kinds,
  projects,
  group,
  project,
  onClose,
  onAdd,
}: {
  kinds: CardKind[];
  projects: Project[];
  group?: string;
  project?: string;
  onClose: () => void;
  onAdd: (kind: string, options: Record<string, any>) => Promise<void>;
}) {
  const [find, setFind] = useState("");
  const [showReported, setShowReported] = useState(false);
  const mine = project
    ? projects.filter((p) => p.id === project)
    : group
      ? projects.filter((p) => p.groupSlug === group)
      : projects;

  const lights = useQuery<{ lights: { id: string; title: string; hosts: string[] }[] }>(
    "/api/capabilities/automation/lights",
  );

  const matches = (text: string) => !find || text.toLowerCase().includes(find.toLowerCase());

  const plain = kinds
    .filter((k) => !k.options?.some((o) => o.name === "projectId"))
    .filter((k) => matches(k.title + k.description));

  return (
    <div className="finder-backdrop" onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div className="finder">
        <div className="finder-find">
          <Icon name="search" size={16} />
          <input
            autoFocus
            placeholder="a lamp, a button, a picture, the time…"
            value={find}
            onChange={(e) => setFind(e.target.value)}
            onKeyDown={(e) => e.key === "Escape" && onClose()}
          />
          <button className="btn ghost icon" aria-label="Close" onClick={onClose}>
            <Icon name="x" size={15} />
          </button>
        </div>

        <div className="finder-body">
          {(lights.data?.lights ?? []).filter((l) => matches(l.title)).length ? (
            <Row tone="lights" title="Lights">
              {(lights.data?.lights ?? [])
                .filter((l) => matches(l.title))
                .map((light) => (
                  <Block
                    key={light.id}
                    icon="lightbulb"
                    title={light.title}
                    detail={`${light.hosts.length} lamps`}
                    onPick={() => void onAdd("light", { account: light.id, title: light.title })}
                  />
                ))}
            </Row>
          ) : null}

          {mine.map((p) => (
            <ProjectRows key={p.id} project={p} matches={matches} showReported={showReported} onAdd={onAdd} />
          ))}

          {plain.length ? (
            <Row tone="plain" title="The plain parts">
              {plain.map((k) => (
                <Block
                  key={k.name}
                  icon={k.icon || "box"}
                  title={k.title}
                  detail={k.description}
                  onPick={() => void onAdd(k.name, {})}
                />
              ))}
            </Row>
          ) : null}

          <button className="btn small ghost finder-more" onClick={() => setShowReported(!showReported)}>
            <Icon name={showReported ? "chevronUp" : "chevronDown"} size={13} />{" "}
            {showReported ? "hide what the server works out" : "also show what the server works out by itself"}
          </button>
        </div>
      </div>
    </div>
  );
}

function ProjectRows({
  project,
  matches,
  showReported,
  onAdd,
}: {
  project: Project;
  matches: (text: string) => boolean;
  showReported: boolean;
  onAdd: (kind: string, options: Record<string, any>) => Promise<void>;
}) {
  const offers = useQuery<{
    offers: { card: string; title: string; detail?: string; icon?: string; from?: string; options: Record<string, any> }[];
  }>(`/api/projects/${project.id}/offers`);
  const all = (offers.data?.offers ?? []).filter((o) => matches(o.title + (o.detail ?? "")));
  const yours = all.filter((o) => o.from !== "reported");
  const reported = all.filter((o) => o.from === "reported");

  return (
    <>
      {yours.length ? (
        <Row tone="mine" title={project.title}>
          {yours.map((offer, i) => (
            <Block
              key={offer.card + i}
              icon={offer.icon || "box"}
              title={offer.title}
              detail={offer.detail}
              onPick={() => void onAdd(offer.card, offer.options)}
            />
          ))}
        </Row>
      ) : null}
      {showReported && reported.length ? (
        <Row tone="quiet" title={`${project.title} · worked out by itself`}>
          {reported.map((offer, i) => (
            <Block
              key={offer.card + i}
              icon={offer.icon || "grid"}
              title={offer.title}
              detail={offer.detail}
              onPick={() => void onAdd(offer.card, offer.options)}
            />
          ))}
        </Row>
      ) : null}
    </>
  );
}

function Row({ tone, title, children }: { tone: string; title: string; children: React.ReactNode }) {
  return (
    <div className={`finder-row tone-${tone}`}>
      <div className="finder-row-head">
        <span className="panel-dot" />
        {title}
      </div>
      <div className="finder-blocks">{children}</div>
    </div>
  );
}

function Block({
  icon,
  title,
  detail,
  onPick,
}: {
  icon: string;
  title: string;
  detail?: string;
  onPick: () => void;
}) {
  return (
    <button className="finder-block" onClick={onPick} title={detail}>
      <Icon name={icon as never} size={16} />
      <span className="finder-block-title">{title}</span>
      {detail ? <span className="meta">{detail}</span> : null}
    </button>
  );
}

/** The selected card, in the sheet: four folds, written as they are changed. */
function CardSheet({
  card,
  kinds,
  layout,
  onSaved,
  onFailed,
  onRemoved,
}: {
  card: Card;
  kinds: CardKind[];
  layout: Tab["layout"];
  onSaved: () => void;
  onFailed: (e: Error) => void;
  onRemoved: () => void;
}) {
  const [draft, setDraft] = useState<CardDraft>({
    options: card.options ?? {},
    style: card.style ?? {},
    visibility: card.visibility,
    w: card.w,
    h: card.h,
  });

  useEffect(() => {
    setDraft({
      options: card.options ?? {},
      style: card.style ?? {},
      visibility: card.visibility,
      w: card.w,
      h: card.h,
    });
  }, [card.id]);

  useEffect(() => {
    const timer = window.setTimeout(async () => {
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
        onFailed(err as Error);
      }
    }, 600);
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draft]);

  const parts: { key: "options" | "size" | "look" | "who"; title: string; open?: boolean }[] = [
    { key: "options", title: "What it shows", open: true },
    { key: "size", title: "How big" },
    { key: "look", title: "How it looks" },
    { key: "who", title: "Who may see it" },
  ];

  return (
    <>
      {parts.map((part) => (
        <Fold key={part.key} title={part.title} open={part.open}>
          <CardFields card={card} kinds={kinds} layout={layout} draft={draft} onChange={setDraft} part={part.key} />
        </Fold>
      ))}
      <button
        className="btn small ghost sheet-remove"
        onClick={async () => {
          await api(`/api/boards/cards/${card.id}`, { method: "DELETE" });
          onRemoved();
        }}
      >
        <Icon name="trash" size={13} /> Remove this card
      </button>
    </>
  );
}

/** The page: its sections, how wide, how tall. */
function PageSheet({
  tab,
  sections,
  onSections,
  onChanged,
  onFailed,
}: {
  tab: Tab;
  sections: Section[];
  onSections: (next: Section[]) => void;
  onChanged: () => void;
  onFailed: (e: Error) => void;
}) {
  const style = (tab.style ?? {}) as TabStyle;
  const patch = async (next: Partial<TabStyle>) => {
    try {
      await api(`/api/boards/tabs/${tab.id}`, { method: "PATCH", body: { style: { ...style, ...next } } });
      onChanged();
    } catch (err) {
      onFailed(err as Error);
    }
  };

  return (
    <>
      <Fold title="Sections" open>
        {sections.map((section, i) => (
          <div key={i} className="sheet-section">
            <input
              value={section.title ?? ""}
              placeholder={`Section ${i + 1}`}
              onChange={(e) => onSections(sections.map((s, j) => (j === i ? { ...s, title: e.target.value } : s)))}
            />
            <select
              value={section.shape}
              aria-label="Columns"
              onChange={(e) =>
                onSections(sections.map((s, j) => (j === i ? { ...s, shape: e.target.value as Shape } : s)))
              }
            >
              {SHAPES.map((s) => (
                <option key={s.key} value={s.key}>
                  {s.title}
                </option>
              ))}
            </select>
            <button
              className={section.look === "band" ? "btn small primary" : "btn small ghost"}
              title="A tinted strip across the page"
              onClick={() =>
                onSections(sections.map((s, j) => (j === i ? { ...s, look: s.look === "band" ? "plain" : "band" } : s)))
              }
            >
              band
            </button>
            <button
              className="btn ghost icon"
              aria-label={`Remove section ${i + 1}`}
              disabled={sections.length === 1}
              onClick={() => {
                const next = sections.filter((_, j) => j !== i);
                next[Math.max(0, i - 1)].columns[0].push(...sections[i].columns.flat());
                onSections(next);
              }}
            >
              <Icon name="trash" size={13} />
            </button>
          </div>
        ))}
        <button className="btn small" onClick={() => onSections([...sections, { shape: "two", columns: [[], []] }])}>
          <Icon name="plus" size={13} /> Another section
        </button>
      </Fold>

      <Fold title="The page itself" open>
        <Field label="How wide">
          <select value={style.width ?? "wide"} onChange={(e) => void patch({ width: e.target.value as TabStyle["width"] })}>
            <option value="wide">full width</option>
            <option value="normal">normal</option>
            <option value="narrow">narrow — reads like a document</option>
          </select>
        </Field>
        <label className="check">
          <input type="checkbox" checked={Boolean(style.fill)} onChange={(e) => void patch({ fill: e.target.checked })} />
          <span>As tall as the window — the cards share the height</span>
        </label>
      </Fold>
    </>
  );
}

/** A part of a sheet, folded away until it is wanted. */
function Fold({ title, open, children }: { title: string; open?: boolean; children: React.ReactNode }) {
  const [shown, setShown] = useState(Boolean(open));
  return (
    <div className="panel-group tone-quiet">
      <button className="panel-group-head" onClick={() => setShown(!shown)}>
        <span className="grow">{title}</span>
        <Icon name={shown ? "chevronUp" : "chevronDown"} size={13} />
      </button>
      {shown ? <div className="panel-group-body">{children}</div> : null}
    </div>
  );
}

import { useEffect, useState } from "react";
import { Icon } from "../Icon";
import { ErrorBox, Field, Spinner } from "../ui";
import { api, type Project } from "../../lib/api";
import { useQuery } from "../../lib/store";
import { CardFields, type CardDraft } from "./CardSettings";
import { SHAPES, type Section, type Shape } from "./Sections";
import type { Card, CardKind, Tab, TabStyle } from "./Board";

/**
 * Building a page, in one panel beside it.
 *
 * The old way put the tools on the page: a grip, a gear and a cross on every
 * card, a bar of buttons over every section, a dialog on top of everything when
 * you wanted to change a word. The page you were building was never the page
 * you would get, and it looked like a workshop.
 *
 * This is the other way round, and it is how every tool that people enjoy using
 * does it: the page stays exactly as it will be, one thing is selected at a
 * time, and everything about that thing is in a panel at the side. Nothing
 * covers the page, nothing is saved by hand — a change is written when it is
 * made, and the panel says so.
 *
 * Three tabs, because there are three things anybody is ever changing: what is
 * on the page, the thing that is selected, and the page itself.
 */

type Where = "add" | "card" | "page";

export function Builder({
  tab,
  kinds,
  projects,
  group,
  project,
  chosen,
  sections,
  onChoose,
  onSections,
  onAdd,
  onChanged,
  onDone,
}: {
  tab: Tab;
  kinds: CardKind[];
  projects: Project[];
  group?: string;
  project?: string;
  chosen: Card | null;
  sections: Section[];
  onChoose: (card: Card | null) => void;
  onSections: (next: Section[]) => void;
  onAdd: (kind: string, options: Record<string, any>) => Promise<void>;
  onChanged: () => void;
  onDone: () => void;
}) {
  const [where, setWhere] = useState<Where>("add");
  const [error, setError] = useState<Error | null>(null);
  const [saved, setSaved] = useState("");

  // Selecting something takes you to it; there is no second click for that.
  useEffect(() => {
    if (chosen) setWhere("card");
  }, [chosen?.id]);

  const said = (what: string) => {
    setSaved(what);
    window.setTimeout(() => setSaved(""), 1600);
  };

  return (
    <aside className="page-builder">
      <div className="page-builder-tabs">
        {(
          [
            ["add", "Add", "plus"],
            ["card", chosen ? "Selected" : "Nothing selected", "settings"],
            ["page", "Page", "grid"],
          ] as [Where, string, string][]
        ).map(([key, label, icon]) => (
          <button
            key={key}
            className={where === key ? "page-builder-tab on" : "page-builder-tab"}
            disabled={key === "card" && !chosen}
            onClick={() => setWhere(key)}
          >
            <Icon name={icon as never} size={14} /> {label}
          </button>
        ))}
        <span className="grow" />
        {saved ? <span className="meta">{saved}</span> : null}
        <button className="btn small primary" onClick={onDone}>
          <Icon name="check" size={13} /> Done
        </button>
      </div>

      <ErrorBox error={error} />

      <div className="page-builder-body">
        {where === "add" ? (
          <AddPanel
            kinds={kinds}
            projects={projects}
            group={group}
            project={project}
            onAdd={async (kind, options) => {
              try {
                await onAdd(kind, options);
                said("added");
              } catch (err) {
                setError(err as Error);
              }
            }}
          />
        ) : null}

        {where === "card" && chosen ? (
          <CardPanel
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
              onChanged();
            }}
          />
        ) : null}

        {where === "page" ? (
          <PagePanel
            tab={tab}
            sections={sections}
            onSections={(next) => {
              onSections(next);
              said("saved");
            }}
            onChanged={onChanged}
            onFailed={setError}
          />
        ) : null}
      </div>
    </aside>
  );
}

/** What can be put on the page: this group's own things first, then the plain parts. */
function AddPanel({
  kinds,
  projects,
  group,
  project,
  onAdd,
}: {
  kinds: CardKind[];
  projects: Project[];
  group?: string;
  project?: string;
  onAdd: (kind: string, options: Record<string, any>) => Promise<void>;
}) {
  const [find, setFind] = useState("");
  const mine = project
    ? projects.filter((p) => p.id === project)
    : group
      ? projects.filter((p) => p.groupSlug === group)
      : projects;

  return (
    <>
      <Field label="">
        <input
          placeholder="what are you looking for?"
          value={find}
          onChange={(e) => setFind(e.target.value)}
        />
      </Field>

      <LightAccounts find={find} onAdd={onAdd} />

      {mine.map((p) => (
        <ProjectOffers key={p.id} project={p} find={find} onAdd={onAdd} />
      ))}

      <h3 className="page-builder-heading">The plain parts</h3>
      <div className="page-builder-blocks">
        {kinds
          .filter((k) => !k.options?.some((o) => o.name === "projectId"))
          .filter((k) => !find || (k.title + k.description).toLowerCase().includes(find.toLowerCase()))
          .map((k) => (
            <button key={k.name} className="page-builder-block" onClick={() => void onAdd(k.name, {})}>
              <Icon name={(k.icon || "box") as never} size={15} />
              <span className="grow">{k.title}</span>
              <Icon name="plus" size={13} />
            </button>
          ))}
      </div>
    </>
  );
}

/**
 * The light accounts: a name and its lamps, switched together.
 *
 * They are not a project's, so they are not among a project's offers — they
 * belong to the house and are listed first, because "all the lights" is the
 * thing anybody puts on a page before anything else.
 */
function LightAccounts({
  find,
  onAdd,
}: {
  find: string;
  onAdd: (kind: string, options: Record<string, any>) => Promise<void>;
}) {
  const lights = useQuery<{ lights: { id: string; title: string; hosts: string[] }[] }>(
    "/api/capabilities/automation/lights",
  );
  const list = (lights.data?.lights ?? []).filter(
    (l) => !find || l.title.toLowerCase().includes(find.toLowerCase()),
  );
  if (!list.length) return null;
  return (
    <>
      <h3 className="page-builder-heading">Lights</h3>
      <div className="page-builder-blocks">
        {list.map((light) => (
          <button
            key={light.id}
            className="page-builder-block"
            onClick={() => void onAdd("light", { account: light.id, title: light.title })}
          >
            <Icon name="lightbulb" size={15} />
            <span className="grow">
              {light.title}
              <span className="meta"> · {light.hosts.length} lamps</span>
            </span>
            <Icon name="plus" size={13} />
          </button>
        ))}
      </div>
    </>
  );
}

/** Everything one project has ready to put on a page. */
function ProjectOffers({
  project,
  find,
  onAdd,
}: {
  project: Project;
  find: string;
  onAdd: (kind: string, options: Record<string, any>) => Promise<void>;
}) {
  const offers = useQuery<{ offers: { card: string; title: string; detail?: string; icon?: string; options: Record<string, any> }[] }>(
    `/api/projects/${project.id}/offers`,
  );
  const list = (offers.data?.offers ?? []).filter(
    (o) => !find || (o.title + (o.detail ?? "")).toLowerCase().includes(find.toLowerCase()),
  );
  if (offers.loading) return <Spinner />;
  if (!list.length) return null;

  return (
    <>
      <h3 className="page-builder-heading">{project.title}</h3>
      <div className="page-builder-blocks">
        {list.map((offer, i) => (
          <button
            key={offer.card + i}
            className="page-builder-block"
            title={offer.detail}
            onClick={() => void onAdd(offer.card, offer.options)}
          >
            <Icon name={(offer.icon || "box") as never} size={15} />
            <span className="grow">
              {offer.title}
              {offer.detail ? <span className="meta"> · {offer.detail}</span> : null}
            </span>
            <Icon name="plus" size={13} />
          </button>
        ))}
      </div>
    </>
  );
}

/** The selected card: its own settings, written as they are changed. */
function CardPanel({
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

  // A different card selected means a different draft.
  useEffect(() => {
    setDraft({
      options: card.options ?? {},
      style: card.style ?? {},
      visibility: card.visibility,
      w: card.w,
      h: card.h,
    });
  }, [card.id]);

  // Written a moment after the typing stops. No Save button: a page builder
  // that can lose what was typed is one nobody trusts with a long text.
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

  return (
    <>
      <CardFields card={card} kinds={kinds} layout={layout} draft={draft} onChange={setDraft} />
      <button
        className="btn small ghost page-builder-remove"
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

/** The page itself: its sections, how wide it is, whether it fills the screen. */
function PagePanel({
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
      await api(`/api/boards/tabs/${tab.id}`, {
        method: "PATCH",
        body: { style: { ...style, ...next } },
      });
      onChanged();
    } catch (err) {
      onFailed(err as Error);
    }
  };

  return (
    <>
      <h3 className="page-builder-heading">Sections</h3>
      {sections.map((section, i) => (
        <div key={i} className="page-builder-section">
          <input
            value={section.title ?? ""}
            placeholder={`Section ${i + 1}`}
            onChange={(e) =>
              onSections(sections.map((s, j) => (j === i ? { ...s, title: e.target.value } : s)))
            }
          />
          <select
            value={section.shape}
            aria-label="Columns"
            onChange={(e) =>
              onSections(
                sections.map((s, j) =>
                  j === i ? { ...s, shape: e.target.value as Shape, columns: s.columns } : s,
                ),
              )
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
              onSections(
                sections.map((s, j) => (j === i ? { ...s, look: s.look === "band" ? "plain" : "band" } : s)),
              )
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
      <button
        className="btn small"
        onClick={() => onSections([...sections, { shape: "two", columns: [[], []] }])}
      >
        <Icon name="plus" size={13} /> Another section
      </button>

      <h3 className="page-builder-heading">The page</h3>
      <Field label="How wide">
        <select value={style.width ?? "wide"} onChange={(e) => void patch({ width: e.target.value as TabStyle["width"] })}>
          <option value="wide">full width</option>
          <option value="normal">normal</option>
          <option value="narrow">narrow — reads like a document</option>
        </select>
      </Field>
      <label className="check">
        <input
          type="checkbox"
          checked={Boolean(style.fill)}
          onChange={(e) => void patch({ fill: e.target.checked })}
        />
        <span>As tall as the window — the cards share the height</span>
      </label>
    </>
  );
}

import { Suspense, useCallback, useRef, useState } from "react";
import { Icon } from "../Icon";
import { Empty, ErrorBox, Field, Modal, Section, Spinner, useAsk } from "../ui";
import { colorVar } from "../../lib/theme";
import { api, type Project, type Variable } from "../../lib/api";
import { useMeta, useQuery, useSession } from "../../lib/store";
import { useLive } from "../../lib/live";
import { Grid, type Placed } from "./Grid";
import { CodeArea } from "../CodeArea";
import HtmlCard from "./HtmlCard";
import { cardViews } from "./cards";

/**
 * A board: tabs of cards, arranged by whoever owns it.
 *
 * The same component is the front page and a group's page — the only
 * difference is which board it asks for. Reading is the normal state; editing
 * is a switch you flip, and then the cards grow handles.
 */

/** The small set of looks a card may choose. Not free CSS — a palette. */
export interface CardStyle {
  color?: string;
  background?: "plain" | "tinted" | "bare";
  border?: boolean;
  size?: "normal" | "large";
  align?: "left" | "center";
}

export interface Card {
  id: string;
  tabId: string;
  kind: string;
  options: Record<string, any>;
  style?: CardStyle;
  visibility: "private" | "public" | "password";
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface Tab {
  id: string;
  title: string;
  icon: string;
  style?: TabStyle;
  /** How its cards lie — or "page", where the tab is one document. */
  layout?: "grid" | "flow" | "free" | "page";
  position: number;
  cards: Card[];
}

interface BoardData {
  id: string;
  scope: string;
  groupId?: string;
  tabs: Tab[];
}

interface CardKind {
  name: string;
  title: string;
  icon: string;
  description?: string;
  options?: {
    name: string;
    label: string;
    type: string;
    placeholder?: string;
    hint?: string;
    required?: boolean;
    options?: { value: string; label: string }[];
  }[];
  w?: number;
  h?: number;
  from?: string;
}

interface Offer {
  card: string;
  title: string;
  icon?: string;
  detail?: string;
  options: Record<string, any>;
  w?: number;
  h?: number;
}

interface Block {
  group: { id: string; slug: string; title: string };
  variables: Variable[];
  derived: { name: string; value: unknown; unit?: string; type: string }[];
}

export default function Board({
  group,
  project,
  title,
  emptyNote,
  exposed,
}: {
  group?: string;
  /** A project's own home page. */
  project?: string;
  /** Shown at the head of the board, beside its tabs. */
  title?: string;
  emptyNote?: string;
  /** Handed to somebody: read it, that is all. */
  exposed?: boolean;
}) {
  const session = useSession();
  const where = group
    ? `?group=${encodeURIComponent(group)}`
    : project
      ? `?project=${encodeURIComponent(project)}`
      : "";
  const board = useQuery<BoardData>(`/api/boards${where}`);
  const kinds = useQuery<{ cards: CardKind[] }>("/api/boards/cards");
  const projects = useQuery<{ projects: Project[] }>("/api/projects");
  const reported = useQuery<{ groups: Block[] }>("/api/dashboard");

  // A board shows what the projects report, so it follows them: when a value
  // changes or a run finishes, the numbers on it are the numbers as they are
  // now. Only that one query is asked again — the cards themselves have not
  // moved.
  useLive((event) => {
    if (event.kind === "variable.changed" || event.kind === "scheduler.finished") reported.reload();
  });

  const ask = useAsk();
  const [editing, setEditing] = useState(false);
  const [tab, setTab] = useState(0);
  const [adding, setAdding] = useState(false);
  const [settling, setSettling] = useState<Card | null>(null);
  const [tabSettings, setTabSettings] = useState<Tab | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const [busy, setBusy] = useState(false);

  const tabs = board.data?.tabs ?? [];
  const current = tabs[Math.min(tab, Math.max(0, tabs.length - 1))];

  /** Every variable there is, by "group/project.name" and by "project.name". */
  const value = useCallback(
    (name: string, groupId?: string): Variable | undefined => {
      const blocks = reported.data?.groups ?? [];
      for (const block of blocks) {
        if (groupId && block.group.id !== groupId) continue;
        const [slug, ...rest] = name.split(".");
        const found = block.variables.find((v) => v.projectSlug === slug && v.name === rest.join("."));
        if (found) return found;
        const derived = block.derived.find((d) => d.name === name);
        if (derived) {
          return { name, value: derived.value, unit: derived.unit ?? "", type: derived.type } as Variable;
        }
      }
      return undefined;
    },
    [reported.data],
  );

  const saveLayout = useCallback(
    async (next: Placed[]) => {
      if (!board.data) return;
      try {
        await api(`/api/boards/${board.data.id}/layout`, { method: "PUT", body: { cards: next } });
        board.reload();
      } catch (err) {
        setError(err as Error);
      }
    },
    [board],
  );

  /** In flow, order is what y says: swapping two is the whole move. */
  const shift = async (index: number, by: number) => {
    if (!current || !board.data) return;
    const list = [...current.cards];
    const other = index + by;
    if (other < 0 || other >= list.length) return;
    [list[index], list[other]] = [list[other], list[index]];
    await api(`/api/boards/${board.data.id}/layout`, {
      method: "PUT",
      body: { cards: list.map((c, i) => ({ id: c.id, x: 0, y: i, w: c.w, h: c.h })) },
    });
    board.reload();
  };

  const setWidth = async (card: Card, w: number) => {
    await api(`/api/boards/cards/${card.id}`, { method: "PATCH", body: { w } });
    board.reload();
  };

  if (board.loading && !board.data) return <Spinner />;

  const look = tabStyle(current);

  return (
    <div className={`board width-${look.width}`}>
      <div className="board-bar">
        {title ? <h1 className="board-title">{title}</h1> : null}
        <div className="board-tabs">
          {tabs.map((t, i) => (
            <button
              key={t.id}
              className={i === tab ? "board-tab on" : "board-tab"}
              onClick={() => (i === tab && editing ? setTabSettings(t) : setTab(i))}
              title={i === tab && editing ? "Set this tab up" : undefined}
            >
              <Icon name={t.icon || "grid"} size={14} /> {t.title}
              {i === tab && editing ? <Icon name="settings" size={12} /> : null}
            </button>
          ))}
          {editing && board.data && !exposed ? (
            <button
              className="board-tab add"
              aria-label="Another tab"
              onClick={async () => {
                const title = await ask.text({
                  title: "A new tab",
                  label: "Name",
                  value: "Tab",
                });
                if (!title) return;
                await api(`/api/boards/${board.data!.id}/tabs`, { body: { title } });
                board.reload();
              }}
            >
              <Icon name="plus" size={14} />
            </button>
          ) : null}
        </div>

        <span className="grow" />

        {session.user && !exposed ? (
          editing ? (
            <>
              <button className="btn small" onClick={() => setAdding(true)}>
                <Icon name="plus" size={14} /> Add a card
              </button>
              {/* How wide, where it can be seen. It was only in the tab's
                  settings, two clicks in, which is why a board that was too
                  narrow looked like something that could not be changed. */}
              {current && !project ? (
                <select
                  className="board-width"
                  title="How wide this board is"
                  value={look.width}
                  onChange={async (e) => {
                    const width = e.target.value as TabStyle["width"];
                    await api(`/api/boards/tabs/${current.id}`, {
                      method: "PATCH",
                      body: { style: { ...(current.style ?? {}), width } },
                    });
                    board.reload();
                  }}
                >
                  <option value="wide">full width</option>
                  <option value="normal">normal</option>
                  <option value="narrow">narrow</option>
                </select>
              ) : null}
              {current ? (
                <button className="btn small ghost" onClick={() => setTabSettings(current)}>
                  <Icon name="settings" size={14} /> This tab
                </button>
              ) : null}
              <button className="btn small primary" onClick={() => setEditing(false)}>
                <Icon name="check" size={14} /> Done
              </button>
            </>
          ) : (
            <button className="btn small" onClick={() => setEditing(true)}>
              <Icon name="settings" size={14} /> Edit
            </button>
          )
        ) : null}
      </div>

      <ErrorBox error={error ?? board.error} onRetry={board.reload} />

      {current && current.cards.length === 0 ? (
        <Empty icon="grid">
          {exposed ? "Nothing here is public." : emptyNote ?? "Nothing on this board yet."}
          {session.user && !exposed ? (
            <div style={{ marginTop: 10, display: "flex", gap: 8, justifyContent: "center", flexWrap: "wrap" }}>
              <button
                className="btn small primary"
                disabled={busy}
                onClick={async () => {
                  if (!board.data || !current) return;
                  setBusy(true);
                  try {
                    await api(`/api/boards/${board.data.id}/fill?tab=${current.id}`, { body: {} });
                    board.reload();
                  } catch (err) {
                    setError(err as Error);
                  } finally {
                    setBusy(false);
                  }
                }}
              >
                <Icon name="zap" size={14} /> Fill it with what I have
              </button>
              <button className="btn small" onClick={() => { setEditing(true); setAdding(true); }}>
                <Icon name="plus" size={14} /> Put one thing on it
              </button>
            </div>
          ) : null}
        </Empty>
      ) : null}

      {editing && current && current.cards.length > 0 && current.layout !== "page" ? (
        <button className="add-here" onClick={() => setAdding(true)}>
          <Icon name="plus" size={16} /> Add a card
        </button>
      ) : null}

      {current && current.layout === "page" ? (
        <PageTab
          group={group}
          tab={current}
          editing={editing && !exposed}
          value={value}
          projects={projects.data?.projects ?? []}
        />
      ) : current && current.layout === "flow" ? (
        <div className="flow">
          {current.cards.map((card, i) => (
            <div
              key={card.id}
              className="flow-item"
              style={{ flexBasis: `calc(${Math.min(100, Math.round((card.w / 12) * 100))}% - 12px)` }}
            >
              {editing ? (
                <div className="card-tools">
                  <span className="widths">
                    {WIDTHS.map((w) => (
                      <button
                        key={w.value}
                        className={card.w === w.value ? "width on" : "width"}
                        title={w.label}
                        onClick={() => void setWidth(card, w.value)}
                      >
                        {w.short}
                      </button>
                    ))}
                  </span>
                  <button className="btn ghost icon" aria-label="Up" onClick={() => void shift(i, -1)}>
                    <Icon name="chevronUp" size={13} />
                  </button>
                  <button className="btn ghost icon" aria-label="Down" onClick={() => void shift(i, 1)}>
                    <Icon name="chevronDown" size={13} />
                  </button>
                  <button
                    className="btn ghost icon"
                    aria-label="Settings for this card"
                    onClick={() => setSettling(card)}
                  >
                    <Icon name="settings" size={13} />
                  </button>
                  <button
                    className="btn ghost icon"
                    aria-label="Remove this card"
                    onClick={async () => {
                      await api(`/api/boards/cards/${card.id}`, { method: "DELETE" });
                      board.reload();
                    }}
                  >
                    <Icon name="x" size={14} />
                  </button>
                </div>
              ) : null}
              <CardBody card={card} value={value} projects={projects.data?.projects ?? []} editing={editing} />
            </div>
          ))}
        </div>
      ) : current ? (
        <Grid
          cards={current.cards.map((c) => ({ id: c.id, x: c.x, y: c.y, w: c.w, h: c.h }))}
          editing={editing}
          free={current.layout === "free"}
          onChange={saveLayout}
        >
          {(placed) => {
            const card = current.cards.find((c) => c.id === placed.id);
            if (!card) return null;
            const look = dress(card.style);
            return (
              <div className={`${look.className} card-${card.kind}`} style={look.style}>
                {editing ? (
                  <div className="card-tools">
                    <button
                      className="btn ghost icon"
                      aria-label="Settings for this card"
                      onClick={() => setSettling(card)}
                    >
                      <Icon name="settings" size={13} />
                    </button>
                    <button
                      className="btn ghost icon"
                      aria-label="Remove this card"
                      onClick={async () => {
                        await api(`/api/boards/cards/${card.id}`, { method: "DELETE" });
                        board.reload();
                      }}
                    >
                      <Icon name="x" size={14} />
                    </button>
                  </div>
                ) : null}
                <CardInner card={card} value={value} projects={projects.data?.projects ?? []} editing={editing} />
              </div>
            );
          }}
        </Grid>
      ) : null}

      {adding && board.data && current ? (
        <AddCard
          kinds={kinds.data?.cards ?? []}
          projects={projects.data?.projects ?? []}
          group={group}
          project={project}
          blocks={reported.data?.groups ?? []}
          onClose={() => setAdding(false)}
          onAdd={async (kind, options, size) => {
            const fallback = (kinds.data?.cards ?? []).find((k) => k.name === kind);
            const loose = current.layout === "free";
            // On a free surface a card is measured in pixels, so a fresh one
            // arrives at a sensible size instead of three columns wide.
            const y = loose
              ? Math.max(0, ...current.cards.map((c) => c.y + c.h)) + 16
              : Math.max(0, ...current.cards.map((c) => c.y + c.h));
            await api("/api/boards/cards", {
              body: {
                tabId: current.id,
                kind,
                options,
                x: 0,
                y,
                w: loose ? ((size?.w ?? fallback?.w ?? 3) * 90) : (size?.w ?? fallback?.w ?? 3),
                h: loose ? ((size?.h ?? fallback?.h ?? 2) * 92) : (size?.h ?? fallback?.h ?? 2),
              },
            });
            setAdding(false);
            board.reload();
          }}
        />
      ) : null}

      {tabSettings ? (
        <TabSettings
          tab={tabSettings}
          canRemove={tabs.length > 1}
          onClose={() => setTabSettings(null)}
          onSaved={() => {
            setTabSettings(null);
            board.reload();
          }}
          onRemoved={() => {
            setTabSettings(null);
            setTab(0);
            board.reload();
          }}
        />
      ) : null}

      {settling ? (
        <CardSettings
          card={settling}
          kinds={kinds.data?.cards ?? []}
          layout={current?.layout ?? "grid"}
          onClose={() => setSettling(null)}
          onSaved={() => {
            setSettling(null);
            board.reload();
          }}
        />
      ) : null}
    </div>
  );
}

/**
 * Putting something on the board.
 *
 * Pick the project, then pick what of it: the button that is already in there,
 * the average out of the grades, the machine, the terminal. Nobody should have
 * to know that an average is a "number" card pointing at a variable.
 *
 * The list comes from the project itself — every capability says what it has —
 * so a new capability turns up here on its own. Underneath sit the few cards
 * that belong to no project: a note, some links, a heading.
 */
function AddCard({
  kinds,
  projects,
  group,
  project,
  onClose,
  onAdd,
}: {
  kinds: CardKind[];
  projects: Project[];
  /** The group this board belongs to, when it belongs to one. */
  group?: string;
  /** The project this board belongs to, when it is a project's own page. */
  project?: string;
  blocks: Block[];
  onClose: () => void;
  onAdd: (kind: string, options: Record<string, any>, size?: { w: number; h: number }) => Promise<void>;
}) {
  const [projectId, setProjectId] = useState(project ?? "");
  // A group's board is about that group. Everything else is possible and not
  // what this board is for, so it is one tick-box away rather than in the list.
  const [outside, setOutside] = useState(false);
  const mine = group ? projects.filter((p) => p.groupSlug === group) : projects;
  const offered = outside ? projects : mine;
  const [free, setFree] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [options, setOptions] = useState<Record<string, any>>({});
  const offers = useQuery<{ offers: Offer[] }>(projectId ? `/api/projects/${projectId}/offers` : null);
  const chosen = kinds.find((k) => k.name === free);

  const place = async (kind: string, opts: Record<string, any>, size?: { w: number; h: number }) => {
    setBusy(true);
    setError(null);
    try {
      await onAdd(kind, opts, size);
    } catch (err) {
      setError(err as Error);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      title="Add a card"
      onClose={onClose}
      wide
      footer={
        free ? (
          <>
            <button className="btn" onClick={() => setFree("")}>Back</button>
            <button
              className="btn primary"
              disabled={busy}
              onClick={() => place(free, options)}
            >
              Put it on
            </button>
          </>
        ) : (
          <button className="btn" onClick={onClose}>Cancel</button>
        )
      }
    >
      <ErrorBox error={error} />

      {!free ? (
        <>
          <Field label="From which project" required>
            <select value={projectId} onChange={(e) => setProjectId(e.target.value)}>
              <option value="">— pick one —</option>
              {byGroup(offered).map(([where, list]) => (
                <optgroup key={where} label={where}>
                  {list.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.title}
                    </option>
                  ))}
                </optgroup>
              ))}
            </select>
          </Field>

          {group && projects.length > mine.length ? (
            <label className="check">
              <input type="checkbox" checked={outside} onChange={(e) => setOutside(e.target.checked)} />
              <span>Also projects from other groups</span>
            </label>
          ) : null}

          {projectId ? (
            offers.loading && !offers.data ? (
              <Spinner />
            ) : (
              <div className="offers">
                {(offers.data?.offers ?? []).map((offer, i) => (
                  <button
                    key={offer.card + offer.title + i}
                    className="offer"
                    disabled={busy}
                    onClick={() =>
                      place(offer.card, offer.options, offer.w ? { w: offer.w, h: offer.h ?? 2 } : undefined)
                    }
                  >
                    <Icon name={offer.icon || "grid"} size={17} />
                    <span className="grow">
                      <strong>{offer.title}</strong>
                      {offer.detail ? <span className="meta"> {offer.detail}</span> : null}
                    </span>
                    <Icon name="plus" size={14} />
                  </button>
                ))}
                {offers.data && offers.data.offers.length === 0 ? (
                  <p className="meta">This project has nothing to show yet.</p>
                ) : null}
              </div>
            )
          ) : null}

          <div className="board-free">
            <span className="meta">or something of your own:</span>
            {kinds
              .filter((k) => ["text", "link", "heading"].includes(k.name))
              .map((k) => (
                <button key={k.name} className="btn small" onClick={() => { setFree(k.name); setOptions({}); }}>
                  <Icon name={k.icon || "grid"} size={14} /> {k.title}
                </button>
              ))}
          </div>
        </>
      ) : (
        <>
          {chosen?.options?.map((option) =>
            option.type === "code" ? (
              <Field key={option.name} label={option.label} hint={option.hint}>
                <CodeArea value={options[option.name] ?? ""} onChange={(text) => setOptions({ ...options, [option.name]: text })} />
              </Field>
            ) : option.type === "select" ? (
              <Field key={option.name} label={option.label} hint={option.hint}>
                <select
                  value={options[option.name] ?? ""}
                  onChange={(e) => setOptions({ ...options, [option.name]: e.target.value })}
                >
                  {(option.options ?? []).map((o) => (
                    <option key={o.value} value={o.value}>
                      {o.label}
                    </option>
                  ))}
                </select>
              </Field>
            ) : (
            <Field key={option.name} label={option.label} hint={option.hint} required={option.required}>
              {option.type === "textarea" ? (
                <textarea
                  value={options[option.name] ?? ""}
                  placeholder={option.placeholder}
                  autoFocus
                  style={{ minHeight: 120 }}
                  onChange={(e) => setOptions({ ...options, [option.name]: e.target.value })}
                />
              ) : (
                <input
                  value={options[option.name] ?? ""}
                  placeholder={option.placeholder}
                  autoFocus
                  onChange={(e) => setOptions({ ...options, [option.name]: e.target.value })}
                />
              )}
            </Field>
            ),
          )}
        </>
      )}
    </Modal>
  );
}

/**
 * What can be dropped into a page.
 *
 * The same offers a board picks from — this project's machine, that terminal,
 * the average out of the grades — except here they come out as a line of HTML
 * rather than a card on a grid. Both roads lead to the same thing: write it by
 * hand if you know the tag, press a button if you would rather not.
 */
function Palette({ group, onInsert }: { group?: string; onInsert: (snippet: string) => void }) {
  const projects = useQuery<{ projects: Project[] }>("/api/projects");
  const [projectId, setProjectId] = useState("");
  const offers = useQuery<{ offers: { card: string; title: string; detail?: string; options: Record<string, any> }[] }>(
    projectId ? `/api/projects/${projectId}/offers` : null,
  );
  const mine = group
    ? (projects.data?.projects ?? []).filter((p) => p.groupSlug === group)
    : (projects.data?.projects ?? []);

  const asTag = (card: string, options: Record<string, any>) => {
    const attrs: string[] = [`kind="${card}"`];
    for (const [key, raw] of Object.entries(options)) {
      if (raw === undefined || raw === null || raw === "") continue;
      if (key === "title") continue;
      const name = key === "projectId" ? "project" : key;
      attrs.push(`${name}="${String(raw).replace(/"/g, "&quot;")}"`);
    }
    return `<hp-card ${attrs.join(" ")}></hp-card>`;
  };

  return (
    <div className="palette">
      <span className="meta">Drop in:</span>
      <select value={projectId} onChange={(e) => setProjectId(e.target.value)}>
        <option value="">— from which project —</option>
        {mine.map((p) => (
          <option key={p.id} value={p.id}>
            {(p.groupSlug ?? "ungrouped") + "/" + p.slug}
          </option>
        ))}
      </select>
      {(offers.data?.offers ?? []).map((offer, i) => (
        <button
          key={offer.card + i}
          className="btn small ghost"
          title={offer.detail}
          onClick={() =>
            onInsert(
              offer.card === "number" || offer.card === "status"
                ? `{{${offer.options.variable}}}`
                : asTag(offer.card, offer.options),
            )
          }
        >
          <Icon name="plus" size={12} /> {offer.title}
        </button>
      ))}
      <span className="grow" />
      {[
        { label: "Heading", snippet: "<h2>A heading</h2>" },
        { label: "A row", snippet: '<div class="row">\n  \n  \n</div>' },
        {
          label: "Sides",
          snippet:
            '<div class="sides">\n' +
            '  <div class="top"></div>\n' +
            '  <div class="left"></div>\n' +
            '  <div class="main"></div>\n' +
            '  <div class="right"></div>\n' +
            '  <div class="bottom"></div>\n' +
            "</div>",
        },
        { label: "Columns", snippet: '<div class="cols" style="--cols:3">\n  \n  \n  \n</div>' },
        { label: "Button", snippet: '<a class="btn primary" href="/groups">A button</a>' },
      ].map((piece) => (
        <button key={piece.label} className="btn small ghost" onClick={() => onInsert(piece.snippet)}>
          {piece.label}
        </button>
      ))}
    </div>
  );
}

/**
 * A tab that is one page.
 *
 * Reading it is the page itself; editing it is the source on the left and what
 * it will look like on the right, saved with one button or Ctrl-S. It is the
 * same document an assistant writes through /api/page, so a person and a
 * program are never looking at two different things.
 */
function PageTab({
  group,
  tab,
  editing,
  value,
  projects,
}: {
  group?: string;
  tab: Tab;
  editing: boolean;
  // A page knows what the rest of the board knows: which projects there are,
  // and what every number says. Without them its cards are strangers.
  value: (variable: string, groupId?: string) => Variable | undefined;
  projects: Project[];
}) {
  const where = `/api/page?${group ? `group=${encodeURIComponent(group)}&` : ""}tab=${tab.id}`;
  const page = useQuery<{ html: string }>(where);
  const [draft, setDraft] = useState<string | null>(null);
  const [saved, setSaved] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const put = useRef<((text: string) => void) | null>(null);

  const html = draft ?? page.data?.html ?? "";

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      await api(where, { method: "PUT", body: { html } });
      setSaved(true);
      page.reload();
    } catch (err) {
      setError(err as Error);
    } finally {
      setBusy(false);
    }
  };

  if (page.loading && !page.data && !draft) return <Spinner />;

  if (!editing) {
    return (
      <div className="board-page">
        <ErrorBox error={page.error} onRetry={page.reload} />
        {html.trim() ? (
          <HtmlCard options={{ html, mode: "inline" }} value={value} projects={projects} editing={false} />
        ) : (
          <Empty icon="code">This page is empty. Edit it, or let an assistant write it.</Empty>
        )}
      </div>
    );
  }

  return (
    <div className="page-editor" onKeyDown={(e) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "s") {
        e.preventDefault();
        void save();
      }
    }}>
      <ErrorBox error={error ?? page.error} />
      <div className="page-editor-bar">
        <span className="meta grow">
          The whole page, as HTML. An assistant writes the same thing through{" "}
          <code className="mono">/api/page</code>.
        </span>
        {saved ? <span className="meta">saved</span> : <span className="badge warn">not saved</span>}
        <button className="btn small primary" disabled={busy || saved} onClick={save}>
          <Icon name="check" size={14} /> Save
        </button>
      </div>
      <Palette
        group={group}
        onInsert={(snippet) => {
          if (put.current) put.current(snippet);
          else {
            setDraft(html + "\n" + snippet);
            setSaved(false);
          }
        }}
      />

      <div className="page-editor-panes">
        <CodeArea
          value={html}
          minHeight={420}
          onReady={(api) => (put.current = api.insert)}
          onChange={(text) => {
            setDraft(text);
            setSaved(false);
          }}
        />
        <div className="page-preview">
          {/* Shown, not connected: writing a page is not the moment to be asked
              for a machine's password. */}
          <HtmlCard options={{ html, mode: "inline" }} value={value} projects={projects} editing={true} />
        </div>
      </div>
    </div>
  );
}

/** The widths a card can have on a page, said the way people say them. */
const WIDTHS = [
  { value: 3, short: "¼", label: "a quarter" },
  { value: 4, short: "⅓", label: "a third" },
  { value: 6, short: "½", label: "half" },
  { value: 8, short: "⅔", label: "two thirds" },
  { value: 12, short: "1", label: "the whole width" },
];

/** How wide a tab's page is, and what sits behind it. */
export interface TabStyle {
  width?: "narrow" | "normal" | "wide";
  background?: "plain" | "bare";
}

function tabStyle(tab?: Tab): Required<TabStyle> {
  const s = (tab?.style ?? {}) as TabStyle;
  // Wide unless the tab says otherwise: a board is for the screen it is on.
  return { width: s.width ?? "wide", background: s.background ?? "plain" };
}

/**
 * A tab's own settings.
 *
 * Name and icon, a grid or a page, and how wide that page is — narrow reads
 * like a document, wide fills the screen. Removing the tab lives here too,
 * beside the thing it removes, rather than as a button you can hit while
 * reaching for something else.
 */
function TabSettings({
  tab,
  canRemove,
  onClose,
  onSaved,
  onRemoved,
}: {
  tab: Tab;
  canRemove: boolean;
  onClose: () => void;
  onSaved: () => void;
  onRemoved: () => void;
}) {
  const ask = useAsk();
  const meta = useMeta();
  const [title, setTitle] = useState(tab.title);
  const [icon, setIcon] = useState(tab.icon || "grid");
  const [layout, setLayout] = useState<"grid" | "flow" | "free" | "page">(tab.layout ?? "grid");
  const [style, setStyle] = useState<TabStyle>((tab.style ?? {}) as TabStyle);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  return (
    <Modal
      title={tab.title}
      onClose={onClose}
      footer={
        <>
          {canRemove ? (
            <button
              className="btn danger"
              style={{ marginRight: "auto" }}
              onClick={async () => {
                const sure = await ask.confirm({
                  title: `Remove “${tab.title}”?`,
                  confirmLabel: "Remove",
                  danger: true,
                  body: <>Its cards go with it.</>,
                });
                if (!sure) return;
                await api(`/api/boards/tabs/${tab.id}`, { method: "DELETE" });
                onRemoved();
              }}
            >
              <Icon name="trash" size={14} /> Remove
            </button>
          ) : null}
          <button className="btn" onClick={onClose}>Cancel</button>
          <button
            className="btn primary"
            disabled={busy || !title.trim()}
            onClick={async () => {
              setBusy(true);
              setError(null);
              try {
                await api(`/api/boards/tabs/${tab.id}`, {
                  method: "PATCH",
                  body: { title, icon, layout, style },
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
      <Field label="Name" required>
        <input value={title} autoFocus onChange={(e) => setTitle(e.target.value)} />
      </Field>

      {layout !== "page" && tab.cards.length > 0 ? (
        <div className="notice">
          <strong>Turn this into one page.</strong> The cards become tags in a document you can then
          write by hand — nothing is lost, and they keep working.
          <div style={{ marginTop: 8 }}>
            <button
              className="btn small"
              disabled={busy}
              onClick={async () => {
                setBusy(true);
                setError(null);
                try {
                  await api(`/api/boards/tabs/${tab.id}/as-html`, { body: {} });
                  onSaved();
                } catch (err) {
                  setError(err as Error);
                } finally {
                  setBusy(false);
                }
              }}
            >
              <Icon name="code" size={14} /> Make it a page
            </button>
          </div>
        </div>
      ) : null}

      <Field label="Icon">
        <div className="swatches">
          {(meta?.icons ?? ["grid", "code", "calendar", "server", "notebook", "link", "zap"]).map((name) => (
            <button
              key={name}
              className="swatch"
              title={name}
              style={{
                background: "var(--ctp-surface0)",
                display: "grid",
                placeItems: "center",
                borderColor: icon === name ? "var(--ctp-text)" : "transparent",
              }}
              onClick={() => setIcon(name)}
            >
              <Icon name={name} size={15} />
            </button>
          ))}
        </div>
      </Field>

      <div className="row">
        <Field
          label="Cards are"
          hint="A page is HTML you write yourself — cards can stand inside it with <hp-card>."
        >
          <select value={layout} onChange={(e) => setLayout(e.target.value as "grid" | "flow")}>
            <option value="grid">placed on a grid</option>
            <option value="flow">one after another</option>
            <option value="free">wherever you put them</option>
            <option value="page">one page of HTML — cards may stand in it</option>
          </select>
        </Field>
        <Field label="Page width">
          <select
            value={style.width ?? "normal"}
            onChange={(e) => setStyle({ ...style, width: e.target.value as TabStyle["width"] })}
          >
            <option value="narrow">narrow — reads like a document</option>
            <option value="normal">normal</option>
            <option value="wide">wide — fills the screen</option>
          </select>
        </Field>
      </div>
    </Modal>
  );
}

function clampTo(value: number, low: number, high: number) {
  if (Number.isNaN(value)) return low;
  return Math.max(low, Math.min(high, value));
}

/** What a card's chosen look means in the page. */
export function dress(style?: CardStyle) {
  const s = style ?? {};
  return {
    className: [
      "card",
      s.background === "tinted" ? "tinted" : "",
      s.background === "bare" ? "bare" : "",
      s.border === false ? "borderless" : "",
      s.size === "large" ? "large" : "",
      s.align === "center" ? "centred" : "",
    ]
      .filter(Boolean)
      .join(" "),
    style: s.color ? ({ ["--card-color" as string]: `var(--ctp-${s.color})` } as const) : undefined,
  };
}

/** A card's own view, wherever the card sits. */
function CardBody(props: {
  card: Card;
  value: (variable: string, groupId?: string) => Variable | undefined;
  projects: Project[];
  editing: boolean;
}) {
  const look = dress(props.card.style);
  return (
    <div className={`${look.className} card-${props.card.kind}`} style={look.style}>
      <CardInner {...props} />
    </div>
  );
}

function CardInner({
  card,
  value,
  projects,
  editing,
}: {
  card: Card;
  value: (variable: string, groupId?: string) => Variable | undefined;
  projects: Project[];
  editing: boolean;
}) {
  const View = cardViews[card.kind];
  return (
    <Suspense fallback={<Spinner />}>
      {View ? (
        <View options={card.options ?? {}} value={value} projects={projects} editing={editing} />
      ) : (
        <div className="meta">No card of kind “{card.kind}” is installed.</div>
      )}
    </Suspense>
  );
}

/** The projects, under the groups they are in. */
function byGroup(projects: Project[]): [string, Project[]][] {
  const out = new Map<string, Project[]>();
  for (const p of projects) {
    const key = p.groupTitle ?? p.groupSlug ?? "Ungrouped";
    out.set(key, [...(out.get(key) ?? []), p]);
  }
  return [...out.entries()].sort(([a], [b]) => a.localeCompare(b));
}

/** What a card shows, and who may see it. */
function CardSettings({
  card,
  kinds,
  layout,
  onClose,
  onSaved,
}: {
  card: Card;
  kinds: CardKind[];
  /** What the tab is: a grid counts in columns and rows, a free surface in pixels. */
  layout: Tab["layout"];
  onClose: () => void;
  onSaved: () => void;
}) {
  const meta = useMeta();
  const [options, setOptions] = useState<Record<string, any>>(card.options ?? {});
  const [style, setStyle] = useState<CardStyle>(card.style ?? {});
  const [visibility, setVisibility] = useState(card.visibility);
  // How big it is, in numbers. Dragging a corner is quick and imprecise; this
  // is the other half of that, and the only way to say "exactly twelve columns"
  // or "exactly 420 pixels" without pushing a mouse until it looks right.
  const [size, setSize] = useState({ w: card.w, h: card.h });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const kind = kinds.find((k) => k.name === card.kind);

  return (
    <Modal
      title={kind?.title ?? card.kind}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>Cancel</button>
          <button
            className="btn primary"
            disabled={busy}
            onClick={async () => {
              setBusy(true);
              setError(null);
              try {
                await api(`/api/boards/cards/${card.id}`, {
                  method: "PATCH",
                  body: { options, style, visibility, w: size.w, h: size.h },
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
      {(kind?.options ?? []).map((option) =>
        option.type === "code" ? (
          <Field key={option.name} label={option.label} hint={option.hint}>
            <CodeArea
              value={options[option.name] ?? ""}
              onChange={(text) => setOptions({ ...options, [option.name]: text })}
            />
          </Field>
        ) : option.type === "select" ? (
          <Field key={option.name} label={option.label} hint={option.hint}>
            <select
              value={options[option.name] ?? ""}
              onChange={(e) => setOptions({ ...options, [option.name]: e.target.value })}
            >
              {(option.options ?? []).map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
          </Field>
        ) : (
          <Field key={option.name} label={option.label} hint={option.hint}>
            {option.type === "textarea" ? (
              <textarea
                value={options[option.name] ?? ""}
                style={{ minHeight: 90 }}
                onChange={(e) => setOptions({ ...options, [option.name]: e.target.value })}
              />
            ) : (
              <input
                value={options[option.name] ?? ""}
                onChange={(e) => setOptions({ ...options, [option.name]: e.target.value })}
              />
            )}
          </Field>
        ),
      )}

      {card.kind === "html" ? (
        <Field label="How it looks">
          <div className="html-preview">
            <HtmlCard options={options} value={() => undefined} projects={[]} editing={false} />
          </div>
        </Field>
      ) : null}
      <Field label="Title">
        <input
          value={options.title ?? ""}
          onChange={(e) => setOptions({ ...options, title: e.target.value })}
        />
      </Field>
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
          checked={style.border !== false}
          onChange={(e) => setStyle({ ...style, border: e.target.checked })}
        />
        <span>A line around it</span>
      </label>

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
    </Modal>
  );
}

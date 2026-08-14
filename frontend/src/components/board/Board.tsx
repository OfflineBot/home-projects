import { Suspense, useCallback, useState } from "react";
import { Icon } from "../Icon";
import { Empty, ErrorBox, Field, Modal, Spinner, useAsk } from "../ui";
import { api, type Project, type Variable } from "../../lib/api";
import { useQuery, useSession } from "../../lib/store";
import { Grid, type Placed } from "./Grid";
import { cardViews } from "./cards";

/**
 * A board: tabs of cards, arranged by whoever owns it.
 *
 * The same component is the front page and a group's page — the only
 * difference is which board it asks for. Reading is the normal state; editing
 * is a switch you flip, and then the cards grow handles.
 */

export interface Card {
  id: string;
  tabId: string;
  kind: string;
  options: Record<string, any>;
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
  emptyNote,
  exposed,
}: {
  group?: string;
  emptyNote?: string;
  /** Handed to somebody: read it, that is all. */
  exposed?: boolean;
}) {
  const session = useSession();
  const where = group ? `?group=${encodeURIComponent(group)}` : "";
  const board = useQuery<BoardData>(`/api/boards${where}`);
  const kinds = useQuery<{ cards: CardKind[] }>("/api/boards/cards");
  const projects = useQuery<{ projects: Project[] }>("/api/projects");
  const reported = useQuery<{ groups: Block[] }>("/api/dashboard");

  const ask = useAsk();
  const [editing, setEditing] = useState(false);
  const [tab, setTab] = useState(0);
  const [adding, setAdding] = useState(false);
  const [settling, setSettling] = useState<Card | null>(null);
  const [error, setError] = useState<Error | null>(null);

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

  if (board.loading && !board.data) return <Spinner />;

  return (
    <div className="board">
      <div className="board-bar">
        <div className="board-tabs">
          {tabs.map((t, i) => (
            <button
              key={t.id}
              className={i === tab ? "board-tab on" : "board-tab"}
              onClick={() => setTab(i)}
              onDoubleClick={async () => {
                if (!editing) return;
                const title = await ask.text({
                  title: "Name this tab",
                  label: "Name",
                  value: t.title,
                });
                if (!title) return;
                await api(`/api/boards/tabs/${t.id}`, { method: "PATCH", body: { title } });
                board.reload();
              }}
            >
              <Icon name={t.icon || "grid"} size={14} /> {t.title}
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
              {tabs.length > 1 && current ? (
                <button
                  className="btn small ghost"
                  onClick={async () => {
                    const sure = await ask.confirm({
                      title: `Remove the tab “${current.title}”?`,
                      confirmLabel: "Remove",
                      danger: true,
                      body: <>Its cards go with it.</>,
                    });
                    if (!sure) return;
                    await api(`/api/boards/tabs/${current.id}`, { method: "DELETE" });
                    setTab(0);
                    board.reload();
                  }}
                >
                  <Icon name="trash" size={14} /> Remove tab
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
            <div style={{ marginTop: 10 }}>
              <button className="btn small" onClick={() => { setEditing(true); setAdding(true); }}>
                <Icon name="plus" size={14} /> Put something on it
              </button>
            </div>
          ) : null}
        </Empty>
      ) : null}

      {current ? (
        <Grid
          cards={current.cards.map((c) => ({ id: c.id, x: c.x, y: c.y, w: c.w, h: c.h }))}
          editing={editing}
          onChange={saveLayout}
        >
          {(placed) => {
            const card = current.cards.find((c) => c.id === placed.id);
            if (!card) return null;
            const View = cardViews[card.kind];
            return (
              <div className={`card card-${card.kind}`}>
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
                <Suspense fallback={<Spinner />}>
                  {View ? (
                    <View
                      options={card.options ?? {}}
                      value={value}
                      projects={projects.data?.projects ?? []}
                      editing={editing}
                    />
                  ) : (
                    <div className="meta">No card of kind “{card.kind}” is installed.</div>
                  )}
                </Suspense>
              </div>
            );
          }}
        </Grid>
      ) : null}

      {adding && board.data && current ? (
        <AddCard
          kinds={kinds.data?.cards ?? []}
          projects={projects.data?.projects ?? []}
          blocks={reported.data?.groups ?? []}
          onClose={() => setAdding(false)}
          onAdd={async (kind, options, size) => {
            const fallback = (kinds.data?.cards ?? []).find((k) => k.name === kind);
            const y = Math.max(0, ...current.cards.map((c) => c.y + c.h));
            await api("/api/boards/cards", {
              body: {
                tabId: current.id,
                kind,
                options,
                x: 0,
                y,
                w: size?.w ?? fallback?.w ?? 3,
                h: size?.h ?? fallback?.h ?? 2,
              },
            });
            setAdding(false);
            board.reload();
          }}
        />
      ) : null}

      {settling ? (
        <CardSettings
          card={settling}
          kinds={kinds.data?.cards ?? []}
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
  onClose,
  onAdd,
}: {
  kinds: CardKind[];
  projects: Project[];
  blocks: Block[];
  onClose: () => void;
  onAdd: (kind: string, options: Record<string, any>, size?: { w: number; h: number }) => Promise<void>;
}) {
  const [projectId, setProjectId] = useState("");
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
          <Field label="From which project">
            <select value={projectId} onChange={(e) => setProjectId(e.target.value)}>
              <option value="">— pick one —</option>
              {projects.map((p) => (
                <option key={p.id} value={p.id}>
                  {(p.groupSlug ?? "ungrouped") + "/" + p.slug}
                </option>
              ))}
            </select>
          </Field>

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
          {chosen?.options?.map((option) => (
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
          ))}
        </>
      )}
    </Modal>
  );
}

/** What a card shows, and who may see it. */
function CardSettings({
  card,
  kinds,
  onClose,
  onSaved,
}: {
  card: Card;
  kinds: CardKind[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [options, setOptions] = useState<Record<string, any>>(card.options ?? {});
  const [visibility, setVisibility] = useState(card.visibility);
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
                  body: { options, visibility },
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
      {(kind?.options ?? []).map((option) => (
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
      ))}
      <Field label="Title">
        <input
          value={options.title ?? ""}
          onChange={(e) => setOptions({ ...options, title: e.target.value })}
        />
      </Field>
      <Field
        label="Who may see it"
        hint="Never wider than what it shows."
      >
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

import { Suspense, useCallback, useMemo, useState } from "react";
import { Icon } from "../Icon";
import { Empty, ErrorBox, Field, Modal, Spinner } from "../ui";
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
  options?: { name: string; label: string; type: string; placeholder?: string; hint?: string }[];
  w?: number;
  h?: number;
  from?: string;
}

interface Block {
  group: { id: string; slug: string; title: string };
  variables: Variable[];
  derived: { name: string; value: unknown; unit?: string; type: string }[];
}

export default function Board({ group, emptyNote }: { group?: string; emptyNote?: string }) {
  const session = useSession();
  const where = group ? `?group=${encodeURIComponent(group)}` : "";
  const board = useQuery<BoardData>(`/api/boards${where}`);
  const kinds = useQuery<{ cards: CardKind[] }>("/api/boards/cards");
  const projects = useQuery<{ projects: Project[] }>("/api/projects");
  const reported = useQuery<{ groups: Block[] }>("/api/dashboard");

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
                const title = prompt("Name for this tab:", t.title);
                if (!title) return;
                await api(`/api/boards/tabs/${t.id}`, { method: "PATCH", body: { title } });
                board.reload();
              }}
            >
              <Icon name={t.icon || "grid"} size={14} /> {t.title}
            </button>
          ))}
          {editing && board.data ? (
            <button
              className="board-tab add"
              aria-label="Another tab"
              onClick={async () => {
                const title = prompt("Name for the new tab:", "Tab");
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

        {session.user ? (
          editing ? (
            <>
              <button className="btn small" onClick={() => setAdding(true)}>
                <Icon name="plus" size={14} /> Add a card
              </button>
              {tabs.length > 1 && current ? (
                <button
                  className="btn small ghost"
                  onClick={async () => {
                    if (!confirm(`Remove the tab "${current.title}" and its cards?`)) return;
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
          {emptyNote ?? "Nothing on this board yet."}
          {session.user ? (
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
          onAdd={async (kind, options) => {
            const size = (kinds.data?.cards ?? []).find((k) => k.name === kind);
            const y = Math.max(0, ...current.cards.map((c) => c.y + c.h));
            await api("/api/boards/cards", {
              body: {
                tabId: current.id,
                kind,
                options,
                x: 0,
                y,
                w: size?.w ?? 3,
                h: size?.h ?? 2,
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

/** The dialog that puts something on the board. */
function AddCard({
  kinds,
  projects,
  blocks,
  onClose,
  onAdd,
}: {
  kinds: CardKind[];
  projects: Project[];
  blocks: Block[];
  onClose: () => void;
  onAdd: (kind: string, options: Record<string, any>) => Promise<void>;
}) {
  const [kind, setKind] = useState("");
  const [options, setOptions] = useState<Record<string, any>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const chosen = kinds.find((k) => k.name === kind);

  const everyVariable = useMemo(
    () =>
      blocks.flatMap((b) =>
        b.variables.map((v) => ({
          groupId: b.group.id,
          name: `${v.projectSlug}.${v.name}`,
          label: `${b.group.title} · ${v.projectSlug}.${v.name}`,
        })),
      ),
    [blocks],
  );

  return (
    <Modal
      title="Add a card"
      onClose={onClose}
      wide
      footer={
        <>
          <button className="btn" onClick={onClose}>Cancel</button>
          <button
            className="btn primary"
            disabled={!kind || busy}
            onClick={async () => {
              setBusy(true);
              setError(null);
              try {
                await onAdd(kind, options);
              } catch (err) {
                setError(err as Error);
              } finally {
                setBusy(false);
              }
            }}
          >
            Put it on
          </button>
        </>
      }
    >
      <ErrorBox error={error} />
      <div className="card-kinds">
        {kinds.map((k) => (
          <button
            key={k.name}
            className={k.name === kind ? "card-kind on" : "card-kind"}
            onClick={() => {
              setKind(k.name);
              setOptions({});
            }}
          >
            <Icon name={k.icon || "grid"} size={18} />
            <strong>{k.title}</strong>
            <span className="meta">{k.description}</span>
            {k.from ? <span className="badge">{k.from}</span> : null}
          </button>
        ))}
      </div>

      {chosen?.options?.map((option) => (
        <Field key={option.name} label={option.label} hint={option.hint}>
          {option.type === "textarea" ? (
            <textarea
              value={options[option.name] ?? ""}
              placeholder={option.placeholder}
              style={{ minHeight: 90 }}
              onChange={(e) => setOptions({ ...options, [option.name]: e.target.value })}
            />
          ) : option.type === "project" ? (
            <select
              value={options[option.name] ?? ""}
              onChange={(e) => setOptions({ ...options, [option.name]: e.target.value })}
            >
              <option value="">— pick one —</option>
              {projects.map((p) => (
                <option key={p.id} value={p.id}>
                  {(p.groupSlug ?? "ungrouped") + "/" + p.slug}
                </option>
              ))}
            </select>
          ) : option.type === "variable" ? (
            <select
              value={options[option.name] ?? ""}
              onChange={(e) => {
                const picked = everyVariable.find((v) => v.name === e.target.value);
                setOptions({ ...options, [option.name]: e.target.value, groupId: picked?.groupId });
              }}
            >
              <option value="">— pick one —</option>
              {everyVariable.map((v) => (
                <option key={v.groupId + v.name} value={v.name}>
                  {v.label}
                </option>
              ))}
            </select>
          ) : (
            <input
              value={options[option.name] ?? ""}
              placeholder={option.placeholder}
              onChange={(e) => setOptions({ ...options, [option.name]: e.target.value })}
            />
          )}
        </Field>
      ))}

      {chosen ? (
        <Field label="Title" hint="Empty keeps whatever the card calls itself.">
          <input
            value={options.title ?? ""}
            onChange={(e) => setOptions({ ...options, title: e.target.value })}
          />
        </Field>
      ) : null}
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
        hint={<>Never more than what it shows allows — a public card on a private project stays private.</>}
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

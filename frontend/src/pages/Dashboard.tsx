import { useState } from "react";
import { Link } from "react-router-dom";
import { Icon } from "../components/Icon";
import { Empty, ErrorBox, Field, Modal, Spinner, formatDate } from "../components/ui";
import { api, type Derived, type Group, type Variable } from "../lib/api";
import { useQuery, useSession } from "../lib/store";
import { colorVar } from "../lib/theme";

interface Tile {
  id: string;
  groupId: string;
  groupSlug?: string;
  variable: string;
  title: string;
  kind: string;
  x: number;
  y: number;
  w: number;
  h: number;
}

interface Block {
  group: Group;
  variables: Variable[];
  derived: Derived[];
}

/**
 * The dashboard is made of added groups. Every project exports variables into
 * its group, the group collects them, and the dashboard shows them — there is
 * no other route to this page.
 */
export default function Dashboard() {
  const session = useSession();
  const { data, error, loading, reload } = useQuery<{ groups: Block[]; tiles: Tile[] }>("/api/dashboard");
  const [adding, setAdding] = useState(false);

  const value = (block: Block, name: string) => {
    const [projectSlug, ...rest] = name.split(".");
    const varName = rest.join(".");
    const found = block.variables.find((v) => v.projectSlug === projectSlug && v.name === varName);
    if (found) return found;
    const derived = block.derived.find((d) => d.name === name);
    if (derived) {
      return {
        name,
        type: derived.type,
        value: derived.value,
        unit: derived.unit ?? "",
        error: derived.error,
        updatedAt: "",
      } as unknown as Variable;
    }
    return undefined;
  };

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Dashboard</h1>
        </div>
        {session.user ? (
          <div className="head-actions">
            <button className="btn" onClick={reload}>
              <Icon name="refresh" size={16} /> Refresh
            </button>
            <button className="btn primary" onClick={() => setAdding(true)}>
              <Icon name="plus" size={16} /> Add a tile
            </button>
          </div>
        ) : null}
      </div>

      <ErrorBox error={error} onRetry={reload} />
      {loading && !data ? <Spinner /> : null}

      {data?.tiles?.length ? (
        <>
          <div className="tiles" style={{ marginBottom: 30 }}>
            {data.tiles.map((tile) => {
              const block = data.groups.find((b) => b.group.id === tile.groupId);
              const variable = block ? value(block, tile.variable) : undefined;
              return (
                <div
                  key={tile.id}
                  className="tile"
                  style={{
                    gridColumn: tile.w > 1 ? `span ${Math.min(tile.w, 3)}` : undefined,
                    ["--tile-color" as string]: colorVar(block?.group.color),
                  }}
                >
                  <div className="tile-top">
                    <div style={{ minWidth: 0, flex: 1 }}>
                      <div className="sub">
                        {block?.group.title} · {tile.variable}
                      </div>
                      <h3>{tile.title || tile.variable}</h3>
                    </div>
                    {session.user ? (
                      <button
                        className="btn ghost icon"
                        aria-label="Remove tile"
                        onClick={async () => {
                          await api(`/api/dashboard/tiles/${tile.id}`, { method: "DELETE" });
                          reload();
                        }}
                      >
                        <Icon name="x" size={15} />
                      </button>
                    ) : null}
                  </div>
                  <TileBody tile={tile} variable={variable} />
                </div>
              );
            })}
          </div>
        </>
      ) : null}

      {data?.groups.length === 0 ? (
        <Empty icon="grid">Nothing pinned.</Empty>
      ) : null}

      {data?.groups.map((block) => (
        <div key={block.group.id} style={{ marginBottom: 26 }}>
          <h2 style={{ fontSize: 17, display: "flex", alignItems: "center", gap: 8 }}>
            <Icon name={block.group.icon} size={17} />
            <Link to={`/groups/${block.group.slug}`}>{block.group.title}</Link>
            {block.group.pinned && session.user ? (
              <button
                className="btn ghost icon"
                title="Take this group off the dashboard"
                onClick={async () => {
                  await api(`/api/groups/${block.group.slug}`, { method: "PATCH", body: { pinned: false } });
                  reload();
                }}
              >
                <Icon name="x" size={14} />
              </button>
            ) : null}
          </h2>
          {block.variables.length === 0 && block.derived.length === 0 ? (
            <p style={{ color: "var(--ctp-subtext0)", marginTop: 0 }}>
              No project in this group reports anything yet.
            </p>
          ) : (
            <div className="tiles">
              {block.derived.map((d) => (
                <div key={d.name} className="tile" style={{ ["--tile-color" as string]: colorVar(block.group.color) }}>
                  <div className="sub">{d.op} · the group's own</div>
                  <h3>{d.name}</h3>
                  <div className="stat">
                    {format(d.value)}
                    {d.unit ? <span className="unit">{d.unit}</span> : null}
                  </div>
                  {d.error ? <div className="sub" style={{ color: "var(--ctp-peach)" }}>{d.error}</div> : null}
                </div>
              ))}
              {block.variables.map((v) => (
                <div
                  key={v.projectId + v.name}
                  className="tile"
                  style={{ ["--tile-color" as string]: colorVar(block.group.color) }}
                >
                  <div className="sub">{v.projectSlug}</div>
                  <h3>{v.name}</h3>
                  {v.error ? (
                    <div className="sub" style={{ color: "var(--ctp-red)" }}>{v.error}</div>
                  ) : (
                    <VariableValue variable={v} />
                  )}
                  <div className="tile-foot">
                    <span className="meta" style={{ color: "var(--ctp-overlay1)" }}>
                      {formatDate(v.updatedAt)}
                    </span>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      ))}

      {adding && data ? (
        <AddTile
          blocks={data.groups}
          onClose={() => setAdding(false)}
          onAdded={() => {
            setAdding(false);
            reload();
          }}
        />
      ) : null}
    </>
  );
}

function TileBody({ tile, variable }: { tile: Tile; variable?: Variable }) {
  if (tile.kind === "button") return <RuleButton tile={tile} />;
  if (!variable) return <div className="sub">nothing reported (yet)</div>;
  if (tile.kind === "status") {
    const on = Boolean(variable.value);
    return (
      <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
        <span className={on ? "dot-status on" : "dot-status off"} />
        <span className="stat" style={{ fontSize: 20 }}>
          {on ? "online" : "offline"}
        </span>
      </div>
    );
  }
  if (tile.kind === "history") return <History tile={tile} />;
  return <VariableValue variable={variable} />;
}

function VariableValue({ variable }: { variable: Variable }) {
  const v = variable.value;
  if (Array.isArray(v)) {
    if (v.length && typeof v[0] === "object") {
      const columns = Object.keys(v[0]).slice(0, 4);
      return (
        <table className="data">
          <thead>
            <tr>
              {columns.map((c) => (
                <th key={c}>{c}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {v.slice(0, 6).map((row: any, i: number) => (
              <tr key={i}>
                {columns.map((c) => (
                  <td key={c}>{String(row[c] ?? "").slice(0, 40)}</td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      );
    }
    return (
      <ul style={{ margin: 0, paddingLeft: 18 }}>
        {v.slice(0, 6).map((item: any, i: number) => (
          <li key={i}>{String(item)}</li>
        ))}
      </ul>
    );
  }
  if (typeof v === "boolean") {
    return (
      <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
        <span className={v ? "dot-status on" : "dot-status off"} />
        <span className="stat" style={{ fontSize: 20 }}>{v ? "yes" : "no"}</span>
      </div>
    );
  }
  return (
    <div className="stat">
      {format(v)}
      {variable.unit ? <span className="unit">{variable.unit}</span> : null}
    </div>
  );
}

/** A tile that fires an automation rule — the same rule the project offers. */
function RuleButton({ tile }: { tile: Tile }) {
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const [projectRef, rule] = tile.variable.split(".");

  return (
    <div>
      <ErrorBox error={error} />
      <button
        className="btn primary"
        disabled={busy || !rule}
        onClick={async () => {
          setBusy(true);
          setError(null);
          setNote(null);
          try {
            const result = await api<{ run?: { status: string }; error?: string }>(
              `/api/projects/${projectRef}/automation/rules/${encodeURIComponent(rule)}/run`,
              { method: "POST" },
            );
            setNote(result.error ? result.error : `ran · ${result.run?.status ?? "ok"}`);
          } catch (err) {
            setError(err as Error);
          } finally {
            setBusy(false);
          }
        }}
      >
        <Icon name="play" size={15} /> {busy ? "…" : rule || "no rule set"}
      </button>
      {note ? <div className="sub" style={{ marginTop: 8 }}>{note}</div> : null}
    </div>
  );
}

function History({ tile }: { tile: Tile }) {
  const [projectSlug, ...rest] = tile.variable.split(".");
  const { data } = useQuery<{ projects: { id: string; slug: string }[] }>(`/api/projects?group=${tile.groupSlug}`);
  const project = data?.projects.find((p) => p.slug === projectSlug);
  const history = useQuery<{ points: { at: string; value: any }[] }>(
    project ? `/api/projects/${project.id}/variables/${rest.join(".")}/history?limit=40` : null,
  );
  const points = (history.data?.points ?? []).map((p) => Number(p.value)).filter((n) => !Number.isNaN(n));
  if (!points.length) return <div className="sub">no history yet</div>;
  const max = Math.max(...points);
  const min = Math.min(...points);
  return (
    <div className="spark">
      {points.map((p, i) => (
        <span key={i} style={{ height: `${max === min ? 50 : ((p - min) / (max - min)) * 100}%` }} />
      ))}
    </div>
  );
}

function format(v: any) {
  if (v === null || v === undefined || v === "") return "—";
  if (typeof v === "number") return Number.isInteger(v) ? v : v.toFixed(2);
  if (typeof v === "string" && /^\d{4}-\d{2}-\d{2}T/.test(v)) return formatDate(v);
  return String(v);
}

function AddTile({
  blocks,
  onClose,
  onAdded,
}: {
  blocks: Block[];
  onClose: () => void;
  onAdded: () => void;
}) {
  const [groupId, setGroupId] = useState(blocks[0]?.group.id ?? "");
  const [variable, setVariable] = useState("");
  const [title, setTitle] = useState("");
  const [kind, setKind] = useState("number");
  const [formula, setFormula] = useState(false);
  const [expr, setExpr] = useState("");
  const [error, setError] = useState<Error | null>(null);
  const block = blocks.find((b) => b.group.id === groupId);

  // Every variable on the server, written the way a formula refers to it.
  const everything = blocks.flatMap((b) =>
    b.variables.map((v) => `${b.group.slug}/${v.projectSlug}/${v.name}`),
  );

  const names = [
    ...(block?.variables.map((v) => `${v.projectSlug}.${v.name}`) ?? []),
    ...(block?.derived.map((d) => d.name) ?? []),
  ];

  return (
    <Modal
      title="Add a tile"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn primary"
            disabled={!groupId || !variable}
            onClick={async () => {
              try {
                // A formula becomes a variable of the group first; the tile
                // then points at it by name like any other.
                if (formula) {
                  const group = blocks.find((b) => b.group.id === groupId);
                  await api(`/api/groups/${group?.group.slug}/variables`, {
                    body: { name: variable, expr, op: "expr" },
                  });
                }
                await api("/api/dashboard/tiles", {
                  body: { groupId, variable, title, kind, w: kind === "table" || kind === "list" ? 2 : 1, h: 1 },
                });
                onAdded();
              } catch (err) {
                setError(err as Error);
              }
            }}
          >
            Add
          </button>
        </>
      }
    >
      <ErrorBox error={error} />
      <Field label="Group">
        <select value={groupId} onChange={(e) => setGroupId(e.target.value)}>
          {blocks.map((b) => (
            <option key={b.group.id} value={b.group.id}>
              {b.group.title}
            </option>
          ))}
        </select>
      </Field>
      {kind === "button" ? (
        <Field
          label="Rule"
          hint="Written as project.rule — the rule has to exist in that project's automation."
        >
          <input value={variable} onChange={(e) => setVariable(e.target.value)} placeholder="pc.Wake it up" />
        </Field>
      ) : (
        <Field label="Variable">
          <select
            value={formula ? "__formula" : variable}
            onChange={(e) => {
              setFormula(e.target.value === "__formula");
              setVariable(e.target.value === "__formula" ? "" : e.target.value);
            }}
          >
            <option value="">— pick one —</option>
            {names.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
            <option value="__formula">a number worked out of others…</option>
          </select>
        </Field>
      )}

      {formula ? (
        <>
          <Field label="Call it">
            <input value={variable} onChange={(e) => setVariable(e.target.value)} placeholder="schnitt" />
          </Field>
          <Field label="Worked out as">
            <textarea
              value={expr}
              onChange={(e) => setExpr(e.target.value)}
              style={{ minHeight: 64, fontFamily: "var(--mono)", fontSize: 13 }}
              placeholder="({studies/noten/durchschnitt} + {studies/noten/durchschnitt2}) / 2"
            />
          </Field>
          <Field label="Insert a variable" hint="Anything on the server, not only this group.">
            <select
              value=""
              onChange={(e) => {
                if (!e.target.value) return;
                setExpr((x) => (x ? x + " " : "") + `{${e.target.value}}`);
              }}
            >
              <option value="">— pick one —</option>
              {everything.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </Field>
          <p className="meta">+ − × ÷ and brackets. A missing reference says so instead of showing a zero.</p>
        </>
      ) : null}
      <Field label="Title">
        <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder={variable} />
      </Field>
      <Field label="Shown as">
        <select value={kind} onChange={(e) => setKind(e.target.value)}>
          <option value="number">a large number</option>
          <option value="text">text</option>
          <option value="status">a status dot</option>
          <option value="list">a list</option>
          <option value="table">a table</option>
          <option value="history">a small graph over time</option>
          <option value="button">a button that runs a rule</option>
        </select>
      </Field>
    </Modal>
  );
}

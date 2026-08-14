import { useState } from "react";
import { Link } from "react-router-dom";
import { Icon } from "../components/Icon";
import { Empty, ErrorBox, Field, Modal, Spinner, formatDate } from "../components/ui";
import { api, type Derived, type Group, type Project, type Variable } from "../lib/api";
import { useQuery, useSession } from "../lib/store";
import { colorVar } from "../lib/theme";

interface Tile {
  id: string;
  groupId: string;
  groupSlug?: string;
  projectId?: string;
  projectSlug?: string;
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

/** One thing somebody took off their dashboard. */
interface Hidden {
  kind: "project" | "variable";
  ref: string;
}

/**
 * The dashboard is made of added groups. Every project exports variables into
 * its group, the group collects them, and the dashboard shows them — there is
 * no other route to this page.
 */
export default function Dashboard() {
  const session = useSession();
  const { data, error, loading, reload } = useQuery<{
    groups: Block[];
    tiles: Tile[];
    hidden: Hidden[];
  }>("/api/dashboard");
  const projects = useQuery<{ projects: Project[] }>(session.user ? "/api/projects" : null);
  const [adding, setAdding] = useState(false);
  const [shut, setShut] = useState<Record<string, boolean>>(() => {
    try {
      return JSON.parse(localStorage.getItem("dashboard.shut") ?? "{}");
    } catch {
      return {};
    }
  });
  const fold = (slug: string, closed: boolean) => {
    const next = { ...shut, [slug]: closed };
    setShut(next);
    localStorage.setItem("dashboard.shut", JSON.stringify(next));
  };

  // Putting something away and bringing it back. A project keeps reporting
  // either way — this only decides what the page shows.
  const hidden = data?.hidden ?? [];
  const isHidden = (kind: Hidden["kind"], ref: string) =>
    hidden.some((h) => h.kind === kind && h.ref === ref);
  const hide = async (kind: Hidden["kind"], ref: string) => {
    await api("/api/dashboard/hidden", { body: { kind, ref } });
    reload();
  };
  const unhide = async (kind: string, ref: string) => {
    await api(`/api/dashboard/hidden?kind=${kind}&ref=${encodeURIComponent(ref)}`, { method: "DELETE" });
    reload();
  };
  const [showHidden, setShowHidden] = useState(false);

  // Tiles are kept in the order they are shown, so moving one is a swap of the
  // two positions and nothing else.
  const move = async (index: number, by: number) => {
    const tiles = data?.tiles ?? [];
    const other = index + by;
    if (other < 0 || other >= tiles.length) return;
    await Promise.all([
      api(`/api/dashboard/tiles/${tiles[index].id}`, { method: "PATCH", body: { y: other, x: 0 } }),
      api(`/api/dashboard/tiles/${tiles[other].id}`, { method: "PATCH", body: { y: index, x: 0 } }),
    ]);
    reload();
  };

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
            {data.tiles.map((tile, index) => {
              const block = data.groups.find((b) => b.group.id === tile.groupId);
              const variable = block ? value(block, tile.variable) : undefined;
              if (tile.kind === "project") {
                const project = (projects.data?.projects ?? []).find((p) => p.id === tile.projectId);
                const numbers = data.groups
                  .flatMap((b) => b.variables)
                  .filter((v) => v.projectId === tile.projectId && typeof v.value === "number")
                  .slice(0, 3);
                return (
                  <ProjectTile
                    key={tile.id}
                    tile={tile}
                    project={project}
                    numbers={numbers}
                    editable={Boolean(session.user)}
                    onMove={(by) => move(index, by)}
                    onRemove={async () => {
                      await api(`/api/dashboard/tiles/${tile.id}`, { method: "DELETE" });
                      reload();
                    }}
                  />
                );
              }
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
                      <div className="tile-tools">
                        <button className="btn ghost icon" aria-label="Move left" onClick={() => move(index, -1)}>
                          <Icon name="chevronLeft" size={14} />
                        </button>
                        <button className="btn ghost icon" aria-label="Move right" onClick={() => move(index, 1)}>
                          <Icon name="chevronRight" size={14} />
                        </button>
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
                      </div>
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
            <button
              className="btn ghost icon"
              aria-label={shut[block.group.slug] ? "Show" : "Hide"}
              onClick={() => fold(block.group.slug, !shut[block.group.slug])}
            >
              <Icon name={shut[block.group.slug] ? "chevronRight" : "chevronDown"} size={14} />
            </button>
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
          {shut[block.group.slug] ? null : block.variables.length === 0 && block.derived.length === 0 ? (
            <p style={{ color: "var(--ctp-subtext0)", marginTop: 0 }}>
              No project in this group reports anything yet.
            </p>
          ) : (
            <>
              {block.derived.filter((d) => !isHidden("variable", `${block.group.id}:${d.name}`)).length ? (
                <div className="tiles">
                  {block.derived
                    .filter((d) => !isHidden("variable", `${block.group.id}:${d.name}`))
                    .map((d) => (
                      <div
                        key={d.name}
                        className="tile"
                        style={{ ["--tile-color" as string]: colorVar(block.group.color) }}
                      >
                        <div className="tile-top">
                          <div style={{ minWidth: 0, flex: 1 }}>
                            <div className="sub">{d.op} · the group's own</div>
                            <h3>{d.name}</h3>
                          </div>
                          {session.user ? (
                            <div className="tile-tools">
                              <button
                                className="btn ghost icon"
                                title="Take this off the dashboard"
                                onClick={() => hide("variable", `${block.group.id}:${d.name}`)}
                              >
                                <Icon name="x" size={14} />
                              </button>
                            </div>
                          ) : null}
                        </div>
                        <div className="stat">
                          {format(d.value)}
                          {d.unit ? <span className="unit">{d.unit}</span> : null}
                        </div>
                        {d.error ? <div className="sub" style={{ color: "var(--ctp-peach)" }}>{d.error}</div> : null}
                      </div>
                    ))}
                </div>
              ) : null}

              {/* Per project, so a project that is finished can go in one move
                  instead of one tile at a time. */}
              {byProject(block)
                .filter(([projectId]) => !isHidden("project", projectId))
                .map(([projectId, list]) => {
                  const shown = list.filter((v) => !isHidden("variable", `${projectId}:${v.name}`));
                  if (!shown.length) return null;
                  return (
                    <div key={projectId} className="project-block">
                      <div className="project-block-head">
                        <Link to={`/groups/${block.group.slug}/${list[0].projectSlug}`}>
                          {list[0].projectSlug}
                        </Link>
                        {session.user ? (
                          <button
                            className="btn ghost icon"
                            title="Take this project off the dashboard"
                            onClick={() => hide("project", projectId)}
                          >
                            <Icon name="x" size={13} />
                          </button>
                        ) : null}
                      </div>
                      <div className="tiles">
                        {shown.map((v) => (
                          <div
                            key={v.projectId + v.name}
                            className="tile"
                            style={{ ["--tile-color" as string]: colorVar(block.group.color) }}
                          >
                            <div className="tile-top">
                              <div style={{ minWidth: 0, flex: 1 }}>
                                <h3>{v.name}</h3>
                              </div>
                              {session.user ? (
                                <div className="tile-tools">
                                  <button
                                    className="btn ghost icon"
                                    title="Take this off the dashboard"
                                    onClick={() => hide("variable", `${v.projectId}:${v.name}`)}
                                  >
                                    <Icon name="x" size={14} />
                                  </button>
                                </div>
                              ) : null}
                            </div>
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
                    </div>
                  );
                })}
            </>
          )}
        </div>
      ))}

      {/* Nothing disappears for good: what was put away is listed, with the way
          back. */}
      {session.user && hidden.length ? (
        <div className="put-away">
          <button className="btn ghost small" onClick={() => setShowHidden(!showHidden)}>
            <Icon name={showHidden ? "chevronDown" : "chevronRight"} size={13} />
            {hidden.length} put away
          </button>
          {showHidden ? (
            <div className="list" style={{ marginTop: 8 }}>
              {hidden.map((h) => (
                <div key={h.kind + h.ref} className="list-row">
                  <Icon name={h.kind === "project" ? "box" : "grid"} size={14} />
                  <span className="grow mono">{label(h, data?.groups ?? [], projects.data?.projects ?? [])}</span>
                  <button className="btn small" onClick={() => unhide(h.kind, h.ref)}>
                    Bring back
                  </button>
                </div>
              ))}
            </div>
          ) : null}
        </div>
      ) : null}

      {adding && data ? (
        <AddTile
          blocks={data.groups}
          projects={projects.data?.projects ?? []}
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

/**
 * A project on the dashboard: the way in, and the two or three numbers it
 * reports, so the page answers before it is clicked.
 */
function ProjectTile({
  tile,
  project,
  numbers,
  editable,
  onMove,
  onRemove,
}: {
  tile: Tile;
  project?: Project;
  numbers: Variable[];
  editable: boolean;
  onMove: (by: number) => void;
  onRemove: () => void;
}) {
  const address = project ? `/groups/${project.groupSlug}/${project.slug}` : "/";
  return (
    <div className="tile project-tile" style={{ ["--tile-color" as string]: colorVar(project?.color) }}>
      <div className="tile-top">
        <div style={{ minWidth: 0, flex: 1 }}>
          <div className="sub">{project?.groupTitle ?? project?.groupSlug ?? "gone"}</div>
          <h3>
            <Link to={address}>
              <Icon name={project?.icon ?? "box"} size={16} /> {tile.title || project?.title || tile.projectSlug}
            </Link>
          </h3>
        </div>
        {editable ? (
          <div className="tile-tools">
            <button className="btn ghost icon" aria-label="Move left" onClick={() => onMove(-1)}>
              <Icon name="chevronLeft" size={14} />
            </button>
            <button className="btn ghost icon" aria-label="Move right" onClick={() => onMove(1)}>
              <Icon name="chevronRight" size={14} />
            </button>
            <button className="btn ghost icon" aria-label="Remove tile" onClick={onRemove}>
              <Icon name="x" size={15} />
            </button>
          </div>
        ) : null}
      </div>

      {project ? (
        <>
          {numbers.length ? (
            <div className="tile-numbers">
              {numbers.map((v) => (
                <div key={v.name}>
                  <div className="stat" style={{ fontSize: 22 }}>
                    {format(v.value)}
                    {v.unit ? <span className="unit">{v.unit}</span> : null}
                  </div>
                  <div className="meta">{v.name}</div>
                </div>
              ))}
            </div>
          ) : null}
          <div className="tile-foot">
            <Link className="btn small" to={address}>
              Open
            </Link>
            {project.defaultTab && project.defaultTab !== "files" ? (
              <Link className="btn small ghost" to={`${address}/files`}>
                Files
              </Link>
            ) : null}
          </div>
        </>
      ) : (
        <div className="sub">That project is gone.</div>
      )}
    </div>
  );
}

/** The group's variables, gathered under the project that reported them. */
function byProject(block: Block): [string, Variable[]][] {
  const out = new Map<string, Variable[]>();
  for (const v of block.variables) {
    const list = out.get(v.projectId) ?? [];
    list.push(v);
    out.set(v.projectId, list);
  }
  return [...out.entries()];
}

/** What to call something that is currently put away. */
function label(h: Hidden, blocks: Block[], projects: Project[]): string {
  if (h.kind === "project") {
    const p = projects.find((x) => x.id === h.ref);
    return p ? `${p.groupSlug ?? "ungrouped"}/${p.slug}` : h.ref;
  }
  const [owner, ...rest] = h.ref.split(":");
  const name = rest.join(":");
  const variable = blocks.flatMap((b) => b.variables).find((v) => v.projectId === owner && v.name === name);
  if (variable) return `${variable.projectSlug}.${name}`;
  const group = blocks.find((b) => b.group.id === owner);
  if (group) return `${group.group.slug} · ${name}`;
  return name || h.ref;
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
  projects,
  onClose,
  onAdded,
}: {
  blocks: Block[];
  projects: Project[];
  onClose: () => void;
  onAdded: () => void;
}) {
  const [groupId, setGroupId] = useState(blocks[0]?.group.id ?? "");
  const [projectId, setProjectId] = useState("");
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
            disabled={kind === "project" ? !projectId : !groupId || !variable}
            onClick={async () => {
              try {
                if (kind === "project") {
                  await api("/api/dashboard/tiles", { body: { kind, projectId, title, w: 1, h: 1 } });
                  onAdded();
                  return;
                }
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
      <Field label="Shown as">
        <select value={kind} onChange={(e) => setKind(e.target.value)}>
          <option value="project">a project, to jump into</option>
          <option value="number">a large number</option>
          <option value="text">text</option>
          <option value="status">a status dot</option>
          <option value="list">a list</option>
          <option value="table">a table</option>
          <option value="history">a small graph over time</option>
          <option value="button">a button that runs a rule</option>
        </select>
      </Field>

      {kind === "project" ? (
        <>
          <Field label="Project">
            <select value={projectId} onChange={(e) => setProjectId(e.target.value)}>
              <option value="">— pick one —</option>
              {projects.map((p) => (
                <option key={p.id} value={p.id}>
                  {(p.groupSlug ?? "ungrouped") + "/" + p.slug}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Title" hint="Empty keeps the project's own name.">
            <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder={
              projects.find((p) => p.id === projectId)?.title ?? ""
            } />
          </Field>
          <p className="meta">The numbers that project reports are shown on the tile as well.</p>
        </>
      ) : (
      <>
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
      </>
      )}
    </Modal>
  );
}

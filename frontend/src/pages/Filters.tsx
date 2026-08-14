import { useState } from "react";
import { Icon } from "../components/Icon";
import { Empty, ErrorBox, Field, Modal, Spinner, useGuarded } from "../components/ui";
import { api, type Filter, type FilterRule, type Project } from "../lib/api";
import { useQuery, useSession } from "../lib/store";

/**
 * Filters are rules that answer one question: where does this belong?
 *
 * A scheduler asks about a course, a project asks about a file. The rules do
 * not know which — which is why they live here rather than inside either.
 */
export default function Filters() {
  const session = useSession();
  const { data, error, loading, reload } = useQuery<{ filters: Filter[] }>("/api/filters");
  const [editing, setEditing] = useState<Filter | "new" | null>(null);
  const [actionError, setActionError] = useState<Error | null>(null);
  const guarded = useGuarded();

  if (!session.user) return <Empty icon="lock">Sign in to see the filters.</Empty>;

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Filters</h1>
        </div>
        <div className="head-actions">
          <button className="btn primary" onClick={() => setEditing("new")}>
            <Icon name="plus" size={16} /> New filter
          </button>
        </div>
      </div>

      <ErrorBox error={actionError ?? error} onRetry={reload} />
      {loading && !data ? <Spinner /> : null}
      {data && data.filters.length === 0 ? <Empty icon="search">No filters yet.</Empty> : null}

      <div className="tiles">
        {data?.filters.map((f) => (
          <div key={f.id} className="tile">
            <div className="tile-top">
              <span className="tile-icon">
                <Icon name="search" />
              </span>
              <div style={{ minWidth: 0 }}>
                <h3>{f.title}</h3>
                <div className="sub">
                  {(f.rules ?? []).length} rule{(f.rules ?? []).length === 1 ? "" : "s"}
                  {f.usedBy ? ` · used by ${f.usedBy} scheduler${f.usedBy === 1 ? "" : "s"}` : ""}
                </div>
              </div>
            </div>
            <pre className="block" style={{ margin: 0, maxHeight: 140 }}>
              {(f.rules ?? []).map(ruleLine).join("\n") || "(empty)"}
            </pre>
            <div className="tile-foot">
              <div style={{ flex: 1 }} />
              <button className="btn small" onClick={() => setEditing(f)}>
                <Icon name="settings" size={13} /> Edit
              </button>
              <button
                className="btn small danger"
                onClick={async () => {
                  if (!confirm(`Delete “${f.title}”?`)) return;
                  try {
                    await guarded("deleting a filter", () => api(`/api/filters/${f.id}`, { method: "DELETE" }));
                    reload();
                  } catch (err) {
                    setActionError(err as Error);
                  }
                }}
              >
                <Icon name="trash" size={13} />
              </button>
            </div>
          </div>
        ))}
      </div>

      {editing ? (
        <FilterDialog
          existing={editing === "new" ? undefined : editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            reload();
          }}
        />
      ) : null}
    </>
  );
}

/** A rule, written the way it was typed. */
function ruleLine(r: FilterRule): string {
  const many = r.pick ? `${r.pick}${r.count && r.count > 1 ? " " + r.count : ""} ` : "";
  return `${many}${r.match} -> ${r.to}`;
}

interface TryResult {
  name: string;
  where?: string;
  isDir?: boolean;
  matched: boolean;
  project: string;
  folder: string;
  skip: boolean;
  rule: string;
}

function FilterDialog({
  existing,
  onClose,
  onSaved,
}: {
  existing?: Filter;
  onClose: () => void;
  onSaved: () => void;
}) {
  const guarded = useGuarded();
  const [title, setTitle] = useState(existing?.title ?? "");
  const [text, setText] = useState(
    (existing?.rules ?? []).map(ruleLine).join("\n"),
  );
  const [preview, setPreview] = useState<string[]>(existing?.preview ?? []);
  const [what, setWhat] = useState("");
  const [how, setHow] = useState("starts");
  const [where, setWhere] = useState("");
  const [folder, setFolder] = useState("");
  const projects = useQuery<{ projects: Project[] }>("/api/projects");
  const [tried, setTried] = useState<{ results: TryResult[]; unusable?: string[] } | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const [busy, setBusy] = useState(false);

  const addresses = (projects.data?.projects ?? []).map((p) => `${p.groupSlug || "ungrouped"}/${p.slug}`);
  // The names that are really in the projects being tried against.
  const found = [...new Set((tried?.results ?? []).map((r) => r.name))].sort();

  /** Writes the line, so nothing has to be typed or remembered. */
  const addRule = () => {
    let left = what;
    if (what !== "*") {
      if (how === "starts") left = what + "*";
      else if (how === "ends") left = "*" + what;
      else if (how === "contains") left = "*" + what + "*";
    }
    const right = where === "folder" ? "./" + folder.replace(/^\.?\//, "") : where;
    setText((t) => (t && !t.endsWith("\n") ? t + "\n" : t) + `${left} -> ${right}`.trimEnd() + "\n");
    setWhat("");
    setFolder("");
    setTried(null);
  };

  const tryIt = async (against = preview) => {
    try {
      setTried(
        await api<{ results: TryResult[]; unusable?: string[] }>("/api/filters/try", {
          body: { text, projects: against },
        }),
      );
    } catch (err) {
      setError(err as Error);
    }
  };

  return (
    <Modal
      title={existing ? existing.title : "New filter"}
      onClose={onClose}
      wide
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn primary"
            disabled={busy || !title.trim()}
            onClick={async () => {
              setBusy(true);
              setError(null);
              try {
                await guarded("saving a filter", () =>
                  existing
                    ? api(`/api/filters/${existing.id}`, { method: "PATCH", body: { title, text, preview } })
                    : api("/api/filters", { body: { title, text, preview } }),
                );
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
      <Field label="Name">
        <input value={title} onChange={(e) => setTitle(e.target.value)} autoFocus />
      </Field>

      <Field label="Try it against" hint="Only while writing it. The filter stays standalone.">
        <div className="chips">
          {preview.map((ref) => (
            <button key={ref} className="badge" onClick={() => setPreview(preview.filter((x) => x !== ref))}>
              {ref} <Icon name="x" size={11} />
            </button>
          ))}
          <select
            value=""
            onChange={(e) => {
              if (!e.target.value || preview.includes(e.target.value)) return;
              const next = [...preview, e.target.value];
              setPreview(next);
              void tryIt(next);
            }}
          >
            <option value="">— add a project —</option>
            {addresses
              .filter((ref) => !preview.includes(ref))
              .map((ref) => (
                <option key={ref} value={ref}>
                  {ref}
                </option>
              ))}
          </select>
        </div>
      </Field>

      <Field label="Add a rule" hint="Built from what is really in those projects.">
        <div className="builder">
          <select value={what} onChange={(e) => setWhat(e.target.value)}>
            <option value="">— what —</option>
            <option value="*">everything left over</option>
            {found.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
          <select value={how} onChange={(e) => setHow(e.target.value)} disabled={what === "*"}>
            <option value="exact">exactly this</option>
            <option value="starts">everything starting like it</option>
            <option value="ends">everything ending like it</option>
            <option value="contains">everything containing it</option>
          </select>
          <span className="meta">→</span>
          <select value={where} onChange={(e) => setWhere(e.target.value)}>
            <option value="">where this project says</option>
            <option value="skip">leave it alone</option>
            <option value="folder">a folder here…</option>
            {addresses.map((ref) => (
              <option key={ref} value={"{" + ref + "}"}>
                {ref}
              </option>
            ))}
          </select>
          {where === "folder" ? (
            <input value={folder} onChange={(e) => setFolder(e.target.value)} placeholder="folder" />
          ) : null}
          <button className="btn small" disabled={!what} onClick={addRule}>
            Add
          </button>
        </div>
      </Field>

      <Field label="Rules">
        <textarea
          value={text}
          onChange={(e) => {
            setText(e.target.value);
            setTried(null);
          }}
          style={{ minHeight: 130, fontFamily: "var(--mono)", fontSize: 13 }}
        />
      </Field>

      <details className="syntax-help">
        <summary>Written by hand, it looks like this</summary>
        <table className="syntax">
          <tbody>
            <tr><td><code className="mono">name</code></td><td>contains</td></tr>
            <tr><td><code className="mono">name*</code></td><td>starts with</td></tr>
            <tr><td><code className="mono">*.ext</code></td><td>ends with</td></tr>
            <tr><td><code className="mono">*part*</code></td><td>contains</td></tr>
            <tr><td><code className="mono">/regex/</code></td><td>a regular expression</td></tr>
            <tr><td><code className="mono">2</code></td><td>a semester, where the source knows one</td></tr>
            <tr><td><code className="mono">*</code></td><td>everything left over</td></tr>
            <tr><td><code className="mono">first</code> <code className="mono">last</code> <code className="mono">newest</code> <code className="mono">oldest</code></td>
                <td>in front, with an optional count</td></tr>
            <tr><td className="head" colSpan={2}>after the arrow</td></tr>
            <tr><td>(nothing)</td><td>where the project using this says</td></tr>
            <tr><td><code className="mono">{"{group/project}"}</code></td><td>that project</td></tr>
            <tr><td><code className="mono">./folder</code></td><td>a folder, here</td></tr>
            <tr><td><code className="mono">skip</code></td><td>leave it alone</td></tr>
          </tbody>
        </table>
        <p className="meta">One per line. The first that matches takes it; later lines do not see it again.</p>
      </details>

      <button className="btn small" disabled={!preview.length} onClick={() => tryIt()}>
        What would it do?
      </button>

      {tried ? (
        <div className="list" style={{ marginTop: 12 }}>
          {tried.unusable?.map((u, i) => (
            <div key={"u" + i} className="list-row">
              <span className="badge bad">not a rule</span>
              <span className="grow mono">{u}</span>
            </div>
          ))}
          {tried.results.map((r, i) => (
            <div key={i} className="list-row">
              <Icon name={r.isDir ? "folder" : "file"} size={13} />
              <span className="grow mono">
                {r.name}
                {r.where ? <span className="meta"> · {r.where}</span> : null}
              </span>
              <Icon name="chevronRight" size={13} />
              <span className="mono">
                {!r.matched ? (
                  <span className="meta">no rule</span>
                ) : r.skip ? (
                  <span className="meta">left alone</span>
                ) : (
                  [r.project || "here", r.folder].filter(Boolean).join("/")
                )}
              </span>
              {r.rule ? <span className="meta">{r.rule}</span> : null}
            </div>
          ))}
        </div>
      ) : null}
    </Modal>
  );
}

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
  const projects = useQuery<{ projects: Project[] }>("/api/projects");
  const [tried, setTried] = useState<{ results: TryResult[]; unusable?: string[] } | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const [busy, setBusy] = useState(false);

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

      <Field label="Rules" hint="One per line. Where things go is set on the project that uses this.">
        <textarea
          value={text}
          onChange={(e) => {
            setText(e.target.value);
            setTried(null);
          }}
          style={{ minHeight: 150, fontFamily: "var(--mono)", fontSize: 13 }}
          placeholder={"Grundlagen* ->\nfirst *.pdf ->\nWerbung -> skip"}
        />
      </Field>

      <details className="syntax-help">
        <summary>What can stand there</summary>
        <table className="syntax">
          <tbody>
            <tr><td><code className="mono">Grundlagen</code></td><td>name contains</td></tr>
            <tr><td><code className="mono">Grundlagen*</code></td><td>starts with</td></tr>
            <tr><td><code className="mono">*.pdf</code></td><td>ends with</td></tr>
            <tr><td><code className="mono">*Kap*</code></td><td>contains</td></tr>
            <tr><td><code className="mono">2</code></td><td>semester</td></tr>
            <tr><td><code className="mono">*</code></td><td>everything left</td></tr>
            <tr><td><code className="mono">/^WDS\d+/</code></td><td>regular expression</td></tr>
            <tr><td><code className="mono">first last newest oldest</code></td><td>in front, with an optional count</td></tr>
            <tr><td className="head" colSpan={2}>after the arrow — only if this rule needs its own destination</td></tr>
            <tr><td><code className="mono">{"{Studies/semester1}"}</code></td><td>that project</td></tr>
            <tr><td><code className="mono">./skripte</code></td><td>a folder</td></tr>
            <tr><td><code className="mono">skip</code></td><td>leave it</td></tr>
          </tbody>
        </table>
      </details>

      <Field label="Try it against" hint="Only while writing it. The filter itself stays standalone.">
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
            {(projects.data?.projects ?? [])
              .map((p) => `${p.groupSlug || "ungrouped"}/${p.slug}`)
              .filter((ref) => !preview.includes(ref))
              .map((ref) => (
                <option key={ref} value={ref}>
                  {ref}
                </option>
              ))}
          </select>
          {preview.length ? (
            <button className="btn small" onClick={() => tryIt()}>
              Try
            </button>
          ) : null}
        </div>
      </Field>

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

import { useState } from "react";
import { Icon } from "../components/Icon";
import { Empty, ErrorBox, Field, Modal, Spinner, useGuarded } from "../components/ui";
import { api, type Filter } from "../lib/api";
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
                  {f.rules.length} rule{f.rules.length === 1 ? "" : "s"}
                  {f.usedBy ? ` · used by ${f.usedBy} scheduler${f.usedBy === 1 ? "" : "s"}` : ""}
                </div>
              </div>
            </div>
            <pre className="block" style={{ margin: 0, maxHeight: 140 }}>
              {f.rules.map((r) => `${r.match} -> ${r.to}`).join("\n") || "(empty)"}
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

interface TryResult {
  name: string;
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
    (existing?.rules ?? []).map((r) => `${r.match} -> ${r.to}`).join("\n"),
  );
  const [names, setNames] = useState("");
  const [tried, setTried] = useState<{ results: TryResult[]; unusable?: string[] } | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const [busy, setBusy] = useState(false);

  const tryIt = async () => {
    try {
      setTried(
        await api<{ results: TryResult[]; unusable?: string[] }>("/api/filters/try", {
          body: { text, names: names.split("\n") },
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
                    ? api(`/api/filters/${existing.id}`, { method: "PATCH", body: { title, text } })
                    : api("/api/filters", { body: { title, text } }),
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

      <Field label="Rules" hint="One per line, first match wins.">
        <textarea
          value={text}
          onChange={(e) => {
            setText(e.target.value);
            setTried(null);
          }}
          style={{ minHeight: 150, fontFamily: "var(--mono)", fontSize: 13 }}
          placeholder={"2 -> semester2\nGrundlagen In -> semester1\nÜbung -> /uebungen\nAlt -> archiv/2024\nWerbung -> skip\n* -> rest"}
        />
      </Field>

      <table className="syntax">
        <tbody>
          <tr>
            <td>
              <code className="mono">2</code>
            </td>
            <td>the semester</td>
          </tr>
          <tr>
            <td>
              <code className="mono">Grundlagen In</code>
            </td>
            <td>a piece of the name</td>
          </tr>
          <tr>
            <td>
              <code className="mono">*</code>
            </td>
            <td>everything else</td>
          </tr>
          <tr>
            <td>
              <code className="mono">-&gt; project</code>
            </td>
            <td>into that project</td>
          </tr>
          <tr>
            <td>
              <code className="mono">-&gt; /folder</code>
            </td>
            <td>into that folder, same place</td>
          </tr>
          <tr>
            <td>
              <code className="mono">-&gt; project/folder</code>
            </td>
            <td>both</td>
          </tr>
          <tr>
            <td>
              <code className="mono">-&gt; skip</code>
            </td>
            <td>leave it alone</td>
          </tr>
        </tbody>
      </table>

      <Field label="Try it" hint="One name per line.">
        <textarea
          value={names}
          onChange={(e) => setNames(e.target.value)}
          onBlur={tryIt}
          style={{ minHeight: 70, fontFamily: "var(--mono)", fontSize: 13 }}
          placeholder={"WDS125 - Grundlagen Informatik (INA)\nÜbung 3.pdf"}
        />
      </Field>
      <button className="btn small" onClick={tryIt} disabled={!names.trim()}>
        Try
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
              <span className="grow mono">{r.name}</span>
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

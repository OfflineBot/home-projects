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
                  {f.rules.length} rule{f.rules.length === 1 ? "" : "s"}
                  {f.usedBy ? ` · used by ${f.usedBy} scheduler${f.usedBy === 1 ? "" : "s"}` : ""}
                </div>
              </div>
            </div>
            <pre className="block" style={{ margin: 0, maxHeight: 140 }}>
              {f.rules.map(ruleLine).join("\n") || "(empty)"}
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
  const [names, setNames] = useState("");
  const projects = useQuery<{ projects: Project[] }>("/api/projects");
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
          placeholder={"Grundlagen* -> {Studies/semester1}\nfirst *.pdf -> ./skripte\nWerbung -> skip\n* -> {Studies/rest}"}
        />
      </Field>

      <details className="syntax-help" open>
        <summary>What can stand there</summary>
        <table className="syntax">
          <tbody>
            <tr><td colSpan={2} className="head">what is matched — on the left</td></tr>
            <tr><td><code className="mono">Grundlagen</code></td><td>the name contains it</td></tr>
            <tr><td><code className="mono">Grundlagen*</code></td><td>the name starts with it</td></tr>
            <tr><td><code className="mono">*.pdf</code></td><td>the name ends with it</td></tr>
            <tr><td><code className="mono">*Kap*</code></td><td>somewhere in the middle</td></tr>
            <tr><td><code className="mono">2</code></td><td>the semester (Moodle says which)</td></tr>
            <tr><td><code className="mono">*</code></td><td>everything left over</td></tr>
            <tr><td><code className="mono">/^WDS\d+ - Grund/</code></td><td>a regular expression, if none of the above fits</td></tr>

            <tr><td colSpan={2} className="head">how many — in front of it</td></tr>
            <tr><td><code className="mono">first Grundlagen*</code></td><td>only the first, by name</td></tr>
            <tr><td><code className="mono">last Grundlagen*</code></td><td>only the last</td></tr>
            <tr><td><code className="mono">first 3 *.pdf</code></td><td>the first three</td></tr>
            <tr><td><code className="mono">newest 5 *</code></td><td>the five most recently changed</td></tr>

            <tr><td colSpan={2} className="head">where it goes — on the right</td></tr>
            <tr><td><code className="mono">{"{Studies/semester1}"}</code></td><td>into that project</td></tr>
            <tr><td><code className="mono">{"{Studies/semester1}/skripte"}</code></td><td>and into a folder in it</td></tr>
            <tr><td><code className="mono">./skripte</code></td><td>a folder where it already is</td></tr>
            <tr><td><code className="mono">skip</code></td><td>leave it alone</td></tr>
          </tbody>
        </table>
        <p className="meta">One rule per line. The first that matches wins, and what it takes, no later rule sees.</p>
      </details>

      <Field label="Insert a project" hint="Puts it in the rules as {group/project}.">
        <select
          value=""
          onChange={(e) => {
            if (!e.target.value) return;
            setText((t) => (t.endsWith("\n") || t === "" ? t : t + "\n") + `* -> {${e.target.value}}`);
            setTried(null);
          }}
        >
          <option value="">— pick one —</option>
          {(projects.data?.projects ?? []).map((p) => (
            <option key={p.id} value={`${p.groupSlug || "ungrouped"}/${p.slug}`}>
              {p.groupSlug || "ungrouped"}/{p.slug}
            </option>
          ))}
        </select>
      </Field>

      <Field label="Try it" hint="One name per line.">
        <textarea
          value={names}
          onChange={(e) => setNames(e.target.value)}
          onBlur={tryIt}
          style={{ minHeight: 70, fontFamily: "var(--mono)", fontSize: 13 }}
          placeholder={"Grundlagen Informatik\nGrundlagen Analysis\nSkript.pdf"}
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

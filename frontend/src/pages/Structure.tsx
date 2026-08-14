import { useRef, useState } from "react";
import { Link } from "react-router-dom";
import { Icon } from "../components/Icon";
import { ErrorBox, Field, Modal, Spinner, useGuarded } from "../components/ui";
import { api, type Group, type Link as LinkRow, type Project } from "../lib/api";
import { useQuery, useSession } from "../lib/store";
import { colorVar } from "../lib/theme";

/** The arrangement as `tree` would print it: one root per group. */
export default function Structure() {
  const session = useSession();
  const [importing, setImporting] = useState(false);
  const { data, error, loading, reload } = useQuery<{
    groups: Group[];
    projects: Project[];
    links: LinkRow[];
  }>("/api/structure");

  const ungrouped = (data?.projects ?? []).filter((p) => !p.groupId);

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Structure</h1>
        </div>
        {session.user ? (
          <div className="head-actions">
            <a className="btn" href="/api/export?download=true">
              <Icon name="download" size={16} /> Export as JSON
            </a>
            <button className="btn" onClick={() => setImporting(true)}>
              <Icon name="upload" size={16} /> Import…
            </button>
          </div>
        ) : null}
      </div>
      <ErrorBox error={error} onRetry={reload} />
      {loading && !data ? <Spinner /> : null}

      <div className="tree">
        {data?.groups.map((g) => {
          const projects = data.projects.filter((p) => p.groupId === g.id);
          return (
            <div key={g.id} className="tree-group">
              <Link to={`/groups/${g.slug}`} className="tree-root" style={{ color: colorVar(g.color) }}>
                <Icon name={g.icon} size={14} />
                {g.slug}/
              </Link>
              {projects.map((p, i) => (
                <Link
                  key={p.id}
                  to={`/groups/${g.slug}/${p.slug}`}
                  className="tree-row"
                >
                  <span className="branch">{i === projects.length - 1 ? "└──" : "├──"}</span>
                  <span className="name">{p.slug}</span>
                  <span className="tail">
                    {p.capabilities.join(" ")}
                    {p.archived ? " archived" : ""}
                    {p.readOnly ? " read-only" : ""}
                    {p.visibility !== "private" ? " " + p.visibility : ""}
                  </span>
                </Link>
              ))}
              {projects.length === 0 ? <div className="tree-row empty">└── (empty)</div> : null}
            </div>
          );
        })}

        {ungrouped.length ? (
          <div className="tree-group">
            <div className="tree-root">ungrouped/</div>
            {ungrouped.map((p, i) => (
              <Link key={p.id} to={`/p/${p.id}`} className="tree-row">
                <span className="branch">{i === ungrouped.length - 1 ? "└──" : "├──"}</span>
                <span className="name">{p.slug}</span>
                <span className="tail">{p.capabilities.join(" ")}</span>
              </Link>
            ))}
          </div>
        ) : null}
      </div>

      {importing ? (
        <ImportBlueprint
          onClose={() => setImporting(false)}
          onDone={() => {
            setImporting(false);
            reload();
          }}
        />
      ) : null}

      <h2 style={{ fontSize: 17, marginTop: 28 }}>Links</h2>
      {data?.links.length ? (
        <div className="list">
          {data.links.map((l) => (
            <div key={l.id} className="list-row">
              <Icon name="link" size={15} />
              <span className="grow">
                <strong>{l.sourceSlug}</strong>:{l.sourcePath} → <strong>{l.targetSlug}</strong>:{l.targetPath}
              </span>
              <span className="badge">{l.kind}</span>
            </div>
          ))}
        </div>
      ) : (
        <p className="meta">none</p>
      )}
    </>
  );
}

interface Step {
  action: string;
  what: string;
  name: string;
  note?: string;
}

/**
 * The arrangement as one document. It says what it would do before it does
 * anything, and it never deletes: what the document no longer mentions is left
 * alone.
 */
function ImportBlueprint({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const guarded = useGuarded();
  const [text, setText] = useState("");
  const [plan, setPlan] = useState<{ steps: Step[]; warnings?: string[] } | null>(null);
  const [applied, setApplied] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [busy, setBusy] = useState(false);
  const file = useRef<HTMLInputElement>(null);

  const send = async (apply: boolean) => {
    setBusy(true);
    setError(null);
    try {
      let document: unknown;
      try {
        document = JSON.parse(text);
      } catch (e) {
        throw new Error("This is not valid JSON: " + (e as Error).message);
      }
      const result = await guarded("importing an arrangement", () =>
        api<{ steps: Step[]; warnings?: string[] }>(`/api/import${apply ? "?apply=true" : ""}`, {
          body: document,
        }),
      );
      setPlan(result);
      if (apply) {
        setApplied(true);
        onDone();
      }
    } catch (err) {
      setError(err as Error);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      title="Import an arrangement"
      onClose={onClose}
      wide
      footer={
        <>
          <button className="btn" onClick={onClose}>
            {applied ? "Close" : "Cancel"}
          </button>
          {!applied ? (
            <>
              <button className="btn" disabled={busy || !text.trim()} onClick={() => send(false)}>
                What would this do?
              </button>
              <button className="btn primary" disabled={busy || !plan} onClick={() => send(true)}>
                Do it
              </button>
            </>
          ) : null}
        </>
      }
    >
      <ErrorBox error={error} />
      <p className="meta" style={{ marginTop: 0 }}>
        Groups, projects, links, schedulers. No files, no passwords. Nothing is deleted.
      </p>

      <Field label="The document">
        <textarea
          value={text}
          onChange={(e) => {
            setText(e.target.value);
            setPlan(null);
            setApplied(false);
          }}
          placeholder='{ "version": 1, "groups": [ … ] }'
          style={{ minHeight: 160 }}
        />
      </Field>
      <input
        ref={file}
        type="file"
        accept=".json,application/json"
        style={{ display: "none" }}
        onChange={async (e) => {
          const picked = e.target.files?.[0];
          if (picked) {
            setText(await picked.text());
            setPlan(null);
            setApplied(false);
          }
          e.target.value = "";
        }}
      />
      <button className="btn small" onClick={() => file.current?.click()}>
        <Icon name="file" size={14} /> Pick a file instead
      </button>

      {plan ? (
        <div style={{ marginTop: 18 }}>
          <h3 style={{ fontSize: 15 }}>{applied ? "What happened" : "What would happen"}</h3>
          {plan.warnings?.length ? (
            <div className="warning">
              {plan.warnings.map((w, i) => (
                <div key={i}>{w}</div>
              ))}
            </div>
          ) : null}
          <div className="list">
            {plan.steps.map((s, i) => (
              <div key={i} className="list-row">
                <span className={`badge ${s.action === "create" ? "good" : s.action === "skip" ? "" : "warn"}`}>
                  {s.action}
                </span>
                <span className="meta" style={{ minWidth: 70 }}>{s.what}</span>
                <span className="grow">{s.name}</span>
                {s.note ? <span className="meta">{s.note}</span> : null}
              </div>
            ))}
            {plan.steps.length === 0 ? (
              <div className="list-row">
                <span className="meta">nothing to do — everything in it is already here</span>
              </div>
            ) : null}
          </div>
        </div>
      ) : null}
    </Modal>
  );
}

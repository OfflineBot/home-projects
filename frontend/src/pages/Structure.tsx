import { useRef, useState } from "react";
import { Link } from "react-router-dom";
import { Icon } from "../components/Icon";
import { ErrorBox, Field, Modal, Spinner, useGuarded } from "../components/ui";
import { api, type Group, type Link as LinkRow, type Project } from "../lib/api";
import { useQuery, useSession } from "../lib/store";
import { colorVar } from "../lib/theme";

/** A visual map: which projects hang in which groups, and which links run between them. */
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
          <p>Everything at a glance: groups, their projects, and the links between them.</p>
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

      <div className="tiles" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(280px, 1fr))" }}>
        {data?.groups.map((g) => {
          const projects = data.projects.filter((p) => p.groupId === g.id);
          return (
            <div key={g.id} className="tile" style={{ ["--tile-color" as string]: colorVar(g.color) }}>
              <div className="tile-top">
                <span className="tile-icon">
                  <Icon name={g.icon} />
                </span>
                <div>
                  <h3>
                    <Link to={`/groups/${g.slug}`}>{g.title}</Link>
                  </h3>
                  <div className="sub">{projects.length} projects</div>
                </div>
              </div>
              <div className="list" style={{ background: "transparent", border: "none" }}>
                {projects.map((p) => (
                  <Link
                    key={p.id}
                    to={`/groups/${g.slug}/${p.slug}`}
                    className="list-row"
                    style={{ padding: "6px 4px" }}
                  >
                    <Icon name={p.icon || "box"} size={14} />
                    <span className="grow">{p.title}</span>
                    {p.archived ? <span className="badge">archived</span> : null}
                    {p.capabilities.length ? <span className="meta">{p.capabilities.join(" · ")}</span> : null}
                  </Link>
                ))}
                {projects.length === 0 ? <span className="meta">empty</span> : null}
              </div>
            </div>
          );
        })}

        {ungrouped.length ? (
          <div className="tile">
            <div className="tile-top">
              <span className="tile-icon">
                <Icon name="box" />
              </span>
              <h3>Ungrouped</h3>
            </div>
            <div className="list" style={{ background: "transparent", border: "none" }}>
              {ungrouped.map((p) => (
                <Link key={p.id} to={`/p/${p.id}`} className="list-row" style={{ padding: "6px 4px" }}>
                  <Icon name={p.icon || "box"} size={14} />
                  <span className="grow">{p.title}</span>
                </Link>
              ))}
            </div>
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
        <p style={{ color: "var(--ctp-subtext0)" }}>
          No links yet. A link shows the same content in a second place — without copying it.
        </p>
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
      <p style={{ marginTop: 0, color: "var(--ctp-subtext0)" }}>
        The shape of a server: groups, projects, links and schedulers. Not the files — those travel by git
        and by the zip download — and no passwords, which is why a password-protected group arrives private.
        Nothing is ever deleted by an import.
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
